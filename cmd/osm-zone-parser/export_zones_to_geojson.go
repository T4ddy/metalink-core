package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"metalink/internal/model"
	"metalink/internal/util"
	"os"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// exportZonesPGToGeoJSON exports zones (FROM MODEL) from database to a GeoJSON file
func exportZonesPGToGeoJSON(zones []*model.Zone, outputFile string, includeFullDetails bool) error {
	log.Printf("Exporting %d zones to GeoJSON file: %s (full details: %v)", len(zones), outputFile, includeFullDetails)

	// Create a GeoJSON FeatureCollection
	fc := geojson.NewFeatureCollection()

	// Calculate bounding box from all zones
	var minLat, maxLat, minLon, maxLon float64
	first := true
	for _, zone := range zones {
		for _, point := range [][]float64{
			zone.TopLeftLatLon,
			zone.TopRightLatLon,
			zone.BottomLeftLatLon,
			zone.BottomRightLatLon,
		} {
			if first {
				minLat, maxLat = point[0], point[0]
				minLon, maxLon = point[1], point[1]
				first = false
			} else {
				if point[0] < minLat {
					minLat = point[0]
				}
				if point[0] > maxLat {
					maxLat = point[0]
				}
				if point[1] < minLon {
					minLon = point[1]
				}
				if point[1] > maxLon {
					maxLon = point[1]
				}
			}
		}
	}

	// Find min and max building areas for color scaling
	var minBuildingArea, maxBuildingArea float64
	firstArea := true
	for _, zone := range zones {
		if zone.Buildings.TotalArea > 0 {
			if firstArea {
				minBuildingArea = zone.Buildings.TotalArea
				maxBuildingArea = zone.Buildings.TotalArea
				firstArea = false
			} else {
				if zone.Buildings.TotalArea < minBuildingArea {
					minBuildingArea = zone.Buildings.TotalArea
				}
				if zone.Buildings.TotalArea > maxBuildingArea {
					maxBuildingArea = zone.Buildings.TotalArea
				}
			}
		}
	}

	log.Printf("Building area range: %.2f - %.2f m²", minBuildingArea, maxBuildingArea)

	// Create boundary points
	topLeft := [2]float64{maxLat, minLon}
	topRight := [2]float64{maxLat, maxLon}
	bottomLeft := [2]float64{minLat, minLon}
	bottomRight := [2]float64{minLat, maxLon}

	// Create boundary polygon
	boundaryRing := orb.Ring{
		{topLeft[1], topLeft[0]},         // [lon, lat] for GeoJSON
		{topRight[1], topRight[0]},       // [lon, lat] for GeoJSON
		{bottomRight[1], bottomRight[0]}, // [lon, lat] for GeoJSON
		{bottomLeft[1], bottomLeft[0]},   // [lon, lat] for GeoJSON
		{topLeft[1], topLeft[0]},         // Close the ring
	}
	boundaryPolygon := orb.Polygon{boundaryRing}

	// Add each zone as a feature
	for _, zone := range zones {
		// Check if at least one corner of the zone is inside the boundary polygon
		topLeftPoint := orb.Point{zone.TopLeftLatLon[1], zone.TopLeftLatLon[0]}
		topRightPoint := orb.Point{zone.TopRightLatLon[1], zone.TopRightLatLon[0]}
		bottomLeftPoint := orb.Point{zone.BottomLeftLatLon[1], zone.BottomLeftLatLon[0]}
		bottomRightPoint := orb.Point{zone.BottomRightLatLon[1], zone.BottomRightLatLon[0]}

		// Skip this zone if none of its corners are inside the boundary polygon
		if !util.PointInPolygon(boundaryPolygon, topLeftPoint) &&
			!util.PointInPolygon(boundaryPolygon, topRightPoint) &&
			!util.PointInPolygon(boundaryPolygon, bottomLeftPoint) &&
			!util.PointInPolygon(boundaryPolygon, bottomRightPoint) {
			continue
		}

		// Create a polygon from the zone corners - convert to orb.Ring for GeoJSON
		ring := orb.Ring{
			{zone.TopLeftLatLon[1], zone.TopLeftLatLon[0]},         // [lon, lat] for GeoJSON
			{zone.TopRightLatLon[1], zone.TopRightLatLon[0]},       // [lon, lat] for GeoJSON
			{zone.BottomRightLatLon[1], zone.BottomRightLatLon[0]}, // [lon, lat] for GeoJSON
			{zone.BottomLeftLatLon[1], zone.BottomLeftLatLon[0]},   // [lon, lat] for GeoJSON
			{zone.TopLeftLatLon[1], zone.TopLeftLatLon[0]},         // Close the ring
		}

		polygon := orb.Polygon{ring}

		// Create a feature from the zone polygon
		feature := geojson.NewFeature(polygon)

		// Calculate actual width and height in meters for this specific zone
		topWidth := util.HaversineDistance(
			zone.TopLeftLatLon[0], zone.TopLeftLatLon[1],
			zone.TopRightLatLon[0], zone.TopRightLatLon[1],
		)
		bottomWidth := util.HaversineDistance(
			zone.BottomLeftLatLon[0], zone.BottomLeftLatLon[1],
			zone.BottomRightLatLon[0], zone.BottomRightLatLon[1],
		)
		leftHeight := util.HaversineDistance(
			zone.TopLeftLatLon[0], zone.TopLeftLatLon[1],
			zone.BottomLeftLatLon[0], zone.BottomLeftLatLon[1],
		)
		rightHeight := util.HaversineDistance(
			zone.TopRightLatLon[0], zone.TopRightLatLon[1],
			zone.BottomRightLatLon[0], zone.BottomRightLatLon[1],
		)

		// Average height
		avgHeight := (leftHeight + rightHeight) / 2

		// Calculate area (approximate for trapezoid)
		area := (topWidth + bottomWidth) * avgHeight / 2

		// Calculate color based on building density
		fillColor, fillOpacity := calculateZoneColor(zone.Buildings.TotalArea, minBuildingArea, maxBuildingArea)

		// Add basic properties
		feature.Properties["id"] = zone.ID
		feature.Properties["name"] = zone.Name
		feature.Properties["top_width_km"] = roundToKilometers(topWidth)
		feature.Properties["bottom_width_km"] = roundToKilometers(bottomWidth)
		feature.Properties["left_height_km"] = roundToKilometers(leftHeight)
		feature.Properties["right_height_km"] = roundToKilometers(rightHeight)
		feature.Properties["area_km"] = roundToKilometers(area / 1000)

		// Add styling properties for visualization
		feature.Properties["fill"] = fillColor
		feature.Properties["fill-opacity"] = fillOpacity
		feature.Properties["stroke"] = "#333333"
		feature.Properties["stroke-width"] = 1
		feature.Properties["stroke-opacity"] = 0.8

		// Add building statistics if requested
		if includeFullDetails {
			// Basic building stats
			feature.Properties["total_buildings"] = zone.Buildings.TotalCount
			feature.Properties["total_area_m2"] = zone.Buildings.TotalArea

			// Height-based stats
			feature.Properties["single_floor_count"] = zone.Buildings.SingleFloorCount
			feature.Properties["single_floor_area"] = zone.Buildings.SingleFloorTotalArea
			feature.Properties["low_rise_count"] = zone.Buildings.LowRiseCount
			feature.Properties["low_rise_area"] = zone.Buildings.LowRiseTotalArea
			feature.Properties["high_rise_count"] = zone.Buildings.HighRiseCount
			feature.Properties["high_rise_area"] = zone.Buildings.HighRiseTotalArea
			feature.Properties["skyscraper_count"] = zone.Buildings.SkyscraperCount
			feature.Properties["skyscraper_area"] = zone.Buildings.SkyscraperTotalArea

			// Building types (game categories)
			if len(zone.Buildings.BuildingTypes) > 0 {
				feature.Properties["building_types"] = zone.Buildings.BuildingTypes
			}

			// Building areas by type (game categories)
			if len(zone.Buildings.BuildingAreas) > 0 {
				feature.Properties["building_areas"] = zone.Buildings.BuildingAreas
			}

			// Water body stats if available
			if zone.WaterBodies.TotalCount > 0 {
				feature.Properties["water_bodies_count"] = zone.WaterBodies.TotalCount
				feature.Properties["water_bodies_area"] = zone.WaterBodies.TotalArea
				feature.Properties["river_count"] = zone.WaterBodies.RiverCount
				feature.Properties["lake_count"] = zone.WaterBodies.LakeCount
				feature.Properties["pond_count"] = zone.WaterBodies.PondCount
			}
		} else {
			// Only basic building count for simple view
			if zone.Buildings.TotalCount > 0 {
				feature.Properties["total_buildings"] = zone.Buildings.TotalCount
			}
		}

		// Add the feature to the collection
		fc.Append(feature)
	}

	// Add markers for the parent polygon corners
	tlMarker := geojson.NewFeature(orb.Point{topLeft[1], topLeft[0]})
	tlMarker.Properties["name"] = "Top Left"
	tlMarker.Properties["type"] = "marker"
	tlMarker.Properties["corner"] = "topLeft"
	fc.Append(tlMarker)

	trMarker := geojson.NewFeature(orb.Point{topRight[1], topRight[0]})
	trMarker.Properties["name"] = "Top Right"
	trMarker.Properties["type"] = "marker"
	trMarker.Properties["corner"] = "topRight"
	fc.Append(trMarker)

	blMarker := geojson.NewFeature(orb.Point{bottomLeft[1], bottomLeft[0]})
	blMarker.Properties["name"] = "Bottom Left"
	blMarker.Properties["type"] = "marker"
	blMarker.Properties["corner"] = "bottomLeft"
	fc.Append(blMarker)

	brMarker := geojson.NewFeature(orb.Point{bottomRight[1], bottomRight[0]})
	brMarker.Properties["name"] = "Bottom Right"
	brMarker.Properties["type"] = "marker"
	brMarker.Properties["corner"] = "bottomRight"
	fc.Append(brMarker)

	// Marshal the FeatureCollection to JSON
	jsonData, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return err
	}

	// Write to file
	err = os.WriteFile(outputFile, jsonData, 0644)
	if err != nil {
		return err
	}

	log.Printf("Successfully exported zones to %s", outputFile)
	return nil
}

// calculateZoneColor returns color and opacity based on building area density
func calculateZoneColor(buildingArea, minArea, maxArea float64) (string, float64) {
	// If no buildings, return light gray
	if buildingArea == 0 {
		return "#f5f5f5", 0.2
	}

	// Avoid division by zero
	if maxArea == minArea {
		return "#4fc3f7", 0.7
	}

	// Use logarithmic normalization for better distribution
	logMin := math.Log(minArea + 1)
	logMax := math.Log(maxArea + 1)
	logCurrent := math.Log(buildingArea + 1)

	normalized := (logCurrent - logMin) / (logMax - logMin)

	// Clamp to 0-1 range
	if normalized < 0 {
		normalized = 0
	}
	if normalized > 1 {
		normalized = 1
	}

	// Create smoother color gradient with more steps
	// Very low: light blue (#e3f2fd)
	// Low: light green (#c8e6c9)
	// Medium-low: yellow (#fff9c4)
	// Medium: orange (#ffcc80)
	// Medium-high: red (#ff8a80)
	// High: dark red (#d32f2f)

	var r, g, b int

	if normalized < 0.2 {
		// Very low: light blue to light green
		t := normalized / 0.2
		r = int(227 + (200-227)*t) // 227 to 200
		g = int(242 + (230-242)*t) // 242 to 230
		b = int(253 + (201-253)*t) // 253 to 201
	} else if normalized < 0.4 {
		// Low: light green to yellow
		t := (normalized - 0.2) / 0.2
		r = int(200 + (255-200)*t) // 200 to 255
		g = int(230 + (249-230)*t) // 230 to 249
		b = int(201 + (196-201)*t) // 201 to 196
	} else if normalized < 0.6 {
		// Medium-low: yellow to orange
		t := (normalized - 0.4) / 0.2
		r = int(255)               // 255 stays
		g = int(249 + (204-249)*t) // 249 to 204
		b = int(196 + (128-196)*t) // 196 to 128
	} else if normalized < 0.8 {
		// Medium: orange to red
		t := (normalized - 0.6) / 0.2
		r = int(255)               // 255 stays
		g = int(204 + (138-204)*t) // 204 to 138
		b = int(128 + (128-128)*t) // 128 to 128
	} else {
		// High: red to dark red
		t := (normalized - 0.8) / 0.2
		r = int(255 + (211-255)*t) // 255 to 211
		g = int(138 + (47-138)*t)  // 138 to 47
		b = int(128 + (47-128)*t)  // 128 to 47
	}

	color := fmt.Sprintf("#%02x%02x%02x", r, g, b)

	// More gradual opacity change (0.4 to 0.85)
	opacity := 0.4 + normalized*0.45

	return color, opacity
}

// roundToKilometers converts meters to kilometers and rounds to 2 decimal places
func roundToKilometers(meters float64) float64 {
	km := meters / 1000.0
	return math.Round(km*100) / 100
}

// exportZonesToGeoJSON exports zones (GameZone) to a GeoJSON file for visualization
func exportZonesToGeoJSON(zones []GameZone, outputFile string, topLeft, topRight, bottomLeft, bottomRight [2]float64) {
	log.Printf("Exporting %d zones to GeoJSON file: %s", len(zones), outputFile)

	// Create a GeoJSON FeatureCollection
	fc := geojson.NewFeatureCollection()

	// Create a polygon from the area boundaries
	boundaryRing := orb.Ring{
		{topLeft[1], topLeft[0]},         // [lon, lat] for GeoJSON
		{topRight[1], topRight[0]},       // [lon, lat] for GeoJSON
		{bottomRight[1], bottomRight[0]}, // [lon, lat] for GeoJSON
		{bottomLeft[1], bottomLeft[0]},   // [lon, lat] for GeoJSON
		{topLeft[1], topLeft[0]},         // Close the ring
	}
	boundaryPolygon := orb.Polygon{boundaryRing}

	// Add each zone as a feature
	for _, zone := range zones {
		// Check if at least one corner of the zone is inside the boundary polygon
		topLeftPoint := orb.Point{zone.TopLeftLatLon[1], zone.TopLeftLatLon[0]}
		topRightPoint := orb.Point{zone.TopRightLatLon[1], zone.TopRightLatLon[0]}
		bottomLeftPoint := orb.Point{zone.BottomLeftLatLon[1], zone.BottomLeftLatLon[0]}
		bottomRightPoint := orb.Point{zone.BottomRightLatLon[1], zone.BottomRightLatLon[0]}

		// Skip this zone if none of its corners are inside the boundary polygon
		if !util.PointInPolygon(boundaryPolygon, topLeftPoint) &&
			!util.PointInPolygon(boundaryPolygon, topRightPoint) &&
			!util.PointInPolygon(boundaryPolygon, bottomLeftPoint) &&
			!util.PointInPolygon(boundaryPolygon, bottomRightPoint) {
			continue
		}

		// Create a polygon from the zone corners - convert to orb.Ring for GeoJSON
		ring := orb.Ring{
			{zone.TopLeftLatLon[1], zone.TopLeftLatLon[0]},         // [lon, lat] for GeoJSON
			{zone.TopRightLatLon[1], zone.TopRightLatLon[0]},       // [lon, lat] for GeoJSON
			{zone.BottomRightLatLon[1], zone.BottomRightLatLon[0]}, // [lon, lat] for GeoJSON
			{zone.BottomLeftLatLon[1], zone.BottomLeftLatLon[0]},   // [lon, lat] for GeoJSON
			{zone.TopLeftLatLon[1], zone.TopLeftLatLon[0]},         // Close the ring
		}

		polygon := orb.Polygon{ring}

		// Create a feature from the zone polygon
		feature := geojson.NewFeature(polygon)

		// Calculate actual width and height in meters for this specific zone
		topWidth := util.HaversineDistance(
			zone.TopLeftLatLon[0], zone.TopLeftLatLon[1],
			zone.TopRightLatLon[0], zone.TopRightLatLon[1],
		)
		bottomWidth := util.HaversineDistance(
			zone.BottomLeftLatLon[0], zone.BottomLeftLatLon[1],
			zone.BottomRightLatLon[0], zone.BottomRightLatLon[1],
		)
		leftHeight := util.HaversineDistance(
			zone.TopLeftLatLon[0], zone.TopLeftLatLon[1],
			zone.BottomLeftLatLon[0], zone.BottomLeftLatLon[1],
		)
		rightHeight := util.HaversineDistance(
			zone.TopRightLatLon[0], zone.TopRightLatLon[1],
			zone.BottomRightLatLon[0], zone.BottomRightLatLon[1],
		)

		// Average height
		avgHeight := (leftHeight + rightHeight) / 2

		// Calculate area (approximate for trapezoid)
		area := (topWidth + bottomWidth) * avgHeight / 2

		// Add properties
		feature.Properties["top_width_km"] = roundToKilometers(topWidth)
		feature.Properties["bottom_width_km"] = roundToKilometers(bottomWidth)
		feature.Properties["left_height_km"] = roundToKilometers(leftHeight)
		feature.Properties["right_height_km"] = roundToKilometers(rightHeight)
		feature.Properties["area_km"] = roundToKilometers(area / 1000)

		// Add the feature to the collection
		fc.Append(feature)
	}

	// Add markers for the parent polygon corners
	tlMarker := geojson.NewFeature(orb.Point{topLeft[1], topLeft[0]})
	tlMarker.Properties["name"] = "Top Left"
	tlMarker.Properties["type"] = "marker"
	tlMarker.Properties["corner"] = "topLeft"
	fc.Append(tlMarker)

	trMarker := geojson.NewFeature(orb.Point{topRight[1], topRight[0]})
	trMarker.Properties["name"] = "Top Right"
	trMarker.Properties["type"] = "marker"
	trMarker.Properties["corner"] = "topRight"
	fc.Append(trMarker)

	blMarker := geojson.NewFeature(orb.Point{bottomLeft[1], bottomLeft[0]})
	blMarker.Properties["name"] = "Bottom Left"
	blMarker.Properties["type"] = "marker"
	blMarker.Properties["corner"] = "bottomLeft"
	fc.Append(blMarker)

	brMarker := geojson.NewFeature(orb.Point{bottomRight[1], bottomRight[0]})
	brMarker.Properties["name"] = "Bottom Right"
	brMarker.Properties["type"] = "marker"
	brMarker.Properties["corner"] = "bottomRight"
	fc.Append(brMarker)

	// Marshal the FeatureCollection to JSON
	jsonData, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal GeoJSON: %v", err)
	}

	// Write to file
	err = os.WriteFile(outputFile, jsonData, 0644)
	if err != nil {
		log.Fatalf("Failed to write GeoJSON file: %v", err)
	}

	log.Printf("Successfully exported zones to %s", outputFile)
}
