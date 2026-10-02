// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"encoding/json"
)

// MetadataEvidenceOutcome distinguishes reusable results from unsuccessful
// queries. Empty means the query completed successfully without a match.
type MetadataEvidenceOutcome string

const (
	MetadataEvidenceSuccess MetadataEvidenceOutcome = "success"
	MetadataEvidenceEmpty   MetadataEvidenceOutcome = "empty"
	MetadataEvidenceFailed  MetadataEvidenceOutcome = "failed"
)

// MetadataEvidence retains one typed lookup result, independently of prepared
// generations and user corrections. Key is a caller-generated hash of the
// query's dependencies; raw query inputs, credentials, and errors are not stored.
// Success and empty outcomes carry valid JSON, including null or empty results,
// capped at 16 MiB. Failed outcomes may retain valid typed partial JSON under
// the same bound, or omit the payload when no usable facts were returned.
// Cancellation is not authoritative and must never produce a record.
type MetadataEvidence struct {
	SourcePath        string
	SourceFingerprint string
	Domain            string
	Key               string
	Outcome           MetadataEvidenceOutcome
	Payload           json.RawMessage
}

// MetadataEvidenceRepository retains source-scoped lookup evidence across
// preparations. Callers own canonical source paths, dependency keys, and outcome
// classification; removing a source from History also removes its evidence.
type MetadataEvidenceRepository interface {
	LoadMetadataEvidence(context.Context, string, string, string, string) (MetadataEvidence, error)
	SaveMetadataEvidence(context.Context, MetadataEvidence) error
}
