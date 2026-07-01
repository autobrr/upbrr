// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// sceneTokenSplit breaks a release name into its dotted/spaced/underscored
// tokens for case-insensitive set comparison.
var sceneTokenSplit = regexp.MustCompile(`[.\s_\-]+`)

var sceneResolutionTokens = []string{"8640p", "4320p", "2160p", "1440p", "1080p", "1080i", "720p", "576p", "576i", "480p", "480i"}

// sceneEditionKeywords are the edition markers used to disambiguate between
// multiple releases of the same title (e.g. theatrical vs. extended).
var sceneEditionKeywords = []string{
	"extended", "unrated", "directors", "director", "remastered", "theatrical",
	"uncut", "imax", "ultimate", "redux", "despecialized", "criterion", "noir",
}

// sceneForeignKeywords flag a release as a foreign-language/dub variant so an
// English-only local release is not matched against one. "dl" is deliberately
// excluded: it is the WEB-DL protocol token, not a language marker, and would
// misclassify every WEB-DL release as foreign.
var sceneForeignKeywords = []string{
	"german", "french", "italian", "spanish", "dutch", "danish", "swedish",
	"norwegian", "finnish", "polish", "czech", "hungarian", "portuguese",
	"russian", "korean", "japanese", "chinese", "hindi", "nordic", "multi",
	"dual", "vff", "vfq", "truefrench", "subbed", "dubbed",
}

// bestSceneCandidate picks the scene release that best matches the parsed local
// metadata, or (nil, 0) when no candidate clears the structural eligibility bar.
// Eligibility — not just the highest score — is what guards against
// false-flagging a non-scene release.
func bestSceneCandidate(meta api.PreparedMetadata, localBase string, candidates []srrdbSearchResult) (*srrdbSearchResult, int) {
	wantRes := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	if wantRes == "" {
		wantRes = scanSceneResolution(localBase)
	}
	localForeign := releaseLooksForeign(meta, localBase)

	bestIdx := -1
	bestScore := 0
	for i := range candidates {
		score, eligible := scoreSceneCandidate(meta, wantRes, localForeign, candidates[i])
		if !eligible {
			continue
		}
		if bestIdx == -1 || score > bestScore {
			bestIdx = i
			bestScore = score
		}
	}
	if bestIdx == -1 {
		return nil, 0
	}
	return &candidates[bestIdx], bestScore
}

// scoreSceneCandidate scores one candidate against the parsed release tokens.
// Eligibility is false for structural conflicts such as resolution mismatch,
// known-group mismatch, foreign-language mismatch, or edition mismatch.
func scoreSceneCandidate(meta api.PreparedMetadata, wantRes string, localForeign bool, cand srrdbSearchResult) (int, bool) {
	tokens := sceneTokens(cand.Release)
	rel := meta.Release
	score := 0

	resOK := true
	resMatched := false
	if wantRes != "" {
		if _, ok := tokens[wantRes]; ok {
			score += 3
			resMatched = true
		} else {
			resOK = false
		}
	}

	yearOK := false
	if rel.Year > 0 {
		if _, ok := tokens[strconv.Itoa(rel.Year)]; ok {
			score += 3
			yearOK = true
		}
	}

	group := sceneGroup(meta)
	groupOK := false
	groupConflict := false
	if group != "" {
		if strings.EqualFold(sceneReleaseGroup(cand.Release), group) {
			score += 3
			groupOK = true
		} else {
			groupConflict = true
		}
	}

	score += scoreTokenField(rel.Source, tokens)
	for _, codec := range rel.Codec {
		score += scoreTokenField(codec, tokens)
	}

	editionScore, editionConflict := scoreEditions(rel.Edition, tokens)
	score += editionScore

	// A foreign-language/dub candidate is a different release from an
	// English/original-language local one; treat it as an incompatibility, not just
	// a score penalty, so it cannot be matched (and then flagged as renamed).
	foreignConflict := false
	if strings.EqualFold(strings.TrimSpace(cand.IsForeign), "yes") {
		if localForeign {
			score++
		} else {
			score -= 3
			foreignConflict = true
		}
	}

	// Require no resolution conflict plus at least two independent strong signals
	// among {resolution matched, year matched, group matched}. Two signals is the
	// false-positive guard: a single agreement (e.g. only the year, when the local
	// resolution is unknown) is too weak to confidently claim a scene match and
	// then flag a rename. A known local group must match; a different scene group
	// is a different release, regardless of title/year/resolution agreement.
	// Foreign/edition incompatibilities are hard failures for the same reason.
	strong := 0
	if resMatched {
		strong++
	}
	if yearOK {
		strong++
	}
	if groupOK {
		strong++
	}
	eligible := resOK && strong >= 2 && !groupConflict && !foreignConflict && !editionConflict
	return score, eligible
}

// scoreTokenField awards a point when any token of a parsed field (e.g. source
// "WEB-DL", codec "H.264") appears in the candidate.
func scoreTokenField(value string, tokens map[string]struct{}) int {
	for _, part := range sceneTokenSplit.Split(strings.ToLower(strings.TrimSpace(value)), -1) {
		if part == "" {
			continue
		}
		if _, ok := tokens[part]; ok {
			return 1
		}
	}
	return 0
}

// scoreEditions rewards matching edition markers and reports an incompatibility
// so an "Extended" candidate is not chosen for a theatrical local release (and
// vice versa) — the multi-edition disambiguation case. conflict is true when the
// candidate and local disagree on any edition marker.
func scoreEditions(localEditions []string, tokens map[string]struct{}) (score int, conflict bool) {
	local := make(map[string]struct{})
	for _, edition := range localEditions {
		for _, part := range sceneTokenSplit.Split(strings.ToLower(strings.TrimSpace(edition)), -1) {
			if part != "" {
				local[part] = struct{}{}
			}
		}
	}
	for _, kw := range sceneEditionKeywords {
		_, inCand := tokens[kw]
		_, inLocal := local[kw]
		switch {
		case inCand && inLocal:
			score += 2
		case inCand && !inLocal:
			score -= 2
			conflict = true
		case !inCand && inLocal:
			score--
			conflict = true
		}
	}
	return score, conflict
}

// releaseLooksForeign reports whether the local release is itself a
// foreign-language/dub variant, so that foreign candidates are preferred rather
// than penalised for it.
func releaseLooksForeign(meta api.PreparedMetadata, localBase string) bool {
	// Parsed language metadata is authoritative when present: a non-English entry
	// means foreign, and an English-only set means not foreign — the name-token
	// heuristic below must not override it (e.g. a "dual"/"multi" token on an
	// otherwise English release).
	knownLanguage := false
	for _, lang := range meta.Release.Language {
		normalized := strings.ToLower(strings.TrimSpace(lang))
		if normalized == "" {
			continue
		}
		knownLanguage = true
		if normalized != "english" && normalized != "en" {
			return true
		}
	}
	if knownLanguage {
		return false
	}

	// No language metadata: fall back to a best-effort scan of the on-disk name.
	tokens := sceneTokens(localBase)
	for _, kw := range sceneForeignKeywords {
		if _, ok := tokens[kw]; ok {
			return true
		}
	}
	return false
}

// sceneGroup resolves the parsed release group, falling back to the trailing tag
// like the tracker rules do.
func sceneGroup(meta api.PreparedMetadata) string {
	if group := strings.TrimSpace(meta.Release.Group); group != "" {
		return group
	}
	return strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-")
}

// sceneReleaseGroup returns the trailing scene group after the final dash.
// Releases without a dash have no group for scoring purposes.
func sceneReleaseGroup(release string) string {
	trimmed := strings.TrimSpace(release)
	if idx := strings.LastIndex(trimmed, "-"); idx >= 0 {
		return strings.TrimSpace(trimmed[idx+1:])
	}
	return ""
}

func sceneTokens(value string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, token := range sceneTokenSplit.Split(strings.ToLower(value), -1) {
		token = strings.TrimSpace(token)
		if token != "" {
			set[token] = struct{}{}
		}
	}
	return set
}

func scanSceneResolution(value string) string {
	lower := strings.ToLower(value)
	for _, candidate := range sceneResolutionTokens {
		if strings.Contains(lower, candidate) {
			return candidate
		}
	}
	return ""
}
