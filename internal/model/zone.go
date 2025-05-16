package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/paulmach/orb"
	"gorm.io/gorm"
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

// Float64Slice is a custom type for JSONB serialization of []float64
type Float64Slice []float64

// Value implements the driver.Valuer interface for database serialization
func (f Float64Slice) Value() (driver.Value, error) {
	return json.Marshal(f)
}

// Scan implements the sql.Scanner interface for database deserialization
func (f *Float64Slice) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("cannot convert %T to Float64Slice", value)
	}
	return json.Unmarshal(bytes, f)
}

type ZoneEffect struct {
	EffectType   EffectType      `gorm:"not null"`
	ResourceType TargetParamType `gorm:"not null"`
	Value        float32         `gorm:"not null"`
}

// ZonePG model for PostgreSQL storage
type ZonePG struct {
	ID                string       `gorm:"primaryKey"`
	Name              string       `gorm:"size:255;not null"`
	TopLeftLatLon     Float64Slice `gorm:"type:jsonb;not null"`
	TopRightLatLon    Float64Slice `gorm:"type:jsonb;not null"`
	BottomLeftLatLon  Float64Slice `gorm:"type:jsonb;not null"`
	BottomRightLatLon Float64Slice `gorm:"type:jsonb;not null"`
	Effects           []ZoneEffect `gorm:"type:jsonb"`

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
	ID                string
	Name              string
	TopLeftLatLon     []float64
	TopRightLatLon    []float64
	BottomLeftLatLon  []float64
	BottomRightLatLon []float64
	Effects           []ZoneEffect

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
		ID:                pg.ID,
		Name:              pg.Name,
		TopLeftLatLon:     pg.TopLeftLatLon,
		TopRightLatLon:    pg.TopRightLatLon,
		BottomLeftLatLon:  pg.BottomLeftLatLon,
		BottomRightLatLon: pg.BottomRightLatLon,
		Effects:           pg.Effects,
		UpdatedAt:         pg.UpdatedAt,
		CreatedAt:         pg.CreatedAt,
		DeletedAt:         pg.DeletedAt,
	}
}
