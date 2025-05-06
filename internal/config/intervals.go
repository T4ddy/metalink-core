package config

import "time"

// Worker intervals
const (
	// MovementWorkerInterval defines how often the movement worker processes target movements
	MovementWorkerInterval = 2 * time.Second

	// RedisBackupInterval defines how often to save changes to Redis
	RedisBackupInterval = 5 * time.Second

	// PostgresBackupInterval defines how often to save changes to PostgreSQL
	PostgresBackupInterval = 6033 * time.Second
)
