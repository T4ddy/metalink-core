package worker

import (
	"log"
	"metalink/internal/config"
	"metalink/internal/service/target"
	"time"
)

// StartTargetsWorker starts the worker that processes target movements and effects
func StartTargetsWorker() {
	targetService := target.GetTargetService()

	ticker := time.NewTicker(config.TargetsWorkerInterval)
	go func() {
		for range ticker.C {
			targetService.ProcessTargets()
		}
	}()

	log.Println("Targets worker started with interval:", config.TargetsWorkerInterval)
}
