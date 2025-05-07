package storage

import (
	"sync"
	"time"
)

// MemoryStorage - universal in-memory object storage
// K - key type, V - stored object type
type MemoryStorage[K comparable, V any] struct {
	data       map[K]V
	mutex      sync.RWMutex
	dirty      map[K]bool
	lastUpdate map[K]time.Time
}

// NewMemoryStorage creates a new storage
func NewMemoryStorage[K comparable, V any]() *MemoryStorage[K, V] {
	return &MemoryStorage[K, V]{
		data:       make(map[K]V),
		dirty:      make(map[K]bool),
		lastUpdate: make(map[K]time.Time),
	}
}

// Set adds or updates an object
func (s *MemoryStorage[K, V]) Set(key K, value V) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.data[key] = value
	s.dirty[key] = true
	s.lastUpdate[key] = time.Now()
}

// Get returns an object by key
func (s *MemoryStorage[K, V]) Get(key K) (V, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	value, exists := s.data[key]
	return value, exists
}

// Delete removes an object by key
func (s *MemoryStorage[K, V]) Delete(key K) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, exists := s.data[key]; !exists {
		return false
	}

	delete(s.data, key)
	s.dirty[key] = true
	return true
}

// GetAll returns all objects
func (s *MemoryStorage[K, V]) GetAll() map[K]V {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	result := make(map[K]V, len(s.data))
	for k, v := range s.data {
		result[k] = v
	}
	return result
}

// GetAllValues returns all values as a slice
func (s *MemoryStorage[K, V]) GetAllValues() []V {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	result := make([]V, 0, len(s.data))
	for _, v := range s.data {
		result = append(result, v)
	}
	return result
}

// GetDirty returns all dirty objects without clearing flags
func (s *MemoryStorage[K, V]) GetDirty() []V {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	result := make([]V, 0, len(s.dirty))
	for k := range s.dirty {
		if v, exists := s.data[k]; exists {
			result = append(result, v)
		}
	}
	return result
}

// ClearDirty clears dirty flags for provided keys
func (s *MemoryStorage[K, V]) ClearDirty(keys []K) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, k := range keys {
		delete(s.dirty, k)
	}
}

// ForEach executes a function for each object
func (s *MemoryStorage[K, V]) ForEach(fn func(key K, value V) bool) {
	// Copy data under lock for subsequent processing
	s.mutex.RLock()
	items := make(map[K]V, len(s.data))
	for k, v := range s.data {
		items[k] = v
	}
	s.mutex.RUnlock()

	// Process copied data without locking
	for k, v := range items {
		if !fn(k, v) {
			break
		}
	}
}

// Count returns the number of objects
func (s *MemoryStorage[K, V]) Count() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return len(s.data)
}
