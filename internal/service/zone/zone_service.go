package zone

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"metalink/internal/model"
	pg "metalink/internal/postgres"
	"metalink/internal/service/storage"
	"metalink/internal/util"

	"github.com/dhconnelly/rtreego"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// ZoneSpatial represents a zone with its spatial information for R-tree indexing
type ZoneSpatial struct {
	ID          string       // Zone identifier
	Polygon     *orb.Polygon // Actual polygon geometry
	BoundingBox *orb.Bound   // Bounding box of the polygon
	Zone        *model.Zone  // Reference to the original zone object
}

// Bounds implements the rtreego.Spatial interface
// Returns the bounding rectangle of the zone for R-tree indexing
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

// ZoneService manages zone data and spatial indexing
type ZoneService struct {
	storage      storage.Storage[string, *model.Zone]
	spatialIndex *rtreego.Rtree // R-tree spatial index
	indexMutex   sync.RWMutex   // Mutex for thread-safe index operations
	initialized  bool           // Flag indicating if service is initialized
	initMutex    sync.RWMutex   // Mutex for initialization
}

var (
	zoneServiceInstance *ZoneService
	zoneServiceOnce     sync.Once
)

// GetZoneService returns the singleton instance of the ZoneService
func GetZoneService() *ZoneService {
	zoneServiceOnce.Do(func() {
		zoneServiceInstance = &ZoneService{
			storage:      storage.NewShardedMemoryStorage[string, *model.Zone](8, nil),
			spatialIndex: rtreego.NewTree(2, 25, 50), // 2D index with min 25, max 50 entries per node
		}
	})
	return zoneServiceInstance
}

// InitService initializes the service by loading data from PostgreSQL
func (s *ZoneService) InitService(ctx context.Context) error {
	s.initMutex.Lock()
	defer s.initMutex.Unlock()

	if s.initialized {
		return nil
	}

	log.Println("Initializing ZoneService...")
	startTime := time.Now()

	// Load data from PostgreSQL
	log.Println("Loading zones from PostgreSQL...")
	zones, err := s.loadAllZonesFromPG()
	if err != nil {
		return fmt.Errorf("failed to load zones from PostgreSQL: %w", err)
	}

	// Load zones into memory
	for _, zone := range zones {
		s.storage.Set(zone.ID, zone)
	}

	// Build spatial index
	s.rebuildSpatialIndex()

	log.Printf("Initialization complete: %d zones loaded in %v",
		s.storage.Count(), time.Since(startTime))

	s.initialized = true
	return nil
}

// loadAllZonesFromPG loads all zones from PostgreSQL
func (s *ZoneService) loadAllZonesFromPG() ([]*model.Zone, error) {
	db := pg.GetDB()
	var pgZones []*model.ZonePG

	result := db.Find(&pgZones)
	if result.Error != nil {
		return nil, result.Error
	}

	// Convert PG models to in-memory models
	zones := make([]*model.Zone, len(pgZones))
	for i, pgZone := range pgZones {
		zones[i] = model.ZoneFromPG(pgZone)
	}

	return zones, nil
}

// rebuildSpatialIndex rebuilds the spatial index for efficient searching
func (s *ZoneService) rebuildSpatialIndex() {
	s.indexMutex.Lock()
	defer s.indexMutex.Unlock()

	// Create a new R-tree
	s.spatialIndex = rtreego.NewTree(2, 25, 50)

	// Add all zones to the index
	s.storage.ForEach(func(id string, zone *model.Zone) bool {
		if zone.Polygon != nil && zone.BoundingBox != nil {
			// Create a ZoneSpatial object for indexing
			zoneSpatial := &ZoneSpatial{
				ID:          zone.ID,
				Polygon:     zone.Polygon,
				BoundingBox: zone.BoundingBox,
				Zone:        zone,
			}
			// Insert into R-tree
			s.spatialIndex.Insert(zoneSpatial)
		} else {
			// Parse geometry if not done yet
			polygon, bounds, err := s.parseGeometry(zone.Geometry)
			if err == nil {
				zone.Polygon = polygon
				zone.BoundingBox = bounds

				// Create and insert ZoneSpatial
				zoneSpatial := &ZoneSpatial{
					ID:          zone.ID,
					Polygon:     polygon,
					BoundingBox: bounds,
					Zone:        zone,
				}
				s.spatialIndex.Insert(zoneSpatial)
			}
		}
		return true
	})
}

// parseGeometry converts a GeoJSON string into a polygon and its bounds
func (s *ZoneService) parseGeometry(geometryStr string) (*orb.Polygon, *orb.Bound, error) {
	fc, err := geojson.UnmarshalFeatureCollection([]byte(geometryStr))
	if err != nil {
		// Attempt to unmarshal as a single Feature if FeatureCollection fails
		feature, err := geojson.UnmarshalFeature([]byte(geometryStr))
		if err != nil {
			return nil, nil, err
		}
		fc = &geojson.FeatureCollection{Features: []*geojson.Feature{feature}}
	}

	if len(fc.Features) == 0 {
		return nil, nil, fmt.Errorf("no features in geometry")
	}

	feature := fc.Features[0]
	geometry := feature.Geometry
	geotype := geometry.GeoJSONType()

	if geotype != "Polygon" && geotype != "MultiPolygon" {
		return nil, nil, fmt.Errorf("geometry is not a polygon: %s", geotype)
	}

	var polygon orb.Polygon
	if geotype == "Polygon" {
		polygon = geometry.(orb.Polygon)
	} else {
		// Take the first polygon from the multipolygon
		multiPolygon := geometry.(orb.MultiPolygon)
		if len(multiPolygon) > 0 {
			polygon = multiPolygon[0]
		} else {
			return nil, nil, fmt.Errorf("empty multipolygon")
		}
	}

	bound := polygon.Bound()
	return &polygon, &bound, nil
}

// GetZonesAtPoint returns all zones containing the given point
func (s *ZoneService) GetZonesAtPoint(lat, lng float64) []*model.Zone {
	if !s.initialized {
		return nil
	}

	s.indexMutex.RLock()
	defer s.indexMutex.RUnlock()

	point := orb.Point{lng, lat}

	// Create a small search rectangle around the point
	// This helps with the initial filtering using the R-tree
	searchRect, err := rtreego.NewRect(
		rtreego.Point{lng, lat},
		[]float64{0.0001, 0.0001}, // Small radius for point search
	)
	if err != nil {
		log.Printf("invalid search rect: %v", err)
		return nil
	}

	// Find candidate zones using the spatial index
	// This returns zones whose bounding boxes contain the point
	spatialResults := s.spatialIndex.SearchIntersect(searchRect)

	if len(spatialResults) == 0 {
		return nil
	}

	// Check if the point is actually inside each zone's polygon
	var result []*model.Zone
	for _, item := range spatialResults {
		zoneSpatial := item.(*ZoneSpatial)

		// Perform precise point-in-polygon check
		if util.PointInPolygon(*zoneSpatial.Polygon, point) {
			result = append(result, zoneSpatial.Zone)
		}
	}

	return result
}

// GetZonesInBounds returns all zones that intersect with the given bounds
func (s *ZoneService) GetZonesInBounds(minLat, minLng, maxLat, maxLng float64) []*model.Zone {
	if !s.initialized {
		return nil
	}

	s.indexMutex.RLock()
	defer s.indexMutex.RUnlock()

	// Create search rectangle from the bounds
	searchRect, _ := rtreego.NewRect(
		rtreego.Point{minLng, minLat},
		[]float64{maxLng - minLng, maxLat - minLat},
	)

	// Find candidate zones using the spatial index
	spatialResults := s.spatialIndex.SearchIntersect(searchRect)

	if len(spatialResults) == 0 {
		return nil
	}

	// Extract the zones from the spatial results
	var result []*model.Zone
	for _, item := range spatialResults {
		zoneSpatial := item.(*ZoneSpatial)
		result = append(result, zoneSpatial.Zone)
	}

	return result
}

// GetEffectsForTarget returns the combined effects for a target at the given position
func (s *ZoneService) GetEffectsForTarget(lat, lng float64) map[model.TargetParamType]float32 {
	zones := s.GetZonesAtPoint(lat, lng)
	if len(zones) == 0 {
		return nil
	}

	effects := make(map[model.TargetParamType]float32)

	for _, zone := range zones {
		if zone.State != model.ZoneStateActive {
			continue
		}

		for _, effect := range zone.Effects {
			currentValue := effects[effect.ResourceType]

			// Apply effect based on type (buff or debuff)
			if effect.EffectType == model.EffectTypeBuff {
				effects[effect.ResourceType] = currentValue + effect.Value
			} else {
				effects[effect.ResourceType] = currentValue - effect.Value
			}
		}
	}

	return effects
}
