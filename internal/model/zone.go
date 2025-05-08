package model

import (
	"time"

	"github.com/paulmach/orb"
	"gorm.io/gorm"
)

type ZoneState int

const (
	ZoneStateActive ZoneState = iota
	ZoneStateInactive
)

type EffectType int

const (
	EffectTypeBuff EffectType = iota
	EffectTypeDebuff
)

// TargetParamType represents the type of parameter affected by the effect
type TargetParamType int

const (
	TargetParamTypeHealth TargetParamType = iota
	TargetParamTypeStamina
	TargetParamTypeStrength
	// ... other target param types
)

type ZoneEffect struct {
	EffectType   EffectType      `gorm:"not null"`
	ResourceType TargetParamType `gorm:"not null"`
	Value        float32         `gorm:"not null"`
}

// ZonePG model for PostgreSQL storage
type ZonePG struct {
	ID       string       `gorm:"primaryKey"`
	Name     string       `gorm:"size:255;not null"`
	Type     string       `gorm:"size:50;not null"`
	State    ZoneState    `gorm:"not null"`
	Geometry string       `gorm:"type:text;not null"`
	Effects  []ZoneEffect `gorm:"type:jsonb"`

	UpdatedAt time.Time      `gorm:"column:updated_at"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

// TableName overrides the table name
func (ZonePG) TableName() string {
	return "zones"
}

// Zone in-memory model
type Zone struct {
	ID       string
	Name     string
	Type     string
	State    ZoneState
	Geometry string // GeoJSON polygon as a string
	Effects  []ZoneEffect

	UpdatedAt time.Time
	CreatedAt time.Time
	DeletedAt gorm.DeletedAt

	// Cached data for quick access
	Polygon     *orb.Polygon // Pre-parsed polygon for quick calculations
	BoundingBox *orb.Bound   // Bounds of the polygon for quick checks
}

// ZoneFromPG creates a Zone from ZonePG
func ZoneFromPG(pg *ZonePG) *Zone {
	return &Zone{
		ID:        pg.ID,
		Name:      pg.Name,
		Type:      pg.Type,
		State:     pg.State,
		Geometry:  pg.Geometry,
		Effects:   pg.Effects,
		UpdatedAt: pg.UpdatedAt,
		CreatedAt: pg.CreatedAt,
		DeletedAt: pg.DeletedAt,
	}
}
