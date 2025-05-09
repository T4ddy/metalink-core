package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"metalink/internal/model"
	"metalink/internal/postgres"
	"metalink/internal/util"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/qedus/osmpbf"
	"gorm.io/gorm"
)

// Settlement represents a settlement
type Settlement struct {
	ID         int64
	Name       string
	Type       string // city, town, village, hamlet
	Lat, Lon   float64
	Population int
	IsNode     bool    // true if this is a node, false if polygon (way)
	NodeIDs    []int64 // Node IDs for ways (polygons)
}

// WayPolygon represents a way with its polygon data
type WayPolygon struct {
	Way     *osmpbf.Way
	Points  []orb.Point
	IsValid bool
}

func main() {
	// Parse command line arguments
	args := parseCommandLineArgs()

	// Initialize database connection
	db := initDatabase(args.dbURL)

	// Process OSM PBF file
	settlements, nodeCache := extractSettlements(args.osmFile)

	// Create polygons for ways
	wayPolygons := createWayPolygons(settlements, nodeCache)

	// Save settlements to PostgreSQL
	saveSettlementsToDB(settlements, wayPolygons, db)
}

// CommandLineArgs holds the parsed command line arguments
type CommandLineArgs struct {
	osmFile string
	dbURL   string
}

// parseCommandLineArgs parses the command line arguments
func parseCommandLineArgs() CommandLineArgs {
	if len(os.Args) < 2 {
		log.Fatal("Usage: program <path-to-osm.pbf> [db-url]")
	}

	args := CommandLineArgs{
		osmFile: os.Args[1],
		dbURL:   "postgres://postgres:postgres@localhost:5432/metalink",
	}

	if len(os.Args) > 2 {
		args.dbURL = os.Args[2]
	}

	log.Printf("Processing file: %s", args.osmFile)
	return args
}

// initDatabase initializes the database connection
func initDatabase(dbURL string) *gorm.DB {
	db := postgres.Init(dbURL)
	log.Println("Connected to database")

	// Ensure ZonePG model is migrated
	err := db.AutoMigrate(&model.ZonePG{})
	if err != nil {
		log.Fatalf("Failed to migrate Zone model: %v", err)
	}
	log.Println("Database migration completed")

	return db
}

// extractSettlements extracts settlements from the OSM PBF file
func extractSettlements(osmFile string) (map[string]Settlement, map[int64]*osmpbf.Node) {
	// Open the file
	f, err := os.Open(osmFile)
	if err != nil {
		log.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()

	// Counters for statistics
	nodeCount := 0
	wayCount := 0
	totalCount := 0

	// Node cache for forming settlement polygons
	nodeCache := make(map[int64]*osmpbf.Node)

	// Map to track settlements to avoid duplicates
	settlements := make(map[string]Settlement)

	// Phase 1: Collecting all settlement nodes and caching other nodes for polygons
	log.Println("Phase 1: Collecting settlement nodes and caching coordinates...")

	extractSettlementNodes(f, settlements, nodeCache, &nodeCount, &totalCount)

	log.Printf("Collected %d settlement nodes", nodeCount)

	// Phase 2: Collecting all ways (polygons) representing settlements
	log.Println("Phase 2: Collecting settlement polygons (ways)...")

	// Reopen the file for the second pass
	f, err = os.Open(osmFile)
	if err != nil {
		log.Fatalf("Failed to reopen file: %v", err)
	}
	defer f.Close()

	extractSettlementWays(f, settlements, nodeCache, &wayCount, &totalCount)

	log.Printf("Collected %d settlement polygons (ways)", wayCount)
	log.Printf("Total settlements found: %d", totalCount)

	return settlements, nodeCache
}

// extractSettlementNodes extracts settlement nodes from the OSM PBF file
func extractSettlementNodes(
	f *os.File,
	settlements map[string]Settlement,
	nodeCache map[int64]*osmpbf.Node,
	nodeCount *int,
	totalCount *int,
) {
	// Create a decoder
	decoder := osmpbf.NewDecoder(f)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)

	// Use all available CPUs for parallel processing
	numProcs := runtime.GOMAXPROCS(-1)
	decoder.Start(numProcs)
	log.Printf("Decoder started with %d processors", numProcs)

	for {
		// Decode the next object
		object, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Error decoding: %v", err)
		}

		// Process node
		if node, ok := object.(*osmpbf.Node); ok {
			// Save all nodes for use in polygons
			nodeCache[node.ID] = node

			// Check if the node is a settlement
			if placeType, isPlace := node.Tags["place"]; isPlace {
				// Filter only main types of settlements
				if isSettlementType(placeType) {
					name := node.Tags["name"]
					if name == "" {
						name = fmt.Sprintf("Unnamed %s", placeType)
					}

					// Extract population if specified
					population := 0
					if popStr, ok := node.Tags["population"]; ok {
						if pop, err := strconv.Atoi(popStr); err == nil {
							population = pop
						}
					}

					// Create a key to avoid duplicates
					key := fmt.Sprintf("node_%d", node.ID)

					// Save the settlement
					settlements[key] = Settlement{
						ID:         node.ID,
						Name:       name,
						Type:       placeType,
						Lat:        node.Lat,
						Lon:        node.Lon,
						Population: population,
						IsNode:     true,
					}

					*nodeCount++
					*totalCount++

					// Output basic information about the settlement
					log.Printf("[Node] %s: %s (%.6f, %.6f)", placeType, name, node.Lat, node.Lon)
				}
			}
		}
	}
}

// extractSettlementWays extracts settlement ways from the OSM PBF file
func extractSettlementWays(
	f *os.File,
	settlements map[string]Settlement,
	nodeCache map[int64]*osmpbf.Node,
	wayCount *int,
	totalCount *int,
) {
	// Create a decoder for ways
	decoder := osmpbf.NewDecoder(f)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)

	// Use all available CPUs for parallel processing
	numProcs := runtime.GOMAXPROCS(-1)
	decoder.Start(numProcs)

	for {
		// Decode the next object
		object, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Error decoding: %v", err)
		}

		// Process way
		if way, ok := object.(*osmpbf.Way); ok {
			// Check if the way is a settlement
			if placeType, isPlace := way.Tags["place"]; isPlace {
				if isSettlementType(placeType) {
					name := way.Tags["name"]
					if name == "" {
						name = fmt.Sprintf("Unnamed %s area", placeType)
					}

					// Extract population if specified
					population := 0
					if popStr, ok := way.Tags["population"]; ok {
						if pop, err := strconv.Atoi(popStr); err == nil {
							population = pop
						}
					}

					// Calculate the centroid of the polygon if coordinates are available
					var lat, lon float64
					if len(way.NodeIDs) > 0 {
						// Try to use the first node of the polygon to get coordinates
						if firstNode, exists := nodeCache[way.NodeIDs[0]]; exists {
							lat = firstNode.Lat
							lon = firstNode.Lon
						}
					}

					// Create a key to avoid duplicates
					key := fmt.Sprintf("way_%d", way.ID)

					// Save the settlement
					settlements[key] = Settlement{
						ID:         way.ID,
						Name:       name,
						Type:       placeType,
						Lat:        lat,
						Lon:        lon,
						Population: population,
						IsNode:     false,
						NodeIDs:    way.NodeIDs, // Save node IDs for polygon creation
					}

					*wayCount++
					*totalCount++

					// Output information about the polygonal settlement
					log.Printf("[Way] %s: %s", placeType, name)
				}
			}
		}
	}
}

// createWayPolygons creates polygons for ways
func createWayPolygons(settlements map[string]Settlement, nodeCache map[int64]*osmpbf.Node) map[string]WayPolygon {
	log.Println("Creating polygons for ways...")
	wayPolygons := make(map[string]WayPolygon)

	for key, settlement := range settlements {
		// Skip nodes, we only need to process ways
		if settlement.IsNode {
			continue
		}

		// Create polygon points from node IDs
		points := make([]orb.Point, 0, len(settlement.NodeIDs))
		isValid := true

		for _, nodeID := range settlement.NodeIDs {
			if node, exists := nodeCache[nodeID]; exists {
				// Add point to polygon (longitude, latitude)
				points = append(points, orb.Point{node.Lon, node.Lat})
			} else {
				// Missing node, polygon will be incomplete
				log.Printf("Warning: Missing node %d for way %d", nodeID, settlement.ID)
				isValid = false
			}
		}

		// Check if we have enough points for a valid polygon
		if len(points) < 3 {
			log.Printf("Warning: Not enough points for way %d, got %d", settlement.ID, len(points))
			isValid = false
		}

		// Check if the polygon is closed (first point = last point)
		if isValid && len(points) > 0 && (points[0][0] != points[len(points)-1][0] || points[0][1] != points[len(points)-1][1]) {
			// Close the polygon by adding the first point again
			points = append(points, points[0])
		}

		wayPolygons[key] = WayPolygon{
			Points:  points,
			IsValid: isValid,
		}
	}

	return wayPolygons
}

// saveSettlementsToDB saves settlements to PostgreSQL
func saveSettlementsToDB(settlements map[string]Settlement, wayPolygons map[string]WayPolygon, db *gorm.DB) {
	log.Println("Saving settlements to PostgreSQL as zones...")

	// Process in batches to improve performance
	batchSize := 100
	batch := make([]*model.ZonePG, 0, batchSize)
	savedCount := 0
	totalCount := len(settlements)

	for key, settlement := range settlements {
		// Create a unique ID for the zone
		zoneID, err := util.GenerateUniqueID(8)
		if err != nil {
			log.Printf("Failed to generate ID for settlement %s: %v", key, err)
			continue
		}

		// Create GeoJSON geometry
		var geometryJSON string

		if settlement.IsNode {
			// For nodes, create a small circular polygon
			geometryJSON, err = createCircleGeometry(settlement.Lat, settlement.Lon, getSettlementRadius(settlement.Type))
		} else {
			// For ways, use the polygon if valid
			if wayPolygon, exists := wayPolygons[key]; exists && wayPolygon.IsValid {
				geometryJSON, err = createPolygonGeometry(wayPolygon.Points)
			} else {
				// Fallback to circle if polygon is invalid
				geometryJSON, err = createCircleGeometry(settlement.Lat, settlement.Lon, getSettlementRadius(settlement.Type))
			}
		}

		if err != nil {
			log.Printf("Failed to create geometry for settlement %s: %v", key, err)
			continue
		}

		// Create zone effects based on settlement type
		effects := createSettlementEffects(settlement.Type)

		// Create a zone record
		zone := &model.ZonePG{
			ID:       zoneID,
			Name:     settlement.Name,
			Type:     settlement.Type,
			State:    model.ZoneStateActive,
			Geometry: geometryJSON,
			Effects:  effects,

			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		// Add to batch
		batch = append(batch, zone)

		// If batch is full or this is the last item, save the batch
		if len(batch) >= batchSize || len(batch) > 0 && savedCount+len(batch) >= totalCount {
			result := db.CreateInBatches(batch, batchSize)
			if result.Error != nil {
				log.Printf("Error saving batch: %v", result.Error)
			} else {
				savedCount += len(batch)
				log.Printf("Saved %d zones (total: %d/%d)", len(batch), savedCount, totalCount)
			}

			// Clear batch
			batch = make([]*model.ZonePG, 0, batchSize)
		}
	}

	log.Printf("Saved %d settlements as zones", savedCount)
	log.Println("Processing complete!")
}

// isSettlementType checks if the type is one of the main types of settlements
func isSettlementType(placeType string) bool {
	switch placeType {
	case "city", "town", "village", "hamlet":
		// case "city", "town", "village", "hamlet", "suburb", "neighbourhood", "quarter", "borough":
		return true
	default:
		return false
	}
}

// createCircleGeometry creates a circular polygon GeoJSON around a point
func createCircleGeometry(lat, lon, radiusMeters float64) (string, error) {
	// Create a circle with 16 points
	numPoints := 16
	circle := make(orb.Ring, numPoints+1)

	for i := 0; i < numPoints; i++ {
		angle := 2 * float64(i) * 3.14159 / float64(numPoints)
		// Calculate point at angle and distance
		// Note: This is a simplified approach that works for small circles
		// For more accurate circles, use proper geodesic calculations
		dx := radiusMeters * 0.000008998 * math.Cos(angle) // ~111km per degree
		dy := radiusMeters * 0.000008998 * math.Sin(angle) * math.Cos(lat*3.14159/180)

		circle[i] = orb.Point{lon + dx, lat + dy}
	}

	// Close the ring
	circle[numPoints] = circle[0]

	// Create a polygon from the ring
	polygon := orb.Polygon{circle}

	// Create a GeoJSON feature
	feature := geojson.NewFeature(polygon)

	// Marshal to JSON string
	bytes, err := json.Marshal(feature)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

// createPolygonGeometry creates a polygon GeoJSON from points
func createPolygonGeometry(points []orb.Point) (string, error) {
	// Create a ring from points
	ring := orb.Ring(points)

	// Create a polygon from the ring
	polygon := orb.Polygon{ring}

	// Create a GeoJSON feature
	feature := geojson.NewFeature(polygon)

	// Marshal to JSON string
	bytes, err := json.Marshal(feature)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

// getSettlementRadius returns an appropriate radius for a settlement type
func getSettlementRadius(settlementType string) float64 {
	// Return radius in meters based on settlement type
	switch settlementType {
	case "city":
		return 5000.0
	case "town":
		return 2000.0
	case "village":
		return 1000.0
	case "hamlet":
		return 500.0
	// case "suburb", "neighbourhood", "quarter", "borough":
	// 	return 800.0
	default:
		return 500.0
	}
}

// createSettlementEffects creates effects based on settlement type
func createSettlementEffects(settlementType string) []model.ZoneEffect {
	effects := make([]model.ZoneEffect, 0)

	// Add effects based on settlement type
	switch settlementType {
	case "city":
		effects = append(effects, model.ZoneEffect{
			EffectType:   model.EffectTypeBuff,
			ResourceType: model.TargetParamTypeStamina,
			Value:        10.0,
		})
	case "town":
		effects = append(effects, model.ZoneEffect{
			EffectType:   model.EffectTypeBuff,
			ResourceType: model.TargetParamTypeStamina,
			Value:        5.0,
		})
	case "village", "hamlet":
		effects = append(effects, model.ZoneEffect{
			EffectType:   model.EffectTypeBuff,
			ResourceType: model.TargetParamTypeHealth,
			Value:        3.0,
		})
	}

	return effects
}
