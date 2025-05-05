package util

// DecodePolyline converts an encoded polyline string to a slice of lat/lng coordinates
// Implementation based on Google's Encoded Polyline Algorithm Format
// Default precision is 1e-5 (the Google Maps standard)
func DecodePolyline(encoded string) [][2]float64 {
	return DecodePolylineWithPrecision(encoded, 1e-5)
}

// DecodePolylineWithPrecision decodes a polyline with a custom precision factor
// For GraphHopper API, use 1e-6 precision (as they use a multiplier of 1,000,000)
func DecodePolylineWithPrecision(encoded string, precision float64) [][2]float64 {
	var points [][2]float64
	index, lat, lng := 0, 0, 0

	for index < len(encoded) {
		// Extract latitude
		shift, result := 0, 0
		for {
			if index >= len(encoded) {
				return points
			}
			b := int(encoded[index]) - 63
			index++
			result |= (b & 0x1f) << shift
			shift += 5
			if b < 0x20 {
				break
			}
		}

		// Handle the sign bit for latitude
		if result&1 != 0 {
			lat -= result >> 1
		} else {
			lat += result >> 1
		}

		// Extract longitude
		shift, result = 0, 0
		for {
			if index >= len(encoded) {
				return points
			}
			b := int(encoded[index]) - 63
			index++
			result |= (b & 0x1f) << shift
			shift += 5
			if b < 0x20 {
				break
			}
		}

		// Handle the sign bit for longitude
		if result&1 != 0 {
			lng -= result >> 1
		} else {
			lng += result >> 1
		}

		// Convert to actual coordinates
		latFloat := float64(lat) * precision
		lngFloat := float64(lng) * precision

		// Add coordinates in Google standard order: [latitude, longitude]
		points = append(points, [2]float64{latFloat, lngFloat})
	}

	return points
}
