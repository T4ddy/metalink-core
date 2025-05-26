package osm_processor

import (
	"fmt"
	"log"
	"metalink/internal/model"
)

// splitZoneIntoFour splits a zone into 4 equal smaller zones
func (p *OSMProcessor) splitZoneIntoFour(zone *model.Zone) ([]*model.Zone, error) {
	log.Printf("Splitting zone %s into 4 smaller zones", zone.ID)

	// Calculate midpoints
	midLat := (zone.TopLeftLatLon[0] + zone.BottomLeftLatLon[0]) / 2
	midLon := (zone.TopLeftLatLon[1] + zone.TopRightLatLon[1]) / 2

	// Create 4 new zones
	zones := make([]*model.Zone, 4)

	// Top-left quadrant
	zones[0] = &model.Zone{
		ID:                fmt.Sprintf("%s_TL", zone.ID),
		Name:              fmt.Sprintf("%s Top-Left", zone.Name),
		TopLeftLatLon:     []float64{zone.TopLeftLatLon[0], zone.TopLeftLatLon[1]},
		TopRightLatLon:    []float64{zone.TopLeftLatLon[0], midLon},
		BottomLeftLatLon:  []float64{midLat, zone.TopLeftLatLon[1]},
		BottomRightLatLon: []float64{midLat, midLon},
		Buildings:         model.BuildingStats{BuildingTypes: make(map[string]int), BuildingAreas: make(map[string]float64)},
		WaterBodies:       model.WaterBodyStats{},
		RecalculateNeeded: true,
	}

	// Top-right quadrant
	zones[1] = &model.Zone{
		ID:                fmt.Sprintf("%s_TR", zone.ID),
		Name:              fmt.Sprintf("%s Top-Right", zone.Name),
		TopLeftLatLon:     []float64{zone.TopRightLatLon[0], midLon},
		TopRightLatLon:    []float64{zone.TopRightLatLon[0], zone.TopRightLatLon[1]},
		BottomLeftLatLon:  []float64{midLat, midLon},
		BottomRightLatLon: []float64{midLat, zone.TopRightLatLon[1]},
		Buildings:         model.BuildingStats{BuildingTypes: make(map[string]int), BuildingAreas: make(map[string]float64)},
		WaterBodies:       model.WaterBodyStats{},
		RecalculateNeeded: true,
	}

	// Bottom-left quadrant
	zones[2] = &model.Zone{
		ID:                fmt.Sprintf("%s_BL", zone.ID),
		Name:              fmt.Sprintf("%s Bottom-Left", zone.Name),
		TopLeftLatLon:     []float64{midLat, zone.BottomLeftLatLon[1]},
		TopRightLatLon:    []float64{midLat, midLon},
		BottomLeftLatLon:  []float64{zone.BottomLeftLatLon[0], zone.BottomLeftLatLon[1]},
		BottomRightLatLon: []float64{zone.BottomLeftLatLon[0], midLon},
		Buildings:         model.BuildingStats{BuildingTypes: make(map[string]int), BuildingAreas: make(map[string]float64)},
		WaterBodies:       model.WaterBodyStats{},
		RecalculateNeeded: true,
	}

	// Bottom-right quadrant
	zones[3] = &model.Zone{
		ID:                fmt.Sprintf("%s_BR", zone.ID),
		Name:              fmt.Sprintf("%s Bottom-Right", zone.Name),
		TopLeftLatLon:     []float64{midLat, midLon},
		TopRightLatLon:    []float64{midLat, zone.BottomRightLatLon[1]},
		BottomLeftLatLon:  []float64{zone.BottomRightLatLon[0], midLon},
		BottomRightLatLon: []float64{zone.BottomRightLatLon[0], zone.BottomRightLatLon[1]},
		Buildings:         model.BuildingStats{BuildingTypes: make(map[string]int), BuildingAreas: make(map[string]float64)},
		WaterBodies:       model.WaterBodyStats{},
		RecalculateNeeded: true,
	}

	return zones, nil
}
