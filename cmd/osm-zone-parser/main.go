package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"metalink/internal/model"
	pg "metalink/internal/postgres"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Command line flags
var (
	dbURL             string
	runMode           int
	osmFilePath       string
	baseZoneSize      float64
	minZoneSize       float64
	exportBaseMapJSON bool
	skipDB            bool
)

// RunMode represents different operation modes
const (
	RunModeBaseInit = 1
	RunModeOSMLayer = 2
)

// GameZone represents a zone in our game grid
type GameZone struct {
	ID                string
	TopLeftLatLon     [2]float64 // [lat, lon]
	TopRightLatLon    [2]float64 // [lat, lon]
	BottomLeftLatLon  [2]float64 // [lat, lon]
	BottomRightLatLon [2]float64 // [lat, lon]
	Size              float64    // Size in meters
}

func init() {
	// Define command line flags
	flag.StringVar(&dbURL, "db-url", "postgresql://postgres:postgres@localhost:5432/metalink?sslmode=disable", "Database connection URL")
	flag.IntVar(&runMode, "mode", 0, "Run mode: 1 = Base USA map initialization, 2 = Add OSM data layer")
	flag.StringVar(&osmFilePath, "osm-file", "", "Path to OSM PBF file")
	flag.Float64Var(&baseZoneSize, "base-zone-size", 100000.0, "Base zone size in meters for USA map (default: 100km)")
	flag.Float64Var(&minZoneSize, "min-zone-size", 500.0, "Minimum zone size in meters (default: 500m)")
	flag.BoolVar(&exportBaseMapJSON, "export-json", true, "Export base USA map to GeoJSON file")
	flag.BoolVar(&skipDB, "skip-db", false, "Skip all database operations")
}

func main() {
	// Parse command line flags
	flag.Parse()

	// Validate run mode
	if runMode == 0 {
		log.Fatal("Run mode must be specified: 1 = Base USA map initialization, 2 = Add OSM data layer")
	}

	// Initialize database only if not skipping DB operations
	if !skipDB {
		initDB()
		defer pg.Close()
	}

	// Execute the appropriate function based on run mode
	switch runMode {
	case RunModeBaseInit:
		runBaseInitMode()
	case RunModeOSMLayer:
		runOSMLayerMode()
	default:
		log.Fatalf("Invalid run mode: %d", runMode)
	}
}

// runBaseInitMode initializes the base USA map
func runBaseInitMode() {
	log.Println("Running in Base USA Map Initialization mode")

	// Generate zones
	zonesUSA := buildBaseUSAGrid()

	// Save zones to database only if not skipping DB operations
	if !skipDB {
		saveZonesToDB(zonesUSA)
		log.Printf("Successfully saved %d zones to database", len(zonesUSA))
	} else {
		log.Printf("Skipping database operations. Generated %d zones", len(zonesUSA))
	}

	// Export zones to GeoJSON if enabled
	if exportBaseMapJSON {
		exportZonesToGeoJSON(zonesUSA, "output_zones.geojson", USATopLeft, USATopRight, USABottomLeft, USABottomRight)
	}
}

// runOSMLayerMode processes OSM data and adds it as a layer
func runOSMLayerMode() {
	log.Println("Running in OSM Data Layer mode")

	// Validate OSM file path
	if osmFilePath == "" {
		log.Fatal("OSM file path must be specified when using OSM Layer mode")
	}

	// Check if file exists
	if _, err := os.Stat(osmFilePath); os.IsNotExist(err) {
		log.Fatalf("OSM file not found: %s", osmFilePath)
	}

	if skipDB {
		log.Println("Database operations are disabled. Will only process OSM file without updating zones.")
	}

	// Process OSM data
	processor := NewOSMProcessor()
	if err := processor.ProcessOSMFile(osmFilePath); err != nil {
		log.Fatalf("Failed to process OSM file: %v", err)
	}

	log.Printf("OSM data processing complete. Found %d buildings.", len(processor.Buildings))

	// Find existing zones in the extended bounding box with 5km buffer
	zones, err := processor.GetZonesForProcessedBuildings(5000.0, skipDB)
	if err != nil {
		log.Fatalf("Failed to find existing zones: %v", err)
	}

	log.Printf("Found %d existing zones intersecting with the objects bounding box (with buffer).", len(zones))

	err = processor.UpdateZonesWithBuildingStats(zones, skipDB)
	if err != nil {
		log.Fatalf("Failed to update zones with building stats: %v", err)
	}

	log.Printf("Successfully updated %d zones with building statistics", len(zones))
}

// initDB initializes the database connection and runs migrations
func initDB() *gorm.DB {
	db := pg.Init(dbURL)

	err := db.AutoMigrate(&model.ZonePG{})
	if err != nil {
		log.Fatalf("Failed to migrate ZonePG model: %v", err)
	}

	return db
}

// saveZonesToDB converts GameZones to ZonePG models and saves them to the database
func saveZonesToDB(zones []GameZone) {
	db := pg.GetDB()

	// Create a batch of zones to insert
	var zonePGs []model.ZonePG
	now := time.Now()

	for _, zone := range zones {
		// Generate a UUID if the zone ID is in format "zone_X_Y"
		id := zone.ID
		if _, err := fmt.Sscanf(zone.ID, "zone_%d_%d", new(int), new(int)); err == nil {
			id = uuid.New().String()
		}

		topLeft := model.Float64Slice{zone.TopLeftLatLon[0], zone.TopLeftLatLon[1]}
		topRight := model.Float64Slice{zone.TopRightLatLon[0], zone.TopRightLatLon[1]}
		bottomLeft := model.Float64Slice{zone.BottomLeftLatLon[0], zone.BottomLeftLatLon[1]}
		bottomRight := model.Float64Slice{zone.BottomRightLatLon[0], zone.BottomRightLatLon[1]}

		// Initialize empty building and water stats
		emptyBuildingStats := model.BuildingStats{
			BuildingTypes: make(map[string]int),
			BuildingAreas: make(map[string]float64),
		}

		emptyWaterBodyStats := model.WaterBodyStats{}

		// Create a ZonePG from the GameZone
		zonePG := model.ZonePG{
			ID:                id,
			Name:              fmt.Sprintf("Zone %s", id),
			TopLeftLatLon:     topLeft,
			TopRightLatLon:    topRight,
			BottomLeftLatLon:  bottomLeft,
			BottomRightLatLon: bottomRight,
			Buildings:         emptyBuildingStats,
			WaterBodies:       emptyWaterBodyStats,
			CreatedAt:         now,
			UpdatedAt:         now,
		}

		zonePGs = append(zonePGs, zonePG)
	}

	// Insert in batches of 100 to avoid overwhelming the database
	batchSize := 100
	for i := 0; i < len(zonePGs); i += batchSize {
		end := i + batchSize
		if end > len(zonePGs) {
			end = len(zonePGs)
		}

		batch := zonePGs[i:end]
		result := db.Create(&batch)
		if result.Error != nil {
			log.Printf("Error saving batch %d-%d: %v", i, end, result.Error)
		} else {
			log.Printf("Saved batch %d-%d successfully", i, end)
		}
	}
}
