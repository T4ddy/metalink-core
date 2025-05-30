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
		log.Println("ZoneService already initialized, skipping")
		return nil
	}

	log.Println("=== Starting ZoneService initialization ===")
	totalStartTime := time.Now()

	// Step 1: Load data from PostgreSQL
	log.Println("Step 1: Loading zones from PostgreSQL...")
	pgLoadStart := time.Now()
	zones, err := s.loadAllZonesFromPG()
	if err != nil {
		log.Printf("ERROR: Failed to load zones from PostgreSQL after %v: %v", time.Since(pgLoadStart), err)
		return fmt.Errorf("failed to load zones from PostgreSQL: %w", err)
	}
	pgLoadDuration := time.Since(pgLoadStart)
	log.Printf("PostgreSQL loading completed: %d zones loaded in %v", len(zones), pgLoadDuration)

	// Step 2: Pre-calculate all zone effects
	log.Println("Step 2: Pre-calculating zone effects...")
	effectsStart := time.Now()
	for _, zone := range zones {
		zone.CalculateEffects()
	}
	effectsDuration := time.Since(effectsStart)
	log.Printf("Effects calculation completed: %d zones processed in %v", len(zones), effectsDuration)

	// Step 3: Load zones into memory storage
	log.Println("Step 3: Loading zones into memory storage...")
	memoryLoadStart := time.Now()
	for i, zone := range zones {
		s.storage.Set(zone.ID, zone)

		if (i+1)%100000 == 0 || i == len(zones)-1 {
			log.Printf("Memory loading progress: %d/%d zones (%.1f%%)",
				i+1, len(zones), float64(i+1)/float64(len(zones))*100)
		}
	}
	memoryLoadDuration := time.Since(memoryLoadStart)
	log.Printf("Memory loading completed: %d zones stored in %v", len(zones), memoryLoadDuration)

	// Step 4: Build spatial index
	log.Println("Step 4: Building spatial R-tree index...")
	indexBuildStart := time.Now()
	s.rebuildSpatialIndex()
	indexBuildDuration := time.Since(indexBuildStart)
	log.Printf("Spatial index built in %v", indexBuildDuration)

	// Final summary
	totalDuration := time.Since(totalStartTime)
	log.Printf("=== ZoneService initialization completed ===")
	log.Printf("Total zones: %d", s.storage.Count())
	log.Printf("Total time: %v", totalDuration)
	log.Printf("Breakdown:")
	log.Printf("  - PostgreSQL loading: %v (%.1f%%)", pgLoadDuration, float64(pgLoadDuration.Nanoseconds())/float64(totalDuration.Nanoseconds())*100)
	log.Printf("  - Effects calculation: %v (%.1f%%)", effectsDuration, float64(effectsDuration.Nanoseconds())/float64(totalDuration.Nanoseconds())*100)
	log.Printf("  - Memory storage: %v (%.1f%%)", memoryLoadDuration, float64(memoryLoadDuration.Nanoseconds())/float64(totalDuration.Nanoseconds())*100)
	log.Printf("  - Spatial indexing: %v (%.1f%%)", indexBuildDuration, float64(indexBuildDuration.Nanoseconds())/float64(totalDuration.Nanoseconds())*100)

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
		// Effects will be calculated later
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
			// Create polygon from corner points
			polygon, bounds := s.createPolygonFromCorners(zone)
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
		return true
	})
}

// createPolygonFromCorners creates a polygon from the four corner points
func (s *ZoneService) createPolygonFromCorners(zone *model.Zone) (*orb.Polygon, *orb.Bound) {
	// Create a polygon from the four corners
	// Order matters: we go clockwise from top-left
	ring := orb.Ring{
		orb.Point{zone.TopLeftLatLon[1], zone.TopLeftLatLon[0]},         // [lon, lat]
		orb.Point{zone.TopRightLatLon[1], zone.TopRightLatLon[0]},       // [lon, lat]
		orb.Point{zone.BottomRightLatLon[1], zone.BottomRightLatLon[0]}, // [lon, lat]
		orb.Point{zone.BottomLeftLatLon[1], zone.BottomLeftLatLon[0]},   // [lon, lat]
		orb.Point{zone.TopLeftLatLon[1], zone.TopLeftLatLon[0]},         // Close the ring
	}

	polygon := orb.Polygon{ring}
	bound := polygon.Bound()
	return &polygon, &bound
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
		// Ensure effects are calculated
		if len(zone.Effects) == 0 {
			zone.CalculateEffects()
		}

		for _, effect := range zone.Effects {
			// Simply add the effect value (positive or negative)
			effects[effect.ResourceType] += effect.Value
		}
	}

	return effects
}
