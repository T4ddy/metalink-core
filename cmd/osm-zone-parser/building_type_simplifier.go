package main

// GetSimplifiedBuildingType maps detailed building types to simplified categories
func GetSimplifiedBuildingType(buildingType string) string {
	// Education buildings
	educationTypes := map[string]string{
		"school":       "education",
		"college":      "education",
		"university":   "education",
		"education":    "education",
		"kindergarten": "education",
		"library":      "education",
		"gymnasium":    "education",
		"schh":         "education",
	}

	// Places that might have weapons
	weaponsTypes := map[string]string{
		"military":   "weapons",
		"police":     "weapons",
		"Police":     "weapons",
		"prison":     "weapons",
		"jail":       "weapons",
		"bunker":     "weapons",
		"guardhouse": "weapons",
	}

	// Residential buildings
	residentialTypes := map[string]string{
		"yes":                "residential",
		"house":              "residential",
		"residential":        "residential",
		"apartments":         "residential",
		"detached":           "residential",
		"terrace":            "residential",
		"bungalow":           "residential",
		"cabin":              "residential",
		"dormitory":          "residential",
		"static_caravan":     "residential",
		"semidetached_house": "residential",
		"townhouse":          "residential",
		"townhome":           "residential",
		"villa":              "residential",
		"mobile_home":        "residential",
		"stilt_house":        "residential",
		"nursing_home":       "residential",
		"chalet":             "residential",
		"residents":          "residential",
		"brownstone":         "residential",
	}

	// Food-related commercial buildings
	foodTypes := map[string]string{
		"restaurant":  "food",
		"cafe":        "food",
		"pub":         "food",
		"coffee":      "food",
		"food_truck":  "food",
		"supermarket": "food",
		"brewery":     "food",
	}

	// Healthcare buildings (potential medical supplies)
	healthcareTypes := map[string]string{
		"hospital":   "healthcare",
		"healthcare": "healthcare",
		"clinic":     "healthcare",
		"medical":    "healthcare",
		"veterinary": "healthcare",
		"pharmacy":   "healthcare",
	}

	// Retail and shopping
	retailTypes := map[string]string{
		"retail":          "retail",
		"shop":            "retail",
		"shops":           "retail",
		"store":           "retail",
		"mall":            "retail",
		"shopping_center": "retail",
		"strip_mall":      "retail",
		"kiosk":           "retail",
		"reail":           "retail",
		"bank":            "retail",
	}

	// Office buildings
	officeTypes := map[string]string{
		"office":                 "office",
		"commercial":             "office",
		"office_building":        "office",
		"data_center":            "office",
		"central_office":         "office",
		"conference_centre":      "office",
		"government_office":      "office",
		"community_group_office": "office",
		"administrative":         "office",
	}

	// Accommodation
	accommodationTypes := map[string]string{
		"hotel": "accommodation",
		"motel": "accommodation",
	}

	// Automotive (potential vehicle parts, tools)
	automotiveTypes := map[string]string{
		"car_repair":             "automotive",
		"car_wash":               "automotive",
		"fuel":                   "automotive",
		"garage":                 "automotive",
		"garages":                "automotive",
		"carport":                "automotive",
		"parking":                "automotive",
		"parking_garage":         "automotive",
		"drive-thru_atm":         "automotive",
		"gas_pumps_under_awning": "automotive",
	}

	// Entertainment
	entertainmentTypes := map[string]string{
		"cinema":                 "entertainment",
		"theatre":                "entertainment",
		"studio":                 "entertainment",
		"stadium":                "entertainment",
		"sports_centre":          "entertainment",
		"arena":                  "entertainment",
		"grandstand":             "entertainment",
		"bleachers":              "entertainment",
		"recreation":             "entertainment",
		"Swimming Pool Facility": "entertainment",
		"Natatorium":             "education",
		"natatorium":             "education",
		"sports_hall":            "education",
	}

	// Industrial buildings
	industrialTypes := map[string]string{
		"industrial":                    "industrial",
		"factory":                       "industrial",
		"warehouse":                     "industrial",
		"ware":                          "industrial",
		"manufacture":                   "industrial",
		"electricity":                   "industrial",
		"substation":                    "industrial",
		"construction":                  "industrial",
		"distribution_center":           "industrial",
		"industrial;big state electric": "industrial",
	}

	// Storage buildings
	storageTypes := map[string]string{
		"storage":                 "storage",
		"storage_rental":          "storage",
		"storage_tank":            "storage",
		"tank":                    "storage",
		"silo":                    "storage",
		"water_tower":             "storage",
		"water_tank":              "storage",
		"sewage_tank":             "storage",
		"slurry_tank":             "storage",
		"old_tank":                "storage",
		"gasometer":               "storage",
		"container":               "storage",
		"shipping container shed": "storage",
	}

	// Government buildings
	governmentTypes := map[string]string{
		"government":      "government",
		"civic":           "government",
		"courthouse":      "government",
		"townhall":        "government",
		"County_Building": "government",
		"public_building": "government",
		"post_office":     "government",
		"fire_station":    "government",
	}

	// Community facilities
	communityTypes := map[string]string{
		"community":               "community",
		"public":                  "community",
		"Charitable_Organization": "community",
		"toilets":                 "community",
		"restrooms":               "community",
		"bathroom":                "community",
		"pavilion":                "community",
		"covered_pavilion":        "community",
		"picnic_shelter":          "community",
		"shelter":                 "community",
	}

	// Cultural buildings
	culturalTypes := map[string]string{
		"museum":              "cultural",
		"historic":            "cultural",
		"historic_building":   "cultural",
		"tourism":             "cultural",
		"theater_arts_center": "education",
	}

	// Religious buildings
	religiousTypes := map[string]string{
		"religious":        "religious",
		"church":           "religious",
		"church;yes":       "religious",
		"mosque":           "religious",
		"temple":           "religious",
		"synagogue":        "religious",
		"chapel":           "religious",
		"cathedral":        "religious",
		"place_of_worship": "religious",
		"campanile":        "religious",
	}

	// Agricultural buildings
	agriculturalTypes := map[string]string{
		"farm":            "agricultural",
		"barn":            "agricultural",
		"greenhouse":      "agricultural",
		"stable":          "agricultural",
		"cowshed":         "agricultural",
		"sty":             "agricultural",
		"farm_auxiliary":  "agricultural",
		"poultry_house":   "agricultural",
		"chicken_coop":    "agricultural",
		"Horse_facility":  "agricultural",
		"riding_hall":     "agricultural",
		"allotment_house": "agricultural",
		"outbuilding":     "agricultural",
	}

	// Transportation buildings
	transportationTypes := map[string]string{
		"transportation":   "transportation",
		"hangar":           "transportation",
		"terminal":         "transportation",
		"airport_terminal": "transportation",
		"train_station":    "transportation",
		"bus_station":      "transportation",
		"station":          "transportation",
		"toll_booth":       "transportation",
		"control_tower":    "transportation",
		"ground_station":   "transportation",
		"service":          "transportation",
	}

	// Check if the building type is in any of the maps
	if simplified, ok := educationTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := weaponsTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := residentialTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := foodTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := healthcareTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := retailTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := officeTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := accommodationTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := automotiveTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := entertainmentTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := industrialTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := storageTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := governmentTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := communityTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := culturalTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := religiousTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := agriculturalTypes[buildingType]; ok {
		return simplified
	}
	if simplified, ok := transportationTypes[buildingType]; ok {
		return simplified
	}

	// Fallback for unknown types
	return "residential"
}
