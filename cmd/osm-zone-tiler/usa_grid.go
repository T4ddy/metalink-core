package main

import (
	"fmt"
	"math"
)

// USA map boundaries in [lat, lon] format
var (
	USATopLeft     = [2]float64{49.3843580, -125.0016500}
	USATopRight    = [2]float64{49.3843580, -66.9345700}
	USABottomLeft  = [2]float64{24.3963080, -125.0016500}
	USABottomRight = [2]float64{24.3963080, -66.9345700}
)

// buildBaseUSAGrid creates a grid of tiles covering the USA
func buildBaseUSAGrid() []GameTile {
	// Build dynamic grid
	tiles := buildFixeSizedGrid(USATopLeft, USATopRight, USABottomLeft, USABottomRight, baseTileSize)
	fmt.Printf("Created %d tiles with buildBaseUSAGrid\n", len(tiles))

	return tiles
}

// buildFixeSizedGrid creates a grid of tiles with area of maxTileSize*maxTileSize sq. meters
// The height is always maxTileSize meters, and width is adjusted to achieve the target area
func buildFixeSizedGrid(topLeft, topRight, bottomLeft, bottomRight [2]float64, maxTileSize float64) []GameTile {
	// Find the extreme points to ensure we cover the entire area
	minLat := math.Min(math.Min(topLeft[0], topRight[0]), math.Min(bottomLeft[0], bottomRight[0]))
	maxLat := math.Max(math.Max(topLeft[0], topRight[0]), math.Max(bottomLeft[0], bottomRight[0]))
	minLon := math.Min(math.Min(topLeft[1], topRight[1]), math.Min(bottomLeft[1], bottomRight[1]))
	maxLon := math.Max(math.Max(topLeft[1], topRight[1]), math.Max(bottomLeft[1], bottomRight[1]))

	// Create tiles array
	var tiles []GameTile
	targetArea := maxTileSize * maxTileSize

	// Start at the northernmost latitude (max) and move south
	lat := maxLat
	row := 0

	// Continue creating rows until we've covered the entire area and beyond if needed
	for {
		// Calculate the next latitude that is exactly maxTileSize meters south
		nextLat := getDestinationPoint(lat, minLon, 180, maxTileSize)[0]

		// Start at the westernmost longitude (min) and move east
		lon := minLon
		col := 0

		for {
			// Calculate the adjusted width for this tile to achieve the target area
			// The width depends on the latitude because longitudes get closer at higher latitudes
			// We'll calculate the width at the midpoint of our tile's latitude
			midLat := (lat + nextLat) / 2

			// Calculate how many degrees of longitude we need to go east to cover the target area
			// We know height is maxTileSize, so width = targetArea / height
			targetWidth := targetArea / maxTileSize

			// Calculate how far to go east in longitude degrees
			// This is based on the formula for distance along a parallel of latitude
			earthRadius := 6371000.0 // Earth's radius in meters
			latRad := midLat * math.Pi / 180
			lonDiffRad := targetWidth / (earthRadius * math.Cos(latRad))
			lonDiff := lonDiffRad * 180 / math.Pi

			// Calculate the next longitude
			nextLon := lon + lonDiff

			// Create the four corners of this tile
			tileTopLeft := [2]float64{lat, lon}
			tileTopRight := [2]float64{lat, nextLon}
			tileBottomLeft := [2]float64{nextLat, lon}
			tileBottomRight := [2]float64{nextLat, nextLon}

			// Create a tile
			tile := GameTile{
				ID:                fmt.Sprintf("tile_%d_%d", row, col),
				TopLeftLatLon:     tileTopLeft,
				TopRightLatLon:    tileTopRight,
				BottomLeftLatLon:  tileBottomLeft,
				BottomRightLatLon: tileBottomRight,
				Size:              maxTileSize,
			}

			tiles = append(tiles, tile)

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

	return tiles
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

// roundToKilometers rounds a value to the nearest kilometer
func roundToKilometers(value float64) float64 {
	return math.Round(value/1000*1000) / 1000
}
