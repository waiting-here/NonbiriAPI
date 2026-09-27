package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// encoding/json deliberately accepts null for scalar Go fields and silently
// fills absent fields with zero values. Check the closed JSON shape first so
// the browser and the authoritative replay accept identical level documents.
func checkLevelShape(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	root, err := schemaObject(value, "level", "format format_version engine_version scoring_version duration_seconds speed_pixels_per_second thresholds fish tools solids hazards bowls switches gates directions", "format format_version engine_version scoring_version duration_seconds speed_pixels_per_second thresholds fish bowls")
	if err != nil {
		return err
	}
	for _, key := range []string{"format_version", "engine_version", "scoring_version", "duration_seconds", "speed_pixels_per_second"} {
		if err := schemaNumber(root[key], "level."+key); err != nil {
			return err
		}
	}
	if err := schemaString(root["format"], "level.format"); err != nil {
		return err
	}
	thresholds, err := schemaArray(root["thresholds"], "level.thresholds", false)
	if err != nil {
		return err
	}
	if len(thresholds) != 3 {
		return fmt.Errorf("level.thresholds must contain exactly three integers")
	}
	for index, threshold := range thresholds {
		if err := schemaNumber(threshold, fmt.Sprintf("level.thresholds[%d]", index)); err != nil {
			return err
		}
	}
	fish, err := schemaArray(root["fish"], "level.fish", false)
	if err != nil {
		return err
	}
	for index, item := range fish {
		location := fmt.Sprintf("level.fish[%d]", index)
		object, err := schemaObject(item, location, "id x y heading", "id x y heading")
		if err != nil {
			return err
		}
		for _, key := range []string{"id", "x", "y", "heading"} {
			if err := schemaNumber(object[key], location+"."+key); err != nil {
				return err
			}
		}
	}
	for _, group := range []struct {
		name     string
		allowed  string
		required string
	}{
		{"tools", "id polygon resource_key placed x y", "id polygon resource_key placed x y"},
		{"solids", "id polygon", "id polygon"},
		{"hazards", "id polygon", "id polygon"},
		{"bowls", "id polygon required capacity", "id polygon required capacity"},
		{"switches", "id polygon mode", "id polygon mode"},
		{"gates", "id polygon initially_open mode switch_ids", "id polygon initially_open mode"},
		{"directions", "id polygon mode heading", "id polygon mode heading"},
	} {
		items, err := schemaArray(root[group.name], "level."+group.name, group.name != "bowls")
		if err != nil {
			return err
		}
		for index, item := range items {
			location := fmt.Sprintf("level.%s[%d]", group.name, index)
			object, err := schemaObject(item, location, group.allowed, group.required)
			if err != nil {
				return err
			}
			if err := schemaNumber(object["id"], location+".id"); err != nil {
				return err
			}
			if err := schemaPolygon(object["polygon"], location+".polygon"); err != nil {
				return err
			}
			switch group.name {
			case "tools":
				if err := schemaString(object["resource_key"], location+".resource_key"); err != nil {
					return err
				}
				if err := schemaBool(object["placed"], location+".placed"); err != nil {
					return err
				}
				for _, key := range []string{"x", "y"} {
					if err := schemaNumber(object[key], location+"."+key); err != nil {
						return err
					}
				}
			case "bowls":
				for _, key := range []string{"required", "capacity"} {
					if err := schemaNumber(object[key], location+"."+key); err != nil {
						return err
					}
				}
			case "switches", "directions", "gates":
				if err := schemaString(object["mode"], location+".mode"); err != nil {
					return err
				}
				if group.name == "directions" {
					if err := schemaNumber(object["heading"], location+".heading"); err != nil {
						return err
					}
				}
				if group.name == "gates" {
					if err := schemaBool(object["initially_open"], location+".initially_open"); err != nil {
						return err
					}
					identifiers, err := schemaArray(object["switch_ids"], location+".switch_ids", true)
					if err != nil {
						return err
					}
					for switchIndex, identifier := range identifiers {
						if err := schemaNumber(identifier, fmt.Sprintf("%s.switch_ids[%d]", location, switchIndex)); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func schemaObject(value any, location, allowed, required string) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", location)
	}
	allowedSet := make(map[string]bool)
	for _, key := range strings.Fields(allowed) {
		allowedSet[key] = true
	}
	for key := range object {
		if !allowedSet[key] {
			return nil, fmt.Errorf("%s.%s is unsupported", location, key)
		}
	}
	for _, key := range strings.Fields(required) {
		if _, exists := object[key]; !exists {
			return nil, fmt.Errorf("%s.%s is required", location, key)
		}
	}
	return object, nil
}

func schemaArray(value any, location string, optional bool) ([]any, error) {
	if optional && value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", location)
	}
	return items, nil
}

func schemaNumber(value any, location string) error {
	if _, ok := value.(json.Number); !ok {
		return fmt.Errorf("%s must be an integer", location)
	}
	return nil
}

func schemaString(value any, location string) error {
	if _, ok := value.(string); !ok {
		return fmt.Errorf("%s must be a string", location)
	}
	return nil
}

func schemaBool(value any, location string) error {
	if _, ok := value.(bool); !ok {
		return fmt.Errorf("%s must be a boolean", location)
	}
	return nil
}

func schemaPolygon(value any, location string) error {
	polygon, err := schemaObject(value, location, "outer holes", "outer")
	if err != nil {
		return err
	}
	for _, key := range []string{"outer", "holes"} {
		points, err := schemaArray(polygon[key], location+"."+key, key == "holes")
		if err != nil {
			return err
		}
		if key == "holes" {
			for holeIndex, hole := range points {
				ring, err := schemaArray(hole, fmt.Sprintf("%s.holes[%d]", location, holeIndex), false)
				if err != nil {
					return err
				}
				for pointIndex, point := range ring {
					if err := schemaPoint(point, fmt.Sprintf("%s.holes[%d][%d]", location, holeIndex, pointIndex)); err != nil {
						return err
					}
				}
			}
		} else {
			for pointIndex, point := range points {
				if err := schemaPoint(point, fmt.Sprintf("%s.outer[%d]", location, pointIndex)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func schemaPoint(value any, location string) error {
	point, err := schemaObject(value, location, "x y", "x y")
	if err != nil {
		return err
	}
	if err := schemaNumber(point["x"], location+".x"); err != nil {
		return err
	}
	return schemaNumber(point["y"], location+".y")
}
