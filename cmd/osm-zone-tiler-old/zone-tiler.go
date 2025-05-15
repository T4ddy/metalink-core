package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"metalink/internal/model"
	"metalink/internal/postgres"
	"metalink/internal/util"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dhconnelly/rtreego"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/qedus/osmpbf"
	"gorm.io/gorm"
)

// OSMObject represents any OSM object of interest for game mechanics
type OSMObject struct {
	ID       int64
	Type     string // building, water, amenity, etc.
	SubType  string // residential, river, parking, etc.
	Tags     map[string]string
	Lat, Lon float64
	NodeIDs  []int64 // For ways
	IsNode   bool
	Points   []orb.Point // For polygons
	IsValid  bool        // Indicates if polygon is valid
}

// GameTile represents a tile in our game grid
type GameTile struct {
	ID       string
	Lat, Lon float64
	Size     float64 // Size in meters
	Objects  []int64 // IDs of objects within this tile
	Effects  []TileEffect
	Geometry orb.Polygon
}

// TileEffect represents an effect applied to a tile
type TileEffect struct {
	EffectType   model.EffectType
	ResourceType model.TargetParamType
	Value        float32
	SourceObject string  // Object type that creates this effect
	Distance     float64 // Distance to the source object
}

// SpatialObject implements rtreego.Spatial interface for spatial indexing
type SpatialObject struct {
	Object *OSMObject
	Rect   *rtreego.Rect // Bounding box for spatial indexing
}

// Bounds returns the bounding box of the object for rtreego
func (so SpatialObject) Bounds() rtreego.Rect {
	return *so.Rect
}

// Config holds the configuration for the zone tiler
type Config struct {
	OSMFile           string
	DBURL             string
	MinTileSize       float64 // Minimum tile size in meters
	MaxTileSize       float64 // Maximum tile size in meters
	InfluenceRadius   float64 // Maximum radius of influence for objects
	ExportGeoJSON     bool    // Whether to export tiles to GeoJSON
	OutputFile        string  // Output file for GeoJSON
	ExportObjects     bool    // Whether to export objects to GeoJSON
	ObjectsOutputFile string  // Output file for objects GeoJSON
}

func main() {
	// Parse command line arguments
	config := parseCommandLineArgs()

	// Initialize database connection
	// db := initDatabase(config.DBURL)

	// Step 1: Load the OSM file
	log.Println("Step 1: Loading OSM file...")
	f, err := os.Open(config.OSMFile)
	if err != nil {
		log.Fatalf("Failed to open OSM file: %v", err)
	}
	defer f.Close()

	// Step 2: Load all objects and build a spatial index
	log.Println("Step 2: Loading objects and building spatial index...")
	objects, spatialIndex := loadObjectsAndBuildIndex(f, config)
	log.Printf("Loaded %d objects into spatial index", len(objects))

	// Export objects to GeoJSON for visualization
	// if config.ExportObjects {
	// 	log.Println("Exporting buildings to GeoJSON...")
	// 	buildingsOutputFile := fmt.Sprintf("%s_buildings.geojson", filepath.Base(config.OSMFile[:len(config.OSMFile)-len(filepath.Ext(config.OSMFile))]))
	// 	exportBuildingsToGeoJSON(objects, buildingsOutputFile)
	// }

	// Step 3: Build dynamic sized grid based on density and type of object
	log.Println("Step 3: Building dynamic grid based on object density...")
	tiles := buildDynamicGrid(objects, spatialIndex, config)
	log.Printf("Created %d tiles in dynamic grid", len(tiles))

	// Step 4: Iterate over the grid and calculate buff/debuff for each tile
	log.Println("Step 4: Calculating effects for each tile...")
	calculateTileEffects(tiles, objects, spatialIndex, config)

	// Step 5: Based on this grid create zones and save them to DB
	// log.Println("Step 5: Saving tiles as zones to database...")
	// saveTilesToDB(tiles, db)

	// Optional: Export tiles to GeoJSON for visualization
	if config.ExportGeoJSON {
		log.Println("Exporting tiles to GeoJSON...")
		exportTilesToGeoJSON(tiles, objects, config.OutputFile)
	}

	log.Println("Zone tiler completed successfully!")
}

// parseCommandLineArgs parses command line arguments
func parseCommandLineArgs() Config {
	osmFile := flag.String("osm", "", "Path to OSM PBF file")
	dbURL := flag.String("db", "postgres://postgres:postgres@localhost:5432/metalink", "Database URL")
	minTileSize := flag.Float64("min-tile", 50.0, "Minimum tile size in meters")
	maxTileSize := flag.Float64("max-tile", 200.0, "Maximum tile size in meters")
	influenceRadius := flag.Float64("radius", 200.0, "Maximum radius of influence for objects in meters")
	exportGeoJSON := flag.Bool("export", true, "Export tiles to GeoJSON")
	outputFile := flag.String("output", "", "Output file for GeoJSON (default: <osm-file>_tiles.geojson)")
	exportObjects := flag.Bool("export-objects", true, "Export objects to GeoJSON")
	objectsOutputFile := flag.String("objects-output", "", "Output file for objects GeoJSON (default: <osm-file>_objects.geojson)")

	flag.Parse()

	if *osmFile == "" {
		log.Fatal("OSM file path is required")
	}

	// Set default output file if not specified
	if *exportGeoJSON && *outputFile == "" {
		baseFileName := filepath.Base(*osmFile)
		ext := filepath.Ext(baseFileName)
		*outputFile = fmt.Sprintf("%s_tiles.geojson", baseFileName[:len(baseFileName)-len(ext)])
	}

	// Set default objects output file if not specified
	if *exportObjects && *objectsOutputFile == "" {
		baseFileName := filepath.Base(*osmFile)
		ext := filepath.Ext(baseFileName)
		*objectsOutputFile = fmt.Sprintf("%s_objects.geojson", baseFileName[:len(baseFileName)-len(ext)])
	}

	return Config{
		OSMFile:           *osmFile,
		DBURL:             *dbURL,
		MinTileSize:       *minTileSize,
		MaxTileSize:       *maxTileSize,
		InfluenceRadius:   *influenceRadius,
		ExportGeoJSON:     *exportGeoJSON,
		OutputFile:        *outputFile,
		ExportObjects:     *exportObjects,
		ObjectsOutputFile: *objectsOutputFile,
	}
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

// loadObjectsAndBuildIndex loads all objects from OSM and builds a spatial index
func loadObjectsAndBuildIndex(f *os.File, config Config) (map[int64]*OSMObject, *rtreego.Rtree) {
	// Create a decoder
	decoder := osmpbf.NewDecoder(f)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)

	// Use all available CPUs for parallel processing
	numProcs := runtime.GOMAXPROCS(-1)
	decoder.Start(numProcs)

	// Maps to store objects and nodes
	objects := make(map[int64]*OSMObject)
	nodeCache := make(map[int64]*osmpbf.Node)

	// First pass: collect all nodes
	log.Println("First pass: collecting nodes...")
	collectNodes(decoder, nodeCache, objects)

	// Rewind the file for second pass
	f.Seek(0, 0)
	decoder = osmpbf.NewDecoder(f)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)
	decoder.Start(numProcs)

	// Second pass: collect all ways
	log.Println("Second pass: collecting ways...")
	collectWays(decoder, nodeCache, objects)

	// Build spatial index
	log.Println("Building spatial index...")
	spatialIndex := rtreego.NewTree(2, 25, 50)

	// Add objects to spatial index
	for _, obj := range objects {
		// Create a bounding box for the object
		var rect rtreego.Rect
		var err error

		if obj.IsNode {
			// For nodes, create a small rectangle around the point
			point := rtreego.Point{obj.Lon, obj.Lat}
			rect, err = rtreego.NewRect(point, []float64{0.0001, 0.0001})
		} else if len(obj.Points) > 0 {
			// For ways, calculate the bounding box of all points
			minX, minY := math.MaxFloat64, math.MaxFloat64
			maxX, maxY := -math.MaxFloat64, -math.MaxFloat64

			for _, point := range obj.Points {
				minX = math.Min(minX, point[0])
				minY = math.Min(minY, point[1])
				maxX = math.Max(maxX, point[0])
				maxY = math.Max(maxY, point[1])
			}

			// Create rectangle from bounds
			point := rtreego.Point{minX, minY}
			sizes := []float64{maxX - minX, maxY - minY}
			rect, err = rtreego.NewRect(point, sizes)
		}

		if err == nil {
			spatialObject := SpatialObject{
				Object: obj,
				Rect:   &rect,
			}
			spatialIndex.Insert(spatialObject)
		}
	}

	return objects, spatialIndex
}

// collectNodes collects all nodes of interest from OSM
func collectNodes(decoder *osmpbf.Decoder, nodeCache map[int64]*osmpbf.Node, objects map[int64]*OSMObject) {
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
			// Cache all nodes for later use with ways
			nodeCache[node.ID] = node

			// Check if the node has tags of interest
			if isNodeOfInterest(node) {
				objType, subType := getObjectType(node.Tags)

				// Create OSM object
				objects[node.ID] = &OSMObject{
					ID:      node.ID,
					Type:    objType,
					SubType: subType,
					Tags:    node.Tags,
					Lat:     node.Lat,
					Lon:     node.Lon,
					IsNode:  true,
				}
			}
		}
	}
}

// collectWays collects all ways of interest from OSM
func collectWays(decoder *osmpbf.Decoder, nodeCache map[int64]*osmpbf.Node, objects map[int64]*OSMObject) {
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
			// Check if the way has tags of interest
			if isWayOfInterest(way) {
				objType, subType := getObjectType(way.Tags)

				// Create polygon points from node IDs
				points := make([]orb.Point, 0, len(way.NodeIDs))
				isValid := true
				var lat, lon float64

				for _, nodeID := range way.NodeIDs {
					if node, exists := nodeCache[nodeID]; exists {
						// Add point to polygon (longitude, latitude)
						points = append(points, orb.Point{node.Lon, node.Lat})

						// Use first node coordinates for the object location
						if lat == 0 && lon == 0 {
							lat = node.Lat
							lon = node.Lon
						}
					} else {
						isValid = false
					}
				}

				// Check if we have enough points for a valid polygon
				if len(points) < 3 {
					isValid = false
				}

				// Check if the polygon is closed (first point = last point)
				if isValid && len(points) > 0 && (points[0][0] != points[len(points)-1][0] || points[0][1] != points[len(points)-1][1]) {
					// Close the polygon by adding the first point again
					points = append(points, points[0])
				}

				// Create OSM object
				objects[way.ID] = &OSMObject{
					ID:      way.ID,
					Type:    objType,
					SubType: subType,
					Tags:    way.Tags,
					Lat:     lat,
					Lon:     lon,
					NodeIDs: way.NodeIDs,
					IsNode:  false,
					Points:  points,
					IsValid: isValid,
				}
			}
		}
	}
}

// isNodeOfInterest checks if a node has tags that are interesting for game mechanics
func isNodeOfInterest(node *osmpbf.Node) bool {
	// Check for amenities, shops, etc.
	if _, ok := node.Tags["amenity"]; ok {
		return true
	}
	if _, ok := node.Tags["shop"]; ok {
		return true
	}
	if _, ok := node.Tags["natural"]; ok {
		return true
	}
	if _, ok := node.Tags["leisure"]; ok {
		return true
	}
	if _, ok := node.Tags["emergency"]; ok {
		return true
	}
	if _, ok := node.Tags["tourism"]; ok {
		return true
	}
	if _, ok := node.Tags["historic"]; ok {
		return true
	}

	return false
}

// isWayOfInterest checks if a way has tags that are interesting for game mechanics
func isWayOfInterest(way *osmpbf.Way) bool {
	// Check for buildings, waterways, etc.
	if _, ok := way.Tags["building"]; ok {
		return true
	}
	if _, ok := way.Tags["highway"]; ok {
		return true
	}
	if _, ok := way.Tags["waterway"]; ok {
		return true
	}
	if _, ok := way.Tags["natural"]; ok {
		return true
	}
	if _, ok := way.Tags["landuse"]; ok {
		return true
	}
	if _, ok := way.Tags["leisure"]; ok {
		return true
	}
	if _, ok := way.Tags["amenity"]; ok {
		return true
	}

	return false
}

// getObjectType determines the type and subtype of an object based on its tags
func getObjectType(tags map[string]string) (string, string) {
	// Check for building
	if buildingType, ok := tags["building"]; ok {
		return "building", buildingType
	}

	// Check for amenity
	if amenityType, ok := tags["amenity"]; ok {
		return "amenity", amenityType
	}

	// Check for shop
	if shopType, ok := tags["shop"]; ok {
		return "shop", shopType
	}

	// Check for highway
	if highwayType, ok := tags["highway"]; ok {
		return "highway", highwayType
	}

	// Check for waterway
	if waterwayType, ok := tags["waterway"]; ok {
		return "waterway", waterwayType
	}

	// Check for natural
	if naturalType, ok := tags["natural"]; ok {
		return "natural", naturalType
	}

	// Check for landuse
	if landuseType, ok := tags["landuse"]; ok {
		return "landuse", landuseType
	}

	// Check for leisure
	if leisureType, ok := tags["leisure"]; ok {
		return "leisure", leisureType
	}

	// Default
	return "unknown", "unknown"
}

// buildDynamicGrid creates a dynamic grid based on object density
func buildDynamicGrid(objects map[int64]*OSMObject, spatialIndex *rtreego.Rtree, config Config) []*GameTile {
	// Find the bounds of all objects
	minLat, minLon := math.MaxFloat64, math.MaxFloat64
	maxLat, maxLon := -math.MaxFloat64, -math.MaxFloat64

	for _, obj := range objects {
		minLat = math.Min(minLat, obj.Lat)
		minLon = math.Min(minLon, obj.Lon)
		maxLat = math.Max(maxLat, obj.Lat)
		maxLon = math.Max(maxLon, obj.Lon)
	}

	// Add a small buffer
	minLat -= 0.01
	minLon -= 0.01
	maxLat += 0.01
	maxLon += 0.01

	log.Printf("Area bounds: [%f, %f] to [%f, %f]", minLat, minLon, maxLat, maxLon)

	// Create quadtree-like grid
	tiles := createQuadTreeGrid(minLat, minLon, maxLat, maxLon, objects, spatialIndex, config)

	return tiles
}

// createQuadTreeGrid creates a quadtree-like grid with adaptive tile sizes
func createQuadTreeGrid(minLat, minLon, maxLat, maxLon float64, objects map[int64]*OSMObject, spatialIndex *rtreego.Rtree, config Config) []*GameTile {
	tiles := make([]*GameTile, 0)

	// Start with a single tile covering the entire area
	initialTile := &GameTile{
		Lat:  (minLat + maxLat) / 2,
		Lon:  (minLon + maxLon) / 2,
		Size: math.Max(haversineDistance(minLat, minLon, maxLat, maxLon)/math.Sqrt2, config.MaxTileSize),
	}

	// Recursively subdivide tiles based on density
	subdivideTile(initialTile, minLat, minLon, maxLat, maxLon, objects, spatialIndex, &tiles, config)

	// Generate unique IDs for tiles
	for i, tile := range tiles {
		id, err := util.GenerateUniqueID(8)
		if err != nil {
			id = fmt.Sprintf("tile_%d", i)
		}
		tile.ID = id

		// Create polygon geometry for the tile
		tile.Geometry = createTilePolygon(tile.Lat, tile.Lon, tile.Size)
	}

	return tiles
}

// subdivideTile recursively subdivides a tile based on object density
func subdivideTile(tile *GameTile, minLat, minLon, maxLat, maxLon float64, objects map[int64]*OSMObject, spatialIndex *rtreego.Rtree, tiles *[]*GameTile, config Config) {
	// Calculate the density of objects in this tile
	objectCount := countObjectsInTile(tile.Lat, tile.Lon, tile.Size, spatialIndex)

	// Decide whether to subdivide based on density and minimum tile size
	if objectCount > 10 && tile.Size > 2*config.MinTileSize {
		// Subdivide into 4 quadrants
		halfSize := tile.Size / 2

		// Northwest quadrant
		nwTile := &GameTile{
			Lat:  tile.Lat + (halfSize / 111000),
			Lon:  tile.Lon - (halfSize / (111000 * math.Cos(tile.Lat*math.Pi/180))),
			Size: halfSize,
		}
		subdivideTile(nwTile, tile.Lat, minLon, maxLat, tile.Lon, objects, spatialIndex, tiles, config)

		// Northeast quadrant
		neTile := &GameTile{
			Lat:  tile.Lat + (halfSize / 111000),
			Lon:  tile.Lon + (halfSize / (111000 * math.Cos(tile.Lat*math.Pi/180))),
			Size: halfSize,
		}
		subdivideTile(neTile, tile.Lat, tile.Lon, maxLat, maxLon, objects, spatialIndex, tiles, config)

		// Southwest quadrant
		swTile := &GameTile{
			Lat:  tile.Lat - (halfSize / 111000),
			Lon:  tile.Lon - (halfSize / (111000 * math.Cos(tile.Lat*math.Pi/180))),
			Size: halfSize,
		}
		subdivideTile(swTile, minLat, minLon, tile.Lat, tile.Lon, objects, spatialIndex, tiles, config)

		// Southeast quadrant
		seTile := &GameTile{
			Lat:  tile.Lat - (halfSize / 111000),
			Lon:  tile.Lon + (halfSize / (111000 * math.Cos(tile.Lat*math.Pi/180))),
			Size: halfSize,
		}
		subdivideTile(seTile, minLat, tile.Lon, tile.Lat, maxLon, objects, spatialIndex, tiles, config)
	} else {
		// This tile is small enough or has few enough objects, add it to the final list
		*tiles = append(*tiles, tile)
	}
}

// countObjectsInTile counts the number of objects within a tile
func countObjectsInTile(lat, lon, size float64, spatialIndex *rtreego.Rtree) int {
	// Convert tile coordinates to a rectangle for spatial query
	// Convert meters to degrees approximately
	latDelta := size / 111000                               // 111km per degree of latitude
	lonDelta := size / (111000 * math.Cos(lat*math.Pi/180)) // Adjust for longitude based on latitude

	minLat := lat - latDelta
	minLon := lon - lonDelta
	maxLat := lat + latDelta
	maxLon := lon + lonDelta

	point := rtreego.Point{minLon, minLat}
	rect, err := rtreego.NewRect(point, []float64{maxLon - minLon, maxLat - minLat})
	if err != nil {
		return 0
	}

	// Query the spatial index
	results := spatialIndex.SearchIntersect(rect)
	return len(results)
}

// createTilePolygon creates a polygon for a tile
func createTilePolygon(lat, lon, size float64) orb.Polygon {
	// Convert meters to degrees approximately
	latDelta := size / 111000                               // 111km per degree of latitude
	lonDelta := size / (111000 * math.Cos(lat*math.Pi/180)) // Adjust for longitude based on latitude

	// Create a square polygon
	ring := orb.Ring{
		{lon - lonDelta, lat - latDelta}, // SW
		{lon + lonDelta, lat - latDelta}, // SE
		{lon + lonDelta, lat + latDelta}, // NE
		{lon - lonDelta, lat + latDelta}, // NW
		{lon - lonDelta, lat - latDelta}, // Close the ring
	}

	return orb.Polygon{ring}
}

// calculateTileEffects calculates effects for each tile based on nearby objects
func calculateTileEffects(tiles []*GameTile, objects map[int64]*OSMObject, spatialIndex *rtreego.Rtree, config Config) {
	for _, tile := range tiles {
		// Find objects within influence radius
		nearbyObjects := findNearbyObjects(tile.Lat, tile.Lon, config.InfluenceRadius, spatialIndex)

		// Store object IDs for reference
		tile.Objects = make([]int64, 0, len(nearbyObjects))
		for _, obj := range nearbyObjects {
			tile.Objects = append(tile.Objects, obj.ID)
		}

		// Calculate effects from each object
		tile.Effects = make([]TileEffect, 0)

		for _, obj := range nearbyObjects {
			// Calculate distance to object
			distance := haversineDistance(tile.Lat, tile.Lon, obj.Lat, obj.Lon)

			// Only consider objects within influence radius
			if distance <= config.InfluenceRadius {
				// Calculate effects based on object type
				effects := calculateObjectEffects(obj, distance, config.InfluenceRadius)
				tile.Effects = append(tile.Effects, effects...)
			}
		}
	}
}

// findNearbyObjects finds objects within a given radius of a point
func findNearbyObjects(lat, lon, radius float64, spatialIndex *rtreego.Rtree) []*OSMObject {
	// Convert radius to degrees approximately
	latDelta := radius / 111000                               // 111km per degree of latitude
	lonDelta := radius / (111000 * math.Cos(lat*math.Pi/180)) // Adjust for longitude based on latitude

	// Create a search rectangle
	point := rtreego.Point{lon - lonDelta, lat - latDelta}
	rect, err := rtreego.NewRect(point, []float64{2 * lonDelta, 2 * latDelta})
	if err != nil {
		return nil
	}

	// Query the spatial index
	results := spatialIndex.SearchIntersect(rect)

	// Extract OSM objects from spatial objects
	objects := make([]*OSMObject, 0, len(results))
	for _, result := range results {
		spatialObj := result.(SpatialObject)
		objects = append(objects, spatialObj.Object)
	}

	return objects
}

// calculateObjectEffects calculates effects based on object type and distance
func calculateObjectEffects(obj *OSMObject, distance, maxRadius float64) []TileEffect {
	effects := make([]TileEffect, 0)

	// Calculate influence factor based on distance (closer = stronger effect)
	influenceFactor := 1.0 - (distance / maxRadius)
	if influenceFactor <= 0 {
		return effects
	}

	// Different effects based on object type
	switch obj.Type {
	case "building":
		// Different effects based on building type
		switch obj.SubType {
		case "residential", "house", "detached", "apartments":
			// Food resources
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeHealth, // Assuming food -> health
				Value:        float32(15.0 * influenceFactor),
				SourceObject: "residential",
				Distance:     distance,
			})

		case "commercial", "retail":
			// More food resources
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeHealth,
				Value:        float32(25.0 * influenceFactor),
				SourceObject: "commercial",
				Distance:     distance,
			})

		case "industrial", "warehouse":
			// Materials
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeStrength,
				Value:        float32(20.0 * influenceFactor),
				SourceObject: "industrial",
				Distance:     distance,
			})
		}

	case "shop":
		switch obj.SubType {
		case "supermarket", "convenience":
			// Food
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeHealth,
				Value:        float32(50.0 * influenceFactor),
				SourceObject: "supermarket",
				Distance:     distance,
			})

		case "hardware", "doityourself":
			// Materials
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeStrength,
				Value:        float32(40.0 * influenceFactor),
				SourceObject: "hardware",
				Distance:     distance,
			})
		}

	case "amenity":
		switch obj.SubType {
		case "parking":
			// Car parts
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeStrength,
				Value:        float32(20.0 * influenceFactor),
				SourceObject: "parking",
				Distance:     distance,
			})

		case "hospital", "pharmacy":
			// Medical supplies
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeHealth,
				Value:        float32(60.0 * influenceFactor),
				SourceObject: "medical",
				Distance:     distance,
			})

		case "restaurant", "fast_food", "cafe":
			// Food
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeHealth,
				Value:        float32(30.0 * influenceFactor),
				SourceObject: "restaurant",
				Distance:     distance,
			})
		}

	case "natural":
		switch obj.SubType {
		case "water":
			// Water resources
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeStamina,
				Value:        float32(50.0 * influenceFactor),
				SourceObject: "water",
				Distance:     distance,
			})

		case "forest", "wood":
			// Shelter and resources
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeHealth,
				Value:        float32(15.0 * influenceFactor),
				SourceObject: "forest",
				Distance:     distance,
			})
		}

	case "waterway":
		// Water resources
		effects = append(effects, TileEffect{
			EffectType:   model.EffectTypeBuff,
			ResourceType: model.TargetParamTypeStamina,
			Value:        float32(40.0 * influenceFactor),
			SourceObject: "waterway",
			Distance:     distance,
		})

	case "highway":
		switch obj.SubType {
		case "motorway", "trunk", "primary", "secondary":
			// Fast travel but dangerous
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeStamina,
				Value:        float32(10.0 * influenceFactor),
				SourceObject: "major_road",
				Distance:     distance,
			})
			// Also add small debuff for danger
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeDebuff,
				ResourceType: model.TargetParamTypeHealth,
				Value:        float32(5.0 * influenceFactor),
				SourceObject: "major_road_danger",
				Distance:     distance,
			})

		case "residential", "service", "unclassified":
			// Easier to travel
			effects = append(effects, TileEffect{
				EffectType:   model.EffectTypeBuff,
				ResourceType: model.TargetParamTypeStamina,
				Value:        float32(5.0 * influenceFactor),
				SourceObject: "minor_road",
				Distance:     distance,
			})
		}
	}

	return effects
}

// saveTilesToDB saves tiles as zones to the database
func saveTilesToDB(tiles []*GameTile, db *gorm.DB) {
	log.Println("Saving tiles to database...")

	// Process in batches to improve performance
	batchSize := 100
	batch := make([]*model.ZonePG, 0, batchSize)
	savedCount := 0
	totalCount := len(tiles)

	for _, tile := range tiles {
		// Create GeoJSON geometry from tile polygon
		geometryJSON, err := createGeometryJSON(tile.Geometry)
		if err != nil {
			log.Printf("Failed to create geometry for tile %s: %v", tile.ID, err)
			continue
		}

		// Combine all effects into a single list
		effects := combineEffects(tile.Effects)

		// Create a zone record
		zone := &model.ZonePG{
			ID:       tile.ID,
			Name:     fmt.Sprintf("Tile %s", tile.ID[:6]),
			Type:     "game_tile",
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

	log.Printf("Saved %d tiles as zones", savedCount)
}

// combineEffects combines multiple effects of the same type and resource
func combineEffects(effects []TileEffect) []model.ZoneEffect {
	// Map to track combined effects
	effectMap := make(map[string]model.ZoneEffect)

	for _, effect := range effects {
		// Create a key based on effect type and resource type
		key := fmt.Sprintf("%d_%d", effect.EffectType, effect.ResourceType)

		// If we already have this effect type, add the values
		if existing, ok := effectMap[key]; ok {
			existing.Value += effect.Value
			effectMap[key] = existing
		} else {
			// Create a new effect
			effectMap[key] = model.ZoneEffect{
				EffectType:   effect.EffectType,
				ResourceType: effect.ResourceType,
				Value:        effect.Value,
			}
		}
	}

	// Convert map to slice
	result := make([]model.ZoneEffect, 0, len(effectMap))
	for _, effect := range effectMap {
		result = append(result, effect)
	}

	return result
}

// createGeometryJSON creates a GeoJSON string from a polygon
func createGeometryJSON(polygon orb.Polygon) (string, error) {
	// Create a GeoJSON feature
	feature := geojson.NewFeature(polygon)

	// Marshal to JSON string
	bytes, err := json.Marshal(feature)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

// exportTilesToGeoJSON exports tiles to a GeoJSON file for visualization
func exportTilesToGeoJSON(tiles []*GameTile, objects map[int64]*OSMObject, outputFile string) {
	log.Printf("Exporting %d tiles to GeoJSON file: %s", len(tiles), outputFile)

	// Create a GeoJSON FeatureCollection
	fc := geojson.NewFeatureCollection()

	// Add each tile as a feature
	for _, tile := range tiles {
		// Create a feature from the tile polygon
		feature := geojson.NewFeature(tile.Geometry)

		// Add properties
		feature.Properties["id"] = tile.ID
		feature.Properties["size"] = tile.Size

		// Add effect information
		effectsInfo := make(map[string]float32)
		for _, effect := range tile.Effects {
			effectName := fmt.Sprintf("%s_%s",
				getEffectTypeName(effect.EffectType),
				getResourceTypeName(effect.ResourceType))

			if val, ok := effectsInfo[effectName]; ok {
				effectsInfo[effectName] = val + effect.Value
			} else {
				effectsInfo[effectName] = effect.Value
			}
		}

		for name, value := range effectsInfo {
			feature.Properties[name] = value
		}

		// Add the feature to the collection
		fc.Append(feature)
	}

	// Marshal the FeatureCollection to JSON
	jsonData, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal GeoJSON: %v", err)
	}

	// Write to file
	err = os.WriteFile(outputFile, jsonData, 0644)
	if err != nil {
		log.Fatalf("Failed to write GeoJSON file: %v", err)
	}

	log.Printf("Successfully exported tiles to %s", outputFile)
}

// getEffectTypeName returns a string name for an effect type
func getEffectTypeName(effectType model.EffectType) string {
	switch effectType {
	case model.EffectTypeBuff:
		return "buff"
	case model.EffectTypeDebuff:
		return "debuff"
	default:
		return "unknown"
	}
}

// getResourceTypeName returns a string name for a resource type
func getResourceTypeName(resourceType model.TargetParamType) string {
	switch resourceType {
	case model.TargetParamTypeHealth:
		return "health"
	case model.TargetParamTypeStamina:
		return "stamina"
	case model.TargetParamTypeStrength:
		return "strength"
	default:
		return "unknown"
	}
}

// haversineDistance calculates the great-circle distance between two points
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	// Convert latitude and longitude from degrees to radians
	lat1 = lat1 * math.Pi / 180
	lon1 = lon1 * math.Pi / 180
	lat2 = lat2 * math.Pi / 180
	lon2 = lon2 * math.Pi / 180

	// Haversine formula
	dLat := lat2 - lat1
	dLon := lon2 - lon1
	a := math.Pow(math.Sin(dLat/2), 2) + math.Cos(lat1)*math.Cos(lat2)*math.Pow(math.Sin(dLon/2), 2)
	c := 2 * math.Asin(math.Sqrt(a))

	// Earth radius in meters
	r := 6371000.0

	// Calculate the distance
	return c * r
}

// exportBuildingsToGeoJSON exports only building objects to a GeoJSON file for visualization
func exportBuildingsToGeoJSON(objects map[int64]*OSMObject, outputFile string) {
	log.Printf("Exporting building objects to GeoJSON file: %s", outputFile)

	// Create a GeoJSON FeatureCollection
	fc := geojson.NewFeatureCollection()

	// Add each object as a feature
	for _, obj := range objects {
		// Only include buildings
		if obj.Type != "building" {
			continue
		}

		var feature *geojson.Feature

		if obj.IsNode {
			// Create a point feature for nodes
			point := orb.Point{obj.Lon, obj.Lat}
			feature = geojson.NewFeature(point)
		} else if obj.IsValid && len(obj.Points) > 0 {
			// Create a polygon feature for ways
			if len(obj.Points) >= 3 {
				// It's a polygon
				polygon := orb.Polygon{obj.Points}
				feature = geojson.NewFeature(polygon)
			} else {
				// Skip invalid buildings
				continue
			}
		} else {
			// Skip invalid objects
			continue
		}

		// Add properties with basic information
		feature.Properties["id"] = obj.ID
		feature.Properties["type"] = obj.Type
		feature.Properties["subtype"] = obj.SubType

		// Add name if available
		if name, ok := obj.Tags["name"]; ok {
			feature.Properties["name"] = name
		}

		// Add the feature to the collection
		fc.Append(feature)
	}

	// Marshal the FeatureCollection to JSON
	jsonData, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal GeoJSON: %v", err)
	}

	// Write to file
	err = os.WriteFile(outputFile, jsonData, 0644)
	if err != nil {
		log.Fatalf("Failed to write GeoJSON file: %v", err)
	}

	log.Printf("Successfully exported %d building objects to %s",
		len(fc.Features), outputFile)
}
