package storage

import (
	"fmt"
	"sync"
	"time"
)

// ShardedMemoryStorage - sharded object storage
type ShardedMemoryStorage[K comparable, V any] struct {
	shards     []*shardData[K, V]
	shardCount int
	shardMask  int
	keyToShard func(K) int // Shard distribution function
}

// shardData - single shard data
type shardData[K comparable, V any] struct {
	data       map[K]V
	mutex      sync.RWMutex
	dirty      map[K]bool
	lastUpdate map[K]time.Time
}

// NewShardedMemoryStorage creates a new sharded storage
func NewShardedMemoryStorage[K comparable, V any](shardCount int, keyToShardFunc func(K) int) *ShardedMemoryStorage[K, V] {
	// Round up to power of two
	realShardCount := 1
	for realShardCount < shardCount {
		realShardCount *= 2
	}

	shards := make([]*shardData[K, V], realShardCount)
	for i := 0; i < realShardCount; i++ {
		shards[i] = &shardData[K, V]{
			data:       make(map[K]V),
			dirty:      make(map[K]bool),
			lastUpdate: make(map[K]time.Time),
		}
	}

	// If no distribution function provided, use standard one for string and numeric keys
	if keyToShardFunc == nil {
		keyToShardFunc = func(key K) int {
			switch k := any(key).(type) {
			case string:
				return int(fnv1a(k)) & (realShardCount - 1)
			case int:
				return k & (realShardCount - 1)
			case int64:
				return int(k) & (realShardCount - 1)
			case uint64:
				return int(k) & (realShardCount - 1)
			default:
				// For other types use interface hash
				hash := fnv1a(fmt.Sprintf("%v", key))
				return int(hash) & (realShardCount - 1)
			}
		}
	}

	return &ShardedMemoryStorage[K, V]{
		shards:     shards,
		shardCount: realShardCount,
		shardMask:  realShardCount - 1,
		keyToShard: keyToShardFunc,
	}
}

// FNV-1a hash function
func fnv1a(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// getShard returns shard for key
func (s *ShardedMemoryStorage[K, V]) getShard(key K) *shardData[K, V] {
	shardIndex := s.keyToShard(key)
	return s.shards[shardIndex]
}

// Set adds or updates an object
func (s *ShardedMemoryStorage[K, V]) Set(key K, value V) {
	shard := s.getShard(key)

	shard.mutex.Lock()
	defer shard.mutex.Unlock()

	shard.data[key] = value
	shard.dirty[key] = true
	shard.lastUpdate[key] = time.Now()
}

// Get returns object by key
func (s *ShardedMemoryStorage[K, V]) Get(key K) (V, bool) {
	shard := s.getShard(key)

	shard.mutex.RLock()
	defer shard.mutex.RUnlock()

	value, exists := shard.data[key]
	return value, exists
}

// Delete removes an object
func (s *ShardedMemoryStorage[K, V]) Delete(key K) bool {
	shard := s.getShard(key)

	shard.mutex.Lock()
	defer shard.mutex.Unlock()

	if _, exists := shard.data[key]; !exists {
		return false
	}

	delete(shard.data, key)
	shard.dirty[key] = true
	return true
}

// GetAll returns all objects from all shards
func (s *ShardedMemoryStorage[K, V]) GetAll() map[K]V {
	result := make(map[K]V)

	for _, shard := range s.shards {
		shard.mutex.RLock()
		for k, v := range shard.data {
			result[k] = v
		}
		shard.mutex.RUnlock()
	}

	return result
}

// GetAllValues returns all values as a slice
func (s *ShardedMemoryStorage[K, V]) GetAllValues() []V {
	// First, calculate approximate result size
	totalCount := 0
	for _, shard := range s.shards {
		shard.mutex.RLock()
		totalCount += len(shard.data)
		shard.mutex.RUnlock()
	}

	result := make([]V, 0, totalCount)

	for _, shard := range s.shards {
		shard.mutex.RLock()
		for _, v := range shard.data {
			result = append(result, v)
		}
		shard.mutex.RUnlock()
	}

	return result
}

// GetDirty returns all dirty objects from all shards
func (s *ShardedMemoryStorage[K, V]) GetDirty() map[K]V {
	result := make(map[K]V)

	for _, shard := range s.shards {
		shard.mutex.Lock()

		for k := range shard.dirty {
			if v, exists := shard.data[k]; exists {
				result[k] = v
			}
			delete(shard.dirty, k)
		}

		shard.mutex.Unlock()
	}

	return result
}

// ForEach executes a function for each object
func (s *ShardedMemoryStorage[K, V]) ForEach(fn func(key K, value V) bool) {
	// Process each shard separately
	for _, shard := range s.shards {
		shard.mutex.RLock()
		items := make(map[K]V, len(shard.data))
		for k, v := range shard.data {
			items[k] = v
		}
		shard.mutex.RUnlock()

		for k, v := range items {
			if !fn(k, v) {
				return
			}
		}
	}
}

// Count returns total number of objects
func (s *ShardedMemoryStorage[K, V]) Count() int {
	count := 0
	for _, shard := range s.shards {
		shard.mutex.RLock()
		count += len(shard.data)
		shard.mutex.RUnlock()
	}
	return count
}

// ForEachParallel processes objects in parallel
func (s *ShardedMemoryStorage[K, V]) ForEachParallel(fn func(key K, value V)) {
	var wg sync.WaitGroup
	wg.Add(s.shardCount)

	for i := 0; i < s.shardCount; i++ {
		go func(shardIndex int) {
			defer wg.Done()

			shard := s.shards[shardIndex]

			// Copy shard data under lock
			shard.mutex.RLock()
			items := make(map[K]V, len(shard.data))
			for k, v := range shard.data {
				items[k] = v
			}
			shard.mutex.RUnlock()

			// Process data copy
			for k, v := range items {
				fn(k, v)
			}
		}(i)
	}

	wg.Wait()
}
