package postgres

import (
	"database/sql"
	"log"
	"metalink/internal/model"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB holds the global database connection
var DB *gorm.DB
var sqlDB *sql.DB

// Init initializes the database connection and sets the global DB variable
func Init(url string) *gorm.DB {
	// Configure GORM logger with higher slow SQL threshold
	gormLogger := logger.New(
		log.New(log.Writer(), "\r\n", log.LstdFlags), // io writer
		logger.Config{
			SlowThreshold: time.Millisecond * 500, // Set threshold to 500ms
		},
	)

	db, err := gorm.Open(postgres.Open(url), &gorm.Config{
		Logger: gormLogger,
	})

	if err != nil {
		log.Fatalln(err)
	}

	// Get underlying SQL database
	sqlDB, err = db.DB()
	if err != nil {
		log.Fatalln("Failed to get SQL DB:", err)
	}

	// Configure connection pool settings
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

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

// Close closes the database connection
func Close() error {
	if sqlDB != nil {
		log.Println("Closing PostgreSQL connection...")
		return sqlDB.Close()
	}
	return nil
}
