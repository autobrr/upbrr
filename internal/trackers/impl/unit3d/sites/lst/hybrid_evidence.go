// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"html"
	"strings"

	"github.com/frustra/bbcode"

	"github.com/autobrr/upbrr/pkg/api"
)

// generatedHybridEvidenceIncluded checks source-bound selected artifacts in the
// actual description. Their presence does not establish comparative quality.
func generatedHybridEvidenceIncluded(subject api.TrackerValidationSubject, description string) bool {
	type included struct{ grade, hdr10plus bool }
	targets := make(map[string]included)
	images := descriptionImageURLs(description)
	for _, artifact := range subject.AssetFacts.HDRArtifacts {
		if artifact.TargetID == "" || artifact.Kind.DecodedMode() == api.HDRDecodedAudio {
			continue
		}
		state := targets[artifact.TargetID]
		present := false
		for _, imageURL := range artifact.ImageURLs {
			present = present || images[imageURL]
		}
		if present && artifact.GenerationComplete {
			//nolint:exhaustive // Only the required comparison and metadata artifacts satisfy this evidence set.
			switch artifact.Kind {
			case api.HDRArtifactAutomaticGrade, api.HDRArtifactGradePlot:
				state.grade = true
			case api.HDRArtifactHDR10PlusPlot:
				state.hdr10plus = true
			}
		}
		targets[artifact.TargetID] = state
	}
	if len(targets) == 0 {
		return false
	}
	for _, state := range targets {
		if !state.grade || !state.hdr10plus {
			return false
		}
	}
	return true
}

func descriptionImageURLs(description string) map[string]bool {
	images := make(map[string]bool)
	var visit func(*bbcode.BBCodeNode)
	visit = func(node *bbcode.BBCodeNode) {
		if tag := node.GetOpeningTag(); tag != nil {
			switch strings.ToLower(tag.Name) {
			case "code", "pre", "noparse", "comparison":
				return
			case "img":
				if node.ClosingTag != nil && len(node.Children) == 1 && node.Children[0].ID == bbcode.TEXT {
					if value, ok := node.Children[0].Value.(string); ok {
						images[strings.TrimSpace(html.UnescapeString(value))] = true
					}
				}
				return
			}
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(bbcode.Parse(bbcode.Lex(description)))
	return images
}
