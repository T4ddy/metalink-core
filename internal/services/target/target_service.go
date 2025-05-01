package target

import (
	"context"
	"encoding/json"
	"fmt"
	"metalink/internal/redis"
	"metalink/internal/utils"
	"sync"
)

const RedisKey = "target"

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

// AddTarget adds a new target to Redis storage
func (s *TargetService) AddTarget(target *Target) error {
	client := redis.GetClient()
	ctx := context.Background()

	// Serialize target to JSON
	targetJSON, err := json.Marshal(target)
	if err != nil {
		return err
	}

	targetKey := fmt.Sprintf("%s:%s", RedisKey, target.ID)

	// Store target in Redis using its ID as field in a hash
	return client.Set(ctx, targetKey, targetJSON, 0).Err()
}

// GetAllTargets retrieves all targets from Redis storage
func (s *TargetService) GetAllTargets() ([]*Target, error) {
	client := redis.GetClient()
	ctx := context.Background()

	// Get all targets from Redis hash
	result, err := client.HGetAll(ctx, RedisKey).Result()
	if err != nil {
		return nil, err
	}

	targets := make([]*Target, 0, len(result))

	// Unmarshal each target from JSON
	for _, val := range result {
		var target Target
		if err := json.Unmarshal([]byte(val), &target); err != nil {
			return nil, err
		}
		targets = append(targets, &target)
	}

	return targets, nil
}

// seed with test targets
func (s *TargetService) SeedTestTargets() error {
	id, err := utils.GenerateUniqueID(6)
	if err != nil {
		return err
	}

	return s.AddTarget(&Target{
		ID:        id,
		Name:      "Target " + id,
		Speed:     10,
		TargetLat: 0,
		TargetLng: 0,
		Route:     "eyiaHbyokV`@AAsPl@@@mG|@?B_BDQN@HCDGBMB_CzB@BsC@gJAE@sC?cNAY@q@@G?uBAgA?yI@a@EM?k@}D@cCDMGEKKeAIk@Me@EYSgA_@kCoBqMyAeKGk@Ai@EMIGAIEAG_@BaBO?y@IEECG@WqHGFiK}m@YmDEo[U?hA",
		State:     TargetStateWalking,
	})
}
