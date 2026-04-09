// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediainfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gomediainfo "github.com/autobrr/go-mediainfo"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/pkg/api"
)

type Exporter interface {
	Export(ctx context.Context, req Request) (Result, error)
}

type Request struct {
	SourcePath string
	DiscType   string
	VideoPath  string
	TempRoot   string
	Release    api.ReleaseInfo
}

type Result struct {
	JSONPath string
	TextPath string
	IFOPath  string
	VOBPath  string
	VOBSet   string
	VOBText  string
	VOBJSON  string
}

type targetSelection struct {
	AnalyzePath string
	IFOPath     string
	VOBPath     string
	VOBSet      string
}

type Analyzer interface {
	Analyze(ctx context.Context, target string) (text string, json []byte, err error)
}

type Service struct {
	logger   api.Logger
	analyzer Analyzer
}

func NewService(logger api.Logger, analyzer Analyzer) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	if analyzer == nil {
		analyzer = moduleAnalyzer{}
	}
	return &Service{logger: logger, analyzer: analyzer}
}

func (s *Service) Export(ctx context.Context, req Request) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}

	target, err := selectTarget(ctx, req)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(target.AnalyzePath) == "" {
		return Result{}, errors.New("mediainfo: empty target")
	}

	tmpDir, _, err := paths.ReleaseTempDir(req.TempRoot, api.PreparedMetadata{Release: req.Release}, req.SourcePath)
	if err != nil {
		return Result{}, err
	}
	textPath := filepath.Join(tmpDir, "mediainfo.txt")
	jsonPath := filepath.Join(tmpDir, "MediaInfo.json")
	if s.logger != nil {
		s.logger.Debugf("mediainfo: checking cache at %s (text=%v json=%v)", tmpDir, fileExists(textPath), fileExists(jsonPath))
	}
	if fileExists(textPath) && fileExists(jsonPath) {
		hasErrors, err := conformanceError(jsonPath, req.DiscType)
		if err == nil && !hasErrors {
			vobText, vobJSON, err := analyzeVOB(ctx, s.analyzer, target.VOBPath)
			if err != nil {
				return Result{}, err
			}
			if s.logger != nil {
				s.logger.Infof("mediainfo: reusing existing artifacts from %s", tmpDir)
			}
			return Result{JSONPath: jsonPath, TextPath: textPath, IFOPath: target.IFOPath, VOBPath: target.VOBPath, VOBSet: target.VOBSet, VOBText: vobText, VOBJSON: vobJSON}, nil
		}
		if s.logger != nil {
			if err != nil {
				s.logger.Warnf("mediainfo: conformance check failed, regenerating: %v", err)
			} else if hasErrors {
				s.logger.Infof("mediainfo: conformance errors found, regenerating")
			}
		}
	}

	if s.logger != nil {
		s.logger.Debugf("mediainfo: analyzing %s", target.AnalyzePath)
	}

	textOutput, jsonOutput, err := s.analyzer.Analyze(ctx, target.AnalyzePath)
	if err != nil {
		return Result{}, fmt.Errorf("mediainfo: analyze: %w", err)
	}

	cleanText := cleanMediaInfoText(textOutput, target.AnalyzePath)

	if err := os.WriteFile(textPath, []byte(cleanText), 0o600); err != nil {
		return Result{}, fmt.Errorf("mediainfo: write text: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonOutput, 0o600); err != nil {
		return Result{}, fmt.Errorf("mediainfo: write json: %w", err)
	}

	if s.logger != nil {
		s.logger.Debugf("mediainfo: exported to %s", tmpDir)
	}

	vobText, vobJSON, err := analyzeVOB(ctx, s.analyzer, target.VOBPath)
	if err != nil {
		return Result{}, err
	}

	return Result{JSONPath: jsonPath, TextPath: textPath, IFOPath: target.IFOPath, VOBPath: target.VOBPath, VOBSet: target.VOBSet, VOBText: vobText, VOBJSON: vobJSON}, nil
}

func analyzeVOB(ctx context.Context, analyzer Analyzer, vobPath string) (string, string, error) {
	trimmed := strings.TrimSpace(vobPath)
	if trimmed == "" {
		return "", "", nil
	}
	text, jsonPayload, err := analyzer.Analyze(ctx, trimmed)
	if err != nil {
		return "", "", fmt.Errorf("mediainfo: analyze dvd vob: %w", err)
	}
	return cleanMediaInfoText(text, trimmed), string(jsonPayload), nil
}

type moduleAnalyzer struct{}

func (moduleAnalyzer) Analyze(_ context.Context, target string) (string, []byte, error) {
	report, err := gomediainfo.AnalyzeFile(target)
	if err != nil {
		return "", nil, err
	}
	reports := []gomediainfo.Report{report}
	text, err := gomediainfo.Render(reports, gomediainfo.OutputText)
	if err != nil {
		return "", nil, err
	}
	json, err := gomediainfo.Render(reports, gomediainfo.OutputJSON)
	if err != nil {
		return "", nil, err
	}
	return text, []byte(json), nil
}

func cleanMediaInfoText(text, target string) string {
	base := filepath.Base(target)
	cleaned := strings.ReplaceAll(text, target, base)
	cleaned = strings.ReplaceAll(cleaned, filepath.ToSlash(target), base)
	lines := strings.Split(cleaned, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ReportBy") {
			continue
		}
		if strings.HasPrefix(trimmed, "Report created by ") {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\n")
}

func selectTarget(ctx context.Context, req Request) (targetSelection, error) {
	if strings.EqualFold(req.DiscType, "DVD") {
		return selectDVDTarget(ctx, req.SourcePath)
	}
	if strings.TrimSpace(req.VideoPath) != "" {
		return targetSelection{AnalyzePath: req.VideoPath}, nil
	}
	if strings.TrimSpace(req.SourcePath) == "" {
		return targetSelection{}, internalerrors.ErrInvalidInput
	}
	return targetSelection{AnalyzePath: req.SourcePath}, nil
}

func selectDVDTarget(ctx context.Context, source string) (targetSelection, error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return targetSelection{}, internalerrors.ErrInvalidInput
	}

	videoTS, err := findVideoTS(ctx, trimmed)
	if err != nil {
		return targetSelection{}, err
	}

	ifo, vobPath, vobSet, err := selectBestIFO(ctx, videoTS)
	if err != nil {
		return targetSelection{}, err
	}

	return targetSelection{
		AnalyzePath: ifo,
		IFOPath:     ifo,
		VOBPath:     vobPath,
		VOBSet:      vobSet,
	}, nil
}

func findVideoTS(ctx context.Context, root string) (string, error) {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("mediainfo: path %q: %w", root, internalerrors.ErrNotFound)
		}
		return "", fmt.Errorf("mediainfo: path %q: %w", root, err)
	}

	if info.IsDir() {
		if strings.EqualFold(filepath.Base(root), "VIDEO_TS") {
			return root, nil
		}
		candidate := filepath.Join(root, "VIDEO_TS")
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate, nil
		}
	}

	var found string
	foundErr := errors.New("videots found")
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() && strings.EqualFold(entry.Name(), "VIDEO_TS") {
			found = path
			return foundErr
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, foundErr) {
		if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
			return "", walkErr
		}
		return "", fmt.Errorf("mediainfo: scan dvd: %w", walkErr)
	}
	if found == "" {
		return "", fmt.Errorf("mediainfo: VIDEO_TS not found: %w", internalerrors.ErrNotFound)
	}
	return found, nil
}

func selectBestIFO(ctx context.Context, videoTS string) (string, string, string, error) {
	entries, err := os.ReadDir(videoTS)
	if err != nil {
		return "", "", "", fmt.Errorf("mediainfo: read VIDEO_TS: %w", err)
	}

	ifoBySet := map[string]string{}
	vobSizes := map[string]int64{}
	vobBySet := map[string][]string{}
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return "", "", "", ctx.Err()
		default:
		}
		name := entry.Name()
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "VTS_") && strings.HasSuffix(upper, "_0.IFO") {
			set := strings.TrimPrefix(strings.TrimSuffix(upper, "_0.IFO"), "VTS_")
			ifoBySet[set] = filepath.Join(videoTS, name)
			continue
		}
		if strings.HasPrefix(upper, "VTS_") && strings.HasSuffix(upper, ".VOB") {
			set := strings.TrimPrefix(strings.TrimSuffix(upper, ".VOB"), "VTS_")
			set = strings.SplitN(set, "_", 2)[0]
			vobBySet[set] = append(vobBySet[set], filepath.Join(videoTS, name))
			info, err := entry.Info()
			if err != nil {
				continue
			}
			vobSizes[set] += info.Size()
		}
	}

	bestSet := ""
	var bestSize int64
	for set, size := range vobSizes {
		if size > bestSize {
			bestSize = size
			bestSet = set
		}
	}
	if bestSet != "" {
		if path, ok := ifoBySet[bestSet]; ok {
			return path, selectBestVOBPath(vobBySet[bestSet]), bestSet, nil
		}
	}

	if fallback := filepath.Join(videoTS, "VIDEO_TS.IFO"); fileExists(fallback) {
		return fallback, "", "", nil
	}

	for _, path := range ifoBySet {
		if fileExists(path) {
			set := dvdSetFromIFO(path)
			return path, selectBestVOBPath(vobBySet[set]), set, nil
		}
	}

	return "", "", "", fmt.Errorf("mediainfo: no IFO found: %w", internalerrors.ErrNotFound)
}

func dvdSetFromIFO(path string) string {
	upper := strings.ToUpper(filepath.Base(path))
	if !strings.HasPrefix(upper, "VTS_") || !strings.HasSuffix(upper, "_0.IFO") {
		return ""
	}
	set := strings.TrimPrefix(strings.TrimSuffix(upper, "_0.IFO"), "VTS_")
	return strings.TrimSpace(set)
}

func selectBestVOBPath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	best := ""
	bestIndex := int(^uint(0) >> 1)
	for _, path := range paths {
		index := dvdVOBIndex(path)
		if index <= 0 {
			continue
		}
		if index < bestIndex {
			bestIndex = index
			best = path
		}
	}
	if best != "" {
		return best
	}
	return paths[0]
}

func dvdVOBIndex(path string) int {
	name := strings.ToUpper(filepath.Base(path))
	if !strings.HasPrefix(name, "VTS_") || !strings.HasSuffix(name, ".VOB") {
		return 0
	}
	trimmed := strings.TrimPrefix(strings.TrimSuffix(name, ".VOB"), "VTS_")
	parts := strings.Split(trimmed, "_")
	if len(parts) != 2 {
		return 0
	}
	index := 0
	_, _ = fmt.Sscanf(parts[1], "%d", &index)
	return index
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
