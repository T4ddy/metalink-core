package model

import (
	"github.com/dhconnelly/rtreego"
	"github.com/paulmach/orb"
)

// Building represents a building extracted from OSM data
type Building struct {
	ID          int64             // OSM ID
	Name        string            // Name of the building (if available)
	Levels      int               // Number of levels/floors
	Height      float64           // Height in meters (if available)
	Type        string            // Building type (residential, commercial, etc.)
	Outline     orb.Polygon       // Building outline as polygon
	BoundingBox orb.Bound         // Bounding box of the building
	Tags        map[string]string // All OSM tags
	CentroidLat float64           // Latitude of the building centroid
	CentroidLon float64           // Longitude of the building centroid
}

// BuildingSpatial represents a building with its spatial information for R-tree indexing
type BuildingSpatial struct {
	Building *Building // Reference to the building
}

// Bounds implements the rtreego.Spatial interface
func (b *BuildingSpatial) Bounds() rtreego.Rect {
	// Convert orb.Bound to rtreego.Rect format
	minX, minY := b.Building.BoundingBox.Min[0], b.Building.BoundingBox.Min[1]
	maxX, maxY := b.Building.BoundingBox.Max[0], b.Building.BoundingBox.Max[1]

	// Create a new rectangle with the bottom-left corner at (minX, minY)
	// and with width and height dimensions
	rect, _ := rtreego.NewRect(
		rtreego.Point{minX, minY},
		[]float64{maxX - minX, maxY - minY},
	)

	return rect
}
