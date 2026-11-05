// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

// candidateKeyboardPresenter drives the same list navigation as terminal users,
// including choices beyond the visible page, rather than setting the list index.
type candidateKeyboardPresenter struct {
	modelPresenter
	t *testing.T
}

func (p *candidateKeyboardPresenter) Ask(ctx context.Context, prompt cliPrompt) (cliAnswer, error) {
	p.prompts = append(p.prompts, prompt)
	reply := make(chan cliAnswer, 1)
	p.model.Update(cliPromptRequest{
		prompt: prompt,
		reply:  reply,
		done:   ctx.Done(),
	})
	value := p.answers[0]
	p.answers = p.answers[1:]
	switch prompt.Kind {
	case cliPromptConfirm:
		key := tea.KeyRight
		if value == "n" {
			key = tea.KeyLeft
		}
		p.model.Update(modelKey(key))
	case cliPromptText:
		p.model.field.SetValue(value)
	case cliPromptSelect:
		found := false
		for index, option := range prompt.Options {
			if option.Value != value {
				continue
			}
			for range index {
				p.model.Update(modelKey(tea.KeyDown))
			}
			found = true
			break
		}
		if !found {
			p.t.Fatalf("missing selection %q in %+v", value, prompt.Options)
		}
	case cliPromptMultiSelect, cliPromptSecret:
		return cliAnswer{}, errors.New("unexpected candidate prompt kind")
	default:
		p.t.Fatalf("unexpected prompt kind %d", prompt.Kind)
	}
	p.model.Update(modelKey(tea.KeyEnter))
	select {
	case answer := <-reply:
		return answer, nil
	default:
		return cliAnswer{}, errors.New("keyboard did not select a match")
	}
}

func TestCLIProviderSelectionPreparesBeforeContinuation(t *testing.T) {
	for _, category := range []api.CanonicalCategory{api.CanonicalCategoryMovie, api.CanonicalCategoryTV} {
		providers := []api.IdentityProvider{api.IdentityProviderTMDB, api.IdentityProviderIMDB}
		if category == api.CanonicalCategoryTV {
			providers = append(providers, api.IdentityProviderTVDB)
		}
		for _, provider := range providers {
			for _, terminal := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/tui=%t", category, provider, terminal), func(t *testing.T) {
					args := []string{"--uac", "--tmdb=11", "--imdb=tt2222222", "--mal=0", "example.mkv"}
					if category == api.CanonicalCategoryTV && provider != api.IdentityProviderTMDB {
						args[1] = "--tmdb=movie/11"
						if terminal {
							args[1] = "--tmdb=https://www.themoviedb.org/movie/11"
						}
					}
					opts, visited, _, err := parseCLIOptions(args)
					if err != nil {
						t.Fatal(err)
					}
					current := requiredFactsCLIResult()
					current.Release.Release.Identity = api.ExternalIdentity{
						TMDBID:   11,
						IMDBID:   2222222,
						Category: category,
					}
					current.Release.Release.Naming.Title = "Example Title"
					coreSvc := &cliWorkflowCoreFake{current: current}
					searchCalls := 0
					selected := false
					coreSvc.searchFn = func(_ context.Context, request api.ProviderSearchRequest) (api.ProviderSearchResult, error) {
						searchCalls++
						if request.Query != "Example Title 2026" || request.Category != category {
							t.Fatalf("search request=%+v", request)
						}
						if coreSvc.current.Release.Release.Identity.TMDBID != 11 || coreSvc.current.Release.Release.Identity.IMDBID != 2222222 {
							t.Fatal("inspection changed identity")
						}
						result := api.ProviderSearchResult{Query: request.Query, Category: category}
						group := api.ProviderCandidateResult{
							Provider: provider,
							Status:   api.MetadataEvidenceStatusPartial,
							Query:    "Example Title",
							Year:     2026,
						}
						for i := range 24 {
							group.Candidates = append(group.Candidates, api.ExternalIdentityCandidate{
								Provider: provider,
								ID:       100 + i,
								Title:    "Example Title",
								Year:     2000 + i,
								Category: category,
							})
						}
						// The final entry has no enrichment at all. It must remain selectable.
						group.Candidates[23].Year = 0
						result.Providers = append(result.Providers, group)
						return result, nil
					}
					coreSvc.continueFn = func(request api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) {
						if input := request.Intent.Preparation; input != nil {
							ids := input.Instructions.Identity
							if ids.MALID == nil || *ids.MALID != 0 {
								t.Fatal("selection lost an explicit sibling clear")
							}
							changed := ids.TMDBID != nil && *ids.TMDBID == 123 || ids.IMDBID != nil && *ids.IMDBID == 123 || ids.TVDBID != nil && *ids.TVDBID == 123
							if changed {
								if value := input.Instructions.ReleaseName.Category; value == nil || !strings.EqualFold(*value, string(category)) {
									t.Fatalf("selection lost its category to a retained provider hint: %+v", input.Instructions.ReleaseName)
								}
								if searchCalls != 1 {
									t.Fatal("provider selection did not follow display")
								}
								if provider != api.IdentityProviderTMDB && (ids.TMDBID == nil || *ids.TMDBID != 11) || provider != api.IdentityProviderIMDB && (ids.IMDBID == nil || *ids.IMDBID != 2222222) {
									t.Fatal("selection replaced another manual provider")
								}
								selected = true
								identity := &coreSvc.current.Release.Release.Identity
								switch provider {
								case api.IdentityProviderTMDB:
									identity.TMDBID = 123
								case api.IdentityProviderIMDB:
									identity.IMDBID = 123
								case api.IdentityProviderTVDB:
									identity.TVDBID = 123
								case api.IdentityProviderTVmaze, api.IdentityProviderMAL:
									t.Fatal("unexpected candidate provider")
								}
								coreSvc.current.Release.Release.Generation = 2
								coreSvc.current.Release.Release.Naming.ReleaseName = "Selected.Example.2026.720p-GRP"
								coreSvc.current.Release.Display.Providers = []api.ProviderDisplay{{SummaryAvailable: true, Summary: api.ProviderDisplaySummary{Title: "Selected Example", Year: 2026}}}
							}
						}
						return coreSvc.current, nil
					}
					answers := []string{"n", "search", strings.ToLower(string(category)), "Example Title 2026", "24", "y"}
					var output strings.Builder
					reader := bufio.NewReader(strings.NewReader(strings.Join(answers, "\n") + "\n"))
					streams := cliIO{in: reader, out: &output}
					var keyboard *candidateKeyboardPresenter
					if terminal {
						model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
						model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
						keyboard = &candidateKeyboardPresenter{
							model:   model,
							answers: answers,
							t:       t,
						}
						streams.out = &cliPresentationWriter{
							Writer:    &output,
							presenter: keyboard,
							ctx:       t.Context(),
							terminal:  true,
						}
						streams.presenter = keyboard
					}
					_, err = runCLIWorkflowInteractive(t.Context(), coreSvc, args, opts, visited, "example.mkv", api.PlaylistInstruction{}, 0, config.Config{}, streams, api.NopLogger{})
					if err != nil {
						t.Fatalf("selection flow: %v\n%s", err, output.String())
					}
					if !selected || searchCalls != 1 {
						t.Fatal("selection never reached preparation")
					}
					text := output.String()
					if terminal {
						last := keyboard.prompts[len(keyboard.prompts)-1]
						if last.Kind != cliPromptConfirm || !strings.Contains(last.Evidence, "Selected Example") || !strings.Contains(last.Evidence, "Selected.Example") {
							t.Fatal("continuation did not require review of prepared identity")
						}
						text = keyboard.prompts[4].Evidence
						if len(keyboard.answers) != 0 {
							t.Fatalf("unused answers=%v", keyboard.answers)
						}
					} else if strings.Index(text, "Selected.Example") < strings.Index(text, "Match number") {
						t.Fatal("refreshed identity was not displayed after selection")
					}
					if !strings.Contains(text, "Example Title (2000)") || !strings.Contains(text, "ID") && !strings.Contains(text, "https://") || !strings.Contains(text, "Some details unavailable") {
						t.Fatalf("candidate evidence missing: %s", text)
					}
				})
			}
		}
	}
}

func TestCLIProviderInspectionDeclineAndCancellationPreserveChoices(t *testing.T) {
	for _, answer := range []string{"back", "cancel", "99\nback", ""} {
		for _, terminal := range []bool{false, true} {
			if terminal && (answer == "" || strings.Contains(answer, "\n")) {
				continue
			}
			t.Run(fmt.Sprintf("%q/tui=%t", answer, terminal), func(t *testing.T) {
				current := requiredFactsCLIResult()
				current.Release.Release.Identity = api.ExternalIdentity{
					TMDBID:    11,
					IMDBID:    22,
					Category:  api.CanonicalCategoryTV,
					Overrides: api.IdentityOverrideState{TVDB: api.OverrideStateClear},
				}
				current.FactInstructions.Instructions.Identity = api.ExternalIDOverrides{
					TMDBID: new(11),
					IMDBID: new(22),
					TVDBID: new(0),
				}
				before, _ := api.CanonicalWorkflowFingerprint(current)
				coreSvc := &cliWorkflowCoreFake{current: current, searchFn: func(context.Context, api.ProviderSearchRequest) (api.ProviderSearchResult, error) {
					return api.ProviderSearchResult{
						Query:    "Example",
						Category: api.CanonicalCategoryTV,
						Providers: []api.ProviderCandidateResult{{
							Provider: api.IdentityProviderTMDB,
							Status:   api.MetadataEvidenceStatusComplete,
							Candidates: []api.ExternalIdentityCandidate{{
								Provider: api.IdentityProviderTMDB,
								ID:       44,
								Title:    "Example",
								Category: api.CanonicalCategoryTV,
							}},
						}},
					}, nil
				}}
				var out strings.Builder
				s := &cliWorkflowSession{
					core:    coreSvc,
					current: current,
					streams: cliIO{out: &out},
				}
				input := "tv\nExample\n" + answer
				if answer != "" {
					input += "\n"
				}
				if terminal {
					model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
					model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
					presenter := &candidateKeyboardPresenter{
						model:   model,
						answers: []string{"tv", "Example", answer},
						t:       t,
					}
					s.streams.out = &cliPresentationWriter{
						Writer:    &out,
						presenter: presenter,
						ctx:       t.Context(),
						terminal:  true,
					}
				}
				edits, err := s.searchMetadataCandidates(t.Context(), bufio.NewReader(strings.NewReader(input)))
				if answer == "cancel" && !errors.Is(err, context.Canceled) || answer == "" && !errors.Is(err, io.EOF) || answer != "cancel" && answer != "" && err != nil {
					t.Fatalf("inspection error=%v", err)
				}
				after, _ := api.CanonicalWorkflowFingerprint(s.current)
				if len(edits) != 0 || len(coreSvc.continuations) != 0 || before != after {
					t.Fatal("inspection or cancellation changed prepared facts or choices")
				}
			})
		}
	}
}

func TestMetadataCandidatesKeepAllProvidersAndRetainedMatches(t *testing.T) {
	result := api.ProviderSearchResult{
		Query:    "Example 2026",
		Category: api.CanonicalCategoryTV,
		Providers: []api.ProviderCandidateResult{
			{Provider: api.IdentityProviderTMDB, Status: api.MetadataEvidenceStatusComplete},
			{
				Provider:     api.IdentityProviderIMDB,
				Status:       api.MetadataEvidenceStatusUnavailable,
				SearchFailed: true,
			},
			{Provider: api.IdentityProviderTVDB, Status: api.MetadataEvidenceStatusUnavailable},
		},
	}
	evidence, items := metadataCandidateEvidence(result, nil)
	if len(items) != 0 {
		t.Fatal("invented a candidate")
	}
	for _, want := range []string{"TMDB — tv", "IMDB — tv", "TVDB — tv", "No matches", "Search failed", "Provider unavailable"} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("missing %q: %s", want, evidence)
		}
	}
	result.Providers[0].Candidates = []api.ExternalIdentityCandidate{{
		Provider: api.IdentityProviderTMDB,
		ID:       9,
		Title:    "Example",
		Category: api.CanonicalCategoryTV,
	}}
	diagnostics := []api.PreparationDiagnostic{{Code: "external_identity_candidates", Candidates: []api.ExternalIdentityCandidate{
		{
			Provider: api.IdentityProviderTMDB,
			ID:       9,
			Title:    "duplicate",
			Category: api.CanonicalCategoryTV,
		},
		{
			Provider: api.IdentityProviderTMDB,
			ID:       10,
			Title:    "Retained Example",
			Category: api.CanonicalCategoryTV,
		},
		{
			Provider: api.IdentityProviderTMDB,
			ID:       11,
			Title:    "Wrong category",
			Category: api.CanonicalCategoryMovie,
		},
	}}}
	evidence, items = metadataCandidateEvidence(result, diagnostics)
	if len(items) != 2 || items[1].ID != 10 || strings.Contains(evidence, "duplicate") || strings.Contains(evidence, "Wrong category") || strings.Contains(evidence, "0 min") || strings.Contains(evidence, "(0)") {
		t.Fatalf("merged candidate evidence=%s items=%+v", evidence, items)
	}
	if !strings.Contains(evidence, "https://www.themoviedb.org/tv/10") {
		t.Fatal("source link missing")
	}
}

func TestCLIProviderChoiceAfterAutoPreservesSiblingResets(t *testing.T) {
	args := []string{"--reset-input", "identity.tmdb", "--reset-input=identity.imdb", "--reset-input=release_name.category", "example.mkv"}
	// Only the prior TMDB/category Auto requests are replaced by this selection.
	next, err := mergeCLIInputEditArgs(args, []string{"--tmdb=123", "--category=TV"})
	if err != nil {
		t.Fatal(err)
	}
	opts, visited, _, err := parseCLIOptions(next)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opts.ResetInput, []string{"identity.imdb"}) || opts.TMDB != "123" || !visited["tmdb"] {
		t.Fatalf("selected options=%+v", opts)
	}
	next, err = mergeCLIInputEditArgs(next, []string{"--reset-input=identity.tmdb"})
	if err != nil {
		t.Fatal(err)
	}
	opts, visited, _, err = parseCLIOptions(next)
	if err != nil || visited["tmdb"] || !reflect.DeepEqual(opts.ResetInput, []string{"identity.tmdb"}) {
		t.Fatalf("Auto after selection retained a manual ID: args=%v, error=%v", next, err)
	}
}

func TestCLIProviderResetTargetsAndCategoryHints(t *testing.T) {
	for _, field := range []string{"identity.tmdb", "release_name.category"} {
		for _, padding := range []string{"", " "} {
			t.Run(field+"/padding="+padding, func(t *testing.T) {
				args, err := mergeCLIInputEditArgs([]string{"--tmdb=movie/123", "--category=TV", "example.mkv"}, []string{"--reset-input", padding + field + padding})
				if err != nil {
					t.Fatal(err)
				}
				opts, visited, paths, err := parseCLIOptions(args)
				if err != nil {
					t.Fatal(err)
				}
				request, err := buildCLIRequest(opts, visited, paths, 0)
				if err != nil {
					t.Fatal(err)
				}
				if field == "release_name.category" && (request.ReleaseNameOverrides.Category != nil || request.ExternalIDOverrides.TMDBID == nil || *request.ExternalIDOverrides.TMDBID != 123) {
					t.Fatalf("category Auto was replaced by a retained TMDB hint: %+v", request)
				}
				choice := "--tmdb=456"
				if field == "release_name.category" {
					choice = "--category=TV"
				}
				args, err = mergeCLIInputEditArgs(args, []string{choice})
				if err != nil {
					t.Fatal(err)
				}
				opts, _, _, err = parseCLIOptions(args)
				if err != nil || len(opts.ResetInput) != 0 {
					t.Fatalf("explicit choice retained padded Auto request: args=%v, error=%v", args, err)
				}
			})
		}
	}
}

func TestMetadataCandidatesDistinguishEmptySearchFromRetainedMatches(t *testing.T) {
	evidence, items := metadataCandidateEvidence(api.ProviderSearchResult{
		Query:    "Different Title 2026",
		Category: api.CanonicalCategoryMovie,
		Providers: []api.ProviderCandidateResult{{
			Provider: api.IdentityProviderTMDB,
			Status:   api.MetadataEvidenceStatusComplete,
			Query:    "Different Title",
		}},
	}, []api.PreparationDiagnostic{{Code: "external_identity_candidates", Candidates: []api.ExternalIdentityCandidate{{
		Provider: api.IdentityProviderTMDB,
		ID:       11,
		Title:    "Earlier Title",
		Category: api.CanonicalCategoryMovie,
	}}}})
	if len(items) != 1 || !strings.Contains(evidence, "No matches") || !strings.Contains(evidence, "Retained matches from earlier resolution") || strings.Index(evidence, "Earlier Title") < strings.Index(evidence, "Retained matches from earlier resolution") {
		t.Fatalf("retained candidate misrepresented as fresh search result: %s, items=%+v", evidence, items)
	}
}

func TestCLIUnattendedAmbiguousCandidatesNeverPrompt(t *testing.T) {
	current := requiredFactsCLIResult()
	current.Release.Diagnostics = []api.PreparationDiagnostic{{Code: "external_identity_candidates", Candidates: []api.ExternalIdentityCandidate{{
		Provider: api.IdentityProviderTMDB,
		ID:       11,
		Category: api.CanonicalCategoryMovie,
	}}}}
	coreSvc := &cliWorkflowCoreFake{current: current}
	coreSvc.continueFn = func(api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) {
		return coreSvc.current, nil
	}
	for _, test := range []struct {
		name      string
		identity  api.ExternalIdentity
		wantError bool
	}{
		{"unresolved", api.ExternalIdentity{Category: api.CanonicalCategoryMovie}, true},
		{"TMDB", api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 44}, false},
		{"IMDb", api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 44}, false},
		{"TVDB", api.ExternalIdentity{Category: api.CanonicalCategoryTV, TVDBID: 44}, false},
		{"TVmaze", api.ExternalIdentity{Category: api.CanonicalCategoryTV, TVmazeID: 44}, false},
		{"MAL", api.ExternalIdentity{Category: api.CanonicalCategoryMovie, MALID: 44}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			coreSvc.current.Release.Release.Identity = test.identity
			coreSvc.current.Release.Diagnostics[0].Candidates[0].Category = test.identity.Category
			args := []string{"--ua", "example.mkv"}
			opts, visited, _, _ := parseCLIOptions(args)
			var output strings.Builder
			_, err := runCLIWorkflowInteractive(t.Context(), coreSvc, args, opts, visited, "example.mkv", api.PlaylistInstruction{}, 0, config.Config{}, cliIO{out: &output}, api.NopLogger{})
			if test.wantError && (err == nil || !strings.Contains(err.Error(), "unattended")) || !test.wantError && err != nil {
				t.Fatalf("identity=%+v error=%v", test.identity, err)
			}
			if strings.Contains(output.String(), "Match number") || strings.Contains(output.String(), "Input args") || strings.Contains(output.String(), "Metadata correct?") {
				t.Fatal("unattended run prompted")
			}
		})
	}
}

func TestCLIConfirmationModeRequiresCandidateChoice(t *testing.T) {
	current := requiredFactsCLIResult()
	current.Release.Diagnostics = []api.PreparationDiagnostic{{Code: "external_identity_candidates", Candidates: []api.ExternalIdentityCandidate{{
		Provider: api.IdentityProviderTMDB,
		ID:       11,
		Category: api.CanonicalCategoryMovie,
	}}}}
	coreSvc := &cliWorkflowCoreFake{current: current}
	coreSvc.searchFn = func(context.Context, api.ProviderSearchRequest) (api.ProviderSearchResult, error) {
		return api.ProviderSearchResult{
			Query:    "Example",
			Category: api.CanonicalCategoryMovie,
			Providers: []api.ProviderCandidateResult{{
				Provider: api.IdentityProviderTMDB,
				Status:   api.MetadataEvidenceStatusComplete,
				Candidates: []api.ExternalIdentityCandidate{{
					Provider: api.IdentityProviderTMDB,
					ID:       99,
					Title:    "Example",
					Category: api.CanonicalCategoryMovie,
				}},
			}},
		}, nil
	}
	coreSvc.continueFn = func(request api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) {
		if instructions := request.Intent.FactInstructions; instructions != nil && instructions.Identity.TMDBID != nil {
			coreSvc.current.Release.Release.Identity.TMDBID = *instructions.Identity.TMDBID
		}
		return coreSvc.current, nil
	}
	args := []string{"--uac", "example.mkv"}
	opts, visited, _, _ := parseCLIOptions(args)
	var output strings.Builder
	_, err := runCLIWorkflowInteractive(t.Context(), coreSvc, args, opts, visited, "example.mkv", api.PlaylistInstruction{}, 0, config.Config{}, cliIO{in: strings.NewReader("continue\nsearch\nmovie\nExample\n1\ny\n"), out: &output}, api.NopLogger{})
	if err != nil {
		t.Fatalf("required selection: %v\n%s", err, output.String())
	}
	if coreSvc.current.Release.Release.Identity.TMDBID != 99 || strings.Count(output.String(), "Metadata correct?") != 1 || !strings.Contains(output.String(), "TMDB: 99") {
		t.Fatal("required selection was bypassed or did not prepare the selected identity")
	}
}
