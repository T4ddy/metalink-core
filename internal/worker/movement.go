package worker

import (
	"log"
	"metalink/internal/config"
	"metalink/internal/service/target"
	"time"
)

// StartMovementWorker starts the worker that processes target movements
func StartMovementWorker() {
	targetService := target.GetTargetService()

	ticker := time.NewTicker(config.MovementWorkerInterval)
	go func() {
		for range ticker.C {
			log.Println("Movement worker: processing target movements")
			targetService.ProcessTargetMovements()
		}
	}()

	log.Println("Movement worker started with interval:", config.MovementWorkerInterval)
}
