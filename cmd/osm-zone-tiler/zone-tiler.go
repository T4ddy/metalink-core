package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// GameTile represents a tile in our game grid
type GameTile struct {
	ID          string
	TopLeft     [2]float64 // [lat, lon]
	TopRight    [2]float64 // [lat, lon]
	BottomLeft  [2]float64 // [lat, lon]
	BottomRight [2]float64 // [lat, lon]
	Size        float64    // Size in meters
}

func main() {
	buildBaseUSAGrid()
}

func buildBaseUSAGrid() []GameTile {
	MaxTileSize := 100 * 1000.0

	// Define corners in [lat, lon] format
	TopLeft := [2]float64{49.3843580, -125.0016500}
	TopRight := [2]float64{49.3843580, -66.9345700}
	BottomLeft := [2]float64{24.3963080, -125.0016500}
	BottomRight := [2]float64{24.3963080, -66.9345700}

	// Build dynamic grid
	tiles := buildFixeSizedGrid(TopLeft, TopRight, BottomLeft, BottomRight, MaxTileSize)
	fmt.Printf("Created %d tiles with buildBaseUSAGrid\n", len(tiles))

	// Export tiles to GeoJSON
	exportTilesToGeoJSON(tiles, "output_tiles.geojson", TopLeft, TopRight, BottomLeft, BottomRight)

	return tiles
}

// haversineDistance calculates the great-circle distance between two points in meters
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	// Convert latitude and longitude from degrees to radians
	lat1 = lat1 * math.Pi / 180
	lon1 = lon1 * math.Pi / 180
	lat2 = lat2 * math.Pi / 180
	lon2 = lon2 * math.Pi / 180

	// Haversine formula
	dLat := lat2 - lat1
	dLon := lon2 - lon1
	a := math.Pow(math.Sin(dLat/2), 2) + math.Cos(lat1)*math.Cos(lat2)*math.Pow(math.Sin(dLon/2), 2)
	c := 2 * math.Asin(math.Sqrt(a))

	// Earth radius in meters
	r := 6371000.0

	// Calculate the distance
	return c * r
}

// roundToKilometers rounds a value to the nearest kilometer
func roundToKilometers(value float64) float64 {
	return math.Round(value/1000*1000) / 1000
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
				ID:          fmt.Sprintf("tile_%d_%d", row, col),
				TopLeft:     tileTopLeft,
				TopRight:    tileTopRight,
				BottomLeft:  tileBottomLeft,
				BottomRight: tileBottomRight,
				Size:        maxTileSize,
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

// exportTilesToGeoJSON exports tiles to a GeoJSON file for visualization
func exportTilesToGeoJSON(tiles []GameTile, outputFile string, topLeft, topRight, bottomLeft, bottomRight [2]float64) {
	log.Printf("Exporting %d tiles to GeoJSON file: %s", len(tiles), outputFile)

	// Create a GeoJSON FeatureCollection
	fc := geojson.NewFeatureCollection()

	// Add each tile as a feature
	for _, tile := range tiles {
		// Create a polygon from the tile corners - convert to orb.Ring for GeoJSON
		ring := orb.Ring{
			{tile.TopLeft[1], tile.TopLeft[0]},         // Top Left (lon, lat)
			{tile.TopRight[1], tile.TopRight[0]},       // Top Right (lon, lat)
			{tile.BottomRight[1], tile.BottomRight[0]}, // Bottom Right (lon, lat)
			{tile.BottomLeft[1], tile.BottomLeft[0]},   // Bottom Left (lon, lat)
			{tile.TopLeft[1], tile.TopLeft[0]},         // Close the ring (lon, lat)
		}

		polygon := orb.Polygon{ring}

		// Create a feature from the tile polygon
		feature := geojson.NewFeature(polygon)

		// Calculate actual width and height in meters for this specific tile
		topWidth := haversineDistance(tile.TopLeft[0], tile.TopLeft[1], tile.TopRight[0], tile.TopRight[1])
		bottomWidth := haversineDistance(tile.BottomLeft[0], tile.BottomLeft[1], tile.BottomRight[0], tile.BottomRight[1])
		leftHeight := haversineDistance(tile.TopLeft[0], tile.TopLeft[1], tile.BottomLeft[0], tile.BottomLeft[1])
		rightHeight := haversineDistance(tile.TopRight[0], tile.TopRight[1], tile.BottomRight[0], tile.BottomRight[1])

		// Average width and height
		// avgWidth := (topWidth + bottomWidth) / 2
		avgHeight := (leftHeight + rightHeight) / 2

		// Calculate area (approximate for trapezoid)
		area := (topWidth + bottomWidth) * avgHeight / 2

		// Add properties
		// feature.Properties["id"] = tile.ID
		// feature.Properties["width_kilometers"] = math.Round(avgWidth/1000*1000) / 1000
		// feature.Properties["height_kilometers"] = math.Round(avgHeight/1000*1000) / 1000
		feature.Properties["top_width_km"] = roundToKilometers(topWidth)
		feature.Properties["bottom_width_km"] = roundToKilometers(bottomWidth)
		feature.Properties["left_height_km"] = roundToKilometers(leftHeight)
		feature.Properties["right_height_km"] = roundToKilometers(rightHeight)
		feature.Properties["area_km"] = roundToKilometers(area / 1000)

		// Add the feature to the collection
		fc.Append(feature)
	}

	// Add markers for the parent polygon corners
	// Top Left marker
	tlMarker := geojson.NewFeature(orb.Point{topLeft[1], topLeft[0]})
	tlMarker.Properties["name"] = "Top Left"
	tlMarker.Properties["type"] = "marker"
	tlMarker.Properties["corner"] = "topLeft"
	fc.Append(tlMarker)

	// Top Right marker
	trMarker := geojson.NewFeature(orb.Point{topRight[1], topRight[0]})
	trMarker.Properties["name"] = "Top Right"
	trMarker.Properties["type"] = "marker"
	trMarker.Properties["corner"] = "topRight"
	fc.Append(trMarker)

	// Bottom Left marker
	blMarker := geojson.NewFeature(orb.Point{bottomLeft[1], bottomLeft[0]})
	blMarker.Properties["name"] = "Bottom Left"
	blMarker.Properties["type"] = "marker"
	blMarker.Properties["corner"] = "bottomLeft"
	fc.Append(blMarker)

	// Bottom Right marker
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

	log.Printf("Successfully exported tiles to %s", outputFile)
}
