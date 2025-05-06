package redis

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient holds the Redis client connection
var redisClient *redis.Client

// Init initializes the Redis connection and sets the global RedisClient variable
func Init(redisURL string) *redis.Client {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}

	client := redis.NewClient(opts)

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	log.Println("Successfully connected to Redis")
	redisClient = client

	return client
}

// GetClient returns the global Redis client connection
func GetClient() *redis.Client {
	return redisClient
}

// Close closes the Redis client connection
func Close() error {
	if redisClient != nil {
		log.Println("Closing Redis connection...")
		return redisClient.Close()
	}
	return nil
}

// Set stores a key-value pair in Redis
func Set(key string, value interface{}, expiration time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return redisClient.Set(ctx, key, value, expiration).Err()
}

// Get retrieves a value by key from Redis
func Get(key string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return redisClient.Get(ctx, key).Result()
}

// Delete removes a key from Redis
func Delete(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return redisClient.Del(ctx, key).Err()
}

// HashSet sets a hash field to value in Redis
func HashSet(key, field string, value interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return redisClient.HSet(ctx, key, field, value).Err()
}

// HashGet gets the value of a hash field
func HashGet(key, field string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return redisClient.HGet(ctx, key, field).Result()
}

// HashGetAll gets all fields and values of a hash
func HashGetAll(key string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return redisClient.HGetAll(ctx, key).Result()
}
