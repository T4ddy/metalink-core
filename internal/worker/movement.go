package worker

import (
	"log"
	"metalink/internal/services/target"
	"time"
)

const (
	MovementWorkerInterval = 10 * time.Second
)

func StartMovementWorker() {
	targetService := target.GetTargetService()
	// targetService.DeleteAllTargetsPG()
	targetService.DeleteAllTargets()
	startTime := time.Now()
	targetService.SeedTestTargets(1000000)
	elapsedTime := time.Since(startTime)
	log.Printf("SeedTestTargets execution time: %d ms", elapsedTime.Milliseconds())

	startTime = time.Now()
	targets, err := targetService.GetAllTargets()
	elapsedTime = time.Since(startTime)
	log.Printf("GetAllTargets execution time: %d ms", elapsedTime.Milliseconds())

	if err != nil {
		log.Printf("Error getting targets: %v", err)
		return
	}

	log.Printf("Found %d targets", len(targets))

	// targetService.SeedTestTargetsPG(1000000)
	// targetService.SeedTestTargetsPGParallel(1000000)

	// targetService.DeleteAllTargets()
	// targetService.SeedTestTargets(1000000)

	// startTime := time.Now()
	// targets, err := targetService.GetAllTargetsPG()
	// elapsedTime := time.Since(startTime)
	// log.Printf("GetAllTargets execution time: %d ms", elapsedTime.Milliseconds())

	// if err != nil {
	// 	log.Printf("Error updating targets: %v", err)
	// 	return
	// }

	// log.Printf("Found %d targets", len(targets))
}
