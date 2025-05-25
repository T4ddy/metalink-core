package osm_processor

import (
	"metalink/internal/model"

	"github.com/dhconnelly/rtreego"
	"github.com/paulmach/orb"
)

// ZoneSpatial represents a zone with its spatial information for R-tree indexing
type ZoneSpatial struct {
	Zone        *model.Zone
	Polygon     *orb.Polygon
	BoundingBox *orb.Bound
}

// Bounds implements the rtreego.Spatial interface
func (z *ZoneSpatial) Bounds() rtreego.Rect {
	// Convert orb.Bound to rtreego.Rect format
	minX, minY := z.BoundingBox.Min[0], z.BoundingBox.Min[1]
	maxX, maxY := z.BoundingBox.Max[0], z.BoundingBox.Max[1]

	// Create a new rectangle with the bottom-left corner at (minX, minY)
	// and with width and height dimensions
	rect, _ := rtreego.NewRect(
		rtreego.Point{minX, minY},
		[]float64{maxX - minX, maxY - minY},
	)

	return rect
}
