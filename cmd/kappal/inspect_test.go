package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestInspectSchemaCompleteness verifies that every JSON field in the inspect
// output has a corresponding entry in the _schema map. This catches cases
// where new fields are added to inspect structs but the schema is not updated.
func TestInspectSchemaCompleteness(t *testing.T) {
	// Collect all JSON field paths from the inspect types
	jsonPaths := collectJSONPaths(reflect.TypeOf(inspectResult{}), "")

	// Remove "_schema" itself — it's the schema, not a data field
	var dataPaths []string
	for _, p := range jsonPaths {
		if p != "_schema" {
			dataPaths = append(dataPaths, p)
		}
	}

	for _, path := range dataPaths {
		// Check for exact match or array-notation match (e.g. "services[]")
		if _, ok := inspectSchema[path]; ok {
			continue
		}
		// Try with [] notation for slices (e.g. "services[].name")
		arrayPath := path
		for {
			idx := strings.Index(arrayPath, ".")
			if idx == -1 {
				break
			}
			prefix := arrayPath[:idx]
			if _, ok := inspectSchema[prefix+"[]"]; ok {
				// Check child under array notation
				childPath := prefix + "[]" + arrayPath[idx:]
				if _, ok := inspectSchema[childPath]; ok {
					goto found
				}
			}
			arrayPath = arrayPath[idx+1:]
		}
		t.Errorf("inspect field %q has no _schema entry", path)
	found:
	}
}

// collectJSONPaths returns all leaf-level JSON field paths for a type.
// For struct fields that are slices of structs, it uses "field[]" prefix.
func collectJSONPaths(t reflect.Type, prefix string) []string {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	var paths []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		jsonName := strings.Split(jsonTag, ",")[0]

		fullPath := jsonName
		if prefix != "" {
			fullPath = prefix + "." + jsonName
		}

		ft := field.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		switch ft.Kind() {
		case reflect.Struct:
			if ft == reflect.TypeOf(json.RawMessage{}) {
				paths = append(paths, fullPath)
			} else {
				paths = append(paths, collectJSONPaths(ft, fullPath)...)
			}
		case reflect.Slice:
			elem := ft.Elem()
			if elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct {
				paths = append(paths, fullPath)
				paths = append(paths, collectJSONPaths(elem, fullPath+"[]")...)
			} else {
				paths = append(paths, fullPath)
			}
		case reflect.Map:
			paths = append(paths, fullPath)
		default:
			paths = append(paths, fullPath)
		}
	}
	return paths
}
