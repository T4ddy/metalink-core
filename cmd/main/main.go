package main

import (
	"context"
	"io"
	"log"
	"metalink/internal/api"
	"metalink/internal/config"
	"metalink/internal/postgres"
	"metalink/internal/redis"
	"metalink/internal/service/target"
	"metalink/internal/service/zone"
	"metalink/internal/worker"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func main() {
	setupLogging()

	cfg, err := loadConfiguration()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	initializeDatabaseAndCache(cfg)
	defer closeConnections()

	setupSignalHandler()

	targetService := initializeServices()

	playgroundFlag := false
	// playgroundFlag = true

	// if false {
	if playgroundFlag {
		playground(targetService)
	} else {
		startWorkers(targetService)
	}

	reportMemoryStats()

	runAPIServer(cfg)
}

func setupLogging() {
	// Set up logging to file and terminal
	logFile, err := os.OpenFile("metalink.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	// Note: We're not closing the file here since it needs to stay open
	// for the entire application lifetime. This is a minor resource leak
	// but acceptable for this use case.

	// Use MultiWriter to output logs to both terminal and file
	multiWriter := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(multiWriter)
}

func loadConfiguration() (config.Config, error) {
	// Try loading from config package first
	cfg, err := config.LoadConfig()
	if err != nil {
		// Fallback to loading from .env file directly
		log.Println("Failed to load config via config package, using fallback method")

		// Using environment file as fallback
		cfg.Port = getEnvWithDefault("PORT", ":3000")
		cfg.DBUrl = getEnvWithDefault("DB_URL", "postgres://postgres:postgres@localhost:5432/metalink")
		cfg.RedisUrl = getEnvWithDefault("REDIS_URL", "redis://localhost:6379/0")
	}

	return cfg, nil
}

func getEnvWithDefault(key, defaultValue string) string {
	value := viper.GetString(key)
	if value == "" {
		log.Printf("%s environment variable is not set, using default", key)
		return defaultValue
	}
	return value
}

func initializeDatabaseAndCache(cfg config.Config) {
	// Initialize PostgreSQL
	postgres.Init(cfg.DBUrl)

	// Initialize Redis
	redis.Init(cfg.RedisUrl)
}

func initializeServices() *target.TargetService {
	// Initialize target service
	targetService := target.GetTargetService()
	ctx := context.Background()

	// Load data from PostgreSQL and Redis
	if err := targetService.InitService(ctx); err != nil {
		log.Fatalf("Failed to initialize target service: %v", err)
	}

	// Initialize zone service
	zoneService := zone.GetZoneService()
	if err := zoneService.InitService(ctx); err != nil {
		log.Fatalf("Failed to initialize zone service: %v", err)
	}

	return targetService
}

func startWorkers(targetService *target.TargetService) {
	// Start background workers managed by worker package
	worker.StartAllWorkers()

	// Start persistence workers (should be moved to worker package)
	targetService.StartPersistenceWorkers()
}

func runAPIServer(cfg config.Config) {
	// Initialize Gin router
	r := gin.Default()

	// Configure API routes
	config := map[string]string{
		"port":     cfg.Port,
		"dbUrl":    cfg.DBUrl,
		"redisUrl": cfg.RedisUrl,
	}
	api.SetupRouter(r, config)

	// Start the server
	r.Run(cfg.Port)
}

func playground(targetService *target.TargetService) {
	log.Println("PLAYGROUND")
	log.Println("PLAYGROUND")
	log.Println("PLAYGROUND")
	log.Println("PLAYGROUND")
	log.Println("PLAYGROUND")
	log.Println("PLAYGROUND")
	log.Println("PLAYGROUND")
	targetService.DeleteAllRedisTargets()
	targetService.SeedTestTargetsPGParallel(300000)
}

func reportMemoryStats() {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for range ticker.C {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			log.Printf("Alloc = %v MiB, TotalAlloc = %v MiB, Sys = %v MiB, NumGC = %v",
				m.Alloc/1024/1024, m.TotalAlloc/1024/1024, m.Sys/1024/1024, m.NumGC)
		}
	}()
}

func closeConnections() {
	if err := postgres.Close(); err != nil {
		log.Printf("Error closing PostgreSQL connection: %v", err)
	}

	if err := redis.Close(); err != nil {
		log.Printf("Error closing Redis connection: %v", err)
	}

	log.Println("PostgreSQL and Redis connections closed successfully")
}

func setupSignalHandler() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Shutdown signal received, closing connections...")
		closeConnections()
		os.Exit(0)
	}()
}
