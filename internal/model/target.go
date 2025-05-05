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

// TargetPG is the model for PostgreSQL storage
type TargetPG struct {
	ID             string      `gorm:"primaryKey"`
	Name           string      `gorm:"size:255;not null"`
	Speed          float32     `gorm:"not null"`
	TargetLat      float32     `gorm:"not null"`
	TargetLng      float32     `gorm:"not null"`
	Route          string      `gorm:"type:text"`
	State          TargetState `gorm:"not null"`
	NextPointIndex int         `gorm:""`

	UpdatedAt time.Time      `gorm:"column:updated_at"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

// TableName overrides the table name used by TargetPG to `targets`
func (TargetPG) TableName() string {
	return "targets"
}

// TargetRedis is the model for Redis storage
type TargetRedis struct {
	ID             string      `json:"id"`
	Speed          float32     `json:"speed"`
	TargetLat      float32     `json:"target_lat"`
	TargetLng      float32     `json:"target_lng"`
	State          TargetState `json:"state"`
	NextPointIndex int         `json:"next_point_index"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// Target is the in-memory model used by the service
type Target struct {
	ID             string
	Name           string
	Speed          float32
	TargetLat      float32
	TargetLng      float32
	Route          string
	State          TargetState
	NextPointIndex int

	UpdatedAt time.Time
	CreatedAt time.Time
	DeletedAt gorm.DeletedAt

	RoutePoints [][2]float64 // For runtime calculations only
}

// ToRedis converts a Target to TargetRedis
func (t *Target) ToRedis() *TargetRedis {
	return &TargetRedis{
		ID:             t.ID,
		Speed:          t.Speed,
		TargetLat:      t.TargetLat,
		TargetLng:      t.TargetLng,
		State:          t.State,
		NextPointIndex: t.NextPointIndex,
		UpdatedAt:      t.UpdatedAt,
	}
}

// ToPG converts a Target to TargetPG
func (t *Target) ToPG() *TargetPG {
	return &TargetPG{
		ID:             t.ID,
		Name:           t.Name,
		Speed:          t.Speed,
		TargetLat:      t.TargetLat,
		TargetLng:      t.TargetLng,
		Route:          t.Route,
		State:          t.State,
		NextPointIndex: t.NextPointIndex,
		UpdatedAt:      t.UpdatedAt,
		CreatedAt:      t.CreatedAt,
		DeletedAt:      t.DeletedAt,
	}
}

// FromPG creates a Target from TargetPG
func FromPG(pg *TargetPG) *Target {
	return &Target{
		ID:             pg.ID,
		Name:           pg.Name,
		Speed:          pg.Speed,
		TargetLat:      pg.TargetLat,
		TargetLng:      pg.TargetLng,
		Route:          pg.Route,
		State:          pg.State,
		NextPointIndex: pg.NextPointIndex,
		UpdatedAt:      pg.UpdatedAt,
		CreatedAt:      pg.CreatedAt,
		DeletedAt:      pg.DeletedAt,
	}
}

// FromRedis creates a Target from TargetRedis
func FromRedis(r *TargetRedis) *Target {
	return &Target{
		ID:             r.ID,
		Speed:          r.Speed,
		TargetLat:      r.TargetLat,
		TargetLng:      r.TargetLng,
		State:          r.State,
		NextPointIndex: r.NextPointIndex,
		UpdatedAt:      r.UpdatedAt,
	}
}
