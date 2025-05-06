package config

import "time"

// Worker intervals
const (
	// MovementWorkerInterval defines how often the movement worker processes target movements
	MovementWorkerInterval = 3 * time.Second

	// RedisBackupInterval defines how often to save changes to Redis
	RedisBackupInterval = 999 * time.Second

	// PostgresBackupInterval defines how often to save changes to PostgreSQL
	PostgresBackupInterval = 5 * time.Second
)
