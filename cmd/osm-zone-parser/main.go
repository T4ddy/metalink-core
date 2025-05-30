package main

import (
	"flag"
	"log"
	"os"

	"metalink/internal/model"
	pg "metalink/internal/postgres"

	"gorm.io/gorm"

	parser_db "metalink/cmd/osm-zone-parser/db"
	osm_processor "metalink/cmd/osm-zone-parser/osm_processor"
	utils "metalink/cmd/osm-zone-parser/utils"
)

// Command line flags
var (
	dbURL               string
	runMode             int
	osmFilePath         string
	baseZoneSize        float64
	minZoneSize         float64
	exportBaseMapJSON   bool
	skipDB              bool
	clearZones          bool
	exportZonesJSON     bool
	exportBuildingsJSON bool

	// Type indexer specific flags
	inputFiles       string
	outputFile       string
	minOccurrences   int
	minAreaInt       int
	filterShortNames bool
	filterSemicolon  bool
	filterLongNames  bool
)

// RunMode represents different operation modes
const (
	RunModeBaseInit    = 1
	RunModeOSMLayer    = 2
	RunModeTypeIndexer = 3
	RunModeTestZone    = 4
)

func init() {
	// Define command line flags
	flag.StringVar(&dbURL, "db-url", "postgresql://postgres:postgres@localhost:5432/metalink?sslmode=disable", "Database connection URL")
	flag.IntVar(&runMode, "mode", 0, "Run mode: 1 = Base USA map initialization, 2 = Add OSM data layer, 3 = Building type indexer, 4 = Save to test zone")
	flag.StringVar(&osmFilePath, "osm-file", "", "Path to OSM PBF file")
	flag.Float64Var(&baseZoneSize, "base-zone-size", 100000.0, "Base zone size in meters for USA map (default: 100km)")
	flag.Float64Var(&minZoneSize, "min-zone-size", 500.0, "Minimum zone size in meters (default: 500m)")
	flag.BoolVar(&exportBaseMapJSON, "export-usa-grid-json", true, "Export base USA map to GeoJSON file")
	flag.BoolVar(&skipDB, "skip-db", false, "Skip all database operations")
	flag.BoolVar(&clearZones, "clear-zones", false, "Clear all zones from database before saving updated ones (test mode)")
	flag.BoolVar(&exportZonesJSON, "export-zones-json", true, "Export processed zones with building stats to GeoJSON file")
	flag.BoolVar(&exportBuildingsJSON, "export-buildings-json", false, "Export buildings as squares to GeoJSON file")

	// Type indexer specific flags
	flag.StringVar(&inputFiles, "input", "", "Comma-separated list of input JSON files")
	flag.StringVar(&outputFile, "output", "usa_buildings_data/bmap_tmp.json", "Output JSON file")
	flag.IntVar(&minOccurrences, "min-occurrences", 2, "Minimum number of occurrences to keep a building type")
	flag.IntVar(&minAreaInt, "min-area", 1000, "Minimum area in square meters to keep a building type")
	flag.BoolVar(&filterShortNames, "filter-short-names", true, "Filter out building types with 1-2 character names")
	flag.BoolVar(&filterSemicolon, "filter-semicolon", false, "Filter out building types with semicolon in name")
	flag.BoolVar(&filterLongNames, "filter-long-names", true, "Filter out building types with names longer than 50 characters")
}

func main() {
	// Parse command line flags
	flag.Parse()

	// Validate run mode
	if runMode == 0 {
		log.Fatal("Run mode must be specified: 1 = Base USA map initialization, 2 = Add OSM data layer, 3 = Building type indexer, 4 = Save to test zone")
	}

	// Initialize database only if not in type indexer mode
	if runMode != RunModeTypeIndexer && runMode != RunModeTestZone {
		initDB()
		defer pg.Close()
	}

	// Execute the appropriate function based on run mode
	switch runMode {
	case RunModeBaseInit:
		runBaseInitMode()
	case RunModeOSMLayer:
		runOSMLayerMode()
	case RunModeTypeIndexer:
		runTypeIndexerMode()
	case RunModeTestZone:
		runTestZoneMode()
	default:
		log.Fatalf("Invalid run mode: %d", runMode)
	}
}

// runBaseInitMode initializes the base USA map
func runBaseInitMode() {
	log.Println("Running in Base USA Map Initialization mode")

	// Generate zones
	zonesUSA := buildBaseUSAGrid()

	// Save zones to database
	parser_db.SaveZonesToDB(zonesUSA)
	log.Printf("Successfully saved %d zones to database", len(zonesUSA))

	// Export zones to GeoJSON if enabled
	if exportBaseMapJSON {
		utils.ExportGameZonesToGeoJSON(zonesUSA, "output_zones.geojson", USATopLeft, USATopRight, USABottomLeft, USABottomRight)
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

	if clearZones {
		log.Println("Clear zones mode enabled. All zones will be deleted and base grid will be regenerated.")

		// Clear all zones from database
		if err := parser_db.ClearAllZonesFromDB(); err != nil {
			log.Fatalf("Failed to clear zones from database: %v", err)
		}

		// Run base initialization to create fresh zone grid
		log.Println("Regenerating base USA grid...")
		zonesUSA := buildBaseUSAGrid()

		// Save zones to database
		parser_db.SaveZonesToDB(zonesUSA)
		log.Printf("Successfully saved %d fresh zones to database", len(zonesUSA))
	}

	// Process OSM data with minimum zone size parameter
	processor := osm_processor.NewOSMProcessor(minZoneSize)
	if err := processor.ProcessOSMFile(osmFilePath); err != nil {
		log.Fatalf("Failed to process OSM file: %v", err)
	}

	log.Printf("OSM data processing complete. Found %d buildings.", len(processor.Buildings))

	// Find existing zones in the extended bounding box with 5km buffer
	zones, err := processor.GetZonesForProcessedBuildings(5000.0)
	if err != nil {
		log.Fatalf("Failed to find existing zones: %v", err)
	}

	log.Printf("Found %d existing zones intersecting with the objects bounding box (with buffer).", len(zones))

	err = processor.UpdateZonesWithBuildingStats(zones, clearZones, exportZonesJSON, exportBuildingsJSON)
	if err != nil {
		log.Fatalf("Failed to update zones with building stats: %v", err)
	}

	log.Printf("Successfully updated %d zones with building statistics", len(zones))
}

// runTestZoneMode processes OSM data and saves everything to a single test zone
func runTestZoneMode() {
	log.Println("Running in Test Zone Save mode")

	// Validate OSM file path
	if osmFilePath == "" {
		log.Fatal("OSM file path must be specified when using Test Zone mode")
	}

	// Check if file exists
	if _, err := os.Stat(osmFilePath); os.IsNotExist(err) {
		log.Fatalf("OSM file not found: %s", osmFilePath)
	}

	log.Println("Processing OSM file for test zone creation...")

	// Process OSM data with minimum zone size parameter
	processor := osm_processor.NewOSMProcessor(minZoneSize)
	if err := processor.ProcessOSMFile(osmFilePath); err != nil {
		log.Fatalf("Failed to process OSM file: %v", err)
	}

	log.Printf("OSM data processing complete. Found %d buildings.", len(processor.Buildings))

	// Create and fill test zone with all buildings
	if err := processor.SaveAllBuildingsToTestZone(exportZonesJSON, exportBuildingsJSON); err != nil {
		log.Fatalf("Failed to save buildings to test zone: %v", err)
	}

	log.Println("Successfully saved all buildings to test zone")
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
