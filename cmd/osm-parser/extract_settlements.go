package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strconv"

	"github.com/qedus/osmpbf"
)

// Settlement represents a settlement
type Settlement struct {
	ID         int64
	Name       string
	Type       string // city, town, village, hamlet
	Lat, Lon   float64
	Population int
	IsNode     bool // true if this is a node, false if polygon (way)
}

func main() {
	// Check if the argument with the file path is provided
	if len(os.Args) < 2 {
		log.Fatal("Usage: program <path-to-osm.pbf>")
	}

	// Path to the OSM PBF file
	osmFile := os.Args[1]
	log.Printf("Processing file: %s", osmFile)

	// Open the file
	f, err := os.Open(osmFile)
	if err != nil {
		log.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()

	// Create a decoder
	decoder := osmpbf.NewDecoder(f)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)

	// Use all available CPUs for parallel processing
	numProcs := runtime.GOMAXPROCS(-1)
	decoder.Start(numProcs)
	log.Printf("Decoder started with %d processors", numProcs)

	// Counters for statistics
	nodeCount := 0
	wayCount := 0
	totalCount := 0

	// Node cache for forming settlement polygons
	nodeCache := make(map[int64]*osmpbf.Node)

	// Map to track settlements to avoid duplicates
	settlements := make(map[string]Settlement)

	// Phase 1: Collecting all settlement nodes and caching other nodes for polygons
	log.Println("Phase 1: Collecting settlement nodes and caching coordinates...")

	for {
		// Decode the next object
		object, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Error decoding: %v", err)
		}

		// Process node
		if node, ok := object.(*osmpbf.Node); ok {
			// Save all nodes for use in polygons
			nodeCache[node.ID] = node

			// Check if the node is a settlement
			if placeType, isPlace := node.Tags["place"]; isPlace {
				// Filter only main types of settlements
				if isSettlementType(placeType) {
					name := node.Tags["name"]
					if name == "" {
						name = fmt.Sprintf("Unnamed %s", placeType)
					}

					// Extract population if specified
					population := 0
					if popStr, ok := node.Tags["population"]; ok {
						if pop, err := strconv.Atoi(popStr); err == nil {
							population = pop
						}
					}

					// Create a key to avoid duplicates
					key := fmt.Sprintf("node_%d", node.ID)

					// Save the settlement
					settlements[key] = Settlement{
						ID:         node.ID,
						Name:       name,
						Type:       placeType,
						Lat:        node.Lat,
						Lon:        node.Lon,
						Population: population,
						IsNode:     true,
					}

					nodeCount++
					totalCount++

					// Output basic information about the settlement
					log.Printf("[Node] %s: %s (%.6f, %.6f)", placeType, name, node.Lat, node.Lon)
				}
			}
		}
	}

	log.Printf("Collected %d settlement nodes", nodeCount)

	// Reset the decoder for the second pass
	f.Seek(0, 0)
	decoder = osmpbf.NewDecoder(f)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)
	decoder.Start(numProcs)

	// Phase 2: Collecting all ways (polygons) representing settlements
	log.Println("Phase 2: Collecting settlement polygons (ways)...")

	for {
		// Decode the next object
		object, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Error decoding: %v", err)
		}

		// Process way
		if way, ok := object.(*osmpbf.Way); ok {
			// Check if the way is a settlement
			if placeType, isPlace := way.Tags["place"]; isPlace {
				if isSettlementType(placeType) {
					name := way.Tags["name"]
					if name == "" {
						name = fmt.Sprintf("Unnamed %s area", placeType)
					}

					// Extract population if specified
					population := 0
					if popStr, ok := way.Tags["population"]; ok {
						if pop, err := strconv.Atoi(popStr); err == nil {
							population = pop
						}
					}

					// Calculate the centroid of the polygon if coordinates are available
					var lat, lon float64
					if len(way.NodeIDs) > 0 {
						// Try to use the first node of the polygon to get coordinates
						if firstNode, exists := nodeCache[way.NodeIDs[0]]; exists {
							lat = firstNode.Lat
							lon = firstNode.Lon
						}
					}

					// Create a key to avoid duplicates
					key := fmt.Sprintf("way_%d", way.ID)

					// Save the settlement
					settlements[key] = Settlement{
						ID:         way.ID,
						Name:       name,
						Type:       placeType,
						Lat:        lat,
						Lon:        lon,
						Population: population,
						IsNode:     false,
					}

					wayCount++
					totalCount++

					// Output information about the polygonal settlement
					log.Printf("[Way] %s: %s", placeType, name)
				}
			}
		}
	}

	log.Printf("Collected %d settlement polygons (ways)", wayCount)
	log.Printf("Total settlements found: %d", totalCount)
	log.Println("Processing complete!")
}

// isSettlementType checks if the type is one of the main types of settlements
func isSettlementType(placeType string) bool {
	switch placeType {
	case "city", "town", "village", "hamlet", "suburb", "neighbourhood", "quarter", "borough":
		return true
	default:
		return false
	}
}
