// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ComparisonAnalysisPolicyVersion binds evidence to decoder selection and
// analysis conversion, independently of screenshot rendering preferences.
const ComparisonAnalysisPolicyVersion = "native-analysis-mobius10-linear1024-libplacebo-hable-v3"
const comparisonAnalysisAlgorithm = "mobius"
const comparisonAnalysisDesat = 10

// ComparisonAnalysisPolicy selects the required normalization from inspected
// facts, independently of final screenshot settings. Missing or unsupported facts
// return ErrComparisonNormalizationUnavailable. Actual conversion must succeed
// before its pixels can become evidence.
func ComparisonAnalysisPolicy(source ComparisonSource) (ComparisonAnalysisRenderer, error) {
	if len(source.HDRFacts.Formats) == 0 {
		return ComparisonAnalysisRenderer{}, fmt.Errorf("%w: missing colour facts", ErrComparisonNormalizationUnavailable)
	}
	mode := "none"
	for _, format := range source.HDRFacts.Formats {
		switch format {
		case api.HDRFormatSDR, api.HDRFormatWCG:
		case api.HDRFormatHDR10, api.HDRFormatHDR10Plus, api.HDRFormatHLG, api.HDRFormatPQ10, api.HDRFormatDolbyVision, api.HDRFormatHDRVivid:
			mode = "software"
		default:
			return ComparisonAnalysisRenderer{}, fmt.Errorf("%w: unsupported colour facts", ErrComparisonNormalizationUnavailable)
		}
	}
	if mode == "software" && !softwareTonemappingCompatible(source.HDRFacts) {
		profile, _, _ := strings.Cut(source.HDRFacts.DolbyVisionProfile, ".")
		if profile == "" || !slices.Contains(source.HDRFacts.Formats, api.HDRFormatDolbyVision) {
			return ComparisonAnalysisRenderer{}, fmt.Errorf("%w: missing compatible colour model", ErrComparisonNormalizationUnavailable)
		}
		mode = "libplacebo"
	}
	return ComparisonAnalysisRenderer{Mode: mode}, nil
}
