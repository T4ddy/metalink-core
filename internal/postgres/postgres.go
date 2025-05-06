package postgres

import (
	"log"
	"metalink/internal/model"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB holds the global database connection
var DB *gorm.DB

// Init initializes the database connection and sets the global DB variable
func Init(url string) *gorm.DB {
	// Configure GORM logger with higher slow SQL threshold
	gormLogger := logger.New(
		log.New(log.Writer(), "\r\n", log.LstdFlags), // io writer
		logger.Config{
			SlowThreshold: time.Millisecond * 500, // Set threshold to 2 seconds instead of default 200ms
		},
	)

	db, err := gorm.Open(postgres.Open(url), &gorm.Config{
		Logger: gormLogger,
	})

	if err != nil {
		log.Fatalln(err)
	}

	// AutoMigrate models
	err = db.AutoMigrate(&model.TargetPG{})
	if err != nil {
		log.Fatalln("Failed to migrate Target model:", err)
	}

	// Set global DB variable
	DB = db

	return db
}

// GetDB returns the global database connection
func GetDB() *gorm.DB {
	return DB
}
