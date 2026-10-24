// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"reflect"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestScreenshotCountOriginSchema(t *testing.T) {
	t.Parallel()
	builder := newSchemaBuilder()
	for _, test := range []struct {
		typeOf reflect.Type
		field  string
	}{
		{reflect.TypeFor[api.ReleaseWorkflowUploadScreenshots](), "countIsDefault"},
		{reflect.TypeFor[api.TrackerProjectionInstructions](), "screenshotCountIsDefault"},
	} {
		builder.ensureNamed(test.typeOf)
		definition := builder.schemas[test.typeOf.Name()]
		property := definition.Properties[test.field]
		if property == nil || property.Type != "boolean" || slices.Contains(definition.Required, test.field) {
			t.Fatalf("%s.%s must be an optional boolean: %#v", test.typeOf.Name(), test.field, property)
		}
	}
}
