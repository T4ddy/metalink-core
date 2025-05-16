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
	baseTileSize      float64
	minTileSize       float64
	exportBaseMapJSON bool
)

// RunMode represents different operation modes
const (
	RunModeBaseInit = 1
	RunModeOSMLayer = 2
)

// GameTile represents a tile in our game grid
type GameTile struct {
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
	flag.Float64Var(&baseTileSize, "base-tile-size", 100000.0, "Base tile size in meters for USA map (default: 100km)")
	flag.Float64Var(&minTileSize, "min-tile-size", 500.0, "Minimum tile size in meters (default: 500m)")
	flag.BoolVar(&exportBaseMapJSON, "export-json", true, "Export base USA map to GeoJSON file")
}

func main() {
	// Parse command line flags
	flag.Parse()

	// Validate run mode
	if runMode == 0 {
		log.Fatal("Run mode must be specified: 1 = Base USA map initialization, 2 = Add OSM data layer")
	}

	// Initialize database
	initDB()
	defer pg.Close()

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

	// Generate tiles
	tilesUSA := buildBaseUSAGrid()

	// Save tiles to database
	saveTilesToDB(tilesUSA)

	log.Printf("Successfully saved %d tiles to database", len(tilesUSA))

	// Export tiles to GeoJSON if enabled
	if exportBaseMapJSON {
		exportTilesToGeoJSON(tilesUSA, "output_tiles.geojson", USATopLeft, USATopRight, USABottomLeft, USABottomRight)
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

	// Process OSM data
	processor := NewOSMProcessor()
	if err := processor.ProcessOSMFile(osmFilePath); err != nil {
		log.Fatalf("Failed to process OSM file: %v", err)
	}

	log.Printf("OSM data processing complete. Found %d buildings.", len(processor.Buildings))
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

// saveTilesToDB converts GameTiles to ZonePG models and saves them to the database
func saveTilesToDB(tiles []GameTile) {
	db := pg.GetDB()

	// Create a batch of zones to insert
	var zones []model.ZonePG
	now := time.Now()

	for _, tile := range tiles {
		// Generate a UUID if the tile ID is in format "tile_X_Y"
		id := tile.ID
		if _, err := fmt.Sscanf(tile.ID, "tile_%d_%d", new(int), new(int)); err == nil {
			id = uuid.New().String()
		}

		topLeft := model.Float64Slice{tile.TopLeftLatLon[0], tile.TopLeftLatLon[1]}
		topRight := model.Float64Slice{tile.TopRightLatLon[0], tile.TopRightLatLon[1]}
		bottomLeft := model.Float64Slice{tile.BottomLeftLatLon[0], tile.BottomLeftLatLon[1]}
		bottomRight := model.Float64Slice{tile.BottomRightLatLon[0], tile.BottomRightLatLon[1]}

		// Create a ZonePG from the GameTile
		zone := model.ZonePG{
			ID:                id,
			Name:              fmt.Sprintf("Zone %s", id),
			TopLeftLatLon:     topLeft,
			TopRightLatLon:    topRight,
			BottomLeftLatLon:  bottomLeft,
			BottomRightLatLon: bottomRight,
			Effects:           []model.ZoneEffect{}, // Empty effects array
			CreatedAt:         now,
			UpdatedAt:         now,
		}

		zones = append(zones, zone)
	}

	// Insert in batches of 100 to avoid overwhelming the database
	batchSize := 100
	for i := 0; i < len(zones); i += batchSize {
		end := i + batchSize
		if end > len(zones) {
			end = len(zones)
		}

		batch := zones[i:end]
		result := db.Create(&batch)
		if result.Error != nil {
			log.Printf("Error saving batch %d-%d: %v", i, end, result.Error)
		} else {
			log.Printf("Saved batch %d-%d successfully", i, end)
		}
	}
}
