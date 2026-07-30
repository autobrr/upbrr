// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import "reflect"

func capabilityIsNil(capability any) bool {
	if capability == nil {
		return true
	}
	value := reflect.ValueOf(capability)
	kind := value.Kind()
	isNilable := kind == reflect.Chan ||
		kind == reflect.Func ||
		kind == reflect.Interface ||
		kind == reflect.Map ||
		kind == reflect.Pointer ||
		kind == reflect.Slice
	return isNilable && value.IsNil()
}
