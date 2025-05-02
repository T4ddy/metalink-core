package models

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

// TargetU is the unified model for Target entity (used for both PostgreSQL and Redis)
type TargetU struct {
	ID        string      `json:"id" gorm:"primaryKey"`
	Name      string      `json:"name" gorm:"size:255;not null"`
	Speed     float32     `json:"speed" gorm:"not null"`
	TargetLat float32     `json:"target_lat" gorm:"not null"`
	TargetLng float32     `json:"target_lng" gorm:"not null"`
	Route     string      `json:"route" gorm:"type:text"`
	State     TargetState `json:"state" gorm:"not null"`

	// Unix timestamp used for Redis
	UpdatedAt int64 `json:"updated_at" gorm:"-"`

	// Standard GORM fields for PostgreSQL
	GormUpdatedAt time.Time      `json:"-" gorm:"column:updated_at"`
	GormCreatedAt time.Time      `json:"-" gorm:"column:created_at"`
	GormDeletedAt gorm.DeletedAt `json:"-" gorm:"column:deleted_at;index"`
}

// TODO: check if this hooks needed. Performance impact?
// TODO: check if this hooks needed. Performance impact?
// TODO: check if this hooks needed. Performance impact?
// TODO: check if this hooks needed. Performance impact?
// TODO: check if this hooks needed. Performance impact?
// TODO: check if this hooks needed. Performance impact?
// TODO: check if this hooks needed. Performance impact?
// TODO: check if this hooks needed. Performance impact?
// TODO: check if this hooks needed. Performance impact?

// BeforeCreate GORM hook to set timestamps when creating a record
func (t *TargetU) BeforeCreate(tx *gorm.DB) error {
	t.GormCreatedAt = time.Now()
	t.GormUpdatedAt = time.Unix(t.UpdatedAt, 0)
	if t.UpdatedAt == 0 {
		t.UpdatedAt = time.Now().Unix()
		t.GormUpdatedAt = time.Unix(t.UpdatedAt, 0)
	}
	return nil
}

// BeforeUpdate GORM hook to update timestamps when updating a record
func (t *TargetU) BeforeUpdate(tx *gorm.DB) error {
	t.GormUpdatedAt = time.Unix(t.UpdatedAt, 0)
	if t.UpdatedAt == 0 {
		t.UpdatedAt = time.Now().Unix()
		t.GormUpdatedAt = time.Unix(t.UpdatedAt, 0)
	}
	return nil
}

// AfterFind GORM hook to update Unix timestamp after loading from database
func (t *TargetU) AfterFind(tx *gorm.DB) error {
	t.UpdatedAt = t.GormUpdatedAt.Unix()
	return nil
}

// ToLightVersion returns a lighter version of the target for memory storage or Redis
func (t *TargetU) ToLightVersion() *TargetU {
	return &TargetU{
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
