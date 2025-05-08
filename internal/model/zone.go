package model

import (
	"time"

	"gorm.io/gorm"
)

// ZoneState represents the current state of a zone
type ZoneState int

const (
	ZoneStateActive ZoneState = iota
	ZoneStateInactive
)

// EffectType represents the type of effect of the zone
type EffectType int

const (
	EffectTypeBuff EffectType = iota
	EffectTypeDebuff
)

// ResourceType represents the type of resource affected by the effect
type ResourceType int

const (
	ResourceTypeFuel ResourceType = iota
	ResourceTypeHealth
	ResourceTypeSpeed
	// Other resource types...
)

// ZonePG model for PostgreSQL storage
type ZonePG struct {
	ID           string       `gorm:"primaryKey"`
	Name         string       `gorm:"size:255;not null"`
	Type         string       `gorm:"size:50;not null"`
	State        ZoneState    `gorm:"not null"`
	Geometry     string       `gorm:"type:text;not null"` // GeoJSON polygon as a string
	EffectType   EffectType   `gorm:"not null"`
	ResourceType ResourceType `gorm:"not null"`
	Value        float32      `gorm:"not null"` // Effect value

	UpdatedAt time.Time      `gorm:"column:updated_at"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

// TableName overrides the table name
func (ZonePG) TableName() string {
	return "zones"
}

// ZoneRedis model for Redis
type ZoneRedis struct {
	ID           string       `json:"id"`
	State        ZoneState    `json:"state"`
	Geometry     string       `json:"geometry"` // GeoJSON polygon
	EffectType   EffectType   `json:"effect_type"`
	ResourceType ResourceType `json:"resource_type"`
	Value        float32      `json:"value"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// Zone in-memory model
type Zone struct {
	ID           string
	Name         string
	Type         string
	State        ZoneState
	Geometry     string // GeoJSON polygon as a string
	EffectType   EffectType
	ResourceType ResourceType
	Value        float32 // Effect value

	UpdatedAt time.Time
	CreatedAt time.Time
	DeletedAt gorm.DeletedAt

	// Cached data for quick access
	Polygon *orb.Polygon // Pre-parsed polygon for quick calculations
	Bounds  *orb.Bound   // Bounds of the polygon for quick checks
}

// ToRedis converts a Zone to ZoneRedis
func (z *Zone) ToRedis() *ZoneRedis {
	return &ZoneRedis{
		ID:           z.ID,
		State:        z.State,
		Geometry:     z.Geometry,
		EffectType:   z.EffectType,
		ResourceType: z.ResourceType,
		Value:        z.Value,
		UpdatedAt:    z.UpdatedAt,
	}
}

// ToPG converts a Zone to ZonePG
func (z *Zone) ToPG() *ZonePG {
	return &ZonePG{
		ID:           z.ID,
		Name:         z.Name,
		Type:         z.Type,
		State:        z.State,
		Geometry:     z.Geometry,
		EffectType:   z.EffectType,
		ResourceType: z.ResourceType,
		Value:        z.Value,
		UpdatedAt:    z.UpdatedAt,
		CreatedAt:    z.CreatedAt,
		DeletedAt:    z.DeletedAt,
	}
}

// ZoneFromPG creates a Zone from ZonePG
func ZoneFromPG(pg *ZonePG) *Zone {
	return &Zone{
		ID:           pg.ID,
		Name:         pg.Name,
		Type:         pg.Type,
		State:        pg.State,
		Geometry:     pg.Geometry,
		EffectType:   pg.EffectType,
		ResourceType: pg.ResourceType,
		Value:        pg.Value,
		UpdatedAt:    pg.UpdatedAt,
		CreatedAt:    pg.CreatedAt,
		DeletedAt:    pg.DeletedAt,
	}
}

// ZoneFromRedis creates a Zone from ZoneRedis
func ZoneFromRedis(r *ZoneRedis) *Zone {
	return &Zone{
		ID:           r.ID,
		State:        r.State,
		Geometry:     r.Geometry,
		EffectType:   r.EffectType,
		ResourceType: r.ResourceType,
		Value:        r.Value,
		UpdatedAt:    r.UpdatedAt,
	}
}
