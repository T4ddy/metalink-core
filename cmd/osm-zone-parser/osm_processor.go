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

	"metalink/internal/model"
	pg "metalink/internal/postgres"

	"github.com/dhconnelly/rtreego"
	"github.com/paulmach/orb"
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

// GetZonesInExtendedBounds finds all existing zones that intersect with the provided bounding box
// extended by a buffer in meters. This implements the 5th point in the generation process.
func GetZonesInExtendedBounds(minLat, minLng, maxLat, maxLng float64, bufferMeters float64) ([]*model.Zone, error) {
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

	// Проверим, есть ли вообще зоны в базе данных
	db := pg.GetDB()
	var count int64
	db.Model(&model.ZonePG{}).Count(&count)
	log.Printf("Total zones in database: %d", count)

	// Для отладки, давайте получим несколько зон и посмотрим их структуру
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

// FindExistingZonesInObjectsBounds finds existing zones that intersect with the bounding box
// of all processed OSM objects plus a buffer distance
func (p *OSMProcessor) FindExistingZonesInObjectsBounds(bufferMeters float64) ([]*model.Zone, error) {
	if len(p.Buildings) == 0 {
		return nil, fmt.Errorf("no buildings processed yet")
	}

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

	log.Printf("Object bounds: [%.6f, %.6f] to [%.6f, %.6f]", minLat, minLng, maxLat, maxLng)

	// Find existing zones in the extended bounding box
	zones, err := GetZonesInExtendedBounds(minLat, minLng, maxLat, maxLng, bufferMeters)
	if err != nil {
		log.Fatalf("Failed to get zones: %v", err)
	}

	err = ExportZonesToGeoJSON(zones, "output_zones.geojson")
	if err != nil {
		log.Fatalf("Failed to export zones: %v", err)
	}

	return zones, nil
}

// ExportZonesToGeoJSON exports zones from database to a GeoJSON file
func ExportZonesToGeoJSON(zones []*model.Zone, outputFile string) error {
	// Convert model.Zone to GameZone
	gameZones := make([]GameZone, len(zones))
	for i, zone := range zones {
		gameZones[i] = GameZone{
			ID:                zone.ID,
			TopLeftLatLon:     [2]float64{zone.TopLeftLatLon[0], zone.TopLeftLatLon[1]},
			TopRightLatLon:    [2]float64{zone.TopRightLatLon[0], zone.TopRightLatLon[1]},
			BottomLeftLatLon:  [2]float64{zone.BottomLeftLatLon[0], zone.BottomLeftLatLon[1]},
			BottomRightLatLon: [2]float64{zone.BottomRightLatLon[0], zone.BottomRightLatLon[1]},
		}
	}

	// Calculate bounding box from all zones
	var minLat, maxLat, minLon, maxLon float64
	first := true
	for _, zone := range zones {
		for _, point := range [][]float64{
			zone.TopLeftLatLon,
			zone.TopRightLatLon,
			zone.BottomLeftLatLon,
			zone.BottomRightLatLon,
		} {
			if first {
				minLat, maxLat = point[0], point[0]
				minLon, maxLon = point[1], point[1]
				first = false
			} else {
				if point[0] < minLat {
					minLat = point[0]
				}
				if point[0] > maxLat {
					maxLat = point[0]
				}
				if point[1] < minLon {
					minLon = point[1]
				}
				if point[1] > maxLon {
					maxLon = point[1]
				}
			}
		}
	}

	// Create boundary points
	topLeft := [2]float64{maxLat, minLon}
	topRight := [2]float64{maxLat, maxLon}
	bottomLeft := [2]float64{minLat, minLon}
	bottomRight := [2]float64{minLat, maxLon}

	// Call the existing export function
	exportZonesToGeoJSON(gameZones, outputFile, topLeft, topRight, bottomLeft, bottomRight)
	return nil
}
