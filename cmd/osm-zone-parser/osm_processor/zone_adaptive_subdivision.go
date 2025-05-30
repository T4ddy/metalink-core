package osm_processor

import (
	"fmt"
	"log"
	mappers "metalink/cmd/osm-zone-parser/mappers"
	"metalink/internal/model"
)

// runAdaptiveZoneSubdivision runs the main iterative algorithm for zone subdivision
func (p *OSMProcessor) runAdaptiveZoneSubdivision(zones *[]*model.Zone) ([]string, error) {
	log.Printf("Starting adaptive zone subdivision algorithm with %d initial zones", len(*zones))

	// Track all deleted zone IDs throughout the algorithm
	var deletedZoneIDs []string

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
			return deletedZoneIDs, fmt.Errorf("failed to update spatial index: %w", err)
		}

		stats, err := p.processRecalculationNeededZones(*zones, zoneIndex)
		if err != nil {
			return deletedZoneIDs, fmt.Errorf("failed to process recalculation zones: %w", err)
		}

		// Step 4: Find overweight zones
		weightThreshold := mappers.GetWeightThreshold()
		overweightZoneIDs := p.findOverweightZonesWithMinSize(*zones, weightThreshold)

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

		// Step 8: Remove overweight zones from list AND track them for DB deletion
		log.Printf("Tracking %d zones for deletion from database", len(overweightZoneIDs))
		deletedZoneIDs = append(deletedZoneIDs, overweightZoneIDs...)
		p.removeZonesFromList(zones, overweightZoneIDs)

		// Step 9: Add new split zones to list
		p.addZonesToList(zones, allNewZones)

		log.Printf("Iteration %d completed: removed %d zones, added %d zones. Total zones: %d",
			iteration, len(overweightZoneIDs), len(allNewZones), len(*zones))
		log.Printf("Total deleted zone IDs so far: %d", len(deletedZoneIDs))

		// Continue to next iteration (goto step 2, NOT step 1!)
	}

	if iteration >= maxIterations {
		return deletedZoneIDs, fmt.Errorf("algorithm did not converge after %d iterations", maxIterations)
	}

	log.Printf("Adaptive zone subdivision completed successfully after %d iterations with %d final zones",
		iteration, len(*zones))
	log.Printf("Total zones to delete from database: %d", len(deletedZoneIDs))

	return deletedZoneIDs, nil
}
