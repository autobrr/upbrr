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
	"time"

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
	receipt, err := inspectComparisonCapabilities(ctx, executable)
	if err != nil {
		return err
	}
	return receipt.err
}

func inspectComparisonCapabilities(ctx context.Context, executable string) (comparisonAnalysisCapability, error) {
	if err := ctx.Err(); err != nil {
		return comparisonAnalysisCapability{}, fmt.Errorf("inspect comparison capabilities: %w", err)
	}
	stat, err := os.Stat(executable) // #nosec G703 -- Resolved local FFmpeg executable; its contents identify the capability receipt.
	if err != nil {
		return comparisonAnalysisCapability{}, fmt.Errorf("comparison scene analysis: %w", err)
	}
	key := comparisonCapabilityIdentity{executable, stat.Size(), stat.ModTime().UnixNano()}
	if value, ok := comparisonAnalysisCapabilities.Load(key); ok {
		if receipt, valid := value.(comparisonAnalysisCapability); valid {
			return receipt, nil
		}
	}
	file, err := os.Open(executable) // #nosec G703 -- Read the resolved FFmpeg executable to bind capabilities to its content hash.
	if err != nil {
		return comparisonAnalysisCapability{}, fmt.Errorf("comparison scene analysis: %w", err)
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(hashErr, closeErr); err != nil {
		return comparisonAnalysisCapability{}, err
	}
	runner := finiteComparisonRunner{base: commandRunner{}, outputLimit: 256 << 10}
	version, err := runner.Run(ctx, executable, []string{"-version"}, "")
	if err != nil {
		return comparisonAnalysisCapability{}, err
	}
	filters, err := runner.Run(ctx, executable, []string{"-hide_banner", "-filters"}, "")
	if err != nil {
		return comparisonAnalysisCapability{}, err
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
			return comparisonAnalysisCapability{}, probeErr
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
		return comparisonAnalysisCapability{}, hardwareErr
	}
	receipt := comparisonAnalysisCapability{
		hash:     hex.EncodeToString(hash.Sum(nil)),
		version:  strings.SplitN(string(version.Stdout), "\n", 2)[0],
		err:      err,
		hardware: strings.Fields(string(hardware.Stdout)),
	}
	comparisonAnalysisCapabilities.Store(key, receipt)
	return receipt, nil
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
	receipt, err := inspectComparisonCapabilities(ctx, executable)
	if err != nil {
		return "", err
	}
	if receipt.err != nil {
		return "", receipt.err
	}
	return receipt.hash + " " + receipt.version, nil
}

var comparisonFileCapabilities sync.Map

func requireComparisonFileCapability(ctx context.Context, runner Runner, executable string) error {
	if _, native := runner.(commandRunner); !native {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("inspect comparison file capability: %w", err)
	}
	stat, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("identify comparison executable: %w", err)
	}
	key := comparisonCapabilityIdentity{executable, stat.Size(), stat.ModTime().UnixNano()}
	if value, ok := comparisonFileCapabilities.Load(key); ok {
		err, _ := value.(error)
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// One synthetic raw frame exercises the exact input filter without opening media.
	probe := finiteComparisonRunner{
		base:        commandRunner{},
		input:       make([]byte, 4),
		outputLimit: 16 << 10,
	}
	result, err := probe.Run(probeCtx, executable, []string{
		"-hide_banner", "-loglevel", "error", "-f", "rawvideo", "-pixel_format", "gray", "-video_size", "2x2",
		"-bsf:V:0", comparisonPacketFilter, "-i", "pipe:0", "-map", "0:V:0", "-c", "copy", "-f", "null", "-",
	}, "")
	if err != nil {
		if result.ExitCode == 0 {
			return err
		}
		err = fmt.Errorf("inspect comparison file capability: %w", api.NewOperationError(api.OperationFailure{
			Code:             api.OperationFailureInvalidSource,
			Operation:        api.OperationKindMedia,
			Recovery:         api.OperationRecoveryEditInput,
			ComparisonReason: "file_packet_filter_unavailable",
			Message:          "Update FFmpeg to a build with input bitstream filtering (the noise filter) to compare video files.",
		}, ErrComparisonNormalizationUnavailable))
	}
	comparisonFileCapabilities.Store(key, err)
	return err
}
