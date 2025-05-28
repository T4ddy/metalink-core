package osm_processor

import (
	"log"
	mappers "metalink/cmd/osm-zone-parser/mappers"
	"metalink/internal/model"
)

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
			// log.Printf("Zone %s exceeds threshold: weight %.2f > %.2f",
			// 	zone.ID, zoneWeight, weightThreshold)
		}
	}

	return overweightZoneIDs
}
