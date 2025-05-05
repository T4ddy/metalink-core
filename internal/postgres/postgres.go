package postgres

import (
	"log"
	"metalink/internal/model"

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
