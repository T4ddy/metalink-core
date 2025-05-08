package util

import (
	"github.com/golang/geo/s1"
	"github.com/golang/geo/s2"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/planar"
)

// TODO: check performance of s2 and orb
// TODO: check performance of s2 and orb
// TODO: check performance of s2 and orb
// TODO: check performance of s2 and orb
// TODO: check performance of s2 and orb
// TODO: check performance of s2 and orb
// TODO: check performance of s2 and orb
// TODO: check performance of s2 and orb
// TODO: check performance of s2 and orb

func MoveToward(startLat, startLng, endLat, endLng, distanceMeters float64) [2]float64 {
	// Convert degrees to S2 points
	startPoint := s2.PointFromLatLng(s2.LatLngFromDegrees(startLat, startLng))
	endPoint := s2.PointFromLatLng(s2.LatLngFromDegrees(endLat, endLng))

	// Calculate total distance between points
	totalDistanceAngle := s1.Angle(s2.ChordAngleBetweenPoints(startPoint, endPoint).Angle())
	earthRadiusMeters := 6371000.0
	totalDistanceMeters := totalDistanceAngle.Radians() * earthRadiusMeters

	// If requested distance exceeds total distance, return end point
	if distanceMeters >= totalDistanceMeters {
		return [2]float64{endLat, endLng}
	}

	// Calculate fraction of total distance
	fraction := distanceMeters / totalDistanceMeters

	// Interpolate on the great circle path
	newPoint := s2.Interpolate(fraction, startPoint, endPoint)
	newLatLng := s2.LatLngFromPoint(newPoint)

	return [2]float64{newLatLng.Lat.Degrees(), newLatLng.Lng.Degrees()}
}

func HaversineDistance(lat1, lng1, lat2, lng2 float64) float64 {
	// Convert coordinates from degrees to S2 points
	point1 := s2.PointFromLatLng(s2.LatLngFromDegrees(lat1, lng1))
	point2 := s2.PointFromLatLng(s2.LatLngFromDegrees(lat2, lng2))

	// Calculate angle between points
	angle := s1.Angle(s2.ChordAngleBetweenPoints(point1, point2).Angle())

	// Convert angle to distance on Earth's surface
	earthRadiusMeters := 6371000.0
	distanceMeters := angle.Radians() * earthRadiusMeters

	return distanceMeters
}

func PointInPolygon(polygon orb.Polygon, point orb.Point) bool {
	if !planar.RingContains(polygon[0], point) {
		return false
	}
	for _, hole := range polygon[1:] {
		if planar.RingContains(hole, point) {
			return false
		}
	}
	return true
}
