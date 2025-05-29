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
	utils "metalink/cmd/osm-zone-parser/utils"
	"metalink/internal/model"

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

// ProcessingStats holds statistics about the building processing
type ProcessingStats struct {
	ProcessedBuildings                  int
	BuildingsDistributedToMultipleZones int
	TotalBuildings                      int
	ZoneDependencies                    *ZoneDependencies
}

// BoundingBox represents a geographic bounding box
type BoundingBox struct {
	minLat, minLng, maxLat, maxLng float64
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

	err = utils.ExportZonesToGeoJSON(zones, "output_zones.geojson", false, false)
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

// UpdateZonesWithBuildingStats updates zones with building statistics using adaptive subdivision
func (p *OSMProcessor) UpdateZonesWithBuildingStats(zones []*model.Zone, clearZones bool, exportZonesJSON bool, exportBuildingsJSON bool) error {
	if len(p.Buildings) == 0 {
		return fmt.Errorf("no buildings processed yet")
	}

	log.Printf("Updating %d zones with building statistics from %d buildings using adaptive subdivision", len(zones), len(p.Buildings))

	// Clear zones from database if requested
	if clearZones {
		if err := parser_db.ClearAllZonesFromDB(); err != nil {
			return fmt.Errorf("failed to clear zones from database: %w", err)
		}
	}

	// Run the adaptive zone subdivision algorithm and get deleted zone IDs
	deletedZoneIDs, err := p.runAdaptiveZoneSubdivision(&zones)
	if err != nil {
		return fmt.Errorf("adaptive zone subdivision failed: %w", err)
	}

	// Save results to database (including deletion of old zones)
	if err := p.saveProcessingResultsToDB(zones, nil, deletedZoneIDs); err != nil {
		return err
	}

	// Export results to files
	if err := p.saveProcessingResultsToGeoJSON(zones, exportZonesJSON, exportBuildingsJSON, nil); err != nil {
		return err
	}

	return nil
}

// saveProcessingResultsToDB saves updated zones and test zone to database, and deletes removed zones
func (p *OSMProcessor) saveProcessingResultsToDB(zones []*model.Zone, testZone *model.Zone, deletedZoneIDs []string) error {
	// Delete old zones from database FIRST
	if len(deletedZoneIDs) > 0 {
		if err := parser_db.DeleteZonesFromDB(deletedZoneIDs); err != nil {
			return fmt.Errorf("failed to delete old zones from database: %w", err)
		}
		log.Printf("Successfully deleted %d old zones from database", len(deletedZoneIDs))
	}

	// Save updated zones to database
	if err := parser_db.SaveUpdatedZonesToDB(zones); err != nil {
		return fmt.Errorf("failed to save zones to database: %w", err)
	}

	// Save test zone separately
	if testZone != nil {
		if err := p.saveTestZoneToDB(testZone); err != nil {
			return fmt.Errorf("failed to save test zone: %w", err)
		}
	}

	return nil
}

// saveProcessingResultsToGeoJSON exports processing results to various file formats
func (p *OSMProcessor) saveProcessingResultsToGeoJSON(zones []*model.Zone, exportZonesJSON bool, exportBuildingsJSON bool, testZone *model.Zone) error {
	// Export zones to GeoJSON if enabled
	if exportZonesJSON {
		if err := utils.ExportZonesToGeoJSON(zones, "processed_zones.geojson", false, false); err != nil {
			log.Printf("Warning: Failed to export zones to GeoJSON: %v", err)
		}
	}

	// Export buildings as squares to GeoJSON if enabled
	if exportBuildingsJSON {
		if err := utils.ExportBuildingsToGeoJSON(p.Buildings, "buildings.geojson", 0); err != nil {
			log.Printf("Warning: Failed to export buildings to GeoJSON: %v", err)
		} else {
			log.Printf("Successfully exported buildings to buildings.geojson")
		}
	}

	// Save test zone to JSON
	if testZone != nil {
		if err := p.SaveTestZoneToJSON(testZone, "test_zone.json"); err != nil {
			log.Printf("Warning: Failed to save test zone to JSON: %v", err)
		}
	}

	return nil
}

// SaveAllBuildingsToTestZone creates a test zone and saves all buildings to it
func (p *OSMProcessor) SaveAllBuildingsToTestZone(exportZonesJSON bool, exportBuildingsJSON bool) error {
	if len(p.Buildings) == 0 {
		return fmt.Errorf("no buildings processed yet")
	}

	log.Printf("Creating test zone and saving all %d buildings to it", len(p.Buildings))

	// Create test zone
	testZone := p.createTestZone()

	// Fill test zone with all buildings
	if err := p.fillTestZoneWithAllBuildings(testZone); err != nil {
		return fmt.Errorf("failed to fill test zone with buildings: %w", err)
	}

	// Save test zone to database
	if err := p.saveTestZoneToDB(testZone); err != nil {
		return fmt.Errorf("failed to save test zone to database: %w", err)
	}
	log.Println("Successfully saved test zone to database")

	// Export buildings to GeoJSON if enabled
	if exportBuildingsJSON {
		if err := utils.ExportBuildingsToGeoJSON(p.Buildings, "test_zone_buildings.geojson", 0); err != nil {
			log.Printf("Warning: Failed to export buildings to GeoJSON: %v", err)
		} else {
			log.Printf("Successfully exported buildings to test_zone_buildings.geojson")
		}
	}

	// Save test zone to JSON
	if err := p.SaveTestZoneToJSON(testZone, "test_zone_complete.json"); err != nil {
		log.Printf("Warning: Failed to save test zone to JSON: %v", err)
	} else {
		log.Printf("Successfully saved test zone statistics to test_zone_complete.json")
	}

	return nil
}
