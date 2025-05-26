package osm_processor

import "log"

// ZoneDependencies tracks connections between zones through shared buildings
type ZoneDependencies struct {
	connections map[string]map[string]bool // zoneID -> set of connected zone IDs
}

// NewZoneDependencies creates a new zone dependencies tracker
func NewZoneDependencies() *ZoneDependencies {
	return &ZoneDependencies{
		connections: make(map[string]map[string]bool),
	}
}

// addConnection adds a bidirectional connection between zones
func (zd *ZoneDependencies) addConnection(zoneID1, zoneID2 string) {
	// Initialize maps if they don't exist
	if zd.connections[zoneID1] == nil {
		zd.connections[zoneID1] = make(map[string]bool)
	}
	if zd.connections[zoneID2] == nil {
		zd.connections[zoneID2] = make(map[string]bool)
	}

	// Add bidirectional connections
	zd.connections[zoneID1][zoneID2] = true
	zd.connections[zoneID2][zoneID1] = true
}

// addMultiZoneBuilding adds connections for a building that affects multiple zones
func (zd *ZoneDependencies) addMultiZoneBuilding(zoneIDs []string) {
	// Connect each zone to every other zone in this group
	for i, zoneID1 := range zoneIDs {
		for j, zoneID2 := range zoneIDs {
			if i != j {
				zd.addConnection(zoneID1, zoneID2)
			}
		}
	}
}

// getConnectedZones returns all zones connected to the given zone IDs
func (zd *ZoneDependencies) getConnectedZones(targetZoneIDs []string) []string {
	visited := make(map[string]bool)
	var result []string

	// Start BFS from each target zone
	queue := make([]string, 0, len(targetZoneIDs))
	for _, zoneID := range targetZoneIDs {
		if !visited[zoneID] {
			queue = append(queue, zoneID)
			visited[zoneID] = true
		}
	}

	// BFS to find all connected zones
	for len(queue) > 0 {
		currentZone := queue[0]
		queue = queue[1:]
		result = append(result, currentZone)

		// Add all connected zones to queue
		for connectedZone := range zd.connections[currentZone] {
			if !visited[connectedZone] {
				visited[connectedZone] = true
				queue = append(queue, connectedZone)
			}
		}
	}

	return result
}

// getConnectionCount returns the total number of zone connections
func (zd *ZoneDependencies) getConnectionCount() int {
	totalConnections := 0
	for _, connections := range zd.connections {
		totalConnections += len(connections)
	}
	return totalConnections / 2 // Divide by 2 because connections are bidirectional
}

// findConnectedZones finds all zones connected to overweight zones through building dependencies
func (p *OSMProcessor) findConnectedZones(overweightZoneIDs []string, dependencies *ZoneDependencies) []string {
	if len(overweightZoneIDs) == 0 {
		return []string{}
	}

	connectedZones := dependencies.getConnectedZones(overweightZoneIDs)

	log.Printf("Found %d zones connected to %d overweight zones through building dependencies",
		len(connectedZones), len(overweightZoneIDs))

	return connectedZones
}
