package worker

import (
	"log"
)

// StartAllWorkers initializes and starts all background workers
func StartAllWorkers() {
	log.Println("Starting all workers...")

	StartTargetsWorker()

	log.Println("All workers started")
}
