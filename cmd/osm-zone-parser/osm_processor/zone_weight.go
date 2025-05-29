package osm_processor

import (
	"log"
	mappers "metalink/cmd/osm-zone-parser/mappers"
	"metalink/internal/model"
	"metalink/internal/util"
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

// calculateZoneDimensions calculates approximate dimensions of a zone
// Assumes zones are approximately square, so only calculates one dimension
func (p *OSMProcessor) calculateZoneDimensions(zone *model.Zone) float64 {
	// For square zones, we only need to calculate one side
	// Use top width as representative dimension
	topWidth := util.HaversineDistance(
		zone.TopLeftLatLon[0], zone.TopLeftLatLon[1],
		zone.TopRightLatLon[0], zone.TopRightLatLon[1],
	)

	// Assume zone is square
	return topWidth
}

// findOverweightZones finds zones that exceed the weight threshold and can be safely split
func (p *OSMProcessor) findOverweightZonesWithMinSize(zones []*model.Zone, weightThreshold float64) []string {
	log.Printf("Analyzing %d zones for weight threshold %.2f and minimum split size %.2f meters",
		len(zones), weightThreshold, p.MinZoneSize)

	var overweightZoneIDs []string

	for _, zone := range zones {
		zoneWeight := p.calculateZoneWeight(zone)

		if zoneWeight > weightThreshold {
			// Check if zone can be safely split without going below minimum size
			width := p.calculateZoneDimensions(zone)

			// After splitting, each dimension will be halved
			halfWidth := width / 2

			// Check if both half dimensions are still larger than minimum zone size
			if halfWidth >= p.MinZoneSize {
				overweightZoneIDs = append(overweightZoneIDs, zone.ID)
			} else {
				log.Printf("Zone %s exceeds threshold but cannot be split: weight %.2f > %.2f, size %.2f m (half: %.2f m would be below min %.2f m)",
					zone.ID, zoneWeight, weightThreshold, width, halfWidth, p.MinZoneSize)
			}
		}
	}

	log.Printf("Found %d zones that can be split safely", len(overweightZoneIDs))
	return overweightZoneIDs
}
