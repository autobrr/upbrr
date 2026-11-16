// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/pkg/api"
)

type comparisonCapabilityIdentity struct {
	path           string
	size, modified int64
}
type comparisonAnalysisCapability struct {
	hash, version string
	err           error
}

var comparisonAnalysisCapabilities sync.Map

func comparisonAnalysisCapabilityFor(ctx context.Context, executable string) error {
	stat, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("comparison scene analysis: %w", err)
	}
	key := comparisonCapabilityIdentity{executable, stat.Size(), stat.ModTime().UnixNano()}
	if value, ok := comparisonAnalysisCapabilities.Load(key); ok {
		if receipt, valid := value.(comparisonAnalysisCapability); valid {
			return receipt.err
		}
	}
	file, err := os.Open(executable)
	if err != nil {
		return fmt.Errorf("comparison scene analysis: %w", err)
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(hashErr, closeErr); err != nil {
		return err
	}
	runner := finiteComparisonRunner{base: commandRunner{}, outputLimit: 256 << 10}
	version, err := runner.Run(ctx, executable, []string{"-version"}, "")
	if err != nil {
		return err
	}
	filters, err := runner.Run(ctx, executable, []string{"-hide_banner", "-filters"}, "")
	if err != nil {
		return err
	}
	available := map[string]bool{}
	for line := range strings.SplitSeq(string(filters.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			available[fields[1]] = true
		}
	}
	for _, name := range []string{"trim", "select", "showinfo", "scale", "format", "split", "pad", "setsar", "setpts", "scdet", "metadata", "gblur", "hstack", "interleave"} {
		if !available[name] {
			err = api.NewOperationError(api.OperationFailure{
				Code:             api.OperationFailureInvalidSource,
				Operation:        api.OperationKindMedia,
				Recovery:         api.OperationRecoveryEditInput,
				ComparisonReason: "analysis_capability_unavailable",
				Message:          "The configured FFmpeg executable lacks required scene-analysis filters.",
			}, ErrComparisonNormalizationUnavailable)
			break
		}
	}
	receipt := comparisonAnalysisCapability{
		hash:    hex.EncodeToString(hash.Sum(nil)),
		version: strings.SplitN(string(version.Stdout), "\n", 2)[0],
		err:     err,
	}
	comparisonAnalysisCapabilities.Store(key, receipt)
	if err != nil {
		return fmt.Errorf("comparison analysis capability: %w", err)
	}
	return nil
}

// ComparisonAnalysisExecutableIdentity binds retained analysis to the actual
// resolved executable and build. It contains no local paths or source facts.
func (s *Service) ComparisonAnalysisExecutableIdentity(ctx context.Context) (string, error) {
	if _, native := s.runner.(commandRunner); !native {
		return "injected-renderer", nil
	}
	executable, err := resolveFFmpeg()
	if err != nil {
		return "", err
	}
	if err := comparisonAnalysisCapabilityFor(ctx, executable); err != nil {
		return "", err
	}
	stat, err := os.Stat(executable)
	if err != nil {
		return "", fmt.Errorf("identify comparison executable: %w", err)
	}
	value, ok := comparisonAnalysisCapabilities.Load(comparisonCapabilityIdentity{executable, stat.Size(), stat.ModTime().UnixNano()})
	if !ok {
		return "", errors.New("comparison executable changed during capability inspection")
	}
	receipt, ok := value.(comparisonAnalysisCapability)
	if !ok {
		return "", errors.New("comparison executable capability receipt is unavailable")
	}
	return receipt.hash + " " + receipt.version, nil
}
