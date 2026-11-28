// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
)

// Section is a rendered group. A nonempty ID uniquely identifies a movable group.
type Section struct {
	ID   string
	Text string
}

// AppendSection keeps consecutive pieces of the same rendered group together.
func AppendSection(parts []Section, id, text string) []Section {
	if last := len(parts) - 1; id != "" && last >= 0 && parts[last].ID == id {
		parts[last].Text += "\n\n" + text
		return parts
	}
	return append(parts, Section{ID: id, Text: text})
}

// OptionalSection reports whether hiding this group can safely omit only
// additional presentation. Required media evidence, text, galleries and
// tracker fixed blocks are never removed by the layout editor.
func OptionalSection(id string) bool {
	switch id {
	case "custom_header", "logo", "poster", "episode_overview", "bluray", "audio_analysis", "plot", "genres", "trailer", "cast", "production", "ratings":
		return true
	default:
		return false
	}
}

// FormatSections moves supported groups, filters optional sections, and applies
// compact spacing without editing the content inside a retained section.
func FormatSections(parts []Section, defaults config.DescriptionSettingsConfig) string {
	return FormatSectionsSeparated(parts, defaults, "\n\n")
}

// FormatSectionsSeparated retains a tracker's original text separator. Only
// standard BBCode spacing supports the compact presentation option.
func FormatSectionsSeparated(parts []Section, defaults config.DescriptionSettingsConfig, separator string) string {
	ordered := orderPresentSections(parts, defaults.SectionOrder, defaults.ItemOptions)
	var out strings.Builder
	for _, part := range ordered {
		if out.Len() > 0 {
			if separator == "\n\n" && defaults.ItemOptions[part.ID].Compact {
				out.WriteByte('\n')
			} else {
				out.WriteString(separator)
			}
		}
		out.WriteString(part.Text)
	}
	return out.String()
}

func orderPresentSections(parts []Section, order []string, options map[string]config.DescriptionItemOptions) []Section {
	visible := func(part Section) bool {
		return part.Text != "" && (!options[part.ID].Hidden || !OptionalSection(part.ID))
	}
	var selected []bool
	var ordered []Section
	if len(order) > 1 {
		positions := make(map[string]int, len(parts))
		for i, part := range parts {
			if part.ID != "" && visible(part) {
				positions[part.ID] = i
			}
		}
		selected = make([]bool, len(parts))
		for _, id := range order {
			if i, present := positions[id]; present && !selected[i] {
				selected[i] = true
				ordered = append(ordered, parts[i])
			}
		}
	}
	text := make([]Section, 0, len(parts))
	for i, part := range parts {
		if len(selected) > 0 && selected[i] {
			part, ordered = ordered[0], ordered[1:]
		}
		if visible(part) {
			text = append(text, part)
		}
	}
	return text
}

// SeparateGeneratedComparison recognizes the application's complete trailing
// comparison block only when that group participates in the configured layout.
// Imported inline comparisons and default layouts remain untouched.
func SeparateGeneratedComparison(text string, defaults config.DescriptionSettingsConfig) (body, comparison string) {
	if !slices.Contains(defaults.SectionOrder, "comparison") && !defaults.ItemOptions["comparison"].Compact &&
		strings.TrimSpace(defaults.Labels["comparison.heading"]) == "" {
		return text, ""
	}
	const open = "[spoiler=source_comparisons]\n"
	trimmed := strings.TrimSpace(text)
	start := strings.LastIndex(trimmed, open)
	if start < 0 || start > 0 && trimmed[start-1] != '\n' || !strings.HasSuffix(trimmed, "[/comparison]\n[/spoiler]") ||
		!strings.HasPrefix(trimmed[start+len(open):], "[comparison=") {
		return text, ""
	}
	// Literals in the kept description are not generated comparison evidence.
	outsideLiteral := false
	MapOutsideLiteralBlocks(trimmed, func(fragment string) string {
		if strings.Contains(fragment, trimmed[start:]) {
			outsideLiteral = true
		}
		return fragment
	})
	if !outsideLiteral {
		return text, ""
	}
	comparison = trimmed[start:]
	if label := MarkupLabel(defaults.Labels["comparison.heading"]); label != "" {
		comparison = "[spoiler=" + label + "]\n" + strings.TrimPrefix(comparison, open)
	}
	return strings.TrimSpace(trimmed[:start]), comparison
}
