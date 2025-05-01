package target

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	redis_client "metalink/internal/redis"
	"metalink/internal/utils"
	"sync"
)

const TargetRedisKey = "target"

type TargetService struct{}

var (
	targetServiceInstance *TargetService
	targetServiceOnce     sync.Once
)

// GetTargetService returns the singleton instance of GetTargetService.
func GetTargetService() *TargetService {
	targetServiceOnce.Do(func() {
		targetServiceInstance = &TargetService{}
	})
	return targetServiceInstance
}

// AddTarget adds a new target to Redis storage using JSON
func (s *TargetService) AddTarget(target *Target) error {
	client := redis_client.GetClient()
	ctx := context.Background()

	targetKey := fmt.Sprintf("%s:%s", TargetRedisKey, target.ID)

	// Marshal target to JSON
	targetJSON, err := json.Marshal(target)
	if err != nil {
		return err
	}

	// Store target in Redis as JSON string
	err = client.Set(ctx, targetKey, targetJSON, 0).Err()
	return err
}

// GetAllTargets retrieves all targets from Redis storage using MGET
func (s *TargetService) GetAllTargets() ([]*Target, error) {
	client := redis_client.GetClient()
	ctx := context.Background()

	var cursor uint64
	var keys []string
	pattern := fmt.Sprintf("%s:*", TargetRedisKey)

	// First collect all target keys
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
		return []*Target{}, nil
	}

	// Use MGET to retrieve all targets in a single operation
	jsonData, err := client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	targets := make([]*Target, 0, len(jsonData))
	for _, data := range jsonData {
		if data == nil {
			continue
		}

		jsonStr, ok := data.(string)
		if !ok || jsonStr == "" {
			continue
		}

		target := &Target{}
		if err := json.Unmarshal([]byte(jsonStr), target); err != nil {
			continue
		}

		targets = append(targets, target)
	}

	return targets, nil
}

// SeedTestTargets creates the specified number of test targets
func (s *TargetService) SeedTestTargets(count int) error {
	client := redis_client.GetClient()
	ctx := context.Background()

	// Use batch size of 100 or smaller if count is less than 100
	batchSize := 100
	if count < batchSize {
		batchSize = count
	}

	for i := 0; i < count; i += batchSize {
		// Calculate current batch size (may be smaller for the last batch)
		currentBatchSize := batchSize
		if i+batchSize > count {
			currentBatchSize = count - i
		}

		pipe := client.Pipeline()

		for j := 0; j < currentBatchSize; j++ {
			id, err := utils.GenerateUniqueID(6)
			if err != nil {
				return err
			}

			target := &Target{
				ID:        id,
				Name:      "Target " + id,
				Speed:     10,
				TargetLat: 0,
				TargetLng: 0,
				Route:     "eyiaHbyokV@AAsPl@@@mG|@?B_BDQN@HCDGBMB_CzB@BsC@gJAE@sC?cNAY@q@@G?uBAgA?yI@a@EM?k@}D@cCDMGEKKeAIk@Me@EYSgA_@kCoBqMyAeKGk@Ai@EMIGAIEAG_@BaBO?y@IEECG@WqHGFiK}m@YmDEo[U?hA",
				State:     TargetStateWalking,
			}

			targetJSON, err := json.Marshal(target)
			if err != nil {
				return err
			}

			targetKey := fmt.Sprintf("%s:%s", TargetRedisKey, id)
			pipe.Set(ctx, targetKey, targetJSON, 0)
		}

		// Execute all commands in the pipeline
		_, err := pipe.Exec(ctx)
		if err != nil {
			return err
		}

		if i%10000 == 0 {
			log.Printf("Seeded %d targets of %d", i+currentBatchSize, count)
		}
	}

	return nil
}

// DeleteAllTargets removes all targets from Redis storage
func (s *TargetService) DeleteAllTargets() error {
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
