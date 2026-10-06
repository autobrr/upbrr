// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/pkg/api"
)

// LanguageOutcome records requirement level independently of presentation.
type LanguageOutcome string

const (
	LanguageProhibited     LanguageOutcome = "Prohibited"
	LanguageStaffException LanguageOutcome = "Staff approval required"
	LanguageTrumpable      LanguageOutcome = "Trumpable release"
	LanguageUnresolved     LanguageOutcome = "Unresolved"
	LanguageAdvisory       LanguageOutcome = "Guidance"
)

// LanguagePolicy contains common predicates only. Sites own conditional rules
// and choose outcomes before invoking these shared checks.
type LanguagePolicy struct {
	OriginalAudio   LanguageOutcome
	OriginalPrimary bool
	ExtraDubs       LanguageOutcome
	// EnglishSubtitles is foreign, foreign_without_dub, without_english, or spoken.
	EnglishSubtitles             string
	MissingSubtitles             LanguageOutcome
	OriginalFirst                LanguageOutcome
	OriginalDefault              LanguageOutcome
	SubtitleDefault              LanguageOutcome
	DisallowedRoles              []api.AudioTrackRole
	CompatibilityRequired        bool
	CompatibilityCodecs          []string
	EmbeddedCompatibilityAllowed bool
	CompatibilityOnlyTrueHD      bool
}

// IsFullDiscUpload distinguishes complete disc uploads from remuxes, including
// canonical DISC subjects whose source-specific disc label is unavailable.
func IsFullDiscUpload(discType, releaseType string) bool {
	if strings.EqualFold(strings.TrimSpace(releaseType), "REMUX") {
		return false
	}
	return IsDiscType(discType) || strings.EqualFold(strings.TrimSpace(releaseType), "DISC")
}

// WithLanguageAssessment composes a site's pure language assessment with its
// existing constructibility checks without another upload-authority path.
func WithLanguageAssessment(binding ValidationPolicyBinding, assess func(api.TrackerValidationSubject) []api.RuleFailure) ValidationPolicyBinding {
	return ValidationPolicyBinding{
		ID: binding.ID + "/languages-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, logger api.Logger) ([]api.RuleFailure, error) {
			failures, err := binding.Check(ctx, subject, logger)
			if err != nil {
				return nil, fmt.Errorf("evaluate base tracker rules: %w", err)
			}
			return append(failures, assess(subject)...), nil
		},
	}
}

// WithLanguagePolicy composes an unconditional tracker-owned policy.
func WithLanguagePolicy(binding ValidationPolicyBinding, policy LanguagePolicy) ValidationPolicyBinding {
	return WithLanguageAssessment(binding, func(subject api.TrackerValidationSubject) []api.RuleFailure {
		return EvaluateLanguagePolicy(subject, policy)
	})
}

// EvaluateLanguagePolicy consumes exact-generation facts and requires only
// evidence used by an enabled predicate. Full discs exit before assessment.
func EvaluateLanguagePolicy(subject api.TrackerValidationSubject, policy LanguagePolicy) []api.RuleFailure {
	if IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	facts := subject.LanguageFacts
	var failures []api.RuleFailure
	add := func(key, reason string, outcome LanguageOutcome) {
		if outcome != "" {
			failures = append(failures, LanguageRuleFailure(subject, key, reason, outcome))
		}
	}
	needsOriginal := policy.OriginalAudio != "" || policy.ExtraDubs != "" || policy.OriginalFirst != "" || policy.OriginalDefault != "" ||
		policy.EnglishSubtitles == "foreign" || policy.EnglishSubtitles == "foreign_without_dub" || policy.EnglishSubtitles == "spoken"
	originalKnown := len(facts.OriginalLanguages) > 0 && !slices.ContainsFunc(facts.OriginalLanguages, func(value string) bool {
		code := languageutil.NormalizeLanguageCode(value)
		return code == "" || code == "und" || code == "mul"
	})
	if needsOriginal && !originalKnown {
		add("original_evidence", "original language evidence needs review", LanguageUnresolved)
	}
	hasEnglish := facts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory && slices.Contains(facts.ProgrammeLanguages, "English")
	englishSubs := slices.Contains(facts.SubtitleLanguages, "English")
	needsProgramme := policy.OriginalAudio != "" || policy.ExtraDubs != "" || policy.OriginalFirst != "" || policy.OriginalDefault != "" ||
		(policy.EnglishSubtitles == "foreign_without_dub" || policy.EnglishSubtitles == "without_english") && !englishSubs && !hasEnglish
	if needsProgramme && facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete {
		add("evidence", "programme language, track role or inspected coverage needs review", LanguageUnresolved)
	}
	if originalKnown && facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete && !facts.HasOriginalAudio() {
		add("original", "missing mandatory original-language programme audio", policy.OriginalAudio)
	}
	if originalKnown && policy.OriginalPrimary {
		primary := slices.IndexFunc(facts.Tracks, func(track api.MediaTrackFacts) bool { return track.ID != "" && track.ID == facts.PrimaryAudioTrackID })
		if primary < 0 || (facts.Tracks[primary].Role != api.AudioRoleProgramme && facts.Tracks[primary].Role != api.AudioRoleAlternateMix) {
			add("primary_evidence", "primary programme track identity is unresolved", LanguageUnresolved)
		} else if !slices.ContainsFunc(facts.Tracks[primary].Languages, func(language string) bool { return slices.Contains(facts.OriginalLanguages, language) }) {
			add("original_primary", "primary programme audio must be in the original language", policy.OriginalAudio)
		}
	}
	if originalKnown {
		for _, language := range facts.ProgrammeLanguages {
			if language == "English" || slices.Contains(facts.OriginalLanguages, language) || languageutil.NormalizeLanguageCode(language) == "" {
				continue
			}
			add("extra_dub", "non-original, non-English programme dub: "+language, policy.ExtraDubs)
		}
	}
	foreign := originalKnown && !slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.OriginalLanguages, "ZXX")
	needsSubs := policy.EnglishSubtitles == "foreign" && foreign || policy.EnglishSubtitles == "foreign_without_dub" && foreign && !hasEnglish ||
		policy.EnglishSubtitles == "without_english" &&
			!hasEnglish || policy.EnglishSubtitles == "spoken" && originalKnown && !slices.Contains(facts.OriginalLanguages, "ZXX")
	if needsSubs && !englishSubs {
		outcome := policy.MissingSubtitles
		if facts.SubtitleStatus != api.MetadataEvidenceStatusComplete ||
			((policy.EnglishSubtitles == "foreign_without_dub" || policy.EnglishSubtitles == "without_english") && facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete) {
			outcome = LanguageUnresolved
		}
		add("subtitles", "missing English subtitles", outcome)
	}
	first := true
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if slices.Contains(policy.DisallowedRoles, track.Role) {
			add("track_role", "disallowed "+string(track.Role)+" track "+track.ID, LanguageProhibited)
		}
		if strings.Contains(strings.ToLower(track.Codec), "truehd") {
			if policy.CompatibilityRequired && (!policy.EmbeddedCompatibilityAllowed || !track.EmbeddedCompatibility) {
				matching := slices.ContainsFunc(facts.Tracks, func(candidate api.MediaTrackFacts) bool {
					return candidate.Kind == api.MediaTrackAudio && candidate.Role == api.AudioRoleCompatibility &&
						slices.ContainsFunc(candidate.Languages, func(language string) bool { return slices.Contains(track.Languages, language) })
				})
				sameLanguageMixes := 0
				for _, other := range facts.Tracks {
					if other.Kind == api.MediaTrackAudio && strings.Contains(strings.ToLower(other.Codec), "truehd") &&
						slices.ContainsFunc(other.Languages, func(language string) bool { return slices.Contains(track.Languages, language) }) {
						sameLanguageMixes++
					}
				}
				if sameLanguageMixes > 1 {
					add("compatibility_mix", "individual TrueHD mix-to-compatibility association is unresolved", LanguageUnresolved)
				}
				if !matching {
					add("compatibility_missing", "per-mix TrueHD compatibility audio is not established for track "+track.ID, LanguageUnresolved)
				}
			}
		}
		if track.Role == api.AudioRoleCompatibility {
			if policy.CompatibilityOnlyTrueHD {
				matches := 0
				for _, main := range facts.Tracks {
					if main.Kind == api.MediaTrackAudio && strings.Contains(strings.ToLower(main.Codec), "truehd") &&
						slices.ContainsFunc(main.Languages, func(language string) bool { return slices.Contains(track.Languages, language) }) {
						matches++
					}
				}
				if matches == 0 {
					add("compatibility_mix", "compatibility track has no matching-language TrueHD mix", LanguageProhibited)
				} else if matches > 1 {
					add("compatibility_mix", "compatibility track association with its TrueHD mix is unresolved", LanguageUnresolved)
				}
			}
			if len(policy.CompatibilityCodecs) > 0 &&
				!slices.ContainsFunc(policy.CompatibilityCodecs, func(codec string) bool { return strings.EqualFold(codec, track.Codec) }) {
				add("compatibility_format", "unsupported compatibility audio format", LanguageProhibited)
			}
		}
		if track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix {
			continue
		}
		original := slices.ContainsFunc(track.Languages, func(language string) bool { return slices.Contains(facts.OriginalLanguages, language) })
		if first {
			if !original {
				add("original_order", "original programme audio should come first", policy.OriginalFirst)
			}
			first = false
		}
	}
	if policy.OriginalDefault != "" && !slices.ContainsFunc(facts.Tracks, func(track api.MediaTrackFacts) bool {
		return track.Kind == api.MediaTrackAudio && track.Default && (track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix) &&
			slices.ContainsFunc(track.Languages, func(language string) bool { return slices.Contains(facts.OriginalLanguages, language) })
	}) {
		add("original_default", "an original programme track should be default", policy.OriginalDefault)
	}
	if foreign && englishSubs && policy.SubtitleDefault != "" && !slices.ContainsFunc(facts.Tracks, func(track api.MediaTrackFacts) bool {
		return track.Kind == api.MediaTrackSubtitle && track.Default && slices.Contains(subtitleLanguages(track.Languages), "English")
	}) {
		add("subtitle_default", "an English subtitle track should be default for foreign content", policy.SubtitleDefault)
	}
	return failures
}

func subtitleLanguages(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		language, _ := languageutil.SubtitleLanguageParts(value)
		result = append(result, language)
	}
	return result
}

func languageDetail(values []string) string {
	if len(values) == 0 {
		return "unresolved"
	}
	return strings.Join(values, ", ")
}

// LanguageRuleFailure includes release context and preserves the explicit
// non-submitting debug bypass without making a strict live-upload rule waivable.
func LanguageRuleFailure(subject api.TrackerValidationSubject, key, reason string, outcome LanguageOutcome) api.RuleFailure {
	disposition := api.RuleDispositionStrict
	status := api.MetadataEvidenceStatusComplete
	switch outcome {
	case LanguageAdvisory:
		disposition = api.RuleDispositionAdvisory
	case LanguageTrumpable:
		disposition = api.RuleDispositionWaivable
	case LanguageUnresolved:
		status = api.MetadataEvidenceStatusPartial
	case LanguageProhibited, LanguageStaffException:
	}
	if outcome == LanguageTrumpable {
		reason += ". A compliant replacement may supersede this upload"
	}
	failure := NewEvidenceRuleFailure(
		"language_"+key,
		fmt.Sprintf(
			"%s — %s (%s). Original: %s; programme audio: %s.",
			outcome,
			reason,
			subject.Tracker,
			languageDetail(subject.LanguageFacts.OriginalLanguages),
			languageDetail(subject.LanguageFacts.ProgrammeLanguages),
		),
		disposition,
		status,
	)
	failure.DebugBypass = true
	return failure
}

// LanguageQuestionKey binds a language attestation to its tracker and exact
// prepared evidence, so retained answers cannot authorize changed tracks.
func LanguageQuestionKey(subject api.TrackerValidationSubject, key string) string {
	evidence := fmt.Sprintf(
		"%q|%q|%d|%#v|%t|%t",
		subject.Tracker,
		subject.SourcePath,
		subject.Identity.Generation,
		subject.LanguageFacts,
		subject.PersonalRelease,
		subject.Anime,
	)
	return fmt.Sprintf("%s_%x", key, sha256.Sum256([]byte(evidence)))
}
