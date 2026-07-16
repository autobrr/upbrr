// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package unit3d provides Unit3D API-based tracker upload implementations.
//
// # Overview
//
// This package implements the tracker.Definition interface for Unit3D-based
// trackers such as Aither, BLU, LST, LUME, and others.
//
// # Architecture
//
// Shared protocol behavior lives in this package. Site-owned profiles, rules,
// banned groups, and payload hooks live under sites/<tracker>.
//
// # Adding a New Unit3D Tracker
//
// To add support for a new Unit3D tracker (e.g., "EXAMPLE"):
//
// 1. Create sites/example/profile.go. Profile returns the complete site
// manifest, including rules and banned groups supplied by sibling files:
//
//	func Profile() unit3d.Profile {
//	    return unit3d.Profile{
//	        Name:         "EXAMPLE",
//	        BaseURL:      "https://example.invalid",
//	        Rules:        Rules(),
//	        BannedGroups: BannedGroups(),
//	    }
//	}
//
// Add only behavior that differs from shared Unit3D handling to Profile.Site.
//
// 2. Add one import and one Profile() entry in impl/registry.go.
//
// 3. Add the identity and default endpoint to the temporary compatibility
// catalog in trackers/unit3dmeta. Registry parity tests keep both lists aligned
// until all callers consume the composed registry directly.
//
// Shared upload, dry-run, dupe, description, and capability registration then
// apply automatically.
//
// # Testing
//
// Unit tests live in upload_test.go and cover:
//   - Category/type/resolution mapping
//   - Site profile callbacks
//   - Form payload construction
//   - Response parsing
//
// Run tests via:
//
//	go test ./internal/trackers/impl/unit3d/...
//
// # Python Reference
//
// The Python implementation lives in:
//   - src/trackers/UNIT3D.py (base class)
//   - src/trackers/AITHER.py (example tracker)
//
// When porting logic, maintain the same behavior but use idiomatic Go patterns.
package unit3d
