package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

// ZoneData represents the structure of the JSON file with zone data
type ZoneData struct {
	Zone      ZoneGeometry  `json:"zone"`
	Buildings BuildingStats `json:"buildings"`
}

// ZoneGeometry represents the geometric data of the zone
type ZoneGeometry struct {
	ID                string     `json:"ID"`
	TopLeftLatLon     [2]float64 `json:"TopLeftLatLon"`
	TopRightLatLon    [2]float64 `json:"TopRightLatLon"`
	BottomLeftLatLon  [2]float64 `json:"BottomLeftLatLon"`
	BottomRightLatLon [2]float64 `json:"BottomRightLatLon"`
	Size              float64    `json:"Size"`
}

// BuildingStats represents the building statistics in the zone
type BuildingStats struct {
	SingleFloorCount     int                `json:"single_floor_count"`
	SingleFloorTotalArea float64            `json:"single_floor_total_area"`
	LowRiseCount         int                `json:"low_rise_count"`
	LowRiseTotalArea     float64            `json:"low_rise_total_area"`
	HighRiseCount        int                `json:"high_rise_count"`
	HighRiseTotalArea    float64            `json:"high_rise_total_area"`
	SkyscraperCount      int                `json:"skyscraper_count"`
	SkyscraperTotalArea  float64            `json:"skyscraper_total_area"`
	TotalCount           int                `json:"total_count"`
	TotalArea            float64            `json:"total_area"`
	BuildingTypes        map[string]int     `json:"building_types"`
	BuildingAreas        map[string]float64 `json:"building_areas"`
}

// MergedStats represents the result of merging building statistics
type MergedStats struct {
	BuildingTypes map[string]int     `json:"building_types"`
	BuildingAreas map[string]float64 `json:"building_areas"`
}

// TypeValue represents a type-value pair for sorting
type TypeValue struct {
	Type  string
	Value interface{}
}

// TypeIndexerConfig holds configuration for the type indexer
type TypeIndexerConfig struct {
	InputFiles       string
	OutputFile       string
	MinOccurrences   int
	MinArea          float64
	FilterShortNames bool
	FilterSemicolon  bool
	FilterLongNames  bool
}

// runTypeIndexerMode processes and merges building statistics from JSON files
func runTypeIndexerMode() {
	log.Println("Running in Building Type Indexer mode")

	config := TypeIndexerConfig{
		InputFiles:       inputFiles,
		OutputFile:       outputFile,
		MinOccurrences:   minOccurrences,
		MinArea:          float64(minAreaInt),
		FilterShortNames: filterShortNames,
		FilterSemicolon:  filterSemicolon,
		FilterLongNames:  filterLongNames,
	}

	if err := processTypeIndexer(config); err != nil {
		log.Fatalf("Type indexer failed: %v", err)
	}
}

// processTypeIndexer is the main function that handles the type indexing logic
func processTypeIndexer(config TypeIndexerConfig) error {
	// Check for input files
	if config.InputFiles == "" {
		return fmt.Errorf("no input files specified. Use --input flag with comma-separated list of files")
	}

	// Parse the list of input files using strings.Split
	files := strings.Split(config.InputFiles, ",")
	if len(files) == 0 {
		return fmt.Errorf("no input files found in the provided list")
	}

	log.Printf("Processing %d input files", len(files))
	log.Printf("Min occurrences: %d, Min area: %.2f m²", config.MinOccurrences, config.MinArea)
	log.Printf("Filter short names (1-2 chars): %v", config.FilterShortNames)
	log.Printf("Filter semicolon names: %v", config.FilterSemicolon)
	log.Printf("Filter long names (>50 chars): %v", config.FilterLongNames)
	log.Printf("Filter logic: Remove if count < min-occurrences AND area < min-area")

	// Create an object to store merged data
	mergedStats := MergedStats{
		BuildingTypes: make(map[string]int),
		BuildingAreas: make(map[string]float64),
	}

	// Process each input file
	for _, filePath := range files {
		// Trim any whitespace that might be present after splitting
		filePath = strings.TrimSpace(filePath)
		log.Printf("Processing file: %s", filePath)

		// Read the file
		data, err := os.ReadFile(filePath)
		if err != nil {
			log.Printf("Error reading file %s: %v", filePath, err)
			continue
		}

		// Parse JSON
		var zoneData ZoneData
		if err := json.Unmarshal(data, &zoneData); err != nil {
			log.Printf("Error parsing JSON from file %s: %v", filePath, err)
			continue
		}

		// Merge building type data
		for buildingType, count := range zoneData.Buildings.BuildingTypes {
			mergedStats.BuildingTypes[buildingType] += count
		}

		// Merge building area data
		for buildingType, area := range zoneData.Buildings.BuildingAreas {
			mergedStats.BuildingAreas[buildingType] += area
		}
	}

	log.Printf("Before filtering: %d building types", len(mergedStats.BuildingTypes))

	// Apply filters
	filteredStats, filterStats := applyFilters(mergedStats, config)

	log.Printf("After filtering: %d building types", len(filteredStats.BuildingTypes))
	log.Printf("Total removed: %d building types", filterStats.TotalRemoved)
	log.Printf("  - Removed by short name filter: %d", filterStats.RemovedByShortName)
	log.Printf("  - Removed by semicolon filter: %d", filterStats.RemovedBySemicolon)
	log.Printf("  - Removed by long name filter: %d", filterStats.RemovedByLongName)
	log.Printf("  - Removed by count+area filter: %d", filterStats.RemovedByCountAndArea)

	// Save the result to a JSON file
	if err := saveResultsToFile(filteredStats, config.OutputFile); err != nil {
		return fmt.Errorf("failed to save results: %v", err)
	}

	// Output statistics
	printStatistics(filteredStats)

	return nil
}

// FilterStats holds statistics about the filtering process
type FilterStats struct {
	TotalRemoved          int
	RemovedByShortName    int
	RemovedBySemicolon    int
	RemovedByLongName     int
	RemovedByCountAndArea int
}

// applyFilters applies all configured filters to the building statistics
func applyFilters(mergedStats MergedStats, config TypeIndexerConfig) (MergedStats, FilterStats) {
	filteredTypes := make(map[string]int)
	filteredAreas := make(map[string]float64)

	var stats FilterStats

	for buildingType, count := range mergedStats.BuildingTypes {
		area := mergedStats.BuildingAreas[buildingType]
		shouldRemove := false

		// Check short name filter
		if config.FilterShortNames && len(buildingType) <= 2 {
			shouldRemove = true
			stats.RemovedByShortName++
		}

		// Check semicolon filter
		if config.FilterSemicolon && strings.Contains(buildingType, ";") {
			shouldRemove = true
			stats.RemovedBySemicolon++
		}

		// Check long name filter
		if config.FilterLongNames && len(buildingType) > 50 {
			shouldRemove = true
			stats.RemovedByLongName++
		}

		// Check count and area filter
		if count < config.MinOccurrences && area < config.MinArea {
			shouldRemove = true
			stats.RemovedByCountAndArea++
		}

		if shouldRemove {
			stats.TotalRemoved++
			continue // Skip this type, don't add to filtered maps
		} else {
			// Keep this type
			filteredTypes[buildingType] = count
			filteredAreas[buildingType] = area
		}
	}

	return MergedStats{
		BuildingTypes: filteredTypes,
		BuildingAreas: filteredAreas,
	}, stats
}

// saveResultsToFile saves the merged statistics to a JSON file
func saveResultsToFile(stats MergedStats, outputFile string) error {
	jsonData, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling JSON: %v", err)
	}

	if err := os.WriteFile(outputFile, jsonData, 0644); err != nil {
		return fmt.Errorf("error writing output file: %v", err)
	}

	log.Printf("Successfully merged and filtered building data to: %s", outputFile)
	return nil
}

// printStatistics outputs top building types by count and area
func printStatistics(stats MergedStats) {
	fmt.Printf("Total building types: %d\n", len(stats.BuildingTypes))

	// Output top-10 building types by count
	fmt.Println("\nTop building types by count:")
	topTypes := getTopBuildingTypes(stats.BuildingTypes, 10)
	for i, item := range topTypes {
		fmt.Printf("%d. %s: %d\n", i+1, item.Type, item.Value)
	}

	// Output top-10 building types by area
	fmt.Println("\nTop building types by area (m²):")
	topAreas := getTopBuildingAreas(stats.BuildingAreas, 10)
	for i, item := range topAreas {
		fmt.Printf("%d. %s: %.2f m²\n", i+1, item.Type, item.Value)
	}
}

// getTopBuildingTypes returns the top-N building types by count
func getTopBuildingTypes(types map[string]int, n int) []TypeValue {
	// Create a slice for sorting
	var items []TypeValue
	for t, count := range types {
		items = append(items, TypeValue{Type: t, Value: count})
	}

	// Sort by descending count
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[i].Value.(int) < items[j].Value.(int) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}

	// Limit the number of results
	if len(items) > n {
		items = items[:n]
	}

	return items
}

// getTopBuildingAreas returns the top-N building types by area
func getTopBuildingAreas(areas map[string]float64, n int) []TypeValue {
	// Create a slice for sorting
	var items []TypeValue
	for t, area := range areas {
		items = append(items, TypeValue{Type: t, Value: area})
	}

	// Sort by descending area
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[i].Value.(float64) < items[j].Value.(float64) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}

	// Limit the number of results
	if len(items) > n {
		items = items[:n]
	}

	return items
}
