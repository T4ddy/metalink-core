package mappers

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
)

// BuildingMappingConfig represents the structure of building mapping configuration
type BuildingMappingConfig struct {
	BuildingMappingConfig map[string][]string `json:"building_mapping_config"`
}

var (
	cachedConfig *BuildingMappingConfig
	configOnce   sync.Once
)

// MapBuildingCategory maps an OSM building category to game category
// Returns "other" if no mapping is found
func MapBuildingCategory(osmCategory string) string {
	config := getBuildingMappingConfig()
	if config == nil {
		return "other"
	}

	return MapBuildingCategoryWithConfig(osmCategory, config)
}

// MapBuildingCategoryWithConfig maps an OSM building category using provided config
// This version allows for dependency injection and easier testing
func MapBuildingCategoryWithConfig(osmCategory string, config *BuildingMappingConfig) string {
	if config == nil {
		return "other"
	}

	// Normalize input category (trim whitespace and convert to lowercase)
	normalizedCategory := strings.TrimSpace(strings.ToLower(osmCategory))

	// Search through all categories to find a match
	for gameCategory, osmCategories := range config.BuildingMappingConfig {
		for _, category := range osmCategories {
			if strings.ToLower(category) == normalizedCategory {
				return gameCategory
			}
		}
	}

	// If no match found, return "other"
	return "other"
}

// getBuildingMappingConfig returns cached config, loading it once if needed
func getBuildingMappingConfig() *BuildingMappingConfig {
	configOnce.Do(func() {
		config, err := loadBuildingMappingConfig()
		if err != nil {
			log.Printf("Failed to load building mapping config: %v", err)
			return
		}
		cachedConfig = config
	})
	return cachedConfig
}

// loadBuildingMappingConfig loads the building mapping configuration from JSON file
func loadBuildingMappingConfig() (*BuildingMappingConfig, error) {
	// Read the JSON file
	data, err := os.ReadFile("usa_buildings_data/bmap.json")
	if err != nil {
		return nil, err
	}

	// Parse JSON
	var config BuildingMappingConfig
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// GetAllGameCategories returns all available game categories
func GetAllGameCategories() []string {
	config := getBuildingMappingConfig()
	if config == nil {
		return []string{"other"}
	}

	categories := make([]string, 0, len(config.BuildingMappingConfig))
	for category := range config.BuildingMappingConfig {
		categories = append(categories, category)
	}

	return categories
}
