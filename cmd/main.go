package main

import (
	"log"
	"metalink/internal/postgres"
	"metalink/internal/redis"
	"metalink/internal/worker"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func main() {
	viper.SetConfigFile("./internal/envs/.env")
	viper.ReadInConfig()

	port := viper.GetString("PORT")
	if port == "" {
		log.Println("PORT environment variable is not set")
		port = ":3000" // Default port
	}

	dbUrl := viper.GetString("DB_URL")
	if dbUrl == "" {
		log.Println("DB_URL environment variable is not set")
		dbUrl = "postgres://postgres:postgres@localhost:5432/postgres" // Default dbUrl
	}

	redisUrl := viper.GetString("REDIS_URL")
	if redisUrl == "" {
		log.Println("REDIS_URL environment variable is not set")
		redisUrl = "redis://localhost:6379/0" // Default redisUrl
	}

	r := gin.Default()
	postgres.Init(dbUrl)
	redis.Init(redisUrl)

	// Initialize application routes
	// config := map[string]string{
	// 	"port":     port,
	// 	"dbUrl":    dbUrl,
	// 	"redisUrl": redisUrl,
	// }
	// api.SetupRouter(r, config)

	// Workers
	go worker.StartMovementWorker()

	r.Run(port)
}
