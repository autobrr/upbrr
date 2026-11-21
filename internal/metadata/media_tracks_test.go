// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/sam"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSAMFormatsCorrectedAudioWithoutOverridingExplicitNameChoices(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		language   string
		omitMarker *bool
		omitGroup  *bool
		marker     string
	}{
		{"Brazilian correction derives multi", "pt-BR", nil, nil, "MULTI"},
		{"withdrawing Brazilian audio removes the generic marker", "fr", nil, nil, ""},
		{"explicit omission is preserved", "pt-BR", new(true), nil, ""},
		{"clearing the release group keeps the computed marker", "pt-BR", nil, new(true), "MULTI"},
	} {
		t.Run(test.name, func(t *testing.T) {
			const source = "Example.2026.1080p.WEB-DL.H264-GRP.mkv"
			meta := preparationstate.State{
				SourcePath:  source,
				Type:        "WEBDL",
				Source:      "WEB",
				VideoEncode: "H.264",
				Tag:         "-GRP",
				Release: api.ReleaseInfo{
					Title:      "Example",
					Year:       2026,
					Resolution: "1080p",
				},
				ReleaseNameOverrides:  api.ReleaseNameOverrides{NoDual: test.omitMarker, NoTag: test.omitGroup},
				TrackCoverageComplete: true,
				Identity: api.ExternalIdentity{
					Category:   api.CanonicalCategoryMovie,
					TMDBID:     123,
					SourcePath: source,
					Generation: 1,
				},
				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: source,
					Generation: 1,
					TMDB: &api.TMDBMetadata{
						TMDBID:           123,
						Category:         "MOVIE",
						Title:            "Example",
						Year:             2026,
						OriginalLanguage: "ja",
						LocalizedTitles:  map[string]string{"en-US": "Example"},
					},
				},
			}
			for i, language := range []string{"ja", "en", "pt"} {
				meta.MediaTracks = append(meta.MediaTracks, api.MediaTrackFacts{
					ID:                  strconv.Itoa(i),
					Kind:                api.MediaTrackAudio,
					Role:                api.AudioRoleProgramme,
					ResourceID:          "movie",
					ManifestFingerprint: "manifest",
					Languages:           []string{language},
				})
			}
			if err := applyTrackLanguageOverrides(&meta, []api.TrackLanguageCorrection{{
				TrackID:             "2",
				ManifestFingerprint: "manifest",
				Languages:           []string{test.language},
			}}); err != nil {
				t.Fatal(err)
			}
			meta.AudioLanguages = aggregateTrackLanguages(meta.MediaTracks, api.MediaTrackAudio)
			meta.Audio = applyAudioLanguagePrefix("AAC 2.0", meta)
			applyReleaseNameValueOverrides(&meta)
			RebuildReleaseName(&meta, api.NopLogger{})
			subject := trackerLookupSubject(meta)
			facts := api.MediaFacts{
				OriginalLanguage:      "ja",
				TrackCoverageComplete: true,
				Tracks:                meta.MediaTracks,
			}
			subject.LanguageFacts = mediafacts.ResolveLanguages(facts)
			subject.ManualLanguages = facts.ManualLanguages()
			subject.MediaFileFacts = api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{{ResourceID: "movie", Primary: true}}}
			binding := unit3d.NewWithProfile(sam.Profile()).ReleaseNamePolicy()
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "SAM", Meta: subject}, binding)
			if failure != nil {
				t.Fatalf("prepare name: %v: %v", failure, failure.Unwrap())
			}
			name, err := prepared.ReviewedUploadName()
			if err != nil || strings.Contains(name, "Dual-Audio") || strings.Contains(name, " MULTI") != (test.marker == "MULTI") ||
				strings.HasSuffix(name, "-GRP") != (test.omitGroup == nil) ||
				prepared.Projection.DuplicateCriteria.Name != name {
				t.Fatalf("upload = %q, search = %q, err = %v, want marker %q", name, prepared.Projection.DuplicateCriteria.Name, err, test.marker)
			}
		})
	}
}

func TestSAMDuplicateEvidenceUsesMeasuredDTSProfiles(t *testing.T) {
	t.Parallel()
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"DTS","Format_AdditionalFeatures":"XLL","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","Language":"ja","StreamOrder":"1"}]}}`)
	tracks, _, _, _, err := mediaTrackFacts(preparationstate.State{SourcePath: "Example.mkv"}, doc)
	if err != nil || len(tracks) != 1 {
		t.Fatalf("measured tracks = %+v, err = %v", tracks, err)
	}
	policy := sam.Profile().DupePolicy
	target, _ := policy.TargetEvidence(api.UploadSubject{
		LanguageFacts:  mediafacts.ResolveLanguages(api.MediaFacts{TrackCoverageComplete: true, Tracks: tracks}),
		MediaFileFacts: api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{{ResourceID: tracks[0].ResourceID}}},
	})
	for _, test := range []struct {
		format string
		want   api.DupeRelation
	}{
		{"DTS XBR", api.DupeRelationCoexists},
		{"DTS\nCommercial name : DTS-HD High Resolution Audio", api.DupeRelationCoexists},
		{"DTS XLL", ""},
		{"DTS", ""},
	} {
		report := "General\nVideo\nFormat : AVC\nAudio\nLanguage : Japanese\nChannel(s) : 6 channels\nFormat : " + test.format
		decision := policy.CompareEvidence(api.DupeEvidenceContext{
			Target:             api.TrackerDuplicateTarget{Evidence: target},
			Candidate:          api.TrackerDuplicateTarget{Evidence: policy.CandidateEvidence(api.DupeEntry{MediaInfo: report})},
			Relation:           api.DupeRelationSameSlot,
			SameWorkAndContent: true,
		})
		if decision.Relation != test.want {
			t.Errorf("%q: decision = %+v, want relation %q", test.format, decision, test.want)
		}
	}
}

func TestMediaTrackFactsMeasuredStreamOrder(t *testing.T) {
	t.Parallel()
	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{
		{
			"@type":       "Audio",
			"StreamOrder": "3",
			"Language":    "eng",
			"Title":       "Dub",
		},
		{
			"@type":       "Text",
			"StreamOrder": "2",
			"Language":    "eng",
		},
		{
			"@type":       "Audio",
			"StreamOrder": "1",
			"Language":    "jpn",
			"Title":       "Main",
		},
	}
	tracks, _, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{3, 2, 1} {
		if !tracks[i].StreamOrderKnown || tracks[i].StreamOrder != want {
			t.Fatalf("track %d measured order = %+v, want %d", i, tracks[i], want)
		}
	}
	if tracks[0].Ordinal != 1 || tracks[1].Ordinal != 1 || tracks[2].Ordinal != 2 {
		t.Fatalf("per-kind identity ordinals changed: %+v", tracks)
	}
}

func TestMediaInfoLanguageFormatsRetainCodeAndNameIdentity(t *testing.T) {
	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{
		{"@type": "Audio", "Language": "ga"},
		{"@type": "Audio", "Language": "tgl"},
		{"@type": "Text", "Language_String": "Ga"},
		{"@type": "Text", "Language_String": "Philippine (Other)"},
	}
	tracks, _, audio, subs, err := mediaTrackFacts(preparationstate.State{}, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(audio, []string{"Irish", "Tagalog"}) || !slices.Equal(subs, []string{"Ga [gaa]", "Philippine languages"}) {
		t.Fatalf("track languages changed source identities: audio=%q subtitles=%q", audio, subs)
	}
	if !slices.Equal(tracks[2].DetectedLanguages, []string{"Ga [gaa]"}) {
		t.Fatalf("detected language=%q", tracks[2].DetectedLanguages)
	}
	fallbackAudio, fallbackSubs := extractMediaInfoLanguages(doc)
	if !slices.Equal(audio, fallbackAudio) || !slices.Equal(subs, fallbackSubs) {
		t.Fatalf("aggregate fallback disagrees with tracks: %q %q", fallbackAudio, fallbackSubs)
	}
}

func TestBDInfoTracksRetainUnresolvedAndNamedLanguages(t *testing.T) {
	meta := preparationstate.State{DiscType: "BDMV", Discs: []preparationstate.DiscResource{{ID: "disc", Reports: []preparationstate.DiscReportResource{{
		Playlist: api.PlaylistInfo{ID: "00001"},
		Summary:  "Audio: Custom Dialect / DTS / 5.1 / 48 kHz / 1000 kbps / 24-bit\nAudio: Ga / DTS / 5.1 / 48 kHz / 1000 kbps / 24-bit\nAudio: Kru / DTS / 5.1 / 48 kHz / 1000 kbps / 24-bit\nAudio: ga / DTS / 5.1 / 48 kHz / 1000 kbps / 24-bit",
	}}}}}
	tracks, err := bdInfoAudioTrackFacts(meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 4 {
		t.Fatalf("tracks=%d", len(tracks))
	}
	for index, want := range []string{"Custom Dialect", "Ga [gaa]", "Kru languages", "Irish"} {
		if !slices.Equal(tracks[index].Languages, []string{want}) || !slices.Equal(tracks[index].DetectedLanguages, []string{want}) {
			t.Errorf("track %d languages=%q detected=%q, want %q", index, tracks[index].Languages, tracks[index].DetectedLanguages, want)
		}
	}
}

func TestMediaTrackFactsStreamOrderEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		value any
		want  int
		known bool
	}{
		{
			name:  "zero",
			value: "0",
			known: true,
		},
		{
			name:  "integer",
			value: 3,
			want:  3,
			known: true,
		},
		{
			name:  "JSON number",
			value: float64(4),
			want:  4,
			known: true,
		},
		{
			name:  "number token",
			value: json.Number("5"),
			want:  5,
			known: true,
		},
		{
			name:  "trimmed",
			value: " 6 ",
			want:  6,
			known: true,
		},
		{name: "missing"},
		{name: "empty", value: ""},
		{name: "negative", value: "-1"},
		{name: "fraction", value: "1.5"},
		{name: "fractional number", value: float64(1.5)},
		{name: "multiple streams", value: "1 / 2"},
		{name: "compound stream", value: "0-1"},
		{name: "labelled value", value: "1 (0x1)"},
		{name: "overflow", value: "99999999999999999999999999999999"},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := mediaInfoDoc{}
			track := map[string]any{
				"@type":    "Audio",
				"ID":       "1",
				"UniqueID": "2",
			}
			if test.value != nil {
				track["StreamOrder"] = test.value
			}
			doc.Media.Track = []map[string]any{track}
			tracks, _, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
			if err != nil {
				t.Fatal(err)
			}
			if tracks[0].StreamOrderKnown != test.known || tracks[0].StreamOrder != test.want {
				t.Fatalf("measured order = %+v, want %d known=%t", tracks[0], test.want, test.known)
			}
		})
	}
}

func TestMediaTrackFactsDuplicateStreamOrderIsUnknown(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"Audio", "Text", "Video"} {
		t.Run(kind, func(t *testing.T) {
			doc := mediaInfoDoc{}
			doc.Media.Track = []map[string]any{
				{
					"@type":       "Audio",
					"StreamOrder": "1",
					"ID":          "2",
				},
				{
					"@type":       kind,
					"StreamOrder": "1",
					"ID":          "3",
				},
			}
			tracks, _, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
			if err != nil {
				t.Fatal(err)
			}
			for _, track := range tracks {
				if track.StreamOrderKnown {
					t.Fatalf("duplicate container order trusted: %+v", tracks)
				}
			}
		})
	}
}

func TestMediaTrackFactsExcludesCommentaryFromAudioAggregate(t *testing.T) {
	t.Parallel()

	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{
		{
			"@type":       "Audio",
			"StreamOrder": "1",
			"Language":    "eng",
		},
		{
			"@type":       "Audio",
			"StreamOrder": "2",
			"Language":    "jpn",
			"Title":       "Commentary",
		},
		{
			"@type":       "Text",
			"StreamOrder": "3",
			"Language":    "en, fra",
		},
	}
	tracks, primaryAudioTrackID, audio, subtitles, err := mediaTrackFacts(
		preparationstate.State{SourcePath: "Example.2026.mkv", VideoPath: "Example.2026.mkv"},
		doc,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 || tracks[0].ID == "" || tracks[0].ID == "Example.2026.mkv" {
		t.Fatalf("tracks = %#v", tracks)
	}
	if primaryAudioTrackID != tracks[0].ID {
		t.Fatalf("primary audio track ID = %q, want %q", primaryAudioTrackID, tracks[0].ID)
	}
	if !slices.Equal(audio, []string{"English"}) || !slices.Equal(subtitles, []string{"English", "French"}) {
		t.Fatalf("aggregate languages = %#v/%#v", audio, subtitles)
	}
}

func TestMediaTrackFactsPrimaryIdentityUsesExistingPolicyWithDuplicateNativeIDs(t *testing.T) {
	t.Parallel()

	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{
		{
			"@type":        "Audio",
			"StreamOrder":  "0",
			"ID":           "7",
			"Title":        "Director Commentary",
			"Format":       "AAC",
			"Channels":     "2",
			"SamplingRate": "48000",
		},
		{
			"@type":         "Audio",
			"StreamOrder":   "2",
			"ID":            "7",
			"Title":         "Main",
			"Format":        "FLAC",
			"ChannelLayout": "L R",
			"Channels":      "2",
			"SamplingRate":  "96000",
		},
		{
			"@type":        "Audio",
			"StreamOrder":  "1",
			"ID":           "7",
			"Title":        "Main Alternate",
			"Format":       "AC-3",
			"Channels":     "6",
			"SamplingRate": "48000",
		},
	}
	tracks, primaryID, _, _, err := mediaTrackFacts(
		preparationstate.State{SourcePath: "Example.2026.mkv", VideoPath: "Example.2026.mkv"},
		doc,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 || primaryID != tracks[2].ID || tracks[0].ID == tracks[1].ID || tracks[1].ID == tracks[2].ID {
		t.Fatalf("tracks=%#v primary=%q", tracks, primaryID)
	}
	if tracks[1].Title != "Main" || tracks[1].Codec != "FLAC" || tracks[1].ChannelLayout != "L R" ||
		tracks[1].Channels != 2 || tracks[1].SampleRate != 96_000 {
		t.Fatalf("audio facts = %#v", tracks[1])
	}
}

func TestMediaTrackFactsNormalizesDolbyCodecsForAnalysisBinding(t *testing.T) {
	t.Parallel()

	doc := mediaInfoDoc{Media: struct {
		Track []map[string]any `json:"track"`
	}{Track: []map[string]any{
		{
			"@type":       "Audio",
			"Format":      "AC-3",
			"StreamOrder": "1",
		},
		{
			"@type":       "Audio",
			"Format":      "E-AC-3",
			"StreamOrder": "2",
		},
	}}}
	tracks, _, _, _, err := mediaTrackFacts(
		preparationstate.State{SourcePath: "Example.2026.mkv", VideoPath: "Example.2026.mkv"},
		doc,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 || tracks[0].Codec != "DD" || tracks[1].Codec != "DD+" {
		t.Fatalf("Dolby track facts = %#v", tracks)
	}
}

func TestSelectPrimaryAudioTrackFallsBackToNumericIDThenSourceOrder(t *testing.T) {
	t.Parallel()

	byID := []map[string]any{
		{
			"@type": "Audio",
			"ID":    "9",
			"Title": "Compatibility",
		},
		{
			"@type": "Audio",
			"ID":    "3",
			"Title": "Commentary",
		},
	}
	if index := selectPrimaryAudioTrackIndex(byID); index != 1 {
		t.Fatalf("numeric ID primary index = %d, want 1", index)
	}
	bySourceOrder := []map[string]any{
		{"@type": "Audio", "Title": "Main A"},
		{"@type": "Audio", "Title": "Main B"},
	}
	if index := selectPrimaryAudioTrackIndex(bySourceOrder); index != 0 {
		t.Fatalf("source-order primary index = %d, want 0", index)
	}
}

func TestApplyTrackLanguageOverridesRequiresCurrentManifest(t *testing.T) {
	t.Parallel()

	meta := preparationstate.State{MediaTracks: []api.MediaTrackFacts{{
		ID:                  "track_1",
		Kind:                api.MediaTrackAudio,
		ManifestFingerprint: "manifest_1",
		Languages:           []string{"English"},
	}}}
	err := applyTrackLanguageOverrides(&meta, []api.TrackLanguageCorrection{{
		TrackID:             "track_1",
		ManifestFingerprint: "other",
		Languages:           []string{"fra"},
	}})
	if _, ok := errors.AsType[*api.CorrectionConflictError](err); !ok {
		t.Fatalf("manifest error = %v, want correction conflict", err)
	}
	if err := applyTrackLanguageOverrides(&meta, []api.TrackLanguageCorrection{{
		TrackID:             "track_1",
		ManifestFingerprint: "manifest_1",
		Languages:           []string{"fra"},
	}}); err != nil {
		t.Fatalf("apply override: %v", err)
	}
	if !slices.Equal(meta.MediaTracks[0].Languages, []string{"French"}) || meta.MediaTracks[0].LanguageProvenance != api.FactProvenanceManual {
		t.Fatalf("track = %#v", meta.MediaTracks[0])
	}
}

func TestTrackOrdinalBindingChangesWithOrderedScanEvidence(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{
		SourcePath:        "Example.mkv",
		VideoPath:         "Example.mkv",
		SourceFingerprint: "source-one",
	}
	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{{"@type": "Audio", "Language": "eng"}, {"@type": "Audio", "Language": "fra"}}
	first, _, _, _, err := mediaTrackFacts(meta, doc)
	if err != nil {
		t.Fatal(err)
	}
	doc.Media.Track[0], doc.Media.Track[1] = doc.Media.Track[1], doc.Media.Track[0]
	second, _, _, _, err := mediaTrackFacts(meta, doc)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].ID == second[0].ID || first[0].ManifestFingerprint == second[0].ManifestFingerprint {
		t.Fatal("reordered stream inherited an ordinal correction")
	}
	meta.MediaTracks = second
	if err := applyTrackLanguageOverrides(&meta, []api.TrackLanguageCorrection{{
		TrackID:             first[0].ID,
		ManifestFingerprint: first[0].ManifestFingerprint,
		Languages:           []string{"Spanish"},
	}}); err == nil {
		t.Fatal("old scan correction applied")
	}
}

func TestEmptyTrackCorrectionClearsDetectedAggregate(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{
		AudioLanguages: []string{"English"},
		MediaTracks: []api.MediaTrackFacts{{
			ID:                  "audio-one",
			Kind:                api.MediaTrackAudio,
			ManifestFingerprint: "scan",
			Languages:           []string{"English"},
		}},
		MetadataOverrides: api.MetadataOverrides{TrackLanguages: []api.TrackLanguageCorrection{{
			TrackID:             "audio-one",
			ManifestFingerprint: "scan",
			Languages:           []string{},
		}}},
	}
	if err := applyMetadataOverrides(&meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.AudioLanguages) != 0 {
		t.Fatal("empty track correction restored detected aggregate")
	}
}

func TestUntaggedMediaInfoTracksPreserveBDInfoLanguages(t *testing.T) {
	meta := preparationstate.State{
		DiscType:          "BDMV",
		AudioLanguages:    []string{"Japanese"},
		SubtitleLanguages: []string{"English"},
		MediaTracks: []api.MediaTrackFacts{
			{Kind: api.MediaTrackAudio, LanguageProvenance: api.FactProvenanceAutomatic},
			{Kind: api.MediaTrackSubtitle, LanguageProvenance: api.FactProvenanceAutomatic},
		},
	}
	if err := applyMetadataOverrides(&meta); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(meta.AudioLanguages, []string{"Japanese"}) || !slices.Equal(meta.SubtitleLanguages, []string{"English"}) {
		t.Fatalf("BDInfo fallback lost: audio=%v subtitles=%v", meta.AudioLanguages, meta.SubtitleLanguages)
	}
}

func TestOrdinaryAudioLanguageDoesNotRequireRecognizedTitle(t *testing.T) {
	var doc mediaInfoDoc
	doc.Media.Track = []map[string]any{
		{
			"@type":    "Audio",
			"ID":       "1",
			"Language": "ja",
			"Default":  "Yes",
		},
		{
			"@type":    "Audio",
			"ID":       "2",
			"Language": "en",
			"Title":    "Unclassified supplementary recording",
		},
		{
			"@type":    "Audio",
			"ID":       "3",
			"Language": "de",
			"Title":    "German Dub",
		},
	}
	tracks, _, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
	if err != nil {
		t.Fatal(err)
	}
	if tracks[0].Role != api.AudioRoleProgramme || tracks[1].Role != api.AudioRoleProgramme || tracks[2].Role != api.AudioRoleProgramme {
		t.Fatalf("track roles=%#v", tracks)
	}
}

func TestDefaultAudioNamingKeepsFinalizedLabelWithoutInspectedCodec(t *testing.T) {
	for _, channels := range []string{"", "2"} {
		t.Run(channels, func(t *testing.T) {
			doc := mediaInfoDoc{}
			doc.Media.Track = []map[string]any{{
				"@type":    "Audio",
				"Default":  "Yes",
				"Title":    "Main",
				"Language": "eng",
				"Channels": channels,
			}}
			facts, _, _, _, err := mediaTrackFacts(preparationstate.State{SourcePath: "source", VideoPath: "source"}, doc)
			if err != nil {
				t.Fatal(err)
			}
			if len(facts) != 1 || facts[0].Codec != "" || facts[0].AudioLabel == "" {
				t.Fatalf("unexpected producer evidence: %+v", facts)
			}
			name := BuildReleaseName(api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "WEBDL",
				Source:      "WEB",
				Resolution:  "1080p",
				VideoEncode: "H.265",
				Tag:         "-GRP",
				Title:       "Example",
				Year:        2026,
				Audio:       "AAC 2.0",
			}, api.NopLogger{})
			subject := api.UploadSubject{
				ReleaseName:   name.Name,
				GeneratedName: name.GeneratedName,
				LanguageFacts: api.LanguageFacts{Tracks: facts},
			}
			binding := trackers.StructuredReleaseNamePolicy("test/default-audio/v1", trackers.StructuredNamePolicy{Defaults: func(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
				return trackers.ApplyDefaultAudioName(editor, meta)
			}})
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "DP", Meta: subject}, binding)
			if failure != nil {
				t.Fatalf("missing default codec blocked finalized naming: %v", failure)
			}
			if got := prepared.Projection.UploadReleaseName; !strings.Contains(got, "AAC 2.0") {
				t.Fatalf("incomplete default evidence replaced finalized audio: %q", got)
			}
		})
	}
}
