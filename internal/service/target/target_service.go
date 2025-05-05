package target

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"metalink/internal/model"
	pg "metalink/internal/postgres"
	redis_client "metalink/internal/redis"
	"metalink/internal/service/storage"
	"metalink/internal/util"

	"gorm.io/gorm"
)

const TargetRedisKey = "target"

type TargetService struct {
	storage     storage.Storage[string, *model.Target]
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
			storage: storage.NewMemoryStorage[string, *model.Target](),
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
	log.Printf("Merged %d newer targets from Redis", mergedCount)

	log.Printf("Initialization complete: %d targets in memory, took %v",
		s.storage.Count(), time.Since(startTime))

	s.initialized = true
	return nil
}

// loadAllTargetsFromPG loads all targets from PostgreSQL
func (s *TargetService) loadAllTargetsFromPG() ([]*model.Target, error) {
	db := pg.GetDB()
	var pgTargets []*model.TargetPG

	result := db.Find(&pgTargets)
	if result.Error != nil {
		return nil, result.Error
	}

	// Convert PG models to in-memory models
	targets := make([]*model.Target, len(pgTargets))
	for i, pgTarget := range pgTargets {
		targets[i] = model.FromPG(pgTarget)
	}

	return targets, nil
}

// loadAllTargetsFromRedis loads all targets from Redis
func (s *TargetService) loadAllTargetsFromRedis(ctx context.Context) (map[string]*model.Target, error) {
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
		return make(map[string]*model.Target), nil
	}

	// Retrieve all targets in a single operation
	jsonData, err := client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	targets := make(map[string]*model.Target)
	for _, data := range jsonData {
		if data == nil {
			continue
		}

		jsonStr, ok := data.(string)
		if !ok || jsonStr == "" {
			continue
		}

		redisTarget := &model.TargetRedis{}
		if err := json.Unmarshal([]byte(jsonStr), redisTarget); err != nil {
			continue
		}

		// Convert Redis model to in-memory model
		targets[redisTarget.ID] = model.FromRedis(redisTarget)
	}

	return targets, nil
}

// mergeTargetsIntoMemory merges targets from PostgreSQL and Redis into memory storage
func (s *TargetService) mergeTargetsIntoMemory(pgTargets []*model.Target, redisTargets map[string]*model.Target) int {
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
			// If exists, we need to merge fields that are not present in Redis model
			if exists {
				// Preserve fields that are not stored in Redis
				// TODO: FIX THIS MERGE
				redisTarget.Name = existingTarget.Name
				redisTarget.Route = existingTarget.Route
				redisTarget.CreatedAt = existingTarget.CreatedAt
				redisTarget.DeletedAt = existingTarget.DeletedAt
				redisTarget.RoutePoints = existingTarget.RoutePoints
			}
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
	s.storage.ForEach(func(id string, target *model.Target) bool {
		if target.State == model.TargetStateWalking {
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
func (s *TargetService) updateTargetPosition(target *model.Target) {
	// Example movement logic
	// In a real implementation, you'd decode the route, calculate the next position, etc.

	// if target.RoutePoints == nil {
	// 	target.RoutePoints = util.DecodePolyline(target.Route)
	// }

	// timeFromLastUpdate := time.Since(target.UpdatedAt)
	// traveledDistance := target.Speed * float32(timeFromLastUpdate.Seconds())

	// Mark the target as updated
	target.UpdatedAt = time.Now()
	s.storage.Set(target.ID, target)
}

// StartPersistenceWorkers starts workers for persisting data to Redis and PostgreSQL
func (s *TargetService) StartPersistenceWorkers() {
	// Redis persistence (every minute)
	redisTimer := time.NewTicker(5 * time.Second)
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
		targetJSON, err := json.Marshal(target.ToRedis())
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
				result := tx.Save(target.ToPG())
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

// TEST FUNCTIONS
// TEST FUNCTIONS
// TEST FUNCTIONS

// DeleteAllTargets removes all targets from Redis storage
func (s *TargetService) DeleteAllRedisTargets() error {
	client := redis_client.GetClient()
	ctx := context.Background()

	// Use Scan instead of Keys to improve performance with large datasets
	var cursor uint64
	var keys []string
	pattern := fmt.Sprintf("%s:*", TargetRedisKey)

	for {
		var batch []string
		var err error
		batch, cursor, err = client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		keys = append(keys, batch...)
		if cursor == 0 {
			break
		}
	}

	// If no targets exist, return early
	if len(keys) == 0 {
		return nil
	}

	// Delete all target keys
	err := client.Del(ctx, keys...).Err()
	if err != nil {
		return err
	}

	log.Printf("Deleted %d targets", len(keys))
	return nil
}

func (s *TargetService) SeedTestTargetsPGParallel(count int) error {
	db := pg.GetDB()

	// Define number of workers
	numWorkers := 8
	batchSize := 500

	// Calculate targets per worker
	targetsPerWorker := count / numWorkers

	// Create wait group for waiting for all goroutines to complete
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	// Create channel for collecting errors
	errChan := make(chan error, numWorkers)

	// Create atomic counter for tracking progress
	var created int64

	// Launch worker goroutines
	for w := 0; w < numWorkers; w++ {
		// Calculate start and end for this worker
		start := w * targetsPerWorker
		end := start + targetsPerWorker
		if w == numWorkers-1 {
			end = count // Last worker takes remainder
		}

		go func(workerID, start, end int) {
			defer wg.Done()

			// Process batches in this worker's range
			for i := start; i < end; i += batchSize {
				// Calculate current batch size
				currentBatchSize := batchSize
				if i+batchSize > end {
					currentBatchSize = end - i
				}

				var targets []model.TargetPG
				for j := 0; j < currentBatchSize; j++ {
					id, err := util.GenerateUniqueID(6)
					if err != nil {
						errChan <- err
						return
					}

					target := model.TargetPG{
						ID:             id,
						Name:           "Target " + id,
						Speed:          10,
						TargetLat:      0,
						TargetLng:      0,
						Route:          "eyiaHbyokV@AAsPl@@@mG|@?B_BDQN@HCDGBMB_CzB@BsC@gJAE@sC?cNAY@q@@G?uBAgA?yI@a@EM?k@}D@cCDMGEKKeAIk@Me@EYSgA_@kCoBqMyAeKGk@Ai@EMIGAIEAG_@BaBO?y@IEECG@WqHGFiK}m@YmDEo[U?hA",
						State:          model.TargetState(model.TargetStateWalking),
						NextPointIndex: -1,
					}

					targets = append(targets, target)
				}

				// Use transaction for batch insertion
				err := db.Transaction(func(tx *gorm.DB) error {
					result := tx.CreateInBatches(targets, currentBatchSize)
					return result.Error
				})

				if err != nil {
					errChan <- err
					return
				}

				// Increment atomic counter for progress tracking
				newCount := atomic.AddInt64(&created, int64(currentBatchSize))
				if newCount%10000 == 0 || newCount == int64(count) {
					log.Printf("Seeded %d targets of %d in PostgreSQL", newCount, count)
				}
			}
		}(w, start, end)
	}

	// Wait for all workers to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	log.Printf("Successfully seeded %d targets in PostgreSQL", count)
	return nil
}
