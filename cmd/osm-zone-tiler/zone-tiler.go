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

// OSMObject represents any OSM object of interest for game mechanics
type OSMObject struct {
	ID       int64
	Type     string // building, water, amenity, etc.
	SubType  string // residential, river, parking, etc.
	Tags     map[string]string
	Lat, Lon float64
	NodeIDs  []int64 // For ways
	IsNode   bool
	Points   [][2]float64 // For polygons [lat, lon]
	IsValid  bool         // Indicates if polygon is valid
}

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
	MaxTileSize := 20000.0

	// Define corners in [lat, lon] format
	TopLeft := [2]float64{45.945233, -104.045464}
	TopRight := [2]float64{45.935212, -96.563728}
	BottomLeft := [2]float64{43.000628, -104.052966}
	BottomRight := [2]float64{42.475416, -96.323787}

	// Build dynamic grid
	tiles := buildDynamicGrid(TopLeft, TopRight, BottomLeft, BottomRight, MaxTileSize)
	fmt.Printf("Created %d tiles in dynamic grid\n", len(tiles))

	// Export tiles to GeoJSON
	exportTilesToGeoJSON(tiles, "output_tiles.geojson", TopLeft, TopRight, BottomLeft, BottomRight)
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

// linearInterpolation calculates a point on a line between two points
func linearInterpolation(p1, p2 [2]float64, fraction float64) [2]float64 {
	return [2]float64{
		p1[0] + (p2[0]-p1[0])*fraction,
		p1[1] + (p2[1]-p1[1])*fraction,
	}
}

func buildDynamicGrid(topLeft, topRight, bottomLeft, bottomRight [2]float64, maxTileSize float64) []GameTile {
	// Calculate the height (left side) of the parent polygon in meters
	leftSideHeight := haversineDistance(topLeft[0], topLeft[1], bottomLeft[0], bottomLeft[1])

	// Calculate the height (right side) of the parent polygon in meters
	rightSideHeight := haversineDistance(topRight[0], topRight[1], bottomRight[0], bottomRight[1])

	// Use the average height to determine the number of rows
	avgHeight := (leftSideHeight + rightSideHeight) / 2
	numRows := int(math.Ceil(avgHeight / maxTileSize))

	// Create tiles array
	var tiles []GameTile

	// For each row
	for row := 0; row < numRows; row++ {
		// Calculate the fraction of the height for the top and bottom of this row
		topFraction := float64(row) / float64(numRows)
		bottomFraction := float64(row+1) / float64(numRows)
		if bottomFraction > 1.0 {
			bottomFraction = 1.0 // Ensure we don't go beyond the bottom boundary
		}

		// Calculate the top and bottom points of this row by interpolating between corners
		topLeftPoint := linearInterpolation(topLeft, bottomLeft, topFraction)
		topRightPoint := linearInterpolation(topRight, bottomRight, topFraction)
		bottomLeftPoint := linearInterpolation(topLeft, bottomLeft, bottomFraction)
		bottomRightPoint := linearInterpolation(topRight, bottomRight, bottomFraction)

		// Calculate the width of the top and bottom of this row
		topWidth := haversineDistance(topLeftPoint[0], topLeftPoint[1], topRightPoint[0], topRightPoint[1])
		bottomWidth := haversineDistance(bottomLeftPoint[0], bottomLeftPoint[1], bottomRightPoint[0], bottomRightPoint[1])

		// Use the maximum width to determine the number of columns
		maxWidth := math.Max(topWidth, bottomWidth)
		numCols := int(math.Ceil(maxWidth / maxTileSize))

		// For each column in this row
		for col := 0; col < numCols; col++ {
			// Calculate the fraction of the width for the left and right of this column
			leftFraction := float64(col) / float64(numCols)
			rightFraction := float64(col+1) / float64(numCols)
			if rightFraction > 1.0 {
				rightFraction = 1.0 // Ensure we don't go beyond the right boundary
			}

			// Calculate the four corners of this tile by interpolating
			topLeft := linearInterpolation(topLeftPoint, topRightPoint, leftFraction)
			topRight := linearInterpolation(topLeftPoint, topRightPoint, rightFraction)
			bottomLeft := linearInterpolation(bottomLeftPoint, bottomRightPoint, leftFraction)
			bottomRight := linearInterpolation(bottomLeftPoint, bottomRightPoint, rightFraction)

			// Create a tile
			tile := GameTile{
				ID:          fmt.Sprintf("tile_%d_%d", row, col),
				TopLeft:     topLeft,
				TopRight:    topRight,
				BottomLeft:  bottomLeft,
				BottomRight: bottomRight,
				Size:        maxTileSize,
			}

			tiles = append(tiles, tile)
		}
	}

	return tiles
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
		feature.Properties["top_width_kilometers"] = math.Round(topWidth/1000*1000) / 1000
		feature.Properties["bottom_width_kilometers"] = math.Round(bottomWidth/1000*1000) / 1000
		feature.Properties["left_height_kilometers"] = math.Round(leftHeight/1000*1000) / 1000
		feature.Properties["right_height_kilometers"] = math.Round(rightHeight/1000*1000) / 1000
		feature.Properties["area_kilometers"] = math.Round(area/1000000*1000) / 1000

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
