package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"metalink/internal/model"
	pg "metalink/internal/postgres"

	"github.com/dhconnelly/rtreego"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geo"
	"github.com/qedus/osmpbf"
	"gorm.io/gorm"
)

// OSMProcessor handles processing of OSM PBF files
type OSMProcessor struct {
	Buildings      []*model.Building
	SpatialIndex   *rtreego.Rtree
	ProcessedNodes map[int64]orb.Point
	mutex          sync.Mutex
}

// NewOSMProcessor creates a new OSM processor
func NewOSMProcessor() *OSMProcessor {
	return &OSMProcessor{
		Buildings:      make([]*model.Building, 0),
		SpatialIndex:   rtreego.NewTree(2, 25, 50), // 2D index with min 25, max 50 entries per node
		ProcessedNodes: make(map[int64]orb.Point),
	}
}

// ProcessOSMFile processes an OSM PBF file and extracts buildings
func (p *OSMProcessor) ProcessOSMFile(osmFilePath string) error {
	log.Printf("Processing OSM file: %s", osmFilePath)

	// Open the OSM PBF file
	file, err := os.Open(osmFilePath)
	if err != nil {
		return fmt.Errorf("failed to open OSM file: %w", err)
	}
	defer file.Close()

	// Create a new decoder
	decoder := osmpbf.NewDecoder(file)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)

	// Use all available CPU cores
	decoder.Start(runtime.GOMAXPROCS(-1))

	// First pass: collect all nodes
	log.Println("First pass: collecting nodes...")
	if err := p.collectNodes(decoder); err != nil {
		return err
	}

	// Rewind the file for the second pass
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to rewind OSM file: %w", err)
	}

	// Reset and restart the decoder
	decoder = osmpbf.NewDecoder(file)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)
	decoder.Start(runtime.GOMAXPROCS(-1))

	// Second pass: process ways (buildings)
	log.Println("Second pass: processing buildings...")
	if err := p.processBuildings(decoder); err != nil {
		return err
	}

	log.Printf("Processing complete. Found %d buildings.", len(p.Buildings))
	return nil
}

// collectNodes collects all nodes from the OSM file
func (p *OSMProcessor) collectNodes(decoder *osmpbf.Decoder) error {
	var nodeCount int

	for {
		// Get the next OSM object
		obj, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error decoding OSM data: %w", err)
		}

		// Process only nodes
		if node, ok := obj.(*osmpbf.Node); ok {
			p.mutex.Lock()
			p.ProcessedNodes[node.ID] = orb.Point{node.Lon, node.Lat}
			p.mutex.Unlock()
			nodeCount++

			// Log progress periodically
			if nodeCount%1000000 == 0 {
				log.Printf("Processed %d nodes...", nodeCount)
			}
		}
	}

	log.Printf("Collected %d nodes", nodeCount)
	return nil
}

// processBuildings processes building ways from the OSM file
func (p *OSMProcessor) processBuildings(decoder *osmpbf.Decoder) error {
	var buildingCount int

	for {
		// Get the next OSM object
		obj, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error decoding OSM data: %w", err)
		}

		// Process only ways
		if way, ok := obj.(*osmpbf.Way); ok {
			// Check if this way is a building
			if isBuildingTag, ok := way.Tags["building"]; ok && isBuildingTag != "no" {
				building := p.processBuilding(way)
				if building != nil {
					p.mutex.Lock()
					p.Buildings = append(p.Buildings, building)

					// Add to spatial index
					p.SpatialIndex.Insert(&model.BuildingSpatial{Building: building})
					p.mutex.Unlock()

					buildingCount++

					// Log progress periodically
					if buildingCount%10000 == 0 {
						log.Printf("Processed %d buildings...", buildingCount)
					}
				}
			}
		}
	}

	log.Printf("Processed %d buildings", buildingCount)
	return nil
}

// processBuilding processes a single building way
func (p *OSMProcessor) processBuilding(way *osmpbf.Way) *model.Building {
	// Skip if not enough nodes to form a polygon
	if len(way.NodeIDs) < 3 {
		return nil
	}

	// Create a polygon from the way nodes
	var points []orb.Point
	for _, nodeID := range way.NodeIDs {
		if point, exists := p.ProcessedNodes[nodeID]; exists {
			points = append(points, point)
		}
	}

	// Skip if we couldn't find all nodes
	if len(points) < 3 {
		return nil
	}

	// Ensure the polygon is closed
	if points[0] != points[len(points)-1] {
		points = append(points, points[0])
	}

	// Create the polygon
	polygon := orb.Polygon{points}
	bound := polygon.Bound()

	// Calculate centroid
	centroid := calculateCentroid(points)

	// Extract building properties
	levels := 1 // Default to 1 level
	if levelsStr, ok := way.Tags["building:levels"]; ok {
		if l, err := strconv.Atoi(levelsStr); err == nil && l > 0 {
			levels = l
		}
	}

	height := 0.0
	if heightStr, ok := way.Tags["height"]; ok {
		if h, err := strconv.ParseFloat(heightStr, 64); err == nil && h > 0 {
			height = h
		}
	}

	// Create building object
	building := &model.Building{
		ID:          way.ID,
		Name:        way.Tags["name"],
		Levels:      levels,
		Height:      height,
		Type:        way.Tags["building"],
		Outline:     polygon,
		BoundingBox: bound,
		Tags:        way.Tags,
		CentroidLat: centroid[1], // Lat
		CentroidLon: centroid[0], // Lon
	}

	return building
}

// calculateCentroid calculates the centroid of a polygon
func calculateCentroid(points []orb.Point) orb.Point {
	var centroidX, centroidY float64

	for _, p := range points {
		centroidX += p[0]
		centroidY += p[1]
	}

	n := float64(len(points))
	return orb.Point{centroidX / n, centroidY / n}
}

// QueryZonesFromDB queries zones from the database that overlap with the given bounding box.
// The bounding box can be expanded by providing a buffer distance in meters.
func QueryZonesFromDB(minLat, minLng, maxLat, maxLng float64, bufferMeters float64) ([]*model.Zone, error) {
	// Validate inputs
	if minLat > maxLat || minLng > maxLng {
		return nil, fmt.Errorf("invalid bounding box: min coordinates must be less than max coordinates")
	}

	if bufferMeters < 0 {
		return nil, fmt.Errorf("buffer distance must be non-negative")
	}

	log.Printf("Finding zones in bounding box [%.6f, %.6f] to [%.6f, %.6f] with %.1f meter buffer",
		minLat, minLng, maxLat, maxLng, bufferMeters)

	// Convert buffer distance from meters to approximate degrees
	// This is a simplification - 1 degree of latitude is ~111km at the equator
	bufferLatDegrees := bufferMeters / 111000.0 // roughly 111km per degree of latitude

	// For longitude, the distance varies with latitude
	// At the equator, 1 degree of longitude is ~111km, but it decreases with latitude
	meanLat := (minLat + maxLat) / 2.0
	bufferLngDegrees := bufferMeters / (111000.0 * math.Cos(meanLat*math.Pi/180.0))

	// Extend the bounding box by the buffer distance
	extendedMinLat := minLat - bufferLatDegrees
	extendedMaxLat := maxLat + bufferLatDegrees
	extendedMinLng := minLng - bufferLngDegrees
	extendedMaxLng := maxLng + bufferLngDegrees

	log.Printf("Extended bounding box: [%.6f, %.6f] to [%.6f, %.6f]",
		extendedMinLat, extendedMinLng, extendedMaxLat, extendedMaxLng)

	// Check if there are any zones in the database
	db := pg.GetDB()
	var count int64
	db.Model(&model.ZonePG{}).Count(&count)
	log.Printf("Total zones in database: %d", count)

	// For debugging, let's get a few zones and check their structure
	var debugZones []*model.ZonePG
	db.Limit(1).Find(&debugZones)

	if len(debugZones) > 0 {
		log.Printf("Debug zone: ID=%s, TopLeft=%v, TopRight=%v, BottomLeft=%v, BottomRight=%v",
			debugZones[0].ID,
			debugZones[0].TopLeftLatLon,
			debugZones[0].TopRightLatLon,
			debugZones[0].BottomLeftLatLon,
			debugZones[0].BottomRightLatLon)
	}

	var pgZones []*model.ZonePG

	query := `
		SELECT * FROM zones
		WHERE 
		  ((top_left_lat_lon->>0)::float BETWEEN ? AND ? AND (top_left_lat_lon->>1)::float BETWEEN ? AND ?)
		  OR ((top_right_lat_lon->>0)::float BETWEEN ? AND ? AND (top_right_lat_lon->>1)::float BETWEEN ? AND ?)
		  OR ((bottom_left_lat_lon->>0)::float BETWEEN ? AND ? AND (bottom_left_lat_lon->>1)::float BETWEEN ? AND ?)
		  OR ((bottom_right_lat_lon->>0)::float BETWEEN ? AND ? AND (bottom_right_lat_lon->>1)::float BETWEEN ? AND ?)
	`

	result := db.Raw(query,
		extendedMinLat, extendedMaxLat, extendedMinLng, extendedMaxLng, // TopLeft bounds
		extendedMinLat, extendedMaxLat, extendedMinLng, extendedMaxLng, // TopRight bounds
		extendedMinLat, extendedMaxLat, extendedMinLng, extendedMaxLng, // BottomLeft bounds
		extendedMinLat, extendedMaxLat, extendedMinLng, extendedMaxLng, // BottomRight bounds
	).Find(&pgZones)

	if result.Error != nil {
		return nil, fmt.Errorf("database query failed: %w", result.Error)
	}

	log.Printf("Found %d zones intersecting with the extended bounding box", len(pgZones))

	// Convert PG models to in-memory models
	zones := make([]*model.Zone, len(pgZones))
	for i, pgZone := range pgZones {
		zones[i] = model.ZoneFromPG(pgZone)
	}

	return zones, nil
}

// BoundingBox represents a geographic bounding box
type BoundingBox struct {
	minLat, minLng, maxLat, maxLng float64
}

// GetZonesForProcessedBuildings calculates the bounding box containing all processed buildings
// and returns zones from the database that intersect with this bounding box plus a buffer.
func (p *OSMProcessor) GetZonesForProcessedBuildings(bufferMeters float64, skipDB bool) ([]*model.Zone, error) {
	if len(p.Buildings) == 0 {
		return nil, fmt.Errorf("no buildings processed yet")
	}

	// Calculate bounding box of all processed buildings
	boundingBox := p.calculateBuildingsBoundingBox()

	log.Printf("Buildings bounding box: [%.6f, %.6f] to [%.6f, %.6f]",
		boundingBox.minLat, boundingBox.minLng, boundingBox.maxLat, boundingBox.maxLng)

	// Query zones that intersect with the buildings' bounding box
	if !skipDB {
		zones, err := QueryZonesFromDB(
			boundingBox.minLat,
			boundingBox.minLng,
			boundingBox.maxLat,
			boundingBox.maxLng,
			bufferMeters,
		)
		if err != nil {
			log.Fatalf("Failed to query zones from database: %v", err)
		}

		err = exportZonesPGToGeoJSON(zones, "output_zones.geojson", true)
		if err != nil {
			log.Fatalf("Failed to export zones: %v", err)
		}

		return zones, nil
	} else {

		zones := make([]*model.Zone, 0)

		// Create 1 test zone with boundingBox as borders
		zones = append(zones, &model.Zone{
			ID:                "ONEZONE",
			Name:              "ONEZONE",
			TopLeftLatLon:     []float64{boundingBox.minLat, boundingBox.minLng},
			TopRightLatLon:    []float64{boundingBox.minLat, boundingBox.maxLng},
			BottomLeftLatLon:  []float64{boundingBox.maxLat, boundingBox.minLng},
			BottomRightLatLon: []float64{boundingBox.maxLat, boundingBox.maxLng},
		})
		return zones, nil
	}
}

// calculateBuildingsBoundingBox calculates the bounding box containing all processed buildings
func (p *OSMProcessor) calculateBuildingsBoundingBox() BoundingBox {
	// Initialize min/max bounds with the first building's bounds
	minLat := p.Buildings[0].CentroidLat
	maxLat := minLat
	minLng := p.Buildings[0].CentroidLon
	maxLng := minLng

	// Find the bounding box containing all buildings
	for _, building := range p.Buildings {
		// Update bounds based on building's bounding box
		bound := building.BoundingBox

		// Min/Max for latitude (Y)
		if bound.Min[1] < minLat {
			minLat = bound.Min[1]
		}
		if bound.Max[1] > maxLat {
			maxLat = bound.Max[1]
		}

		// Min/Max for longitude (X)
		if bound.Min[0] < minLng {
			minLng = bound.Min[0]
		}
		if bound.Max[0] > maxLng {
			maxLng = bound.Max[0]
		}
	}

	return BoundingBox{
		minLat: minLat,
		minLng: minLng,
		maxLat: maxLat,
		maxLng: maxLng,
	}
}

// ZoneSpatial represents a zone with its spatial information for R-tree indexing
type ZoneSpatial struct {
	Zone        *model.Zone
	Polygon     *orb.Polygon
	BoundingBox *orb.Bound
}

// Bounds implements the rtreego.Spatial interface
func (z *ZoneSpatial) Bounds() rtreego.Rect {
	// Convert orb.Bound to rtreego.Rect format
	minX, minY := z.BoundingBox.Min[0], z.BoundingBox.Min[1]
	maxX, maxY := z.BoundingBox.Max[0], z.BoundingBox.Max[1]

	// Create a new rectangle with the bottom-left corner at (minX, minY)
	// and with width and height dimensions
	rect, _ := rtreego.NewRect(
		rtreego.Point{minX, minY},
		[]float64{maxX - minX, maxY - minY},
	)

	return rect
}

// calculateBuildingInfluenceRadius calculates the radius of influence for a building
// Returns radius in meters, capped at 1000m (1km)
func calculateBuildingInfluenceRadius(buildingArea float64, radiusKf int) float64 {
	// If radiusKf is 0 or negative, use a default small radius
	if radiusKf <= 0 {
		radiusKf = 1
	}

	// Calculate radius: sqrt(area) * radiusKf
	// This gives us a radius proportional to the building size
	radius := math.Sqrt(buildingArea) * float64(radiusKf)

	// Cap at 1km for very large buildings
	if radius > 1000.0 {
		radius = 1000.0
	}

	return radius
}

// metersToDegrees converts a distance in meters to degrees at a given latitude
func metersToDegrees(meters float64, latitude float64) float64 {
	// Earth's radius in meters
	earthRadius := 6371000.0

	// Convert to radians
	latRad := latitude * math.Pi / 180.0

	// For longitude: depends on latitude
	metersPerDegree := earthRadius * math.Pi / 180.0 * math.Cos(latRad)

	return meters / metersPerDegree
}

// findZonesInRadius finds all zones that intersect with a circle of given radius around a point
func (p *OSMProcessor) findZonesInRadius(zoneIndex *rtreego.Rtree, centerLon, centerLat, radiusMeters float64) []*ZoneSpatial {
	// Convert radius from meters to degrees (approximate)
	// For latitude: 1 degree ≈ 111km
	radiusLat := radiusMeters / 111000.0
	// For longitude: depends on latitude
	radiusLon := metersToDegrees(radiusMeters, centerLat)

	// Create a search rectangle that contains the circle
	searchRect, _ := rtreego.NewRect(
		rtreego.Point{centerLon - radiusLon, centerLat - radiusLat},
		[]float64{2 * radiusLon, 2 * radiusLat},
	)

	// Find all zones that intersect with the search rectangle
	spatialResults := zoneIndex.SearchIntersect(searchRect)

	// Filter to only include zones that actually intersect with the circle
	var intersectingZones []*ZoneSpatial
	for _, item := range spatialResults {
		zoneSpatial := item.(*ZoneSpatial)
		// For now, we'll include all zones in the rectangle
		// In a more precise implementation, we could check circle-polygon intersection
		intersectingZones = append(intersectingZones, zoneSpatial)
	}

	return intersectingZones
}

// UpdateZonesWithBuildingStats updates zones with building statistics using spatial indexing and influence radius
func (p *OSMProcessor) UpdateZonesWithBuildingStats(zones []*model.Zone, skipDB bool, clearZones bool) error {
	if len(p.Buildings) == 0 {
		return fmt.Errorf("no buildings processed yet")
	}

	log.Printf("Updating %d zones with building statistics from %d buildings", len(zones), len(p.Buildings))
	log.Printf("Using influence radius calculation with game type mapping")

	// Clear all zones from database if requested and not skipping DB
	if clearZones && !skipDB {
		if err := clearAllZonesFromDB(); err != nil {
			return fmt.Errorf("failed to clear zones from database: %w", err)
		}
	}

	// Create spatial index for zones to optimize lookups
	zoneIndex := rtreego.NewTree(2, 25, 50) // 2D index with min 25, max 50 entries per node

	// First, prepare all zones and add them to the spatial index
	zoneSpatials := make(map[string]*ZoneSpatial, len(zones))

	for _, zone := range zones {
		// Create polygon from corners if not already created
		if zone.Polygon == nil {
			ring := orb.Ring{
				orb.Point{zone.TopLeftLatLon[1], zone.TopLeftLatLon[0]},         // [lon, lat]
				orb.Point{zone.TopRightLatLon[1], zone.TopRightLatLon[0]},       // [lon, lat]
				orb.Point{zone.BottomRightLatLon[1], zone.BottomRightLatLon[0]}, // [lon, lat]
				orb.Point{zone.BottomLeftLatLon[1], zone.BottomLeftLatLon[0]},   // [lon, lat]
				orb.Point{zone.TopLeftLatLon[1], zone.TopLeftLatLon[0]},         // Close the ring
			}
			polygon := orb.Polygon{ring}
			bound := polygon.Bound()
			zone.Polygon = &polygon
			zone.BoundingBox = &bound
		}

		// Initialize building stats with empty maps if not set
		if zone.Buildings.BuildingTypes == nil {
			zone.Buildings.BuildingTypes = make(map[string]int)
		}
		if zone.Buildings.BuildingAreas == nil {
			zone.Buildings.BuildingAreas = make(map[string]float64)
		}

		// Create a spatial object for this zone
		zoneSpatial := &ZoneSpatial{
			Zone:        zone,
			Polygon:     zone.Polygon,
			BoundingBox: zone.BoundingBox,
		}

		// Add to index
		zoneIndex.Insert(zoneSpatial)
		zoneSpatials[zone.ID] = zoneSpatial
	}

	log.Printf("Created spatial index for %d zones", len(zones))

	// Create test zone for all buildings
	testZone := p.createTestZone()

	// Process each building with influence radius
	processedBuildings := 0
	for i, building := range p.Buildings {
		// Calculate approximate building area in square meters
		buildingArea := geo.Area(building.Outline) * float64(building.Levels)

		// Map OSM building type to game category
		gameCategory := MapBuildingCategory(building.Type)

		// Get building configuration
		buildingConfig := GetBuildingEffectsConfig(gameCategory)
		if buildingConfig == nil {
			// Use default if no config found
			buildingConfig = &BuildingTypeConfig{
				RadiusKf: 1,
				Weight:   1,
			}
		}

		// Calculate influence radius
		influenceRadius := calculateBuildingInfluenceRadius(buildingArea, buildingConfig.RadiusKf)

		// Find all zones within the influence radius
		zonesInRadius := p.findZonesInRadius(zoneIndex, building.CentroidLon, building.CentroidLat, influenceRadius)

		if len(zonesInRadius) > 0 {
			// Distribute building area equally among all affected zones
			areaPerZone := buildingArea / float64(len(zonesInRadius))

			for _, zoneSpatial := range zonesInRadius {
				zone := zoneSpatial.Zone

				// Update building count and area by game type (not OSM type)
				zone.Buildings.BuildingTypes[gameCategory]++
				zone.Buildings.BuildingAreas[gameCategory] += areaPerZone
				zone.Buildings.TotalCount++
				zone.Buildings.TotalArea += areaPerZone

				// Update stats based on building height
				if building.Levels <= 1 {
					zone.Buildings.SingleFloorCount++
					zone.Buildings.SingleFloorTotalArea += areaPerZone
				} else if building.Levels >= 2 && building.Levels <= 9 {
					zone.Buildings.LowRiseCount++
					zone.Buildings.LowRiseTotalArea += areaPerZone
				} else if building.Levels >= 10 && building.Levels <= 29 {
					zone.Buildings.HighRiseCount++
					zone.Buildings.HighRiseTotalArea += areaPerZone
				} else if building.Levels >= 30 {
					zone.Buildings.SkyscraperCount++
					zone.Buildings.SkyscraperTotalArea += areaPerZone
				}
			}

			processedBuildings++
		}

		// Add building to test zone with full area and game category
		p.addBuildingToTestZoneWithGameType(building, buildingArea, gameCategory, testZone)

		// Log progress
		if (i+1)%10000 == 0 {
			log.Printf("Processed %d/%d buildings...", i+1, len(p.Buildings))
		}
	}

	log.Printf("Distributed %d buildings across zones using influence radius", processedBuildings)
	log.Printf("Created test zone with %d buildings", testZone.Buildings.TotalCount)

	// Save updated zones to database
	if !skipDB {
		err := saveUpdatedZonesToDB(zones)
		if err != nil {
			return err
		}
	}

	// Save test zone separately
	if !skipDB {
		err := p.saveTestZoneToDB(testZone)
		if err != nil {
			return err
		}
	}

	// Export zones to GeoJSON if enabled
	if exportZonesJSON {
		err := exportZonesPGToGeoJSON(zones, "processed_zones.geojson", true)
		if err != nil {
			log.Printf("Warning: Failed to export zones to GeoJSON: %v", err)
		} else {
			log.Printf("Successfully exported processed zones to processed_zones.geojson")
		}
	}

	err := p.SaveTestZoneToJSON(testZone, "test_zone.json")
	if err != nil {
		log.Printf("Warning: Failed to save test zone to JSON: %v", err)
	}

	return nil
}

// addBuildingToTestZoneWithGameType adds building statistics to the test zone using game type
func (p *OSMProcessor) addBuildingToTestZoneWithGameType(building *model.Building, buildingArea float64, gameCategory string, testZone *model.Zone) {
	// Update building count by game type
	testZone.Buildings.BuildingTypes[gameCategory]++
	testZone.Buildings.BuildingAreas[gameCategory] += buildingArea
	testZone.Buildings.TotalCount++
	testZone.Buildings.TotalArea += buildingArea

	// Update stats based on building height
	if building.Levels <= 1 {
		testZone.Buildings.SingleFloorCount++
		testZone.Buildings.SingleFloorTotalArea += buildingArea
	} else if building.Levels >= 2 && building.Levels <= 9 {
		testZone.Buildings.LowRiseCount++
		testZone.Buildings.LowRiseTotalArea += buildingArea
	} else if building.Levels >= 10 && building.Levels <= 29 {
		testZone.Buildings.HighRiseCount++
		testZone.Buildings.HighRiseTotalArea += buildingArea
	} else if building.Levels >= 30 {
		testZone.Buildings.SkyscraperCount++
		testZone.Buildings.SkyscraperTotalArea += buildingArea
	}
}

// saveUpdatedZonesToDB saves updated zones back to the database using UPSERT
func saveUpdatedZonesToDB(zones []*model.Zone) error {
	db := pg.GetDB()

	// Process in batches
	batchSize := 50
	for i := 0; i < len(zones); i += batchSize {
		end := i + batchSize
		if end > len(zones) {
			end = len(zones)
		}

		batch := zones[i:end]

		// Convert to PG models and upsert
		err := db.Transaction(func(tx *gorm.DB) error {
			for _, zone := range batch {
				now := time.Now()
				pgZone := model.ZonePG{
					ID:                zone.ID,
					Name:              zone.Name,
					TopLeftLatLon:     model.Float64Slice(zone.TopLeftLatLon),
					TopRightLatLon:    model.Float64Slice(zone.TopRightLatLon),
					BottomLeftLatLon:  model.Float64Slice(zone.BottomLeftLatLon),
					BottomRightLatLon: model.Float64Slice(zone.BottomRightLatLon),
					Buildings:         zone.Buildings,
					WaterBodies:       zone.WaterBodies,
					UpdatedAt:         now,
					CreatedAt:         now, // Set CreatedAt for new records
				}

				// Use Save method which performs UPSERT (INSERT or UPDATE)
				result := tx.Save(&pgZone)
				if result.Error != nil {
					return result.Error
				}
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to upsert zones batch %d-%d: %w", i, end, err)
		}

		log.Printf("Upserted zone batch %d-%d", i, end)
	}

	return nil
}

// TEST ZONE

// createTestZone creates a test zone that will contain all buildings
func (p *OSMProcessor) createTestZone() *model.Zone {
	return &model.Zone{
		ID:   "TESTID",
		Name: "Test Zone with All Buildings",
		// Используем координаты, охватывающие все здания
		TopLeftLatLon:     []float64{90, -180},
		TopRightLatLon:    []float64{90, 180},
		BottomLeftLatLon:  []float64{-90, -180},
		BottomRightLatLon: []float64{-90, 180},
		Buildings: model.BuildingStats{
			BuildingTypes: make(map[string]int),
			BuildingAreas: make(map[string]float64),
		},
	}
}

// saveTestZoneToDB saves the test zone to database using upsert
func (p *OSMProcessor) saveTestZoneToDB(testZone *model.Zone) error {
	db := pg.GetDB()
	now := time.Now()

	pgZone := model.ZonePG{
		ID:                testZone.ID,
		Name:              testZone.Name,
		TopLeftLatLon:     model.Float64Slice(testZone.TopLeftLatLon),
		TopRightLatLon:    model.Float64Slice(testZone.TopRightLatLon),
		BottomLeftLatLon:  model.Float64Slice(testZone.BottomLeftLatLon),
		BottomRightLatLon: model.Float64Slice(testZone.BottomRightLatLon),
		Buildings:         testZone.Buildings,
		WaterBodies:       testZone.WaterBodies,
		UpdatedAt:         now,
		CreatedAt:         now,
	}

	// Используем UPSERT (ON CONFLICT DO UPDATE)
	query := `
		INSERT INTO zones (
			id, name, top_left_lat_lon, top_right_lat_lon, 
			bottom_left_lat_lon, bottom_right_lat_lon, 
			buildings, water_bodies, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			top_left_lat_lon = EXCLUDED.top_left_lat_lon,
			top_right_lat_lon = EXCLUDED.top_right_lat_lon,
			bottom_left_lat_lon = EXCLUDED.bottom_left_lat_lon,
			bottom_right_lat_lon = EXCLUDED.bottom_right_lat_lon,
			buildings = EXCLUDED.buildings,
			water_bodies = EXCLUDED.water_bodies,
			updated_at = EXCLUDED.updated_at
	`

	buildingsJSON, err := json.Marshal(pgZone.Buildings)
	if err != nil {
		return fmt.Errorf("failed to marshal buildings JSON: %w", err)
	}

	waterBodiesJSON, err := json.Marshal(pgZone.WaterBodies)
	if err != nil {
		return fmt.Errorf("failed to marshal water bodies JSON: %w", err)
	}

	topLeftJSON, err := json.Marshal(pgZone.TopLeftLatLon)
	if err != nil {
		return fmt.Errorf("failed to marshal TopLeftLatLon JSON: %w", err)
	}

	topRightJSON, err := json.Marshal(pgZone.TopRightLatLon)
	if err != nil {
		return fmt.Errorf("failed to marshal TopRightLatLon JSON: %w", err)
	}

	bottomLeftJSON, err := json.Marshal(pgZone.BottomLeftLatLon)
	if err != nil {
		return fmt.Errorf("failed to marshal BottomLeftLatLon JSON: %w", err)
	}

	bottomRightJSON, err := json.Marshal(pgZone.BottomRightLatLon)
	if err != nil {
		return fmt.Errorf("failed to marshal BottomRightLatLon JSON: %w", err)
	}

	result := db.Exec(
		query,
		pgZone.ID,
		pgZone.Name,
		topLeftJSON,
		topRightJSON,
		bottomLeftJSON,
		bottomRightJSON,
		buildingsJSON,
		waterBodiesJSON,
		pgZone.CreatedAt,
		pgZone.UpdatedAt,
	)

	if result.Error != nil {
		return fmt.Errorf("failed to upsert test zone: %w", result.Error)
	}

	log.Printf("Saved test zone with ID 'TESTID' to database")
	return nil
}

// SaveTestZoneToJSON saves the test zone to a JSON file
func (p *OSMProcessor) SaveTestZoneToJSON(testZone *model.Zone, outputFile string) error {
	log.Printf("Saving test zone to JSON file: %s", outputFile)

	// Convert model.Zone to GameZone for consistency with existing export functions
	gameZone := GameZone{
		ID:                testZone.ID,
		TopLeftLatLon:     [2]float64{testZone.TopLeftLatLon[0], testZone.TopLeftLatLon[1]},
		TopRightLatLon:    [2]float64{testZone.TopRightLatLon[0], testZone.TopRightLatLon[1]},
		BottomLeftLatLon:  [2]float64{testZone.BottomLeftLatLon[0], testZone.BottomLeftLatLon[1]},
		BottomRightLatLon: [2]float64{testZone.BottomRightLatLon[0], testZone.BottomRightLatLon[1]},
	}

	// Create a structure to hold both zone geometry and building statistics
	type TestZoneExport struct {
		Zone      GameZone            `json:"zone"`
		Buildings model.BuildingStats `json:"buildings"`
	}

	export := TestZoneExport{
		Zone:      gameZone,
		Buildings: testZone.Buildings,
	}

	// Marshal the export structure to JSON with indentation
	jsonData, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal test zone to JSON: %w", err)
	}

	// Write to file
	err = os.WriteFile(outputFile, jsonData, 0644)
	if err != nil {
		return fmt.Errorf("failed to write test zone JSON file: %w", err)
	}

	log.Printf("Successfully saved test zone to %s", outputFile)
	return nil
}

// clearAllZonesFromDB removes all zones from the database
func clearAllZonesFromDB() error {
	db := pg.GetDB()

	log.Println("Clearing all zones from database...")

	// Delete all zones
	result := db.Exec("DELETE FROM zones")
	if result.Error != nil {
		return fmt.Errorf("failed to clear zones from database: %w", result.Error)
	}

	log.Printf("Successfully cleared %d zones from database", result.RowsAffected)
	return nil
}
