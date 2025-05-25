package utils

import (
	"math"

	mappers "metalink/cmd/osm-zone-parser/mappers"

	"github.com/paulmach/orb"
)

// calculateCentroid calculates the centroid of a polygon
func CalculateCentroid(points []orb.Point) orb.Point {
	var centroidX, centroidY float64

	for _, p := range points {
		centroidX += p[0]
		centroidY += p[1]
	}

	n := float64(len(points))
	return orb.Point{centroidX / n, centroidY / n}
}

// metersToDegrees converts a distance in meters to degrees at a given latitude
func MetersToDegrees(meters float64, latitude float64) float64 {
	// Earth's radius in meters
	earthRadius := 6371000.0

	// Convert to radians
	latRad := latitude * math.Pi / 180.0

	// For longitude: depends on latitude
	metersPerDegree := earthRadius * math.Pi / 180.0 * math.Cos(latRad)

	return meters / metersPerDegree
}

// CalculateBuildingInfluenceRadius calculates the radius of influence for a building
// Returns radius in meters, capped at 1000m (1km)
func CalculateBuildingInfluenceRadius(buildingArea float64, extraRadiusKf float64) float64 {
	// If extraRadiusKf is 0 or negative, use a default small radius
	if extraRadiusKf <= 0 {
		extraRadiusKf = 1
	}

	// Calculate radius: base_radius + sqrt(area) * extra_radius_kf * base_area_kf
	// This gives us a radius proportional to the building size
	radius := mappers.GetBuildingBaseRadius() +
		math.Sqrt(buildingArea)*extraRadiusKf*mappers.GetBaseAreaKf()

	// Cap at 1km for very large buildings
	if radius > 1000.0 {
		radius = 1000.0
	}

	return radius
}

// RoundToKilometers converts meters to kilometers and rounds to 2 decimal places
func RoundToKilometers(meters float64) float64 {
	km := meters / 1000.0
	return math.Round(km*100) / 100
}
