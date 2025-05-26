package osm_processor

import (
	"fmt"
	"log"
	mappers "metalink/cmd/osm-zone-parser/mappers"
	"metalink/internal/model"
)

// runAdaptiveZoneSubdivision runs the main iterative algorithm for zone subdivision
func (p *OSMProcessor) runAdaptiveZoneSubdivision(zones *[]*model.Zone, testZone *model.Zone) error {
	log.Printf("Starting adaptive zone subdivision algorithm with %d initial zones", len(*zones))

	// Fill test zone with ALL buildings ONCE at the beginning
	if err := p.fillTestZoneWithAllBuildings(testZone); err != nil {
		return fmt.Errorf("failed to fill test zone: %w", err)
	}

	// Step 1: Mark ALL zones for recalculation (ONLY ONCE at the beginning)
	log.Printf("Marking all %d zones for recalculation", len(*zones))
	for _, zone := range *zones {
		zone.RecalculateNeeded = true
	}

	maxIterations := 50 // Safety limit to prevent infinite loops
	iteration := 0

	for iteration < maxIterations {
		iteration++
		log.Printf("=== Iteration %d ===", iteration)

		// Step 2: Create backups of all recalculation-needed zones
		zoneBackups := p.createZoneBackupsForRecalcNeeded(*zones)

		// Step 3: Process buildings for recalculation-needed zones
		zoneIndex, err := p.updateSpatialIndexWithNewZones(*zones)
		if err != nil {
			return fmt.Errorf("failed to update spatial index: %w", err)
		}

		stats, err := p.processRecalculationNeededZones(*zones, zoneIndex)
		if err != nil {
			return fmt.Errorf("failed to process recalculation zones: %w", err)
		}

		// Step 4: Find overweight zones
		weightThreshold := mappers.GetWeightThreshold()
		overweightZoneIDs := p.findOverweightZones(*zones, weightThreshold)

		if len(overweightZoneIDs) == 0 {
			log.Printf("No overweight zones found.")
			break
		}

		log.Printf("Found %d overweight zones in iteration %d", len(overweightZoneIDs), iteration)

		// Step 5: Find affected zones (connected through building dependencies)
		affectedZoneIDs := p.findConnectedZones(overweightZoneIDs, stats.ZoneDependencies)

		// Remove overweight zones from affected list to avoid duplicates
		affectedZoneIDs = p.removeOverweightFromAffected(overweightZoneIDs, affectedZoneIDs)

		log.Printf("Found %d additional affected zones", len(affectedZoneIDs))

		// Step 6: Restore overweight and affected zones from backups
		// (this automatically restores recalculate_needed flag)
		allZonesToRestore := append(overweightZoneIDs, affectedZoneIDs...)
		p.restoreZonesFromBackups(allZonesToRestore, zoneBackups, *zones)

		// Step 7: Split overweight zones into 4 parts
		// (new zones automatically get recalculate_needed = true)
		var allNewZones []*model.Zone
		for _, zoneID := range overweightZoneIDs {
			zone := p.findZoneByID(*zones, zoneID)
			if zone == nil {
				log.Printf("Warning: Could not find zone %s for splitting", zoneID)
				continue
			}

			newZones, err := p.splitZoneIntoFour(zone)
			if err != nil {
				log.Printf("Warning: Failed to split zone %s: %v", zoneID, err)
				continue
			}

			allNewZones = append(allNewZones, newZones...)
		}

		// Step 8: Remove overweight zones from list
		p.removeZonesFromList(zones, overweightZoneIDs)

		// Step 9: Add new split zones to list
		p.addZonesToList(zones, allNewZones)

		log.Printf("Iteration %d completed: removed %d zones, added %d zones. Total zones: %d",
			iteration, len(overweightZoneIDs), len(allNewZones), len(*zones))

		// Continue to next iteration (goto step 2, NOT step 1!)
	}

	if iteration >= maxIterations {
		return fmt.Errorf("algorithm did not converge after %d iterations", maxIterations)
	}

	log.Printf("Adaptive zone subdivision completed successfully after %d iterations with %d final zones",
		iteration, len(*zones))
	return nil
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
