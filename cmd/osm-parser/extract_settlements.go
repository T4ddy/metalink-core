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

// Settlement представляет населенный пункт
type Settlement struct {
	ID         int64
	Name       string
	Type       string // city, town, village, hamlet
	Lat, Lon   float64
	Population int
	IsNode     bool // true если это узел, false если полигон (way)
}

func main() {
	// Проверяем, передан ли аргумент с путем к файлу
	if len(os.Args) < 2 {
		log.Fatal("Usage: program <path-to-osm.pbf>")
	}

	// Путь к OSM PBF файлу
	osmFile := os.Args[1]
	log.Printf("Processing file: %s", osmFile)

	// Открываем файл
	f, err := os.Open(osmFile)
	if err != nil {
		log.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()

	// Создаем декодер
	decoder := osmpbf.NewDecoder(f)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)

	// Используем все доступные CPU для параллельной обработки
	numProcs := runtime.GOMAXPROCS(-1)
	decoder.Start(numProcs)
	log.Printf("Decoder started with %d processors", numProcs)

	// Счетчики для статистики
	nodeCount := 0
	wayCount := 0
	totalCount := 0

	// Кэш узлов для формирования полигонов населенных пунктов
	nodeCache := make(map[int64]*osmpbf.Node)

	// Карта для отслеживания населенных пунктов, чтобы избежать дубликатов
	settlements := make(map[string]Settlement)

	// Этап 1: Собираем все узлы-населенные пункты и кэшируем другие узлы для полигонов
	log.Println("Phase 1: Collecting settlement nodes and caching coordinates...")

	for {
		// Декодируем следующий объект
		object, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Error decoding: %v", err)
		}

		// Обрабатываем узел
		if node, ok := object.(*osmpbf.Node); ok {
			// Сохраняем все узлы для использования в полигонах
			nodeCache[node.ID] = node

			// Проверяем, является ли узел населенным пунктом
			if placeType, isPlace := node.Tags["place"]; isPlace {
				// Фильтруем только основные типы населенных пунктов
				if isSettlementType(placeType) {
					name := node.Tags["name"]
					if name == "" {
						name = fmt.Sprintf("Unnamed %s", placeType)
					}

					// Извлекаем население, если указано
					population := 0
					if popStr, ok := node.Tags["population"]; ok {
						if pop, err := strconv.Atoi(popStr); err == nil {
							population = pop
						}
					}

					// Создаем ключ для избежания дубликатов
					key := fmt.Sprintf("node_%d", node.ID)

					// Сохраняем населенный пункт
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

					// Выводим базовую информацию о населенном пункте
					log.Printf("[Node] %s: %s (%.6f, %.6f)", placeType, name, node.Lat, node.Lon)
				}
			}
		}
	}

	log.Printf("Collected %d settlement nodes", nodeCount)

	// Сбрасываем декодер для второго прохода
	f.Seek(0, 0)
	decoder = osmpbf.NewDecoder(f)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)
	decoder.Start(numProcs)

	// Этап 2: Собираем все пути (полигоны), представляющие населенные пункты
	log.Println("Phase 2: Collecting settlement polygons (ways)...")

	for {
		// Декодируем следующий объект
		object, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Error decoding: %v", err)
		}

		// Обрабатываем путь
		if way, ok := object.(*osmpbf.Way); ok {
			// Проверяем, является ли путь населенным пунктом
			if placeType, isPlace := way.Tags["place"]; isPlace {
				if isSettlementType(placeType) {
					name := way.Tags["name"]
					if name == "" {
						name = fmt.Sprintf("Unnamed %s area", placeType)
					}

					// Извлекаем население, если указано
					population := 0
					if popStr, ok := way.Tags["population"]; ok {
						if pop, err := strconv.Atoi(popStr); err == nil {
							population = pop
						}
					}

					// Вычисляем центроид полигона, если доступны координаты
					var lat, lon float64
					if len(way.NodeIDs) > 0 {
						// Пытаемся использовать первый узел полигона для получения координат
						if firstNode, exists := nodeCache[way.NodeIDs[0]]; exists {
							lat = firstNode.Lat
							lon = firstNode.Lon
						}
					}

					// Создаем ключ для избежания дубликатов
					key := fmt.Sprintf("way_%d", way.ID)

					// Сохраняем населенный пункт
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

					// Выводим информацию о полигональном населенном пункте
					log.Printf("[Way] %s: %s", placeType, name)
				}
			}
		}
	}

	log.Printf("Collected %d settlement polygons (ways)", wayCount)
	log.Printf("Total settlements found: %d", totalCount)
	log.Println("Processing complete!")
}

// isSettlementType проверяет, является ли тип одним из основных типов населенных пунктов
func isSettlementType(placeType string) bool {
	switch placeType {
	case "city", "town", "village", "hamlet", "suburb", "neighbourhood", "quarter", "borough":
		return true
	default:
		return false
	}
}
