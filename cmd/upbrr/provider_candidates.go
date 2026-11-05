// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/pkg/api"
)

// metadataSelectionRequired leaves valid automatic identities and explicit
// provider clears alone, but never treats unresolved candidates as a selection.
func metadataSelectionRequired(preview api.MetadataPreview) bool {
	identity := preview.Identity
	if identity.TMDBID > 0 || identity.IMDBID > 0 || identity.TVDBID > 0 || identity.TVmazeID > 0 || identity.MALID > 0 {
		return false
	}
	for _, diagnostic := range preview.Diagnostics {
		for _, candidate := range diagnostic.Candidates {
			if candidate.ID <= 0 {
				continue
			}
			if candidate.Provider == api.IdentityProviderTMDB && identity.Overrides.TMDB != api.OverrideStateClear ||
				candidate.Provider == api.IdentityProviderIMDB && identity.Overrides.IMDB != api.OverrideStateClear ||
				candidate.Provider == api.IdentityProviderTVDB && identity.Overrides.TVDB != api.OverrideStateClear {
				return true
			}
		}
	}
	return false
}

// searchMetadataCandidates only inspects provider evidence. A selected ID is
// returned as correction arguments for the normal preparation and review loop.
func (s *cliWorkflowSession) searchMetadataCandidates(ctx context.Context, reader *bufio.Reader) ([]string, error) {
	if s.intent.interaction == api.InteractionModeUnattended {
		return nil, errors.New("upbrr: unattended provider selection requires an exact provider ID or --unattended_confirm/--uac")
	}
	preview := cliWorkflowMetadataPreview(s.current)
	bindCLIQuestion(
		ctx,
		s.streams.out,
		s.current.Workflow.ID,
		s.current.Workflow.Revision,
		"",
		"Provider title search: inspecting matches leaves the current identity unchanged.",
	)
	categoryText, err := askCLIField(reader, s.streams.out, cliPrompt{
		Kind:     cliPromptSelect,
		Question: "Search category (movie/tv), back, or cancel: ",
		Help:     "TMDB and IMDb support movies and TV. TVDB searches TV series only. Back returns to metadata review; cancel stops this item.",
		Options: []cliPromptOption{
			{Label: "Back without selecting", Value: "back"},
			{Label: "Movie", Value: "movie"},
			{Label: "TV series", Value: "tv"},
			{Label: "Cancel this item", Value: "cancel"},
		},
	})
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(categoryText, "cancel") {
		return nil, fmt.Errorf("provider selection: %w", context.Canceled)
	}
	category, err := api.NormalizeCanonicalCategory(categoryText)
	if err != nil || category != api.CanonicalCategoryMovie && category != api.CanonicalCategoryTV {
		return nil, nil
	}
	query := s.current.Release.Release.Naming.Title
	if year := s.current.Release.Release.Naming.Year; query != "" && year > 0 {
		query += fmt.Sprintf(" %d", year)
	}
	answer, err := askCLIField(reader, s.streams.out, cliPrompt{
		Kind:     cliPromptText,
		Question: fmt.Sprintf("Search title and optional year [%s], back, or cancel: ", query),
		Help:     "Press Enter to search the displayed title, or enter a different title. Search never changes provider IDs or saved corrections.",
	})
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "back":
		return nil, nil
	case "cancel":
		return nil, fmt.Errorf("provider search: %w", context.Canceled)
	case "":
	default:
		query = answer
	}
	if strings.TrimSpace(query) == "" {
		fmt.Fprintln(s.streams.out, "Enter a non-empty title with search, or supply an exact provider ID.")
		return nil, nil
	}
	fmt.Fprintln(s.streams.out, "Searching metadata providers…")
	result, err := s.core.SearchProviderCandidates(ctx, api.ProviderSearchRequest{Query: query, Category: category})
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("provider search: %w", ctx.Err())
		}
		fmt.Fprintln(s.streams.out, "Provider search failed. Retry with search, check metadata settings, or supply an exact provider ID.")
		return nil, nil
	}
	var diagnostics []api.PreparationDiagnostic
	if category == preview.Identity.Category {
		diagnostics = preview.Diagnostics
	}
	evidence, candidates := metadataCandidateEvidence(result, diagnostics)
	options := []cliPromptOption{{Label: "Decline matches; return without selecting", Value: "back"}}
	for index, candidate := range candidates {
		options = append(options, cliPromptOption{Label: metadataCandidateLabel(candidate), Value: strconv.Itoa(index + 1)})
	}
	options = append(options, cliPromptOption{Label: "Cancel this item", Value: "cancel"})
	for {
		selected, err := askCLIField(reader, s.streams.out, cliPrompt{
			Kind:     cliPromptSelect,
			Question: "Match number, back (decline), or cancel: ",
			Evidence: evidence,
			Help: "Choose one provider ID to apply through metadata preparation, then review the resulting identity before continuing. " +
				"Other manual provider IDs and clears remain independent. All listed matches are accessible; use arrow keys to scroll in the TUI. " +
				"Back keeps current choices. Retry search with another title/year, check metadata settings for unavailable providers, or enter exact --tmdb/--imdb/--tvdb correction flags. Use --reset-input identity.PROVIDER for Auto.",
			Options: options,
		})
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(strings.TrimSpace(selected)) {
		case "back", "":
			return nil, nil
		case "cancel":
			return nil, fmt.Errorf("provider selection: %w", context.Canceled)
		}
		number, err := strconv.Atoi(selected)
		if err != nil || number < 1 || number > len(candidates) {
			fmt.Fprintln(s.streams.out, "Choose a listed match number, back, or cancel.")
			continue
		}
		candidate := candidates[number-1]
		return []string{"--" + string(candidate.Provider) + "=" + strconv.Itoa(candidate.ID), "--category=" + string(candidate.Category)}, nil
	}
}

// metadataCandidateEvidence merges retained resolution candidates with explicit
// search results without truncating or promoting either into prepared facts.
func metadataCandidateEvidence(result api.ProviderSearchResult, diagnostics []api.PreparationDiagnostic) (string, []api.ExternalIdentityCandidate) {
	var evidence strings.Builder
	fmt.Fprintf(&evidence, "Provider title matches: %s (%s)\n", result.Query, result.Category)
	var candidates []api.ExternalIdentityCandidate
	var retained []api.ExternalIdentityCandidate
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "external_identity_candidates" {
			retained = append(retained, diagnostic.Candidates...)
		}
	}
	for _, group := range result.Providers {
		provider := group.Provider
		fmt.Fprintf(&evidence, "\n%s — %s\n", strings.ToUpper(string(provider)), result.Category)
		switch {
		case group.SearchFailed:
			fmt.Fprintln(&evidence, "Search failed; retry search or supply an exact ID.")
		case group.Status == api.MetadataEvidenceStatusUnavailable:
			fmt.Fprintln(&evidence, "Provider unavailable; check metadata settings or supply an exact ID.")
		case group.Status == api.MetadataEvidenceStatusPartial:
			fmt.Fprintln(&evidence, "Some details unavailable; listed matches remain selectable.")
		}
		if group.Query != "" {
			fmt.Fprintf(&evidence, "Effective query: %s", group.Query)
			if group.Year > 0 {
				fmt.Fprintf(&evidence, " | year %d", group.Year)
			}
			fmt.Fprintln(&evidence)
		}
		seen := make(map[int]bool)
		for sourceIndex, items := range [][]api.ExternalIdentityCandidate{group.Candidates, retained} {
			count := 0
			for _, candidate := range items {
				category, _ := api.NormalizeCanonicalCategory(string(candidate.Category))
				if candidate.Provider != provider || candidate.ID <= 0 || seen[candidate.ID] ||
					category != api.CanonicalCategoryUnknown && category != result.Category {
					continue
				}
				candidate.Category = result.Category
				seen[candidate.ID] = true
				if count == 0 {
					if sourceIndex == 0 {
						fmt.Fprintln(&evidence, "Fresh provider matches:")
					} else {
						fmt.Fprintln(&evidence, "Retained matches from earlier resolution:")
					}
				}
				count++
				candidates = append(candidates, candidate)
				fmt.Fprintf(&evidence, "%d. %s\n", len(candidates), metadataCandidateLabel(candidate))
				if candidate.OriginalTitle != "" && candidate.OriginalTitle != candidate.Title {
					fmt.Fprintf(&evidence, "   Original title: %s\n", candidate.OriginalTitle)
				}
				if overview := summarizeMetadataText(candidate.Overview, 260); overview != "" {
					fmt.Fprintf(&evidence, "   %s\n", overview)
				}
				if len(candidate.Directors) > 0 {
					fmt.Fprintf(&evidence, "   Directors: %s\n", strings.Join(candidate.Directors, ", "))
				}
				if len(candidate.Cast) > 0 {
					fmt.Fprintf(&evidence, "   Cast: %s\n", strings.Join(candidate.Cast, ", "))
				}
				if candidate.RuntimeMinutes > 0 {
					fmt.Fprintf(&evidence, "   Runtime: %d min", candidate.RuntimeMinutes)
					if candidate.RuntimeKind != "" {
						fmt.Fprintf(&evidence, " (%s)", candidate.RuntimeKind)
					}
					fmt.Fprintln(&evidence)
				}
			}
			if sourceIndex == 0 && count == 0 && group.Status == api.MetadataEvidenceStatusComplete {
				fmt.Fprintln(&evidence, "No matches. Try another title/year or supply an exact ID.")
			}
		}
	}
	return safeTerminalText(evidence.String()), candidates
}

func metadataCandidateLabel(candidate api.ExternalIdentityCandidate) string {
	title := candidate.Title
	if candidate.Year > 0 {
		title += fmt.Sprintf(" (%d)", candidate.Year)
	}
	id := strconv.Itoa(candidate.ID)
	url := ""
	switch candidate.Provider {
	case api.IdentityProviderTMDB:
		url = fmt.Sprintf("https://www.themoviedb.org/%s/%d", strings.ToLower(string(candidate.Category)), candidate.ID)
	case api.IdentityProviderIMDB:
		id = providerid.IMDb(candidate.ID).Prefixed()
		url = providerid.IMDb(candidate.ID).URL()
	case api.IdentityProviderTVDB:
		url = fmt.Sprintf("https://thetvdb.com/dereferrer/series/%d", candidate.ID)
	case api.IdentityProviderTVmaze, api.IdentityProviderMAL:
	}
	return safeTerminalText(fmt.Sprintf("%s | %s %s | %s | %s", title, strings.ToUpper(string(candidate.Provider)), id, candidate.Category, url))
}
