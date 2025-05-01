package worker

import (
	"metalink/internal/services/target"
	"time"
)

const (
	MovementWorkerInterval = 1 * time.Second
)

func StartMovementWorker() {
	targetService := target.GetTargetService()
	targetService.SeedTestTargets()
}
