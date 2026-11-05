// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// TVDBRemoteLinks records series-level links. An unset Status is legacy evidence,
// not proof that links are absent. Only complete/partial positive links may be used.
type TVDBRemoteLinks struct {
	Status MetadataEvidenceStatus
	Links  []TVDBRemoteLink
}

// TVDBRemoteLink preserves recognized positive IDs and unusable evidence separately.
// State is valid, malformed, unsupported or conflicting; unusable IDs are zero.
type TVDBRemoteLink struct {
	Provider IdentityProvider
	ID       int
	State    string
}
