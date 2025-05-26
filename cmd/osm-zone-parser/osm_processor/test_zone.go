package osm_processor

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	parser_model "metalink/cmd/osm-zone-parser/models"
	"metalink/internal/model"
	pg "metalink/internal/postgres"
)

// createTestZone creates a test zone that will contain all buildings
func (p *OSMProcessor) createTestZone() *model.Zone {
	return &model.Zone{
		ID:   "TESTID",
		Name: "Test Zone with All Buildings",
		// Global coordinates that encompass all buildings
		TopLeftLatLon:     []float64{90, -180},
		TopRightLatLon:    []float64{90, 180},
		BottomLeftLatLon:  []float64{-90, -180},
		BottomRightLatLon: []float64{-90, 180},
		Buildings: model.BuildingStats{
			BuildingTypes: make(map[string]int),
			BuildingAreas: make(map[string]float64),
		},
	}
}

// saveTestZoneToDB saves the test zone to database using upsert
func (p *OSMProcessor) saveTestZoneToDB(testZone *model.Zone) error {
	db := pg.GetDB()
	now := time.Now()

	pgZone := model.ZonePG{
		ID:                testZone.ID,
		Name:              testZone.Name,
		TopLeftLatLon:     model.Float64Slice(testZone.TopLeftLatLon),
		TopRightLatLon:    model.Float64Slice(testZone.TopRightLatLon),
		BottomLeftLatLon:  model.Float64Slice(testZone.BottomLeftLatLon),
		BottomRightLatLon: model.Float64Slice(testZone.BottomRightLatLon),
		Buildings:         testZone.Buildings,
		WaterBodies:       testZone.WaterBodies,
		UpdatedAt:         now,
		CreatedAt:         now,
	}

	// Use UPSERT (ON CONFLICT DO UPDATE)
	query := `
		INSERT INTO zones (
			id, name, top_left_lat_lon, top_right_lat_lon, 
			bottom_left_lat_lon, bottom_right_lat_lon, 
			buildings, water_bodies, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			top_left_lat_lon = EXCLUDED.top_left_lat_lon,
			top_right_lat_lon = EXCLUDED.top_right_lat_lon,
			bottom_left_lat_lon = EXCLUDED.bottom_left_lat_lon,
			bottom_right_lat_lon = EXCLUDED.bottom_right_lat_lon,
			buildings = EXCLUDED.buildings,
			water_bodies = EXCLUDED.water_bodies,
			updated_at = EXCLUDED.updated_at
	`

	buildingsJSON, err := json.Marshal(pgZone.Buildings)
	if err != nil {
		return fmt.Errorf("failed to marshal buildings JSON: %w", err)
	}

	waterBodiesJSON, err := json.Marshal(pgZone.WaterBodies)
	if err != nil {
		return fmt.Errorf("failed to marshal water bodies JSON: %w", err)
	}

	topLeftJSON, err := json.Marshal(pgZone.TopLeftLatLon)
	if err != nil {
		return fmt.Errorf("failed to marshal TopLeftLatLon JSON: %w", err)
	}

	topRightJSON, err := json.Marshal(pgZone.TopRightLatLon)
	if err != nil {
		return fmt.Errorf("failed to marshal TopRightLatLon JSON: %w", err)
	}

	bottomLeftJSON, err := json.Marshal(pgZone.BottomLeftLatLon)
	if err != nil {
		return fmt.Errorf("failed to marshal BottomLeftLatLon JSON: %w", err)
	}

	bottomRightJSON, err := json.Marshal(pgZone.BottomRightLatLon)
	if err != nil {
		return fmt.Errorf("failed to marshal BottomRightLatLon JSON: %w", err)
	}

	result := db.Exec(
		query,
		pgZone.ID,
		pgZone.Name,
		topLeftJSON,
		topRightJSON,
		bottomLeftJSON,
		bottomRightJSON,
		buildingsJSON,
		waterBodiesJSON,
		pgZone.CreatedAt,
		pgZone.UpdatedAt,
	)

	if result.Error != nil {
		return fmt.Errorf("failed to upsert test zone: %w", result.Error)
	}

	log.Printf("Saved test zone with ID 'TESTID' to database")
	return nil
}

// SaveTestZoneToJSON saves the test zone to a JSON file
func (p *OSMProcessor) SaveTestZoneToJSON(testZone *model.Zone, outputFile string) error {
	// Convert model.Zone to GameZone for consistency with existing export functions
	gameZone := parser_model.GameZone{
		ID:                testZone.ID,
		TopLeftLatLon:     [2]float64{testZone.TopLeftLatLon[0], testZone.TopLeftLatLon[1]},
		TopRightLatLon:    [2]float64{testZone.TopRightLatLon[0], testZone.TopRightLatLon[1]},
		BottomLeftLatLon:  [2]float64{testZone.BottomLeftLatLon[0], testZone.BottomLeftLatLon[1]},
		BottomRightLatLon: [2]float64{testZone.BottomRightLatLon[0], testZone.BottomRightLatLon[1]},
	}

	// Create a structure to hold both zone geometry and building statistics
	type TestZoneExport struct {
		Zone      parser_model.GameZone `json:"zone"`
		Buildings model.BuildingStats   `json:"buildings"`
	}

	export := TestZoneExport{
		Zone:      gameZone,
		Buildings: testZone.Buildings,
	}

	// Marshal the export structure to JSON with indentation
	jsonData, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal test zone to JSON: %w", err)
	}

	// Write to file
	err = os.WriteFile(outputFile, jsonData, 0644)
	if err != nil {
		return fmt.Errorf("failed to write test zone JSON file: %w", err)
	}

	log.Printf("Successfully saved test zone to %s", outputFile)
	return nil
}

// addBuildingToTestZoneWithGameType adds building statistics to the test zone using game type
func (p *OSMProcessor) addBuildingToTestZoneWithGameType(building *model.Building, buildingArea float64, gameCategory string, testZone *model.Zone) {
	// Update building count by game type
	testZone.Buildings.BuildingTypes[gameCategory]++
	testZone.Buildings.BuildingAreas[gameCategory] += buildingArea
	testZone.Buildings.TotalCount++
	testZone.Buildings.TotalArea += buildingArea

	// Update stats based on building height
	if building.Levels <= 1 {
		testZone.Buildings.SingleFloorCount++
		testZone.Buildings.SingleFloorTotalArea += buildingArea
	} else if building.Levels >= 2 && building.Levels <= 9 {
		testZone.Buildings.LowRiseCount++
		testZone.Buildings.LowRiseTotalArea += buildingArea
	} else if building.Levels >= 10 && building.Levels <= 29 {
		testZone.Buildings.HighRiseCount++
		testZone.Buildings.HighRiseTotalArea += buildingArea
	} else if building.Levels >= 30 {
		testZone.Buildings.SkyscraperCount++
		testZone.Buildings.SkyscraperTotalArea += buildingArea
	}
}
