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
	hardware      []string
	err           error
}

var comparisonAnalysisCapabilities sync.Map

func comparisonAnalysisCapabilityFor(ctx context.Context, executable string) error {
	stat, err := os.Stat(executable) // #nosec G703 -- Resolved local FFmpeg executable; its contents identify the capability receipt.
	if err != nil {
		return fmt.Errorf("comparison scene analysis: %w", err)
	}
	key := comparisonCapabilityIdentity{executable, stat.Size(), stat.ModTime().UnixNano()}
	if value, ok := comparisonAnalysisCapabilities.Load(key); ok {
		if receipt, valid := value.(comparisonAnalysisCapability); valid {
			return receipt.err
		}
	}
	file, err := os.Open(executable) // #nosec G703 -- Read the resolved FFmpeg executable to bind capabilities to its content hash.
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
	for _, name := range []string{"trim", "select", "showinfo", "scale", "format", "split", "pad", "setsar", "scdet", "metadata", "gblur", "hstack"} {
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
	for _, capability := range []struct {
		option, name string
		column       int
	}{{"-protocols", "http", 0}, {"-muxers", "rawvideo", 1}, {"-pix_fmts", "gray", 1}} {
		result, probeErr := runner.Run(ctx, executable, []string{"-hide_banner", capability.option}, "")
		if probeErr != nil {
			return probeErr
		}
		found := false
		output := capability.option != "-protocols"
		for line := range strings.SplitSeq(string(result.Stdout), "\n") {
			if strings.TrimSpace(line) == "Output:" {
				output = true
				continue
			}
			fields := strings.Fields(line)
			if output && len(fields) > capability.column && fields[capability.column] == capability.name {
				found = true
			}
		}
		if !found {
			err = fmt.Errorf("%w: required analysis output capability unavailable", ErrComparisonNormalizationUnavailable)
		}
	}
	hardware, hardwareErr := runner.Run(ctx, executable, []string{"-hide_banner", "-hwaccels"}, "")
	if hardwareErr != nil {
		return hardwareErr
	}
	receipt := comparisonAnalysisCapability{
		hash:     hex.EncodeToString(hash.Sum(nil)),
		version:  strings.SplitN(string(version.Stdout), "\n", 2)[0],
		err:      err,
		hardware: strings.Fields(string(hardware.Stdout)),
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
