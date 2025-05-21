package main

import (
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
	"metalink/internal/util"

	"github.com/dhconnelly/rtreego"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/planar"
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
func (p *OSMProcessor) GetZonesForProcessedBuildings(bufferMeters float64) ([]*model.Zone, error) {
	if len(p.Buildings) == 0 {
		return nil, fmt.Errorf("no buildings processed yet")
	}

	// Calculate bounding box of all processed buildings
	boundingBox := p.calculateBuildingsBoundingBox()

	log.Printf("Buildings bounding box: [%.6f, %.6f] to [%.6f, %.6f]",
		boundingBox.minLat, boundingBox.minLng, boundingBox.maxLat, boundingBox.maxLng)

	// Query zones that intersect with the buildings' bounding box
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

	err = exportZonesPGToGeoJSON(zones, "output_zones.geojson")
	if err != nil {
		log.Fatalf("Failed to export zones: %v", err)
	}

	return zones, nil
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

// UpdateZonesWithBuildingStats updates zones with building statistics
func (p *OSMProcessor) UpdateZonesWithBuildingStats(zones []*model.Zone) error {
	if len(p.Buildings) == 0 {
		return fmt.Errorf("no buildings processed yet")
	}

	log.Printf("Updating %d zones with building statistics from %d buildings", len(zones), len(p.Buildings))

	// Initialize building stats for all zones
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
	}

	// Process each building
	for _, building := range p.Buildings {
		// Calculate approximate building area
		buildingArea := planar.Area(building.Outline)

		// Find which zones contain this building
		point := orb.Point{building.CentroidLon, building.CentroidLat}

		for _, zone := range zones {
			// Check if building is in this zone
			if util.PointInPolygon(*zone.Polygon, point) {
				// Update building count by type
				buildingType := building.Type
				if buildingType == "" {
					buildingType = "unknown"
				}
				zone.Buildings.BuildingTypes[buildingType]++
				zone.Buildings.BuildingAreas[buildingType] += buildingArea
				zone.Buildings.TotalCount++
				zone.Buildings.TotalArea += buildingArea

				// Update stats based on building height
				if building.Levels <= 1 {
					zone.Buildings.SingleFloorCount++
					zone.Buildings.SingleFloorTotalArea += buildingArea
				} else if building.Levels >= 2 && building.Levels <= 9 {
					zone.Buildings.LowRiseCount++
					zone.Buildings.LowRiseTotalArea += buildingArea
				} else if building.Levels >= 10 && building.Levels <= 29 {
					zone.Buildings.HighRiseCount++
					zone.Buildings.HighRiseTotalArea += buildingArea
				} else if building.Levels >= 30 {
					zone.Buildings.SkyscraperCount++
					zone.Buildings.SkyscraperTotalArea += buildingArea
				}
			}
		}
	}

	// Log statistics
	var totalBuildings int
	for _, zone := range zones {
		totalBuildings += zone.Buildings.TotalCount
	}
	log.Printf("Added %d buildings to %d zones", totalBuildings, len(zones))

	// Save updated zones to database
	return saveUpdatedZonesToDB(zones)
}

// saveUpdatedZonesToDB saves updated zones back to the database
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

		// Convert to PG models and update
		err := db.Transaction(func(tx *gorm.DB) error {
			for _, zone := range batch {
				pgZone := model.ZonePG{
					ID:                zone.ID,
					Name:              zone.Name,
					TopLeftLatLon:     model.Float64Slice(zone.TopLeftLatLon),
					TopRightLatLon:    model.Float64Slice(zone.TopRightLatLon),
					BottomLeftLatLon:  model.Float64Slice(zone.BottomLeftLatLon),
					BottomRightLatLon: model.Float64Slice(zone.BottomRightLatLon),
					Buildings:         zone.Buildings,
					WaterBodies:       zone.WaterBodies,
					UpdatedAt:         time.Now(),
				}

				result := tx.Model(&model.ZonePG{}).Where("id = ?", zone.ID).Updates(pgZone)
				if result.Error != nil {
					return result.Error
				}
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to update zones batch %d-%d: %w", i, end, err)
		}

		log.Printf("Updated zone batch %d-%d", i, end)
	}

	return nil
}
