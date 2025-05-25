package parser_model

// GameZone represents a zone in our game grid
type GameZone struct {
	ID                string
	TopLeftLatLon     [2]float64 // [lat, lon]
	TopRightLatLon    [2]float64 // [lat, lon]
	BottomLeftLatLon  [2]float64 // [lat, lon]
	BottomRightLatLon [2]float64 // [lat, lon]
	Size              float64    // Size in meters
}
