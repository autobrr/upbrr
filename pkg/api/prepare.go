// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// PreparationIntent describes why preparation was requested. It may influence
// reuse/progress decisions but never the canonical fact set.
type PreparationIntent string

const (
	// PreparationIntentPreview prepares a release for metadata preview.
	PreparationIntentPreview PreparationIntent = "preview"
	// PreparationIntentDuplicateCheck prepares a release for duplicate checking.
	PreparationIntentDuplicateCheck PreparationIntent = "duplicate_check"
	// PreparationIntentMedia prepares a release for media work.
	PreparationIntentMedia PreparationIntent = "media"
	// PreparationIntentDescription prepares a release for description work.
	PreparationIntentDescription PreparationIntent = "description"
	// PreparationIntentDryRun prepares a release for tracker dry run.
	PreparationIntentDryRun PreparationIntent = "dry_run"
	// PreparationIntentUpload prepares a release for upload review or execution.
	PreparationIntentUpload PreparationIntent = "upload"
)

// PrepareInput requests one canonical prepared-release generation.
type PrepareInput struct {
	SourcePath   string
	Intent       PreparationIntent
	Instructions ReleaseFactInstructions
	Policy       PreparationPolicy
	// Search contains fact-producing client search choices included in compatibility.
	Search ClientSearchPolicy
	// Controls contains one-shot permissions excluded from compatibility.
	Controls PreparationControls
	Force    bool
}

// ReleaseFactInstructions contains caller intent that can change finalized
// reusable release facts.
type ReleaseFactInstructions struct {
	Identity        ExternalIDOverrides
	Category        *CanonicalCategory
	ReleaseName     ReleaseNameOverrides
	Metadata        MetadataOverrides
	SourceLookup    string
	BlurayReleaseID string
	Playlist        PlaylistInstruction
	// TrackerIDs maps normalized tracker names to caller-supplied source IDs.
	TrackerIDs map[string]string
}

// PlaylistInstruction preserves tri-state playlist selection: Set=false means
// no instruction, while Set=true with an empty selection is an explicit clear.
type PlaylistInstruction struct {
	Set      bool
	Selected []string
	UseAll   bool
}

// PreparationPolicy contains non-secret preparation behavior that can affect
// canonical fact derivation and therefore participates in compatibility.
type PreparationPolicy struct {
	KeepFolder bool
	// KeepImages preserves tracker-sourced images while collecting metadata evidence.
	KeepImages bool
	OnlyID     bool
}

// ClientSearchPolicy contains fact-producing torrent-client search choices.
// These values participate in preparation compatibility.
type ClientSearchPolicy struct {
	// Skip disables torrent-client discovery for this preparation.
	Skip bool
	// Client optionally selects one configured client; nil uses normal client selection.
	Client *string
}

// PreparationControls contains one-shot permissions and presentation choices.
// These values never participate in preparation compatibility.
type PreparationControls struct {
	// Interaction determines whether preparation may request manual input.
	Interaction InteractionMode
	// ConfirmBDMVRescan permits replacing a partial cached Blu-ray analysis.
	ConfirmBDMVRescan bool
	// ForceRecheck forwards an explicit torrent-client hash recheck choice.
	ForceRecheck *bool
}
