package main

import (
	"fmt"
	"math"
	parser_model "metalink/cmd/osm-zone-parser/models"
)

// USA map boundaries in [lat, lon] format
var (
	USATopLeft     = [2]float64{49.3843580, -125.0016500}
	USATopRight    = [2]float64{49.3843580, -66.9345700}
	USABottomLeft  = [2]float64{24.3963080, -125.0016500}
	USABottomRight = [2]float64{24.3963080, -66.9345700}
)

// buildBaseUSAGrid creates a grid of zones covering the USA
func buildBaseUSAGrid() []parser_model.GameZone {
	// Build dynamic grid
	zones := buildFixeSizedGrid(USATopLeft, USATopRight, USABottomLeft, USABottomRight, baseZoneSize)
	fmt.Printf("Created %d zones with buildBaseUSAGrid\n", len(zones))

	return zones
}

// buildFixeSizedGrid creates a grid of zones with area of maxZoneSize*maxZoneSize sq. meters
// The height is always maxZoneSize meters, and width is adjusted to achieve the target area
func buildFixeSizedGrid(topLeft, topRight, bottomLeft, bottomRight [2]float64, maxZoneSize float64) []parser_model.GameZone {
	// Find the extreme points to ensure we cover the entire area
	minLat := math.Min(math.Min(topLeft[0], topRight[0]), math.Min(bottomLeft[0], bottomRight[0]))
	maxLat := math.Max(math.Max(topLeft[0], topRight[0]), math.Max(bottomLeft[0], bottomRight[0]))
	minLon := math.Min(math.Min(topLeft[1], topRight[1]), math.Min(bottomLeft[1], bottomRight[1]))
	maxLon := math.Max(math.Max(topLeft[1], topRight[1]), math.Max(bottomLeft[1], bottomRight[1]))

	// Create zones array
	var zones []parser_model.GameZone
	targetArea := maxZoneSize * maxZoneSize

	// Start at the northernmost latitude (max) and move south
	lat := maxLat
	row := 0

	// Continue creating rows until we've covered the entire area and beyond if needed
	for {
		// Calculate the next latitude that is exactly maxZoneSize meters south
		nextLat := getDestinationPoint(lat, minLon, 180, maxZoneSize)[0]

		// Start at the westernmost longitude (min) and move east
		lon := minLon
		col := 0

		for {
			// Calculate the adjusted width for this zone to achieve the target area
			// The width depends on the latitude because longitudes get closer at higher latitudes
			// We'll calculate the width at the midpoint of our zone's latitude
			midLat := (lat + nextLat) / 2

			// Calculate how many degrees of longitude we need to go east to cover the target area
			// We know height is maxZoneSize, so width = targetArea / height
			targetWidth := targetArea / maxZoneSize

			// Calculate how far to go east in longitude degrees
			// This is based on the formula for distance along a parallel of latitude
			earthRadius := 6371000.0 // Earth's radius in meters
			latRad := midLat * math.Pi / 180
			lonDiffRad := targetWidth / (earthRadius * math.Cos(latRad))
			lonDiff := lonDiffRad * 180 / math.Pi

			// Calculate the next longitude
			nextLon := lon + lonDiff

			// Create the four corners of this zone
			zoneTopLeft := [2]float64{lat, lon}
			zoneTopRight := [2]float64{lat, nextLon}
			zoneBottomLeft := [2]float64{nextLat, lon}
			zoneBottomRight := [2]float64{nextLat, nextLon}

			// Create a zone
			zone := parser_model.GameZone{
				ID:                fmt.Sprintf("zone_%d_%d", row, col),
				TopLeftLatLon:     zoneTopLeft,
				TopRightLatLon:    zoneTopRight,
				BottomLeftLatLon:  zoneBottomLeft,
				BottomRightLatLon: zoneBottomRight,
				Size:              maxZoneSize,
			}

			zones = append(zones, zone)

			// Move to the next column
			lon = nextLon
			col++

			// Stop when we've gone beyond the eastern boundary
			if lon > maxLon {
				break
			}
		}

		// Move to the next row
		lat = nextLat
		row++

		// Stop when we've gone beyond the southern boundary
		if lat < minLat {
			break
		}
	}

	return zones
}

// getDestinationPoint calculates a destination point given a starting point, bearing and distance
// lat, lon are in degrees, bearing in degrees (0=north, 90=east, etc), distance in meters
// Returns [lat, lon] in degrees
func getDestinationPoint(lat, lon, bearing, distance float64) [2]float64 {
	// Convert to radians
	latRad := lat * math.Pi / 180
	lonRad := lon * math.Pi / 180
	bearingRad := bearing * math.Pi / 180

	// Earth's radius in meters
	earthRadius := 6371000.0

	// Calculate
	distRatio := distance / earthRadius

	// Calculate new latitude
	newLatRad := math.Asin(
		math.Sin(latRad)*math.Cos(distRatio) +
			math.Cos(latRad)*math.Sin(distRatio)*math.Cos(bearingRad),
	)

	// Calculate new longitude
	newLonRad := lonRad + math.Atan2(
		math.Sin(bearingRad)*math.Sin(distRatio)*math.Cos(latRad),
		math.Cos(distRatio)-math.Sin(latRad)*math.Sin(newLatRad),
	)

	// Convert back to degrees
	newLat := newLatRad * 180 / math.Pi
	newLon := newLonRad * 180 / math.Pi

	return [2]float64{newLat, newLon}
}
