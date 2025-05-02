package storage

// Storage defines interface for any object storage
type Storage[K comparable, V any] interface {
	Set(key K, value V)
	Get(key K) (V, bool)
	Delete(key K) bool
	GetAll() map[K]V
	GetAllValues() []V
	GetDirty() map[K]V
	ClearDirty(keys []K)
	ForEach(fn func(key K, value V) bool)
	Count() int
}
