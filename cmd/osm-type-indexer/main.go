package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

// TODO: сделай мапу похожих типов зданий. Конфиг для веса эффектов должен быть в джейсоне

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

func main() {
	// Define command line flags
	inputFiles := flag.String("input", "", "Comma-separated list of input JSON files")
	outputFile := flag.String("output", "test_data/merged_buildings.json", "Output JSON file")
	minOccurrences := flag.Int("min-occurrences", 2, "Minimum number of occurrences to keep a building type")
	minAreaInt := flag.Int("min-area", 1000, "Minimum area in square meters to keep a building type")

	// Additional filters (enabled by default)
	filterShortNames := flag.Bool("filter-short-names", true, "Filter out building types with 1-2 character names")
	filterSemicolon := flag.Bool("filter-semicolon", true, "Filter out building types with semicolon in name")
	filterLongNames := flag.Bool("filter-long-names", true, "Filter out building types with names longer than 50 characters")

	flag.Parse()

	// Convert minAreaInt to float64
	minArea := float64(*minAreaInt)

	// Check for input files
	if *inputFiles == "" {
		log.Fatal("No input files specified. Use --input flag with comma-separated list of files")
	}

	// Parse the list of input files using strings.Split
	files := strings.Split(*inputFiles, ",")
	if len(files) == 0 {
		log.Fatal("No input files found in the provided list")
	}

	log.Printf("Processing %d input files", len(files))
	log.Printf("Min occurrences: %d, Min area: %.2f m²", *minOccurrences, minArea)
	log.Printf("Filter short names (1-2 chars): %v", *filterShortNames)
	log.Printf("Filter semicolon names: %v", *filterSemicolon)
	log.Printf("Filter long names (>50 chars): %v", *filterLongNames)
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
	filteredTypes := make(map[string]int)
	filteredAreas := make(map[string]float64)
	removedCount := 0
	removedByShortName := 0
	removedBySemicolon := 0
	removedByLongName := 0
	removedByCountAndArea := 0

	for buildingType, count := range mergedStats.BuildingTypes {
		area := mergedStats.BuildingAreas[buildingType]
		shouldRemove := false

		// Check short name filter
		if *filterShortNames && len(buildingType) <= 2 {
			shouldRemove = true
			removedByShortName++
		}

		// Check semicolon filter
		if *filterSemicolon && strings.Contains(buildingType, ";") {
			shouldRemove = true
			removedBySemicolon++
		}

		// Check long name filter
		if *filterLongNames && len(buildingType) > 50 {
			shouldRemove = true
			removedByLongName++
		}

		// Check count and area filter
		if count < *minOccurrences && area < minArea {
			shouldRemove = true
			removedByCountAndArea++
		}

		if shouldRemove {
			removedCount++
			continue // Skip this type, don't add to filtered maps
		} else {
			// Keep this type
			filteredTypes[buildingType] = count
			filteredAreas[buildingType] = area
		}
	}

	// Update merged data with filtered results
	mergedStats.BuildingTypes = filteredTypes
	mergedStats.BuildingAreas = filteredAreas

	log.Printf("After filtering: %d building types", len(mergedStats.BuildingTypes))
	log.Printf("Total removed: %d building types", removedCount)
	log.Printf("  - Removed by short name filter: %d", removedByShortName)
	log.Printf("  - Removed by semicolon filter: %d", removedBySemicolon)
	log.Printf("  - Removed by long name filter: %d", removedByLongName)
	log.Printf("  - Removed by count+area filter: %d", removedByCountAndArea)

	// Save the result to a JSON file
	jsonData, err := json.MarshalIndent(mergedStats, "", "  ")
	if err != nil {
		log.Fatalf("Error marshaling JSON: %v", err)
	}

	if err := os.WriteFile(*outputFile, jsonData, 0644); err != nil {
		log.Fatalf("Error writing output file: %v", err)
	}

	log.Printf("Successfully merged and filtered building data to: %s", *outputFile)

	// Output statistics
	fmt.Printf("Total building types: %d\n", len(mergedStats.BuildingTypes))

	// Output top-10 building types by count
	fmt.Println("\nTop building types by count:")
	topTypes := getTopBuildingTypes(mergedStats.BuildingTypes, 10)
	for i, item := range topTypes {
		fmt.Printf("%d. %s: %d\n", i+1, item.Type, item.Value)
	}

	// Output top-10 building types by area
	fmt.Println("\nTop building types by area (m²):")
	topAreas := getTopBuildingAreas(mergedStats.BuildingAreas, 10)
	for i, item := range topAreas {
		fmt.Printf("%d. %s: %.2f m²\n", i+1, item.Type, item.Value)
	}
}

// TypeValue represents a type-value pair for sorting
type TypeValue struct {
	Type  string
	Value interface{}
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
