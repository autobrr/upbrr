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

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	gomediainfo "github.com/autobrr/go-mediainfo"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/pkg/api"
)

// Exporter materializes or reuses source-scoped MediaInfo artifacts.
type Exporter interface {
	Export(ctx context.Context, req Request) (Result, error)
}

// Request names host filesystem paths for one source and its release-scoped
// temporary root. VideoPath overrides SourcePath for non-DVD analysis.
type Request struct {
	SourcePath string
	DiscType   string
	VideoPath  string
	TempRoot   string
	Release    api.ReleaseInfo
}

// Result identifies persisted MediaInfo artifacts and optional DVD evidence.
// Path fields are host filesystem paths; VOBText and VOBJSON are in-memory
// reports for the selected DVD title set.
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

// Analyzer renders text and JSON MediaInfo reports for one host filesystem
// target. Service owns artifact persistence.
type Analyzer interface {
	Analyze(ctx context.Context, target string) (text string, json []byte, err error)
}

// Service selects the analysis target and owns release-scoped artifact reuse and
// persistence.
type Service struct {
	logger   api.Logger
	analyzer Analyzer
}

// NewService substitutes a no-op logger and the module-backed analyzer for nil
// dependencies.
func NewService(logger api.Logger, analyzer Analyzer) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	if analyzer == nil {
		analyzer = moduleAnalyzer{}
	}
	return &Service{logger: logger, analyzer: analyzer}
}

// Export writes mode-0600 text and JSON reports beneath the release temporary
// directory. Text reports reduce the analyzed target path to its basename;
// JSON reports retain the analyzer output. Existing artifacts are reused only
// when both files exist and the JSON has no conformance error; DVD VOB evidence
// is analyzed on every call. Errors return no Result, although a failed JSON
// write may leave the text file.
func (s *Service) Export(ctx context.Context, req Request) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	target, err := selectTarget(ctx, req)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(target.AnalyzePath) == "" {
		return Result{}, errors.New("mediainfo: empty target")
	}

	tmpDir, _, err := paths.ReleaseTempDir(req.TempRoot, preparationstate.State{Release: req.Release}, req.SourcePath)
	if err != nil {
		return Result{}, fmt.Errorf("metadata: %w", err)
	}
	textPath := filepath.Join(tmpDir, "mediainfo.txt")
	jsonPath := filepath.Join(tmpDir, "MediaInfo.json")
	if s.logger != nil {
		s.logger.Debugf("mediainfo: checking cache at %s (text=%v json=%v)", tmpDir, fileExists(textPath), fileExists(jsonPath))
	}
	if fileExists(textPath) && fileExists(jsonPath) {
		hasErrors, err := conformanceError(jsonPath, req.DiscType)
		if err == nil && !hasErrors {
			textOutput, err := os.ReadFile(textPath)
			if err != nil {
				return Result{}, fmt.Errorf("mediainfo: read cached text: %w", err)
			}
			cleanText := cleanMediaInfoText(string(textOutput), target.AnalyzePath)
			if cleanText != string(textOutput) {
				if err := writeMediaInfoText(textPath, []byte(cleanText)); err != nil {
					return Result{}, fmt.Errorf("mediainfo: write cached text: %w", err)
				}
			}
			if err := os.Chmod(textPath, 0o600); err != nil {
				return Result{}, fmt.Errorf("mediainfo: chmod cached text: %w", err)
			}
			vobText, vobJSON, err := analyzeVOB(ctx, s.analyzer, target.VOBPath)
			if err != nil {
				return Result{}, err
			}
			if s.logger != nil {
				s.logger.Debugf("mediainfo: reusing existing artifacts from %s", tmpDir)
			}
			return Result{
				JSONPath: jsonPath,
				TextPath: textPath,
				IFOPath:  target.IFOPath,
				VOBPath:  target.VOBPath,
				VOBSet:   target.VOBSet,
				VOBText:  vobText,
				VOBJSON:  vobJSON,
			}, nil
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

	return Result{
		JSONPath: jsonPath,
		TextPath: textPath,
		IFOPath:  target.IFOPath,
		VOBPath:  target.VOBPath,
		VOBSet:   target.VOBSet,
		VOBText:  vobText,
		VOBJSON:  vobJSON,
	}, nil
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
		return "", nil, fmt.Errorf("mediainfo: analyze file: %w", err)
	}
	reports := []gomediainfo.Report{report}
	text, err := gomediainfo.Render(reports, gomediainfo.OutputText)
	if err != nil {
		return "", nil, fmt.Errorf("mediainfo: render text: %w", err)
	}
	json, err := gomediainfo.Render(reports, gomediainfo.OutputJSON)
	if err != nil {
		return "", nil, fmt.Errorf("mediainfo: render json: %w", err)
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
		if field, _, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(field) == "Complete name" {
			line = field + ": " + base
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\n")
}

// writeMediaInfoText replaces path from a synced same-directory temp file as a
// single filesystem update where the platform supports it.
func writeMediaInfoText(path string, data []byte) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	removeTemp = false
	return nil
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
			return fmt.Errorf("context canceled: %w", ctx.Err())
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
			return "", fmt.Errorf("mediainfo: scan dvd interrupted: %w", walkErr)
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
			return "", "", "", fmt.Errorf("context canceled: %w", ctx.Err())
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
