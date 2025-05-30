package config

import "time"

// Worker intervals
const (
	// TargetsWorkerInterval defines how often the targets worker processes target movements and effects
	TargetsWorkerInterval = 10 * time.Second

	// RedisBackupInterval defines how often to save changes to Redis
	RedisBackupInterval = 60 * time.Second

	// PostgresBackupInterval defines how often to save changes to PostgreSQL
	PostgresBackupInterval = 15 * time.Minute
)
