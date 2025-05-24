package main

import (
	"encoding/json"
	"log"
	"os"
	"sync"
)

// BuildingEffect represents individual effect configuration
type BuildingEffect struct {
	SleepQuality       int `json:"sleep_quality,omitempty"`
	FoodSearch         int `json:"food_search,omitempty"`
	WaterSearch        int `json:"water_search,omitempty"`
	MedicineSearch     int `json:"medicine_search,omitempty"`
	AirQuality         int `json:"air_quality,omitempty"`
	StaminaConsumption int `json:"stamina_consumption,omitempty"`
}

// BuildingTypeConfig represents configuration for a specific building type
type BuildingTypeConfig struct {
	RadiusKf int            `json:"radiusKf"`
	Effects  BuildingEffect `json:"effects"`
}

// BuildingEffectsConfig represents the structure of building effects configuration
type BuildingEffectsConfig struct {
	BuildingEffectsConfig map[string]BuildingTypeConfig `json:"building_effects_config"`
}

var (
	cachedEffectsConfig *BuildingEffectsConfig
	effectsConfigOnce   sync.Once

	// Cache for OSM type to effects config mapping
	osmTypeToEffectsCache sync.Map // map[string]*BuildingTypeConfig
)

// GetBuildingEffectsConfig returns the configuration for a specific building type
// Returns nil if no configuration is found for the given building type
func GetBuildingEffectsConfig(buildingType string) *BuildingTypeConfig {
	config := getBuildingEffectsConfig()
	if config == nil {
		return nil
	}

	if typeConfig, exists := config.BuildingEffectsConfig[buildingType]; exists {
		return &typeConfig
	}

	return nil
}

// GetBuildingEffectsConfigByOSMType returns the configuration for a building by OSM type
// Uses caching: OSM type -> effects config directly
// First maps OSM building type to game category, then finds effects configuration
// Returns nil if no mapping or configuration is found
func GetBuildingEffectsConfigByOSMType(osmBuildingType string) *BuildingTypeConfig {
	// Check cache first
	if cachedConfig, ok := osmTypeToEffectsCache.Load(osmBuildingType); ok {
		if cachedConfig == nil {
			return nil
		}
		return cachedConfig.(*BuildingTypeConfig)
	}

	// Cache miss - perform lookup and cache result
	gameCategory := MapBuildingCategory(osmBuildingType)
	effectsConfig := GetBuildingEffectsConfig(gameCategory)

	// Cache the result (even if nil)
	osmTypeToEffectsCache.Store(osmBuildingType, effectsConfig)

	return effectsConfig
}

// GetBuildingRadiusKfByOSMType returns the radius coefficient for a building by OSM type
// Returns 0 if no mapping or configuration is found
func GetBuildingRadiusKfByOSMType(osmBuildingType string) int {
	config := GetBuildingEffectsConfigByOSMType(osmBuildingType)
	if config == nil {
		return 0
	}
	return config.RadiusKf
}

// GetBuildingEffectsByOSMType returns the effects for a building by OSM type
// Returns nil if no mapping or configuration is found
func GetBuildingEffectsByOSMType(osmBuildingType string) *BuildingEffect {
	config := GetBuildingEffectsConfigByOSMType(osmBuildingType)
	if config == nil {
		return nil
	}
	return &config.Effects
}

// getBuildingEffectsConfig returns cached config, loading it once if needed
func getBuildingEffectsConfig() *BuildingEffectsConfig {
	effectsConfigOnce.Do(func() {
		config, err := loadBuildingEffectsConfig()
		if err != nil {
			log.Printf("Failed to load building effects config: %v", err)
			return
		}
		cachedEffectsConfig = config
	})
	return cachedEffectsConfig
}

// loadBuildingEffectsConfig loads the building effects configuration from JSON file
func loadBuildingEffectsConfig() (*BuildingEffectsConfig, error) {
	// Read the JSON file
	data, err := os.ReadFile("usa_buildings_data/building_cat_kf_config.json")
	if err != nil {
		return nil, err
	}

	// Parse JSON
	var config BuildingEffectsConfig
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// GetBuildingRadiusKf returns the radius coefficient for a specific building type
// Returns 0 if no configuration is found
func GetBuildingRadiusKf(buildingType string) int {
	config := GetBuildingEffectsConfig(buildingType)
	if config == nil {
		return 0
	}
	return config.RadiusKf
}

// GetBuildingEffects returns the effects for a specific building type
// Returns nil if no configuration is found
func GetBuildingEffects(buildingType string) *BuildingEffect {
	config := GetBuildingEffectsConfig(buildingType)
	if config == nil {
		return nil
	}
	return &config.Effects
}
