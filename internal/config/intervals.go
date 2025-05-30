package config

import "time"

// Worker intervals
const (
	// TargetsWorkerInterval defines how often the targets worker processes target movements and effects
	TargetsWorkerInterval = 5 * time.Second

	// RedisBackupInterval defines how often to save changes to Redis
	RedisBackupInterval = 999 * time.Second

	// PostgresBackupInterval defines how often to save changes to PostgreSQL
	PostgresBackupInterval = 999 * time.Minute
)
