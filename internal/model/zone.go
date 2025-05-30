package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"metalink/cmd/osm-zone-parser/mappers"
	"time"

	"github.com/paulmach/orb"
	"gorm.io/gorm"
)

// TargetParamType represents the type of parameter affected by the effect
type TargetParamType int

const (
	TargetParamTypeHealth TargetParamType = iota
	TargetParamTypeStamina
	TargetParamTypeStrength
	TargetParamTypeSleepQuality
	TargetParamTypeFoodSearch
	TargetParamTypeWaterSearch
	TargetParamTypeMedicineSearch
	TargetParamTypeAirQuality
	TargetParamTypeStaminaConsumption
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

// BuildingStats holds the statistics for buildings in a zone
type BuildingStats struct {
	// Buildings with 1 floor
	SingleFloorCount     int     `json:"single_floor_count"`
	SingleFloorTotalArea float64 `json:"single_floor_total_area"`

	// Buildings with 2-9 floors
	LowRiseCount     int     `json:"low_rise_count"`
	LowRiseTotalArea float64 `json:"low_rise_total_area"`

	// Buildings with 10-29 floors
	HighRiseCount     int     `json:"high_rise_count"`
	HighRiseTotalArea float64 `json:"high_rise_total_area"`

	// Buildings with 30+ floors
	SkyscraperCount     int     `json:"skyscraper_count"`
	SkyscraperTotalArea float64 `json:"skyscraper_total_area"`

	// Total stats
	TotalCount    int                `json:"total_count"`
	TotalArea     float64            `json:"total_area"`
	BuildingTypes map[string]int     `json:"building_types"` // Count by building type
	BuildingAreas map[string]float64 `json:"building_areas"` // Total area by building type
}

// Value implements the driver.Valuer interface for database serialization
func (bs BuildingStats) Value() (driver.Value, error) {
	return json.Marshal(bs)
}

// Scan implements the sql.Scanner interface for database deserialization
func (bs *BuildingStats) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("cannot convert %T to BuildingStats", value)
	}
	return json.Unmarshal(bytes, bs)
}

// WaterBodyStats holds the statistics for water bodies in a zone
type WaterBodyStats struct {
	RiverCount     int     `json:"river_count"`
	RiverTotalArea float64 `json:"river_total_area"`

	LakeCount     int     `json:"lake_count"`
	LakeTotalArea float64 `json:"lake_total_area"`

	PondCount     int     `json:"pond_count"`
	PondTotalArea float64 `json:"pond_total_area"`

	TotalCount int     `json:"total_count"`
	TotalArea  float64 `json:"total_area"`
}

// Value implements the driver.Valuer interface for database serialization
func (wbs WaterBodyStats) Value() (driver.Value, error) {
	return json.Marshal(wbs)
}

// Scan implements the sql.Scanner interface for database deserialization
func (wbs *WaterBodyStats) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("cannot convert %T to WaterBodyStats", value)
	}
	return json.Unmarshal(bytes, wbs)
}

// ZoneEffect represents an effect that a zone has on targets inside it
// These effects are calculated dynamically and not stored in DB
// Positive values are buffs, negative values are debuffs
type ZoneEffect struct {
	ResourceType TargetParamType
	Value        float32 // Positive for buff, negative for debuff
}

// ZonePG model for PostgreSQL storage
type ZonePG struct {
	ID                string       `gorm:"primaryKey"`
	Name              string       `gorm:"size:255;not null"`
	TopLeftLatLon     Float64Slice `gorm:"type:jsonb;not null"`
	TopRightLatLon    Float64Slice `gorm:"type:jsonb;not null"`
	BottomLeftLatLon  Float64Slice `gorm:"type:jsonb;not null"`
	BottomRightLatLon Float64Slice `gorm:"type:jsonb;not null"`

	Buildings   BuildingStats  `gorm:"type:jsonb"`
	WaterBodies WaterBodyStats `gorm:"type:jsonb"`

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

	Buildings   BuildingStats
	WaterBodies WaterBodyStats

	// Calculated effects (not stored in DB)
	Effects []ZoneEffect

	UpdatedAt time.Time
	CreatedAt time.Time
	DeletedAt gorm.DeletedAt

	// Cached data for quick access
	Polygon     *orb.Polygon // Pre-parsed polygon for quick calculations
	BoundingBox *orb.Bound   // Bounds of the polygon for quick checks

	// Processing flags (not stored in DB)
	RecalculateNeeded bool // Flag to mark zones that need recalculation
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
		Buildings:         pg.Buildings,
		WaterBodies:       pg.WaterBodies,
		UpdatedAt:         pg.UpdatedAt,
		CreatedAt:         pg.CreatedAt,
		DeletedAt:         pg.DeletedAt,
		Effects:           []ZoneEffect{}, // Initialize empty effects, they'll be calculated on demand
	}
}

// CalculateEffects calculates zone effects based on building types and their areas
func (z *Zone) CalculateEffects() {
	z.Effects = []ZoneEffect{}

	// Initialize effect accumulator map
	effectAccumulator := make(map[TargetParamType]float32)

	// Process building effects based on building types and areas
	for buildingType, buildingArea := range z.Buildings.BuildingAreas {
		if buildingArea <= 0 {
			continue
		}

		// Get effects configuration for this building type
		effects := mappers.GetBuildingEffects(buildingType)
		if effects == nil {
			continue
		}

		// Calculate area coefficient (effect strength based on area)
		areaCoefficient := z.calculateAreaCoefficient(buildingArea)

		// Apply each effect type (preserving original sign - positive for buff, negative for debuff)
		if effects.SleepQuality != 0 {
			effectValue := float32(effects.SleepQuality) * areaCoefficient
			effectAccumulator[TargetParamTypeSleepQuality] += effectValue
		}

		if effects.FoodSearch != 0 {
			effectValue := float32(effects.FoodSearch) * areaCoefficient
			effectAccumulator[TargetParamTypeFoodSearch] += effectValue
		}

		if effects.WaterSearch != 0 {
			effectValue := float32(effects.WaterSearch) * areaCoefficient
			effectAccumulator[TargetParamTypeWaterSearch] += effectValue
		}

		if effects.MedicineSearch != 0 {
			effectValue := float32(effects.MedicineSearch) * areaCoefficient
			effectAccumulator[TargetParamTypeMedicineSearch] += effectValue
		}

		if effects.AirQuality != 0 {
			effectValue := float32(effects.AirQuality) * areaCoefficient
			effectAccumulator[TargetParamTypeAirQuality] += effectValue
		}

		if effects.StaminaConsumption != 0 {
			effectValue := float32(effects.StaminaConsumption) * areaCoefficient
			effectAccumulator[TargetParamTypeStaminaConsumption] += effectValue
		}
	}

	// Convert accumulated effects to ZoneEffect array
	for paramType, value := range effectAccumulator {
		if value == 0 {
			continue
		}

		effect := ZoneEffect{
			ResourceType: paramType,
			Value:        value, // Keep the original sign (positive for buff, negative for debuff)
		}

		z.Effects = append(z.Effects, effect)
	}
}

// calculateAreaCoefficient calculates coefficient based on building area
func (z *Zone) calculateAreaCoefficient(buildingArea float64) float32 {
	if buildingArea <= 0 {
		return 0
	}

	// Simple area-based scaling
	// Convert area to coefficient (1000 sq meters = 1.0 coefficient)
	return float32(buildingArea) / 1000.0
}
