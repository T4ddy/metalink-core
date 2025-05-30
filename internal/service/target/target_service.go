package target

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"metalink/internal/config"
	"metalink/internal/model"
	pg "metalink/internal/postgres"
	redis_client "metalink/internal/redis"
	"metalink/internal/service/storage"
	"metalink/internal/service/zone"
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
		// Use sharded storage for better performance with large datasets
		targetServiceInstance = &TargetService{
			storage: storage.NewShardedMemoryStorage[string, *model.Target](16, nil),
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

// ProcessTargets updates target positions and calculates zone effects
func (s *TargetService) ProcessTargets() {
	// Step 1: Process movements
	movementStart := time.Now()
	processedMovements := 0
	s.storage.ForEach(func(id string, target *model.Target) bool {
		if target.State == model.TargetStateWalking {
			s.updateTargetPosition(target)
			processedMovements++
		}
		return true
	})
	movementDuration := time.Since(movementStart)

	// Step 2: Process effects
	effectsStart := time.Now()
	zoneService := zone.GetZoneService()
	processedEffects := 0
	totalEffectsValue := 0.0
	s.storage.ForEach(func(id string, target *model.Target) bool {
		zoneService.GetEffectsForTarget(float64(target.CurrentLat), float64(target.CurrentLng))
		// for _, effect := range effects {
		// 	totalEffectsValue += float64(effect)
		// }
		processedEffects++
		return true
	})
	effectsDuration := time.Since(effectsStart)

	log.Printf("MOVEMENTS processed in >> %v", movementDuration)
	log.Printf("EFFECTS processed in >> %v", effectsDuration)
	log.Printf("Total effects value: %f", totalEffectsValue)
}

// updateTargetPosition updates a target's position based on its speed and route
func (s *TargetService) updateTargetPosition(target *model.Target) {
	// Decode route points if not already decoded
	if target.RoutePoints == nil {
		target.RoutePoints = util.DecodePolyline(target.Route)
	}

	remainingDistance := float64(target.Speed * float32(config.TargetsWorkerInterval.Seconds()))

	// Initialize target position if not set
	if target.NextPointIndex <= 0 {
		if len(target.RoutePoints) > 0 {
			target.CurrentLat = float32(target.RoutePoints[0][0])
			target.CurrentLng = float32(target.RoutePoints[0][1])
			target.NextPointIndex = 1
		} else {
			// No route points, can't move
			target.UpdatedAt = time.Now()
			s.storage.Set(target.ID, target)
			return
		}
	}

	// Move target along route
	for remainingDistance > 0 && target.NextPointIndex < len(target.RoutePoints) {
		// Get next point coordinates
		nextPointLat := target.RoutePoints[target.NextPointIndex][0]
		nextPointLng := target.RoutePoints[target.NextPointIndex][1]

		// Calculate distance to next point
		distance := util.HaversineDistance(
			float64(target.CurrentLat),
			float64(target.CurrentLng),
			nextPointLat,
			nextPointLng,
		)

		if remainingDistance >= distance {
			// We can reach (or pass) the next point
			target.CurrentLat = float32(nextPointLat)
			target.CurrentLng = float32(nextPointLng)
			remainingDistance -= distance
			target.NextPointIndex++

			// Check if we reached the end of the route
			if target.NextPointIndex >= len(target.RoutePoints) {
				target.State = model.TargetStateStopped
				break
			}
		} else {
			// Move partially toward the next point
			newPosition := util.MoveToward(
				float64(target.CurrentLat),
				float64(target.CurrentLng),
				nextPointLat,
				nextPointLng,
				remainingDistance,
			)

			target.CurrentLat = float32(newPosition[0])
			target.CurrentLng = float32(newPosition[1])
			remainingDistance = 0
		}
	}

	// Mark the target as updated
	target.UpdatedAt = time.Now()
	s.storage.Set(target.ID, target)
}

// StartPersistenceWorkers starts workers for persisting data to Redis and PostgreSQL
func (s *TargetService) StartPersistenceWorkers() {
	// Redis persistence (every minute)
	redisTimer := time.NewTicker(config.RedisBackupInterval)
	go func() {
		for range redisTimer.C {
			startTime := time.Now()
			if err := s.SaveAllTargetsToRedisV4(); err != nil {
				log.Printf("Error saving to Redis: %v", err)
			}
			log.Printf("Time taken to save dirty targets to REDIS << %v", time.Since(startTime))
		}
	}()

	// PostgreSQL persistence (every hour)
	pgTimer := time.NewTicker(config.PostgresBackupInterval)
	go func() {
		for range pgTimer.C {
			startTime := time.Now() // Start timing
			if err := s.SaveAllTargetsToPGv2(); err != nil {
				log.Printf("Error saving to PostgreSQL: %v", err)
			}
			log.Printf("Time taken to save all targets to POSTGRESQL << %v", time.Since(startTime))
		}
	}()
}

// SaveAllTargetsToPGv3 saves all targets to PostgreSQL using bulk upsert SQL
func (s *TargetService) SaveAllTargetsToPGv2() error {
	allTargets := s.storage.GetAllValues()
	if len(allTargets) == 0 {
		return nil
	}

	db := pg.GetDB()
	batchSize := 2000

	// Process in batches to avoid overwhelming the database
	for i := 0; i < len(allTargets); i += batchSize {
		end := i + batchSize
		if end > len(allTargets) {
			end = len(allTargets)
		}

		batch := allTargets[i:end]

		err := db.Transaction(func(tx *gorm.DB) error {
			// Prepare for bulk upsert
			sql := `INSERT INTO targets (id, name, speed, state, current_lat, current_lng, 
                   target_lat, target_lng, next_point_index, route, created_at, updated_at)
                   VALUES `

			values := []interface{}{}
			placeholders := []string{}

			for i, target := range batch {
				pgTarget := target.ToPG()
				offset := i * 12

				placeholders = append(placeholders,
					fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
						offset+1, offset+2, offset+3, offset+4, offset+5, offset+6,
						offset+7, offset+8, offset+9, offset+10, offset+11, offset+12))

				values = append(values,
					pgTarget.ID, pgTarget.Name, pgTarget.Speed, pgTarget.State,
					pgTarget.CurrentLat, pgTarget.CurrentLng, pgTarget.TargetLat, pgTarget.TargetLng,
					pgTarget.NextPointIndex, pgTarget.Route, pgTarget.CreatedAt, pgTarget.UpdatedAt)
			}

			sql += strings.Join(placeholders, ",")
			sql += ` ON CONFLICT (id) DO UPDATE SET 
                  name = EXCLUDED.name,
                  speed = EXCLUDED.speed,
                  state = EXCLUDED.state,
                  current_lat = EXCLUDED.current_lat,
                  current_lng = EXCLUDED.current_lng,
                  target_lat = EXCLUDED.target_lat,
                  target_lng = EXCLUDED.target_lng,
                  next_point_index = EXCLUDED.next_point_index,
                  route = EXCLUDED.route,
                  updated_at = EXCLUDED.updated_at`

			return tx.Exec(sql, values...).Error
		})

		if err != nil {
			return err
		}

		if end%10000 == 0 {
			log.Printf("Saved batch of %d targets to PostgreSQL (%d/%d)",
				len(batch), end, len(allTargets))
		}
	}

	return nil
}

// SaveAllTargetsToRedisV4 saves targets to Redis using parallel processing
func (s *TargetService) SaveAllTargetsToRedisV4() error {
	allTargets := s.storage.GetAllValues()

	if len(allTargets) == 0 {
		return nil
	}

	// Define parallel processing parameters
	numWorkers := 8
	batchSize := 500
	targetsPerWorker := len(allTargets) / numWorkers

	// Setup wait group and error channel
	var wg sync.WaitGroup
	wg.Add(numWorkers)
	errChan := make(chan error, numWorkers)

	// Create atomic counter for tracking progress
	var saved int64

	// Launch worker goroutines
	for w := 0; w < numWorkers; w++ {
		// Calculate start and end for this worker
		start := w * targetsPerWorker
		end := start + targetsPerWorker
		if w == numWorkers-1 {
			end = len(allTargets) // Last worker takes remainder
		}

		go func(workerStart, workerEnd int) {
			defer wg.Done()

			client := redis_client.GetClient()
			ctx := context.Background()

			// Process in smaller batches
			for i := workerStart; i < workerEnd; i += batchSize {
				batchEnd := i + batchSize
				if batchEnd > workerEnd {
					batchEnd = workerEnd
				}

				pipe := client.Pipeline()
				for j := i; j < batchEnd; j++ {
					target := allTargets[j]
					targetKey := fmt.Sprintf("%s:%s", TargetRedisKey, target.ID)
					targetJSON, err := json.Marshal(target.ToRedis())
					if err != nil {
						errChan <- err
						return
					}
					pipe.Set(ctx, targetKey, targetJSON, 0)
				}

				_, err := pipe.Exec(ctx)
				if err != nil {
					errChan <- err
					return
				}

				// Update progress
				newCount := atomic.AddInt64(&saved, int64(batchEnd-i))
				if newCount%100000 == 0 || newCount == int64(len(allTargets)) {
					log.Printf("Saved %d/%d targets to Redis", newCount, len(allTargets))
				}
			}
		}(start, end)
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
					id, err := util.GenerateUUIDWithLength(6)
					if err != nil {
						errChan <- err
						return
					}

					target := model.TargetPG{
						ID:             id,
						Name:           "Target " + id,
						Speed:          config.DefaultTargetSpeed,
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
