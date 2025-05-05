package model

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

// Target is the unified model for target entity (used for both PostgreSQL and Redis)
type Target struct {
	ID             string      `json:"id" gorm:"primaryKey"`
	Name           string      `json:"name" gorm:"size:255;not null"`
	Speed          float32     `json:"speed" gorm:"not null"`
	TargetLat      float32     `json:"target_lat" gorm:"not null"`
	TargetLng      float32     `json:"target_lng" gorm:"not null"`
	Route          string      `json:"route" gorm:"type:text"`
	State          TargetState `json:"state" gorm:"not null"`
	NextPointIndex int         `json:"next_point_index" gorm:""`

	UpdatedAt time.Time      `json:"updated_at" gorm:"column:updated_at"`
	CreatedAt time.Time      `json:"-" gorm:"column:created_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"column:deleted_at;index"`

	RoutePoints [][2]float64 `json:"-" gorm:"-"`
}

// ToLightVersion returns a lighter version of the target for memory storage or Redis
func (t *Target) ToLightVersion() *Target {
	return &Target{
		ID:        t.ID,
		Name:      t.Name,
		Speed:     t.Speed,
		TargetLat: t.TargetLat,
		TargetLng: t.TargetLng,
		Route:     t.Route,
		State:     t.State,
		UpdatedAt: t.UpdatedAt,
	}
}
