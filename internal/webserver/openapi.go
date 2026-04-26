// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

type openAPIDocument map[string]any

func buildOpenAPIDocument(routes []apiV1Route) (openAPIDocument, error) {
	components := map[string]any{}
	paths := map[string]any{}

	for _, route := range routes {
		item, _ := paths[route.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[route.Path] = item
		}
		op := map[string]any{
			"operationId": route.OperationID,
			"summary":     route.Summary,
			"tags":        []string{operationTag(route.Path)},
			"responses": map[string]any{
				"200": map[string]any{
					"description": "OK",
					"content":     responseContent(route.Response, components),
				},
				"400": errorResponse(),
				"401": errorResponse(),
				"403": errorResponse(),
				"404": errorResponse(),
				"429": errorResponse(),
				"500": errorResponse(),
			},
		}
		if !route.Public {
			op["security"] = []map[string][]string{{"bearerAuth": []string{}}}
		}
		if params := pathParameters(route.Path); len(params) > 0 {
			op["parameters"] = params
		}
		if route.Request != nil {
			op["requestBody"] = map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": schemaFor(reflect.TypeOf(route.Request), components),
					},
				},
			}
		}
		if route.Path == "/api/v1/events" {
			op["responses"] = map[string]any{
				"200": map[string]any{
					"description": "Server-sent event stream",
					"content": map[string]any{
						"text/event-stream": map[string]any{
							"schema": map[string]any{"type": "string"},
						},
					},
				},
				"401": errorResponse(),
			}
		}
		item[strings.ToLower(route.Method)] = op
	}

	_ = schemaFor(reflect.TypeOf(apiErrorResponse{}), components)

	return openAPIDocument{
		"openapi": "3.1.1",
		"info": map[string]any{
			"title":       "upbrr Public API",
			"version":     "v1",
			"description": "Public REST API for upbrr automation and embedded web workflows.",
		},
		"servers": []map[string]string{{"url": "/"}},
		"paths":   paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":   "http",
					"scheme": "bearer",
				},
			},
			"schemas": components,
		},
	}, nil
}

func responseContent(response any, components map[string]any) map[string]any {
	if response == nil {
		return map[string]any{}
	}
	return map[string]any{
		"application/json": map[string]any{
			"schema": schemaFor(reflect.TypeOf(response), components),
		},
	}
}

func errorResponse() map[string]any {
	return map[string]any{
		"description": "Error",
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/webserver_apiErrorResponse"},
			},
		},
	}
}

func operationTag(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 {
		return "api"
	}
	return parts[2]
}

func pathParameters(path string) []map[string]any {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	params := make([]map[string]any, 0)
	for _, part := range parts {
		if !strings.HasPrefix(part, "{") || !strings.HasSuffix(part, "}") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
		params = append(params, map[string]any{
			"name":     name,
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		})
	}
	return params
}

func schemaFor(t reflect.Type, components map[string]any) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if t.Kind() == reflect.Struct && t.Name() != "" {
		name := schemaName(t)
		if _, exists := components[name]; !exists {
			components[name] = map[string]any{}
			components[name] = structSchema(t, components)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer", "minimum": 0}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem(), components)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem(), components)}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Struct:
		return structSchema(t, components)
	case reflect.Invalid, reflect.Uintptr, reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func, reflect.Ptr, reflect.UnsafePointer:
		return map[string]any{}
	}
	return map[string]any{}
}

func structSchema(t reflect.Type, components map[string]any) map[string]any {
	props := map[string]any{}
	required := make([]string, 0)
	for idx := 0; idx < t.NumField(); idx++ {
		field := t.Field(idx)
		if field.PkgPath != "" {
			continue
		}
		name, omitEmpty := jsonFieldName(field)
		if name == "-" {
			continue
		}
		props[name] = schemaFor(field.Type, components)
		if !omitEmpty && field.Type.Kind() != reflect.Pointer {
			required = append(required, name)
		}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "" {
		parts[0] = field.Name
	}
	omitEmpty := false
	for _, part := range parts[1:] {
		if part == "omitempty" {
			omitEmpty = true
		}
	}
	return parts[0], omitEmpty
}

func schemaName(t reflect.Type) string {
	if t.PkgPath() == "" {
		return t.Name()
	}
	parts := strings.Split(t.PkgPath(), "/")
	pkg := parts[len(parts)-1]
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, fmt.Sprintf("%s_%s", pkg, t.Name()))
}
