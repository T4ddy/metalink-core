package main

import (
	"encoding/json"
	"log"
	"metalink/internal/model"
	"metalink/internal/util"
	"os"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// exportZonesPGToGeoJSON exports zones (FROM MODEL) from database to a GeoJSON file
func exportZonesPGToGeoJSON(zones []*model.Zone, outputFile string) error {
	// Convert model.Zone to GameZone
	gameZones := make([]GameZone, len(zones))
	for i, zone := range zones {
		gameZones[i] = GameZone{
			ID:                zone.ID,
			TopLeftLatLon:     [2]float64{zone.TopLeftLatLon[0], zone.TopLeftLatLon[1]},
			TopRightLatLon:    [2]float64{zone.TopRightLatLon[0], zone.TopRightLatLon[1]},
			BottomLeftLatLon:  [2]float64{zone.BottomLeftLatLon[0], zone.BottomLeftLatLon[1]},
			BottomRightLatLon: [2]float64{zone.BottomRightLatLon[0], zone.BottomRightLatLon[1]},
		}
	}

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

	// Create boundary points
	topLeft := [2]float64{maxLat, minLon}
	topRight := [2]float64{maxLat, maxLon}
	bottomLeft := [2]float64{minLat, minLon}
	bottomRight := [2]float64{minLat, maxLon}

	// Call the existing export function
	exportZonesToGeoJSON(gameZones, outputFile, topLeft, topRight, bottomLeft, bottomRight)
	return nil
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
