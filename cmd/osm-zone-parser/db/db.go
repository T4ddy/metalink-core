package parser_db

import (
	"fmt"
	"log"
	"math"
	"time"

	parser_model "metalink/cmd/osm-zone-parser/models"
	"metalink/internal/model"
	pg "metalink/internal/postgres"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// QueryZonesFromDB queries zones from the database that overlap with the given bounding box.
// The bounding box can be expanded by providing a buffer distance in meters.
func QueryZonesFromDB(minLat, minLng, maxLat, maxLng float64, bufferMeters float64) ([]*model.Zone, error) {
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

	// Check if there are any zones in the database
	db := pg.GetDB()
	var count int64
	db.Model(&model.ZonePG{}).Count(&count)
	log.Printf("Total zones in database: %d", count)

	// For debugging, let's get a few zones and check their structure
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

// saveUpdatedZonesToDB saves updated zones back to the database using UPSERT
func SaveUpdatedZonesToDB(zones []*model.Zone) error {
	db := pg.GetDB()

	// Process in batches
	batchSize := 50
	for i := 0; i < len(zones); i += batchSize {
		end := i + batchSize
		if end > len(zones) {
			end = len(zones)
		}

		batch := zones[i:end]

		// Convert to PG models and upsert
		err := db.Transaction(func(tx *gorm.DB) error {
			for _, zone := range batch {
				now := time.Now()
				pgZone := model.ZonePG{
					ID:                zone.ID,
					Name:              zone.Name,
					TopLeftLatLon:     model.Float64Slice(zone.TopLeftLatLon),
					TopRightLatLon:    model.Float64Slice(zone.TopRightLatLon),
					BottomLeftLatLon:  model.Float64Slice(zone.BottomLeftLatLon),
					BottomRightLatLon: model.Float64Slice(zone.BottomRightLatLon),
					Buildings:         zone.Buildings,
					WaterBodies:       zone.WaterBodies,
					UpdatedAt:         now,
					CreatedAt:         now, // Set CreatedAt for new records
				}

				// Use Save method which performs UPSERT (INSERT or UPDATE)
				result := tx.Save(&pgZone)
				if result.Error != nil {
					return result.Error
				}
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to upsert zones batch %d-%d: %w", i, end, err)
		}

		log.Printf("Upserted zone batch %d-%d", i, end)
	}

	return nil
}

// clearAllZonesFromDB removes all zones from the database
func ClearAllZonesFromDB() error {
	db := pg.GetDB()

	log.Println("Clearing all zones from database...")

	// Delete all zones
	result := db.Exec("DELETE FROM zones")
	if result.Error != nil {
		return fmt.Errorf("failed to clear zones from database: %w", result.Error)
	}

	log.Printf("Successfully cleared %d zones from database", result.RowsAffected)
	return nil
}

// saveZonesToDB converts GameZones to ZonePG models and saves them to the database
func SaveZonesToDB(zones []parser_model.GameZone) {
	db := pg.GetDB()

	// Create a batch of zones to insert
	var zonePGs []model.ZonePG
	now := time.Now()

	for _, zone := range zones {
		// Generate a UUID if the zone ID is in format "zone_X_Y"
		id := zone.ID
		if _, err := fmt.Sscanf(zone.ID, "zone_%d_%d", new(int), new(int)); err == nil {
			id = uuid.New().String()
		}

		topLeft := model.Float64Slice{zone.TopLeftLatLon[0], zone.TopLeftLatLon[1]}
		topRight := model.Float64Slice{zone.TopRightLatLon[0], zone.TopRightLatLon[1]}
		bottomLeft := model.Float64Slice{zone.BottomLeftLatLon[0], zone.BottomLeftLatLon[1]}
		bottomRight := model.Float64Slice{zone.BottomRightLatLon[0], zone.BottomRightLatLon[1]}

		// Initialize empty building and water stats
		emptyBuildingStats := model.BuildingStats{
			BuildingTypes: make(map[string]int),
			BuildingAreas: make(map[string]float64),
		}

		emptyWaterBodyStats := model.WaterBodyStats{}

		// Create a ZonePG from the GameZone
		zonePG := model.ZonePG{
			ID:                id,
			Name:              fmt.Sprintf("Zone %s", id),
			TopLeftLatLon:     topLeft,
			TopRightLatLon:    topRight,
			BottomLeftLatLon:  bottomLeft,
			BottomRightLatLon: bottomRight,
			Buildings:         emptyBuildingStats,
			WaterBodies:       emptyWaterBodyStats,
			CreatedAt:         now,
			UpdatedAt:         now,
		}

		zonePGs = append(zonePGs, zonePG)
	}

	// Insert in batches of 100 to avoid overwhelming the database
	batchSize := 100
	for i := 0; i < len(zonePGs); i += batchSize {
		end := i + batchSize
		if end > len(zonePGs) {
			end = len(zonePGs)
		}

		batch := zonePGs[i:end]
		result := db.Create(&batch)
		if result.Error != nil {
			log.Printf("Error saving batch %d-%d: %v", i, end, result.Error)
		} else {
			log.Printf("Saved batch %d-%d successfully", i, end)
		}
	}
}

// DeleteZonesFromDB deletes zones with specified IDs from the database
func DeleteZonesFromDB(zoneIDs []string) error {
	if len(zoneIDs) == 0 {
		return nil
	}

	db := pg.GetDB()

	log.Printf("Deleting %d zones from database", len(zoneIDs))

	// Delete zones in batches to avoid SQL statement limits
	batchSize := 100
	for i := 0; i < len(zoneIDs); i += batchSize {
		end := i + batchSize
		if end > len(zoneIDs) {
			end = len(zoneIDs)
		}

		batch := zoneIDs[i:end]

		// Use IN clause to delete multiple zones at once
		result := db.Exec("DELETE FROM zones WHERE id IN (?)", batch)
		if result.Error != nil {
			return fmt.Errorf("failed to delete zones batch %d-%d: %w", i, end, result.Error)
		}

		log.Printf("Deleted batch %d-%d: %d zones affected", i, end, result.RowsAffected)
	}

	log.Printf("Successfully deleted zones from database")
	return nil
}
