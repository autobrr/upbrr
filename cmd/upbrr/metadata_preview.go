// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/pkg/api"
)

// printMetadataPreview writes the canonical workflow metadata projection shown
// by the CLI before tracker projection and upload review.
func printMetadataPreview(output io.Writer, preview api.MetadataPreview, debug bool) {
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Release details")
	if debug {
		fmt.Fprintln(output, "Debug mode: validates the end-to-end workflow while suppressing tracker submission.")
	}
	fmt.Fprintf(output, "Source: %s\n", formatPathLabel(preview.SourcePath))
	fmt.Fprintf(output, "Upload name: %s\n", preview.ReleaseName)
	if external := primaryMetadataPreview(preview); external != nil {
		printMetadataDatabaseInfo(output, *external)
	}
	if preview.TrackerName != "" {
		fmt.Fprintf(output, "Tracker data from: %s\n", preview.TrackerName)
	}
	printMetadataExternalIdentity(output, preview)
	if metadataPreviewHasCandidates(preview.Diagnostics) {
		fmt.Fprintln(output, "Candidate IDs available; use override args if needed.")
	}
	warnings := make([]string, 0)
	for _, diagnostic := range preview.Diagnostics {
		if diagnostic.Severity == api.DiagnosticSeverityWarning && strings.TrimSpace(diagnostic.Message) != "" {
			warnings = append(warnings, diagnostic.Message)
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintln(output, "Warnings:")
		for _, warning := range warnings {
			fmt.Fprintf(output, "- %s\n", warning)
		}
	}
}

func printMetadataDatabaseInfo(output io.Writer, external api.ProviderDisplay) {
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Database info")
	summary := external.Summary
	if summary.Title != "" && summary.Year != 0 {
		fmt.Fprintf(output, "Title: %s (%d)\n", summary.Title, summary.Year)
	} else if summary.Title != "" {
		fmt.Fprintf(output, "Title: %s\n", summary.Title)
	}
	if overview := summarizeMetadataText(summary.Overview, 260); overview != "" {
		fmt.Fprintf(output, "Overview: %s\n", overview)
	}
	if genres := strings.TrimSpace(summary.Genres); genres != "" {
		fmt.Fprintf(output, "Genres: %s\n", genres)
	}
	if summary.Category != "" {
		fmt.Fprintf(output, "Category: %s\n", strings.ToUpper(summary.Category))
	}
	if summary.Date != "" {
		fmt.Fprintf(output, "Date: %s\n", summary.Date)
	}
	if summary.RuntimeMinutes != 0 {
		fmt.Fprintf(output, "Runtime: %d min\n", summary.RuntimeMinutes)
	}
	if summary.Rating != 0 {
		if summary.RatingCount != 0 {
			fmt.Fprintf(output, "Rating: %.1f (%d votes)\n", summary.Rating, summary.RatingCount)
		} else {
			fmt.Fprintf(output, "Rating: %.1f\n", summary.Rating)
		}
	}
}

func printMetadataExternalIdentity(output io.Writer, preview api.MetadataPreview) {
	identity := preview.Identity
	printedHeader := false
	printHeader := func() {
		if printedHeader {
			return
		}
		fmt.Fprintln(output)
		fmt.Fprintln(output, "External IDs")
		printedHeader = true
	}
	if identity.TMDBID != 0 {
		printHeader()
		fmt.Fprintf(output, "TMDB: %d\n", identity.TMDBID)
	}
	if identity.IMDBID != 0 {
		printHeader()
		fmt.Fprintf(output, "IMDb: %s\n", providerid.IMDb(identity.IMDBID).Prefixed())
	}
	if identity.TVDBID != 0 {
		printHeader()
		fmt.Fprintf(output, "TVDB: %d\n", identity.TVDBID)
	}
	if identity.TVmazeID != 0 {
		printHeader()
		fmt.Fprintf(output, "TVmaze: %d\n", identity.TVmazeID)
	}
	if identity.MALID != 0 {
		printHeader()
		fmt.Fprintf(output, "MAL: %d\n", identity.MALID)
	}
}

func primaryMetadataPreview(preview api.MetadataPreview) *api.ProviderDisplay {
	for index := range preview.Display.Providers {
		if preview.Display.Providers[index].SummaryAvailable {
			return &preview.Display.Providers[index]
		}
	}
	return nil
}

func metadataPreviewHasCandidates(diagnostics []api.PreparationDiagnostic) bool {
	for _, diagnostic := range diagnostics {
		if len(diagnostic.Candidates) > 0 {
			return true
		}
	}
	return false
}

func summarizeMetadataText(value string, limit int) string {
	compact := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if compact == "" || limit <= 0 {
		return compact
	}
	runes := []rune(compact)
	if len(runes) <= limit {
		return compact
	}
	return string(runes[:limit]) + "..."
}
