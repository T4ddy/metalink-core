package osm_processor

import (
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strconv"
	"sync"

	parser_db "metalink/cmd/osm-zone-parser/db"
	mappers "metalink/cmd/osm-zone-parser/mappers"
	utils "metalink/cmd/osm-zone-parser/utils"
	"metalink/internal/model"

	"github.com/dhconnelly/rtreego"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geo"
	"github.com/qedus/osmpbf"
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
	centroid := utils.CalculateCentroid(points)

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
		zones, err := parser_db.QueryZonesFromDB(
			boundingBox.minLat,
			boundingBox.minLng,
			boundingBox.maxLat,
			boundingBox.maxLng,
			bufferMeters,
		)
		if err != nil {
			log.Fatalf("Failed to query zones from database: %v", err)
		}

		err = utils.ExportZonesToGeoJSON(zones, "output_zones.geojson", true)
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

// UpdateZonesWithBuildingStats updates zones with building statistics using spatial indexing and influence radius
func (p *OSMProcessor) UpdateZonesWithBuildingStats(zones []*model.Zone, skipDB bool, clearZones bool, exportZonesJSON bool, exportBuildingsJSON bool) error {
	if len(p.Buildings) == 0 {
		return fmt.Errorf("no buildings processed yet")
	}

	log.Printf("Updating %d zones with building statistics from %d buildings", len(zones), len(p.Buildings))
	log.Printf("Using influence radius calculation with game type mapping")

	// Clear all zones from database if requested and not skipping DB
	if clearZones && !skipDB {
		if err := parser_db.ClearAllZonesFromDB(); err != nil {
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
	buildingsDistributedToMultipleZones := 0
	for i, building := range p.Buildings {
		// Calculate approximate building area in square meters
		buildingArea := geo.Area(building.Outline) * float64(building.Levels)

		// Map OSM building type to game category
		gameCategory := mappers.MapBuildingCategory(building.Type)

		// Get building configuration
		buildingConfig := mappers.GetBuildingEffectsConfig(gameCategory)
		if buildingConfig == nil {
			// Use default if no config found
			buildingConfig = &mappers.BuildingTypeConfig{
				ExtraRadiusKf: 1.0,
				Weight:        1,
			}
		}

		// Calculate influence radius
		influenceRadius := utils.CalculateBuildingInfluenceRadius(buildingArea, buildingConfig.ExtraRadiusKf)
		// log.Printf("%v >> %.2f m² >> %.2f m", i+1, buildingArea, influenceRadius)

		// Find all zones within the influence radius
		zonesInRadius := p.findZonesInRadius(zoneIndex, building.CentroidLon, building.CentroidLat, influenceRadius)

		if len(zonesInRadius) > 0 {
			// Count buildings that are distributed across multiple zones
			if len(zonesInRadius) > 1 {
				buildingsDistributedToMultipleZones++
			}

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
		if (i+1)%20000 == 0 {
			log.Printf("Processed %d/%d buildings...", i+1, len(p.Buildings))
		}
	}

	log.Printf("Distributed %d buildings across zones using influence radius", processedBuildings)
	log.Printf("Buildings distributed to multiple zones: %d out of %d total (%.2f%%)",
		buildingsDistributedToMultipleZones, len(p.Buildings),
		float64(buildingsDistributedToMultipleZones)/float64(len(p.Buildings))*100)
	log.Printf("Created test zone with %d buildings", testZone.Buildings.TotalCount)

	// Save updated zones to database
	if !skipDB {
		err := parser_db.SaveUpdatedZonesToDB(zones)
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
		err := utils.ExportZonesToGeoJSON(zones, "processed_zones.geojson", true)
		if err != nil {
			log.Printf("Warning: Failed to export zones to GeoJSON: %v", err)
		} else {
			log.Printf("Successfully exported processed zones to processed_zones.geojson")
		}
	}

	// Export buildings as squares to GeoJSON if enabled
	if exportBuildingsJSON {
		err := utils.ExportBuildingsToGeoJSON(p.Buildings, "buildings.geojson", 0)
		if err != nil {
			log.Printf("Warning: Failed to export buildings to GeoJSON: %v", err)
		} else {
			log.Printf("Successfully exported buildings to buildings.geojson")
		}
	}

	err := p.SaveTestZoneToJSON(testZone, "test_zone.json")
	if err != nil {
		log.Printf("Warning: Failed to save test zone to JSON: %v", err)
	}

	return nil
}

// findZonesInRadius finds all zones that intersect with a circle of given radius around a point
func (p *OSMProcessor) findZonesInRadius(zoneIndex *rtreego.Rtree, centerLon, centerLat, radiusMeters float64) []*ZoneSpatial {
	// Convert radius from meters to degrees (approximate)
	// For latitude: 1 degree ≈ 111km
	radiusLat := radiusMeters / 111000.0
	// For longitude: depends on latitude
	radiusLon := utils.MetersToDegrees(radiusMeters, centerLat)

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
