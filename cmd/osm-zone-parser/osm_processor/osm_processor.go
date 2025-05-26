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

// UpdateZonesWithBuildingStats updates zones with building statistics
func (p *OSMProcessor) UpdateZonesWithBuildingStats(zones []*model.Zone, skipDB bool, clearZones bool, exportZonesJSON bool, exportBuildingsJSON bool) error {
	if len(p.Buildings) == 0 {
		return fmt.Errorf("no buildings processed yet")
	}

	log.Printf("Updating %d zones with building statistics from %d buildings", len(zones), len(p.Buildings))

	// Clear zones from database if requested
	if clearZones && !skipDB {
		if err := parser_db.ClearAllZonesFromDB(); err != nil {
			return fmt.Errorf("failed to clear zones from database: %w", err)
		}
	}

	// Prepare zones and create spatial index
	zoneIndex, err := p.prepareZonesForProcessing(zones)
	if err != nil {
		return err
	}

	// Create backups of all zones before processing starts
	zoneBackups := p.createZoneBackups(zones)
	log.Printf("Created backups for %d zones before applying building modifications", len(zoneBackups))

	// Create test zone for all buildings
	testZone := p.createTestZone()

	// Process all buildings
	stats, err := p.processAllBuildings(zoneIndex, testZone)
	if err != nil {
		return err
	}

	log.Printf("Distributed %d buildings across zones using influence radius", stats.ProcessedBuildings)
	log.Printf("Buildings distributed to multiple zones: %d out of %d total (%.2f%%)",
		stats.BuildingsDistributedToMultipleZones, stats.TotalBuildings,
		float64(stats.BuildingsDistributedToMultipleZones)/float64(stats.TotalBuildings)*100)

	// Calculate weights and find overweight zones using configured threshold
	weightThreshold := mappers.GetWeightThreshold()
	log.Printf("Using weight threshold: %.2f", weightThreshold)
	overweightZoneIDs := p.findOverweightZones(zones, weightThreshold)

	if len(overweightZoneIDs) > 0 {
		// Find all connected zones that need to be processed together
		connectedZoneIDs := p.findConnectedZones(overweightZoneIDs, stats.ZoneDependencies)
		log.Printf("Total zones to process (including connected): %d", len(connectedZoneIDs))

		// TODO: In next step - handle connected zones (split into 4, restore from backups, etc.)
	} else {
		log.Printf("All zones are within weight threshold - no subdivision needed")
	}

	// Save results to database
	if err := p.saveProcessingResultsToDB(zones, testZone, skipDB); err != nil {
		return err
	}

	// Export results to files
	if err := p.saveProcessingResultsToGeoJSON(zones, exportZonesJSON, exportBuildingsJSON, testZone); err != nil {
		return err
	}

	return nil
}

// ProcessingStats holds statistics about the building processing
type ProcessingStats struct {
	ProcessedBuildings                  int
	BuildingsDistributedToMultipleZones int
	TotalBuildings                      int
	ZoneDependencies                    *ZoneDependencies
}

// prepareZonesForProcessing prepares zones and creates spatial index
func (p *OSMProcessor) prepareZonesForProcessing(zones []*model.Zone) (*rtreego.Rtree, error) {
	// Create spatial index for zones to optimize lookups
	zoneIndex := rtreego.NewTree(2, 25, 50) // 2D index with min 25, max 50 entries per node

	for _, zone := range zones {
		if err := p.prepareZoneGeometry(zone); err != nil {
			return nil, fmt.Errorf("failed to prepare zone %s: %w", zone.ID, err)
		}

		p.initializeZoneBuildingStats(zone)

		// Create a spatial object for this zone
		zoneSpatial := &ZoneSpatial{
			Zone:        zone,
			Polygon:     zone.Polygon,
			BoundingBox: zone.BoundingBox,
		}

		// Add to index
		zoneIndex.Insert(zoneSpatial)
	}

	log.Printf("Created spatial index for %d zones", len(zones))
	return zoneIndex, nil
}

// prepareZoneGeometry creates polygon and bounding box for zone if not already created
func (p *OSMProcessor) prepareZoneGeometry(zone *model.Zone) error {
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
	return nil
}

// initializeZoneBuildingStats initializes building stats maps if not set
func (p *OSMProcessor) initializeZoneBuildingStats(zone *model.Zone) {
	if zone.Buildings.BuildingTypes == nil {
		zone.Buildings.BuildingTypes = make(map[string]int)
	}
	if zone.Buildings.BuildingAreas == nil {
		zone.Buildings.BuildingAreas = make(map[string]float64)
	}
}

// processAllBuildings processes each building and distributes it to affected zones
func (p *OSMProcessor) processAllBuildings(zoneIndex *rtreego.Rtree, testZone *model.Zone) (*ProcessingStats, error) {
	stats := &ProcessingStats{
		TotalBuildings:   len(p.Buildings),
		ZoneDependencies: NewZoneDependencies(),
	}

	for i, building := range p.Buildings {
		if err := p.processSingleBuilding(building, zoneIndex, testZone, stats); err != nil {
			return nil, fmt.Errorf("failed to process building %d: %w", building.ID, err)
		}

		// Log progress
		if (i+1)%20000 == 0 {
			log.Printf("Processed %d/%d buildings...", i+1, len(p.Buildings))
		}
	}

	log.Printf("Built zone dependency map with %d connections", stats.ZoneDependencies.getConnectionCount())
	return stats, nil
}

// processSingleBuilding processes a single building and distributes it to affected zones
func (p *OSMProcessor) processSingleBuilding(building *model.Building, zoneIndex *rtreego.Rtree, testZone *model.Zone, stats *ProcessingStats) error {
	// Calculate building properties
	buildingArea := geo.Area(building.Outline) * float64(building.Levels)
	gameCategory := mappers.MapBuildingCategory(building.Type)

	// Get building configuration
	buildingConfig := mappers.GetBuildingEffectsConfig(gameCategory)
	if buildingConfig == nil {
		buildingConfig = &mappers.BuildingTypeConfig{
			ExtraRadiusKf: 1.0,
			Weight:        1,
		}
	}

	// Calculate influence radius
	influenceRadius := utils.CalculateBuildingInfluenceRadius(buildingArea, buildingConfig.ExtraRadiusKf)

	// Find all zones within the influence radius
	zonesInRadius := p.findZonesInRadius(zoneIndex, building.CentroidLon, building.CentroidLat, influenceRadius)

	if len(zonesInRadius) > 0 {
		// Count buildings that are distributed across multiple zones
		if len(zonesInRadius) > 1 {
			stats.BuildingsDistributedToMultipleZones++

			// Build dependency map for multi-zone buildings
			zoneIDs := make([]string, len(zonesInRadius))
			for i, zoneSpatial := range zonesInRadius {
				zoneIDs[i] = zoneSpatial.Zone.ID
			}
			stats.ZoneDependencies.addMultiZoneBuilding(zoneIDs)
		}

		// Distribute building to zones
		p.distributeBuildingToZones(building, buildingArea, gameCategory, zonesInRadius)
		stats.ProcessedBuildings++
	}

	// Add building to test zone with full area and game category
	p.addBuildingToTestZoneWithGameType(building, buildingArea, gameCategory, testZone)

	return nil
}

// distributeBuildingToZones distributes a building's area and stats to affected zones
func (p *OSMProcessor) distributeBuildingToZones(building *model.Building, buildingArea float64, gameCategory string, zonesInRadius []*ZoneSpatial) {
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
		p.updateZoneHeightStats(zone, building, areaPerZone)
	}
}

// updateZoneHeightStats updates zone statistics based on building height
func (p *OSMProcessor) updateZoneHeightStats(zone *model.Zone, building *model.Building, area float64) {
	if building.Levels <= 1 {
		zone.Buildings.SingleFloorCount++
		zone.Buildings.SingleFloorTotalArea += area
	} else if building.Levels >= 2 && building.Levels <= 9 {
		zone.Buildings.LowRiseCount++
		zone.Buildings.LowRiseTotalArea += area
	} else if building.Levels >= 10 && building.Levels <= 29 {
		zone.Buildings.HighRiseCount++
		zone.Buildings.HighRiseTotalArea += area
	} else if building.Levels >= 30 {
		zone.Buildings.SkyscraperCount++
		zone.Buildings.SkyscraperTotalArea += area
	}
}

// saveProcessingResultsToDB saves updated zones and test zone to database
func (p *OSMProcessor) saveProcessingResultsToDB(zones []*model.Zone, testZone *model.Zone, skipDB bool) error {
	if skipDB {
		return nil
	}

	// Save updated zones to database
	if err := parser_db.SaveUpdatedZonesToDB(zones); err != nil {
		return fmt.Errorf("failed to save zones to database: %w", err)
	}

	// Save test zone separately
	if err := p.saveTestZoneToDB(testZone); err != nil {
		return fmt.Errorf("failed to save test zone: %w", err)
	}

	return nil
}

// saveProcessingResultsToGeoJSON exports processing results to various file formats
func (p *OSMProcessor) saveProcessingResultsToGeoJSON(zones []*model.Zone, exportZonesJSON bool, exportBuildingsJSON bool, testZone *model.Zone) error {
	// Export zones to GeoJSON if enabled
	if exportZonesJSON {
		if err := utils.ExportZonesToGeoJSON(zones, "processed_zones.geojson", true); err != nil {
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
	if err := p.SaveTestZoneToJSON(testZone, "test_zone.json"); err != nil {
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

// ZoneBackup represents a backup copy of a zone before modifications
type ZoneBackup struct {
	Zone      *model.Zone
	Buildings model.BuildingStats
}

// ZoneDependencyMap tracks which buildings affect which zones
type ZoneDependencyMap map[string][]string // buildingID -> []zoneID

// createZoneBackups creates clean copies of zones before processing starts
func (p *OSMProcessor) createZoneBackups(zones []*model.Zone) map[string]*ZoneBackup {
	log.Printf("Creating backup copies of %d zones before processing", len(zones))

	backups := make(map[string]*ZoneBackup, len(zones))

	for _, zone := range zones {
		// Create a deep copy of the zone
		zoneCopy := &model.Zone{
			ID:                zone.ID,
			Name:              zone.Name,
			TopLeftLatLon:     make([]float64, len(zone.TopLeftLatLon)),
			TopRightLatLon:    make([]float64, len(zone.TopRightLatLon)),
			BottomLeftLatLon:  make([]float64, len(zone.BottomLeftLatLon)),
			BottomRightLatLon: make([]float64, len(zone.BottomRightLatLon)),
			UpdatedAt:         zone.UpdatedAt,
			CreatedAt:         zone.CreatedAt,
			DeletedAt:         zone.DeletedAt,
			Polygon:           zone.Polygon,
			BoundingBox:       zone.BoundingBox,
		}

		// Copy coordinate slices
		copy(zoneCopy.TopLeftLatLon, zone.TopLeftLatLon)
		copy(zoneCopy.TopRightLatLon, zone.TopRightLatLon)
		copy(zoneCopy.BottomLeftLatLon, zone.BottomLeftLatLon)
		copy(zoneCopy.BottomRightLatLon, zone.BottomRightLatLon)

		// Create a deep copy of building stats
		buildingStatsCopy := model.BuildingStats{
			SingleFloorCount:     zone.Buildings.SingleFloorCount,
			SingleFloorTotalArea: zone.Buildings.SingleFloorTotalArea,
			LowRiseCount:         zone.Buildings.LowRiseCount,
			LowRiseTotalArea:     zone.Buildings.LowRiseTotalArea,
			HighRiseCount:        zone.Buildings.HighRiseCount,
			HighRiseTotalArea:    zone.Buildings.HighRiseTotalArea,
			SkyscraperCount:      zone.Buildings.SkyscraperCount,
			SkyscraperTotalArea:  zone.Buildings.SkyscraperTotalArea,
			TotalCount:           zone.Buildings.TotalCount,
			TotalArea:            zone.Buildings.TotalArea,
			BuildingTypes:        make(map[string]int),
			BuildingAreas:        make(map[string]float64),
		}

		// Copy building type maps
		for buildingType, count := range zone.Buildings.BuildingTypes {
			buildingStatsCopy.BuildingTypes[buildingType] = count
		}
		for buildingType, area := range zone.Buildings.BuildingAreas {
			buildingStatsCopy.BuildingAreas[buildingType] = area
		}

		// Set the copy as zone's buildings
		zoneCopy.Buildings = buildingStatsCopy

		// Create backup entry
		backup := &ZoneBackup{
			Zone:      zoneCopy,
			Buildings: buildingStatsCopy,
		}

		backups[zone.ID] = backup
	}

	log.Printf("Successfully created %d zone backups", len(backups))
	return backups
}

// calculateZoneWeight calculates the total weight of a zone based on building areas and types
func (p *OSMProcessor) calculateZoneWeight(zone *model.Zone) float64 {
	var totalWeight float64

	// Calculate weight for each building type
	for buildingType, area := range zone.Buildings.BuildingAreas {
		// Get weight coefficient for this building type
		weight := mappers.GetBuildingWeight(buildingType)
		if weight == 0 {
			weight = 1 // Default weight if not found in config
		}

		// Weight = area * weight_coefficient
		buildingWeight := area * weight
		totalWeight += buildingWeight
	}

	return totalWeight
}

// findOverweightZones finds zones that exceed the weight threshold
func (p *OSMProcessor) findOverweightZones(zones []*model.Zone, weightThreshold float64) []string {
	log.Printf("Analyzing %d zones for weight threshold %.2f", len(zones), weightThreshold)

	var overweightZoneIDs []string

	for _, zone := range zones {
		zoneWeight := p.calculateZoneWeight(zone)

		if zoneWeight > weightThreshold {
			overweightZoneIDs = append(overweightZoneIDs, zone.ID)
			log.Printf("Zone %s exceeds threshold: weight %.2f > %.2f",
				zone.ID, zoneWeight, weightThreshold)
		}
	}

	log.Printf("Found %d zones exceeding weight threshold", len(overweightZoneIDs))
	return overweightZoneIDs
}
