package osm_processor

import (
	"fmt"
	"log"
	mappers "metalink/cmd/osm-zone-parser/mappers"
	utils "metalink/cmd/osm-zone-parser/utils"
	"metalink/internal/model"

	"github.com/dhconnelly/rtreego"
	"github.com/paulmach/orb/geo"
)

// processRecalculationNeededZones processes only zones marked for recalculation
func (p *OSMProcessor) processRecalculationNeededZones(zones []*model.Zone, zoneIndex *rtreego.Rtree, testZone *model.Zone) (*ProcessingStats, error) {
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
		if err := p.processSingleBuildingForRecalcZones(building, zones, zoneIndex, testZone, stats); err != nil {
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
func (p *OSMProcessor) processSingleBuildingForRecalcZones(building *model.Building, zones []*model.Zone, zoneIndex *rtreego.Rtree, testZone *model.Zone, stats *ProcessingStats) error {
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

// fillTestZoneWithAllBuildings fills the test zone with all buildings (only called once)
func (p *OSMProcessor) fillTestZoneWithAllBuildings(testZone *model.Zone) error {
	log.Printf("Filling test zone with all %d buildings", len(p.Buildings))

	for i, building := range p.Buildings {
		// Calculate building properties
		buildingArea := geo.Area(building.Outline) * float64(building.Levels)
		gameCategory := mappers.MapBuildingCategory(building.Type)

		// Add building to test zone with full area and game category
		p.addBuildingToTestZoneWithGameType(building, buildingArea, gameCategory, testZone)

		// Log progress
		if (i+1)%20000 == 0 {
			log.Printf("Added %d/%d buildings to test zone...", i+1, len(p.Buildings))
		}
	}

	log.Printf("Successfully filled test zone with %d buildings", len(p.Buildings))
	return nil
}
