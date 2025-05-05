package worker

import (
	"log"
	"metalink/internal/service/target"
	"time"
)

const (
	MovementWorkerInterval = 3 * time.Second
)

// StartMovementWorker starts the worker that processes target movements
func StartMovementWorker() {
	targetService := target.GetTargetService()

	ticker := time.NewTicker(MovementWorkerInterval)
	go func() {
		for range ticker.C {
			log.Println("Movement worker: processing target movements")
			targetService.ProcessTargetMovements()
		}
	}()

	log.Println("Movement worker started with interval:", MovementWorkerInterval)
}
