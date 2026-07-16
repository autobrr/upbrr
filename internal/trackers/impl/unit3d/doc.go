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
// # Adding a Standard Unit3D Tracker
//
// To add support for a new Unit3D tracker (e.g., "EXAMPLE"):
//
// 1. Create sites/example/profile.go. Profile owns the tracker identity,
// endpoint, and composed site policies:
//
//	func Profile() unit3d.Profile {
//	    return unit3d.Profile{
//	        Name:    "EXAMPLE",
//	        BaseURL: "https://example.invalid",
//	        Rules:   Rules(),
//	    }
//	}
//
// 2. Create sites/example/rules.go for release eligibility and typed policies
// such as language and audio handling. Optional site-owned behavior stays beside
// these files, for example banned_groups.go, auth.go, or custom payload hooks.
//
// 3. Add one import and one Profile() entry to unit3DDefinitions in
// internal/trackers/impl/registry.go.
//
// 4. Add one credential/config stanza to internal/config/defaults/example.yaml.
// Do not add a tracker URL; Profile.BaseURL is authoritative.
//
// 5. Add cross-tracker rule cases to internal/trackers/rules_test.go. Add
// focused site tests only for behavior that differs from shared Unit3D handling.
//
// No generic metadata, auth, image-hosting, torrent-client, or frontend
// tracker-name edit is required. Unsupported saved entries remain inert; the
// runtime does not infer or register custom Unit3D trackers.
//
// # Testing
//
// Shared unit tests live in upload_test.go and cover:
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
