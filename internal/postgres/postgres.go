package postgres

import (
	"log"
	"metalink/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// DB holds the global database connection
var DB *gorm.DB

// Init initializes the database connection and sets the global DB variable
func Init(url string) *gorm.DB {
	db, err := gorm.Open(postgres.Open(url), &gorm.Config{})

	if err != nil {
		log.Fatalln(err)
	}

	db.AutoMigrate(&models.Route{}, &models.RouteProgress{})

	// Set global DB variable
	DB = db

	return db
}

// GetDB returns the global database connection
func GetDB() *gorm.DB {
	return DB
}
