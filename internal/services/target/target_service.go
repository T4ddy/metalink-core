package target

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"metalink/internal/models"
	pg "metalink/internal/postgres"
	redis_client "metalink/internal/redis"
	"metalink/internal/services/storage"

	"gorm.io/gorm"
)

const TargetRedisKey = "target"

type TargetService struct {
	storage     storage.Storage[string, *models.Target]
	initialized bool
	initMutex   sync.RWMutex
}

var (
	targetServiceInstance *TargetService
	targetServiceOnce     sync.Once
)

// GetTargetService returns the singleton instance of GetTargetService.
func GetTargetService() *TargetService {
	targetServiceOnce.Do(func() {
		targetServiceInstance = &TargetService{
			storage: storage.NewMemoryStorage[string, *models.Target](),
		}
	})
	return targetServiceInstance
}

// InitService initializes the service by loading data from PostgreSQL and Redis
func (s *TargetService) InitService(ctx context.Context) error {
	s.initMutex.Lock()
	defer s.initMutex.Unlock()

	if s.initialized {
		return nil
	}

	log.Println("Initializing TargetService...")
	startTime := time.Now()

	// Step 1: Load full data from PostgreSQL
	log.Println("Loading targets from PostgreSQL...")
	pgTargets, err := s.loadAllTargetsFromPG()
	if err != nil {
		return fmt.Errorf("failed to load targets from PostgreSQL: %w", err)
	}
	log.Printf("Loaded %d targets from PostgreSQL in %v", len(pgTargets), time.Since(startTime))

	// Step 2: Load updates from Redis (with timestamps)
	log.Println("Loading target updates from Redis...")
	redisTargets, err := s.loadAllTargetsFromRedis(ctx)
	if err != nil {
		return fmt.Errorf("failed to load targets from Redis: %w", err)
	}
	log.Printf("Loaded %d target updates from Redis", len(redisTargets))

	// Step 3: Merge data (Redis updates override PostgreSQL data)
	log.Println("Merging data from PostgreSQL and Redis...")
	mergedCount := s.mergeTargetsIntoMemory(pgTargets, redisTargets)
	log.Printf("Merged %d targets from PostgreSQL and Redis", mergedCount)

	log.Printf("Initialization complete: %d targets in memory, took %v",
		s.storage.Count(), time.Since(startTime))

	s.initialized = true
	return nil
}

// loadAllTargetsFromPG loads all targets from PostgreSQL
func (s *TargetService) loadAllTargetsFromPG() ([]*models.Target, error) {
	db := pg.GetDB()
	var targets []*models.Target

	result := db.Find(&targets)
	if result.Error != nil {
		return nil, result.Error
	}

	return targets, nil
}

// loadAllTargetsFromRedis loads all targets from Redis
func (s *TargetService) loadAllTargetsFromRedis(ctx context.Context) (map[string]*models.Target, error) {
	client := redis_client.GetClient()
	var cursor uint64
	var keys []string
	pattern := fmt.Sprintf("%s:*", TargetRedisKey)

	// Collect all target keys
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
		return make(map[string]*models.Target), nil
	}

	// Retrieve all targets in a single operation
	jsonData, err := client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	targets := make(map[string]*models.Target)
	for _, data := range jsonData {
		if data == nil {
			continue
		}

		jsonStr, ok := data.(string)
		if !ok || jsonStr == "" {
			continue
		}

		target := &models.Target{}
		if err := json.Unmarshal([]byte(jsonStr), target); err != nil {
			continue
		}

		targets[target.ID] = target
	}

	return targets, nil
}

// mergeTargetsIntoMemory merges targets from PostgreSQL and Redis into memory storage
func (s *TargetService) mergeTargetsIntoMemory(pgTargets []*models.Target, redisTargets map[string]*models.Target) int {
	// First load all PostgreSQL targets into memory
	for _, pgTarget := range pgTargets {
		s.storage.Set(pgTarget.ID, pgTarget)
	}

	// Override with Redis data where available (more recent)
	mergedCount := 0
	for id, redisTarget := range redisTargets {
		// Check if we should update based on timestamp
		existingTarget, exists := s.storage.Get(id)
		if !exists || redisTarget.UpdatedAt.After(existingTarget.UpdatedAt) {
			s.storage.Set(id, redisTarget)
			mergedCount++
		}
	}

	return mergedCount
}

// ProcessTargetMovements updates target positions based on their routes and speeds
func (s *TargetService) ProcessTargetMovements() {
	startTime := time.Now()

	// For each target, calculate new position
	processedCount := 0
	s.storage.ForEach(func(id string, target *models.Target) bool {
		if target.State == models.TargetStateWalking {
			// Calculate new position based on route and speed
			// This would be your movement logic
			s.updateTargetPosition(target)
			processedCount++
		}
		return true
	})

	log.Printf("Processed movements for %d targets in %v",
		processedCount, time.Since(startTime))
}

// updateTargetPosition updates a target's position based on its speed and route
func (s *TargetService) updateTargetPosition(target *models.Target) {
	// Example movement logic
	// In a real implementation, you'd decode the route, calculate the next position, etc.

	// Mark the target as updated
	target.UpdatedAt = time.Now()
	s.storage.Set(target.ID, target)
}

// StartPersistenceWorkers starts workers for persisting data to Redis and PostgreSQL
func (s *TargetService) StartPersistenceWorkers() {
	// Redis persistence (every minute)
	redisTimer := time.NewTicker(3 * time.Second)
	go func() {
		for range redisTimer.C {
			if err := s.SaveDirtyTargetsToRedis(); err != nil {
				log.Printf("Error saving to Redis: %v", err)
			}
		}
	}()

	// PostgreSQL persistence (every hour)
	pgTimer := time.NewTicker(20 * time.Second)
	go func() {
		for range pgTimer.C {
			if err := s.SaveAllTargetsToPG(); err != nil {
				log.Printf("Error saving to PostgreSQL: %v", err)
			}
		}
	}()
}

// SaveDirtyTargetsToRedis saves modified targets to Redis
func (s *TargetService) SaveDirtyTargetsToRedis() error {
	dirtyTargets := s.storage.GetDirty()
	if len(dirtyTargets) == 0 {
		return nil
	}

	client := redis_client.GetClient()
	ctx := context.Background()
	pipe := client.Pipeline()

	// Collect keys to clear flags after successful save
	keys := make([]string, 0, len(dirtyTargets))

	for id, target := range dirtyTargets {
		targetKey := fmt.Sprintf("%s:%s", TargetRedisKey, id)
		targetJSON, err := json.Marshal(target)
		if err != nil {
			return err
		}
		pipe.Set(ctx, targetKey, targetJSON, 0)
		keys = append(keys, id)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return err
	}

	// Clear flags only after successful save
	s.storage.ClearDirty(keys)

	log.Printf("Saved %d targets to Redis", len(dirtyTargets))
	return nil
}

// SaveAllTargetsToPG saves all targets to PostgreSQL in batches
func (s *TargetService) SaveAllTargetsToPG() error {
	allTargets := s.storage.GetAllValues()
	if len(allTargets) == 0 {
		return nil
	}

	db := pg.GetDB()
	batchSize := 1000

	// Process in batches to avoid overwhelming the database
	for i := 0; i < len(allTargets); i += batchSize {
		end := i + batchSize
		if end > len(allTargets) {
			end = len(allTargets)
		}

		batch := allTargets[i:end]

		err := db.Transaction(func(tx *gorm.DB) error {
			for _, target := range batch {
				result := tx.Save(target)
				if result.Error != nil {
					return result.Error
				}
			}
			return nil
		})

		if err != nil {
			return err
		}

		log.Printf("Saved batch of %d targets to PostgreSQL (%d/%d)",
			len(batch), end, len(allTargets))
	}

	return nil
}
