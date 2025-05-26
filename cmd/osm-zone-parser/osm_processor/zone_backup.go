package osm_processor

import (
	"log"
	"metalink/internal/model"
)

// ZoneBackup represents a backup copy of a zone before modifications
type ZoneBackup struct {
	Zone      *model.Zone
	Buildings model.BuildingStats
}

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

// restoreZonesFromBackups restores zones from their backup copies
func (p *OSMProcessor) restoreZonesFromBackups(zoneIDs []string, zoneBackups map[string]*ZoneBackup, zones []*model.Zone) {
	log.Printf("Restoring %d zones from backups", len(zoneIDs))

	for _, zoneID := range zoneIDs {
		if backup, exists := zoneBackups[zoneID]; exists {
			// Find the zone in zones slice and restore it
			for i, zone := range zones {
				if zone.ID == zoneID {
					// Restore from backup
					*zones[i] = *backup.Zone
					zones[i].RecalculateNeeded = true
					break
				}
			}
		}
	}
}
