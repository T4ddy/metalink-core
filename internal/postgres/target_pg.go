package postgres

import (
	"time"

	"gorm.io/gorm"
)

// TargetState represents the current state of a target
type TargetState int

const (
	TargetStateWalking TargetState = iota
	TargetStateStopped
)

// TargetPG is the GORM model for the Target entity
type TargetPG struct {
	ID        string      `gorm:"primaryKey"`
	Name      string      `gorm:"size:255;not null"`
	Speed     float32     `gorm:"not null"`
	TargetLat float32     `gorm:"not null"`
	TargetLng float32     `gorm:"not null"`
	Route     string      `gorm:"type:text"`
	State     TargetState `gorm:"not null"`

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}
