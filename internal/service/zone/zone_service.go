package zone

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"metalink/internal/config"
	"metalink/internal/model"
	pg "metalink/internal/postgres"
	redis_client "metalink/internal/redis"
	"metalink/internal/service/storage"
	"metalink/internal/util"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geo"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/quadtree"
)

const ZoneRedisKey = "zone"

type ZoneService struct {
	storage      storage.Storage[string, *model.Zone]
	spatialIndex *quadtree.Quadtree
	indexMutex   sync.RWMutex
	initialized  bool
	initMutex    sync.RWMutex
}

var (
	zoneServiceInstance *ZoneService
	zoneServiceOnce     sync.Once
)

// GetZoneService returns the singleton instance of the ZoneService
func GetZoneService() *ZoneService {
	zoneServiceOnce.Do(func() {
		zoneServiceInstance = &ZoneService{
			storage: storage.NewShardedMemoryStorage[string, *model.Zone](8, nil),
			spatialIndex: quadtree.New(orb.Bound{
				Min: orb.Point{-180, -90},
				Max: orb.Point{180, 90},
			}),
		}
	})
	return zoneServiceInstance
}

// InitService initializes the service by loading data from PostgreSQL and Redis
func (s *ZoneService) InitService(ctx context.Context) error {
	s.initMutex.Lock()
	defer s.initMutex.Unlock()

	if s.initialized {
		return nil
	}

	log.Println("Initializing ZoneService...")
	startTime := time.Now()

	// Step 1: Load data from PostgreSQL
	log.Println("Loading zones from PostgreSQL...")
	pgZones, err := s.loadAllZonesFromPG()
	if err != nil {
		return fmt.Errorf("failed to load zones from PostgreSQL: %w", err)
	}
	log.Printf("Loaded %d zones from PostgreSQL in %v", len(pgZones), time.Since(startTime))

	// Step 2: Load updates from Redis
	log.Println("Loading zone updates from Redis...")
	redisZones, err := s.loadAllZonesFromRedis(ctx)
	if err != nil {
		return fmt.Errorf("failed to load zones from Redis: %w", err)
	}
	log.Printf("Loaded %d zone updates from Redis", len(redisZones))

	// Step 3: Merge data and update spatial index
	log.Println("Merging data and updating spatial index...")
	mergedCount := s.mergeZonesIntoMemory(pgZones, redisZones)
	s.rebuildSpatialIndex()
	log.Printf("Merged %d newer zones from Redis", mergedCount)

	log.Printf("Initialization complete: %d zones in memory, took %v",
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

// loadAllZonesFromRedis loads all zones from Redis
func (s *ZoneService) loadAllZonesFromRedis(ctx context.Context) (map[string]*model.Zone, error) {
	client := redis_client.GetClient()
	var cursor uint64
	var keys []string
	pattern := fmt.Sprintf("%s:*", ZoneRedisKey)

	// Collect all zone keys
	for {
		batch, nextCursor, err := client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if len(keys) == 0 {
		return make(map[string]*model.Zone), nil
	}

	// Retrieve all zones in a single operation
	jsonData, err := client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	zones := make(map[string]*model.Zone)
	for _, data := range jsonData {
		if data == nil {
			continue
		}

		jsonStr, ok := data.(string)
		if !ok || jsonStr == "" {
			continue
		}

		redisZone := &model.ZoneRedis{}
		if err := json.Unmarshal([]byte(jsonStr), redisZone); err != nil {
			continue
		}

		// Convert Redis model to in-memory model
		zones[redisZone.ID] = model.ZoneFromRedis(redisZone)
	}

	return zones, nil
}

// mergeZonesIntoMemory merges zones from PostgreSQL and Redis into memory storage
func (s *ZoneService) mergeZonesIntoMemory(pgZones []*model.Zone, redisZones map[string]*model.Zone) int {
	// First load all PostgreSQL zones into memory
	for _, pgZone := range pgZones {
		s.storage.Set(pgZone.ID, pgZone)
	}

	// Override with Redis data where available (more recent)
	mergedCount := 0
	for id, redisZone := range redisZones {
		// Check if we should update based on timestamp
		existingZone, exists := s.storage.Get(id)
		if !exists || redisZone.UpdatedAt.After(existingZone.UpdatedAt) {
			// If exists, we need to merge fields that are not present in Redis model
			if exists {
				// Preserve fields that are not stored in Redis
				redisZone.Name = existingZone.Name
				redisZone.Type = existingZone.Type
				redisZone.CreatedAt = existingZone.CreatedAt
				redisZone.DeletedAt = existingZone.DeletedAt
			}
			s.storage.Set(id, redisZone)
			mergedCount++
		}
	}

	return mergedCount
}

// rebuildSpatialIndex rebuilds the spatial index for efficient searching
func (s *ZoneService) rebuildSpatialIndex() {
	s.indexMutex.Lock()
	defer s.indexMutex.Unlock()

	// Reset the current index
	s.spatialIndex = quadtree.New(orb.Bound{
		Min: orb.Point{-180, -90},
		Max: orb.Point{180, 90},
	})

	// Add all zones to the index
	s.storage.ForEach(func(id string, zone *model.Zone) bool {
		if zone.Polygon != nil && zone.Bounds != nil {
			s.spatialIndex.Add(zone.ID, *zone.Bounds)
		} else {
			// Parse geometry if not done yet
			polygon, bounds, err := s.parseGeometry(zone.Geometry)
			if err == nil {
				zone.Polygon = polygon
				zone.Bounds = bounds
				s.spatialIndex.Add(zone.ID, *bounds)
			}
		}
		return true
	})
}

// parseGeometry converts a GeoJSON string into a polygon and its bounds
func (s *ZoneService) parseGeometry(geometryStr string) (*orb.Polygon, *orb.Bound, error) {
	fc, err := geojson.UnmarshalFeatureCollection([]byte(geometryStr))
	if err != nil {
		return nil, nil, err
	}

	if len(fc.Features) == 0 {
		return nil, nil, fmt.Errorf("no features in geometry")
	}

	geom := fc.Features[0].Geometry
	if geom.Type != "Polygon" && geom.Type != "MultiPolygon" {
		return nil, nil, fmt.Errorf("geometry is not a polygon: %s", geom.Type)
	}

	var polygon orb.Polygon
	if geom.Type == "Polygon" {
		polygon = geom.Coordinates.(orb.Polygon)
	} else {
		// Take the first polygon from the multipolygon
		multiPolygon := geom.Coordinates.(orb.MultiPolygon)
		if len(multiPolygon) > 0 {
			polygon = multiPolygon[0]
		} else {
			return nil, nil, fmt.Errorf("empty multipolygon")
		}
	}

	bounds := polygon.Bound()
	return &polygon, &bounds, nil
}

// GetZonesAtPoint finds all zones containing the specified point
func (s *ZoneService) GetZonesAtPoint(lat, lng float64) []*model.Zone {
	if !s.initialized {
		log.Println("Warning: ZoneService not initialized")
		return nil
	}

	point := orb.Point{lng, lat}

	s.indexMutex.RLock()
	// Get IDs of zones that potentially contain the point
	candidates := s.spatialIndex.Find(point.Bound().Pad(0.0001))
	s.indexMutex.RUnlock()

	var result []*model.Zone

	for _, id := range candidates {
		zoneID, ok := id.(string)
		if !ok {
			continue
		}

		zone, exists := s.storage.Get(zoneID)
		if !exists || zone.Polygon == nil {
			continue
		}

		// Check if the point is inside the polygon
		if geo.PointInPolygon(point, *zone.Polygon) {
			result = append(result, zone)
		}
	}

	return result
}

// GetEffectsForTarget returns all effects for the specified target based on its position
func (s *ZoneService) GetEffectsForTarget(targetID string, lat, lng float64) map[model.ResourceType]float32 {
	zones := s.GetZonesAtPoint(lat, lng)
	effects := make(map[model.ResourceType]float32)

	for _, zone := range zones {
		if zone.State != model.ZoneStateActive {
			continue
		}

		// Accumulate effects by resource type
		current := effects[zone.ResourceType]
		if zone.EffectType == model.EffectTypeBuff {
			effects[zone.ResourceType] = current + zone.Value
		} else {
			effects[zone.ResourceType] = current - zone.Value
		}
	}

	return effects
}

// CreateZone creates a new zone
func (s *ZoneService) CreateZone(zone *model.Zone) error {
	if zone.ID == "" {
		zone.ID = util.GenerateID()
	}

	zone.CreatedAt = time.Now()
	zone.UpdatedAt = zone.CreatedAt

	// Parse and validate geometry
	polygon, bounds, err := s.parseGeometry(zone.Geometry)
	if err != nil {
		return fmt.Errorf("invalid geometry: %w", err)
	}

	zone.Polygon = polygon
	zone.Bounds = bounds

	// Add to storage
	s.storage.Set(zone.ID, zone)

	// Update spatial index
	s.indexMutex.Lock()
	s.spatialIndex.Add(zone.ID, *bounds)
	s.indexMutex.Unlock()

	return nil
}

// UpdateZone updates an existing zone
func (s *ZoneService) UpdateZone(id string, zoneUpdate *model.Zone) error {
	existingZone, exists := s.storage.Get(id)
	if !exists {
		return fmt.Errorf("zone not found: %s", id)
	}

	// Update fields
	existingZone.Name = zoneUpdate.Name
	existingZone.Type = zoneUpdate.Type
	existingZone.State = zoneUpdate.State
	existingZone.EffectType = zoneUpdate.EffectType
	existingZone.ResourceType = zoneUpdate.ResourceType
	existingZone.Value = zoneUpdate.Value
	existingZone.UpdatedAt = time.Now()

	// Update geometry if changed
	if existingZone.Geometry != zoneUpdate.Geometry {
		existingZone.Geometry = zoneUpdate.Geometry

		// Parse and validate new geometry
		polygon, bounds, err := s.parseGeometry(zoneUpdate.Geometry)
		if err != nil {
			return fmt.Errorf("invalid geometry: %w", err)
		}

		existingZone.Polygon = polygon
		existingZone.Bounds = bounds

		// Update spatial index
		s.indexMutex.Lock()
		s.spatialIndex.Remove(id, false) // Remove old entry
		s.spatialIndex.Add(id, *bounds)  // Add new entry
		s.indexMutex.Unlock()
	}

	// Save updated zone
	s.storage.Set(id, existingZone)

	return nil
}

// DeleteZone soft-deletes a zone
func (s *ZoneService) DeleteZone(id string) error {
	zone, exists := s.storage.Get(id)
	if !exists {
		return fmt.Errorf("zone not found: %s", id)
	}

	// Soft delete
	now := time.Now()
	zone.DeletedAt.Time = now
	zone.DeletedAt.Valid = true
	zone.UpdatedAt = now
	zone.State = model.ZoneStateInactive

	// Update storage
	s.storage.Set(id, zone)

	// Remove from spatial index
	s.indexMutex.Lock()
	s.spatialIndex.Remove(id, false)
	s.indexMutex.Unlock()

	return nil
}

// GetZonesInBounds returns all zones that intersect with the given bounds
func (s *ZoneService) GetZonesInBounds(minLat, minLng, maxLat, maxLng float64) []*model.Zone {
	if !s.initialized {
		log.Println("Warning: ZoneService not initialized")
		return nil
	}

	bounds := orb.Bound{
		Min: orb.Point{minLng, minLat},
		Max: orb.Point{maxLng, maxLat},
	}

	s.indexMutex.RLock()
	// Get IDs of zones that potentially intersect with the bounds
	candidates := s.spatialIndex.Find(bounds)
	s.indexMutex.RUnlock()

	var result []*model.Zone

	for _, id := range candidates {
		zoneID, ok := id.(string)
		if !ok {
			continue
		}

		zone, exists := s.storage.Get(zoneID)
		if !exists || zone.Polygon == nil {
			continue
		}

		// Add to result
		result = append(result, zone)
	}

	return result
}

// SaveAllZonesToRedis saves all zones to Redis
func (s *ZoneService) SaveAllZonesToRedis() error {
	startTime := time.Now()
	client := redis_client.GetClient()
	ctx := context.Background()

	// Get dirty items (modified since last save)
	dirtyZones := s.storage.GetDirty()
	if len(dirtyZones) == 0 {
		log.Println("No dirty zones to save to Redis")
		return nil
	}

	// Prepare pipeline
	pipe := client.Pipeline()
	savedCount := 0

	for _, zone := range dirtyZones {
		// Skip deleted zones
		if zone.DeletedAt.Valid {
			// Delete from Redis if exists
			key := fmt.Sprintf("%s:%s", ZoneRedisKey, zone.ID)
			pipe.Del(ctx, key)
			continue
		}

		// Convert to Redis model
		redisZone := zone.ToRedis()

		// Serialize to JSON
		data, err := json.Marshal(redisZone)
		if err != nil {
			log.Printf("Error marshaling zone %s: %v", zone.ID, err)
			continue
		}

		// Add to Redis
		key := fmt.Sprintf("%s:%s", ZoneRedisKey, zone.ID)
		pipe.Set(ctx, key, string(data), 0)
		savedCount++
	}

	// Execute pipeline
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to save zones to Redis: %w", err)
	}

	// Clear dirty flag
	var dirtyKeys []string
	for _, zone := range dirtyZones {
		dirtyKeys = append(dirtyKeys, zone.ID)
	}
	s.storage.ClearDirty(dirtyKeys)

	log.Printf("Saved %d zones to Redis in %v", savedCount, time.Since(startTime))
	return nil
}

// SaveAllZonesToPG saves all zones to PostgreSQL
func (s *ZoneService) SaveAllZonesToPG() error {
	startTime := time.Now()
	db := pg.GetDB()

	// Get all zones
	dirtyZones := s.storage.GetDirty()
	if len(dirtyZones) == 0 {
		log.Println("No dirty zones to save to PostgreSQL")
		return nil
	}

	// Prepare batch operation
	var pgZones []*model.ZonePG
	deletedIDs := make([]string, 0)

	for _, zone := range dirtyZones {
		if zone.DeletedAt.Valid {
			deletedIDs = append(deletedIDs, zone.ID)
		} else {
			pgZones = append(pgZones, zone.ToPG())
		}
	}

	// Start transaction
	tx := db.Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}

	// Handle deleted zones
	if len(deletedIDs) > 0 {
		result := tx.Model(&model.ZonePG{}).Where("id IN ?", deletedIDs).Update("deleted_at", time.Now())
		if result.Error != nil {
			tx.Rollback()
			return fmt.Errorf("failed to delete zones: %w", result.Error)
		}
		log.Printf("Marked %d zones as deleted in PostgreSQL", len(deletedIDs))
	}

	// Save zones in batches to avoid large transactions
	batchSize := 100
	totalSaved := 0

	for i := 0; i < len(pgZones); i += batchSize {
		end := i + batchSize
		if end > len(pgZones) {
			end = len(pgZones)
		}

		batch := pgZones[i:end]

		// Upsert batch
		for _, pgZone := range batch {
			result := tx.Save(pgZone)
			if result.Error != nil {
				tx.Rollback()
				return fmt.Errorf("failed to save zone %s: %w", pgZone.ID, result.Error)
			}
		}

		totalSaved += len(batch)
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Clear dirty flag
	var dirtyKeys []string
	for _, zone := range dirtyZones {
		dirtyKeys = append(dirtyKeys, zone.ID)
	}
	s.storage.ClearDirty(dirtyKeys)

	log.Printf("Saved %d zones to PostgreSQL in %v", totalSaved, time.Since(startTime))
	return nil
}

// StartPersistenceWorkers starts workers for periodic data saving
func (s *ZoneService) StartPersistenceWorkers() {
	// Redis backup worker
	go func() {
		ticker := time.NewTicker(config.RedisBackupInterval)
		for range ticker.C {
			if err := s.SaveAllZonesToRedis(); err != nil {
				log.Printf("Error saving zones to Redis: %v", err)
			}
		}
	}()

	// PostgreSQL backup worker
	go func() {
		ticker := time.NewTicker(config.PostgresBackupInterval)
		for range ticker.C {
			if err := s.SaveAllZonesToPG(); err != nil {
				log.Printf("Error saving zones to PostgreSQL: %v", err)
			}
		}
	}()

	log.Println("Zone persistence workers started")
}
