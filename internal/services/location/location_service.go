package location

import (
	"metalink/internal/redis"
	"sync"
)

type LocationService struct{}

var (
	locationServiceInstance *LocationService
	locationServiceOnce     sync.Once
)

// GetLocationService returns the singleton instance of LocationService.
func GetLocationService() *LocationService {
	locationServiceOnce.Do(func() {
		locationServiceInstance = &LocationService{}
	})
	return locationServiceInstance
}

func (s *LocationService) GetRouteByKey(key string) (string, error) {
	return redis.Get(key)
}
