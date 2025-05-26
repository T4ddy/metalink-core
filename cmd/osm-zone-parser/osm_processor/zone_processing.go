package osm_processor

import (
	"fmt"
	"log"
	mappers "metalink/cmd/osm-zone-parser/mappers"
	utils "metalink/cmd/osm-zone-parser/utils"
	"metalink/internal/model"

	"github.com/dhconnelly/rtreego"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geo"
)

// processRecalculationNeededZones processes only zones marked for recalculation
func (p *OSMProcessor) processRecalculationNeededZones(zones []*model.Zone, zoneIndex *rtreego.Rtree) (*ProcessingStats, error) {
	stats := &ProcessingStats{
		TotalBuildings:   len(p.Buildings),
		ZoneDependencies: NewZoneDependencies(),
	}

	// Count zones needing recalculation
	recalcZones := 0
	for _, zone := range zones {
		if zone.RecalculateNeeded {
			recalcZones++
		}
	}
	log.Printf("Processing buildings for %d zones marked for recalculation", recalcZones)

	// Process each building
	for i, building := range p.Buildings {
		if err := p.processSingleBuildingForRecalcZones(building, zoneIndex, stats); err != nil {
			return nil, fmt.Errorf("failed to process building %d: %w", building.ID, err)
		}

		// Log progress
		if (i+1)%20000 == 0 {
			log.Printf("Processed %d/%d buildings for recalculation zones...", i+1, len(p.Buildings))
		}
	}

	// Clear RecalculateNeeded flag for processed zones
	for _, zone := range zones {
		if zone.RecalculateNeeded {
			zone.RecalculateNeeded = false
		}
	}

	log.Printf("Cleared RecalculateNeeded flag for processed zones")
	return stats, nil
}

// processSingleBuildingForRecalcZones processes a building only for zones needing recalculation
func (p *OSMProcessor) processSingleBuildingForRecalcZones(building *model.Building, zoneIndex *rtreego.Rtree, stats *ProcessingStats) error {
	// Calculate building properties FIRST
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

	// Find all zones within influence radius
	zonesInRadius := p.findZonesInRadius(zoneIndex, building.CentroidLon, building.CentroidLat, influenceRadius)

	// Filter to only zones that need recalculation
	var recalcZonesInRadius []*ZoneSpatial
	for _, zoneSpatial := range zonesInRadius {
		if zoneSpatial.Zone.RecalculateNeeded {
			recalcZonesInRadius = append(recalcZonesInRadius, zoneSpatial)
		}
	}

	if len(recalcZonesInRadius) > 0 {
		// Count multi-zone buildings and build dependencies
		if len(recalcZonesInRadius) > 1 {
			stats.BuildingsDistributedToMultipleZones++
			zoneIDs := make([]string, len(recalcZonesInRadius))
			for i, zoneSpatial := range recalcZonesInRadius {
				zoneIDs[i] = zoneSpatial.Zone.ID
			}
			stats.ZoneDependencies.addMultiZoneBuilding(zoneIDs)
		}

		// Distribute building to recalculation zones
		p.distributeBuildingToZones(building, buildingArea, gameCategory, recalcZonesInRadius)
		stats.ProcessedBuildings++
	}

	// DO NOT add to test zone on repeat passes!
	// Test zone is filled only once at the beginning
	return nil
}

// removeZonesFromList removes zones with specified IDs from the zones list
func (p *OSMProcessor) removeZonesFromList(zones *[]*model.Zone, zoneIDsToRemove []string) {
	if len(zoneIDsToRemove) == 0 {
		return
	}

	log.Printf("Removing %d zones from zones list", len(zoneIDsToRemove))

	// Create a map for fast lookup
	removeMap := make(map[string]bool, len(zoneIDsToRemove))
	for _, id := range zoneIDsToRemove {
		removeMap[id] = true
	}

	// Filter out zones to remove
	filteredZones := make([]*model.Zone, 0, len(*zones))
	for _, zone := range *zones {
		if !removeMap[zone.ID] {
			filteredZones = append(filteredZones, zone)
		}
	}

	*zones = filteredZones
	log.Printf("Zones list now contains %d zones", len(*zones))
}

// addZonesToList adds new zones to the zones list
func (p *OSMProcessor) addZonesToList(zones *[]*model.Zone, newZones []*model.Zone) {
	if len(newZones) == 0 {
		return
	}

	log.Printf("Adding %d new zones to zones list", len(newZones))
	*zones = append(*zones, newZones...)
	log.Printf("Zones list now contains %d zones", len(*zones))
}

// updateSpatialIndexWithNewZones rebuilds spatial index to include new zones
func (p *OSMProcessor) updateSpatialIndexWithNewZones(zones []*model.Zone) (*rtreego.Rtree, error) {
	// Create new spatial index
	zoneIndex := rtreego.NewTree(2, 25, 50)

	for _, zone := range zones {
		if err := p.prepareZoneGeometry(zone); err != nil {
			return nil, fmt.Errorf("failed to prepare zone %s: %w", zone.ID, err)
		}

		p.initializeZoneBuildingStats(zone)

		// Create spatial object
		zoneSpatial := &ZoneSpatial{
			Zone:        zone,
			Polygon:     zone.Polygon,
			BoundingBox: zone.BoundingBox,
		}

		// Add to index
		zoneIndex.Insert(zoneSpatial)
	}

	log.Printf("Rebuilt spatial index for %d zones", len(zones))
	return zoneIndex, nil
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

// Helper functions
func (p *OSMProcessor) removeOverweightFromAffected(overweightZoneIDs, affectedZoneIDs []string) []string {
	overweightMap := make(map[string]bool)
	for _, id := range overweightZoneIDs {
		overweightMap[id] = true
	}

	var result []string
	for _, id := range affectedZoneIDs {
		if !overweightMap[id] {
			result = append(result, id)
		}
	}
	return result
}

func (p *OSMProcessor) findZoneByID(zones []*model.Zone, targetID string) *model.Zone {
	for _, zone := range zones {
		if zone.ID == targetID {
			return zone
		}
	}
	return nil
}
