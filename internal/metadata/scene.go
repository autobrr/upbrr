// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/pkg/api"
)

const srrdbBaseURL = "https://api.srrdb.com"

// SceneDetector resolves scene metadata from a prepared item.
type SceneDetector interface {
	Detect(ctx context.Context, meta api.PreparedMetadata) (SceneResult, error)
}

// SceneResult captures scene metadata from external sources.
type SceneResult struct {
	IsScene         bool
	SceneName       string
	TMDBID          int
	IMDBID          int
	TVDBID          int
	TVmazeID        int
	MALID           int
	Service         string
	ServiceLongName string
	NFOPath         string
	NFONew          bool
}

type srrdbDetector struct {
	client   *http.Client
	baseURL  string
	cacheDir string
	nfoDir   string
}

func newSRRDBDetector(client *http.Client, baseURL, cacheDir, nfoDir string) *srrdbDetector {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = srrdbBaseURL
	}
	return &srrdbDetector{
		client:   client,
		baseURL:  baseURL,
		cacheDir: strings.TrimSpace(cacheDir),
		nfoDir:   strings.TrimSpace(nfoDir),
	}
}

func (d *srrdbDetector) Detect(ctx context.Context, meta api.PreparedMetadata) (SceneResult, error) {
	base := sceneBase(meta)
	if base == "" {
		return SceneResult{}, nil
	}

	endpoint := fmt.Sprintf("%s/v1/search/r:%s", strings.TrimRight(d.baseURL, "/"), url.PathEscape(base))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return SceneResult{}, fmt.Errorf("scene: build request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return SceneResult{}, fmt.Errorf("scene: srrdb request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return SceneResult{}, nil
	}

	var payload srrdbResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return SceneResult{}, fmt.Errorf("scene: decode response: %w", err)
	}

	if payload.ResultsCount <= 0 || len(payload.Results) == 0 {
		return SceneResult{}, nil
	}

	result := payload.Results[0]
	tmdbID := 0
	imdbID := parseSRRDBIMDbID(result.IMDBID)
	tvdbID := 0
	tvmazeID := 0
	malID := 0
	service := ""
	serviceLongName := ""
	if imdbID == 0 {
		if details, err := d.fetchIMDB(ctx, result.Release); err == nil {
			imdbID = details.firstIMDbID()
		}
	}

	nfoPath := ""
	nfoNew := false
	if strings.EqualFold(result.HasNFO, "yes") {
		if path, downloaded, err := d.fetchNFO(ctx, result.Release); err == nil {
			nfoPath = path
			nfoNew = downloaded
			if nfoIDs, readErr := parseNFOExternalIDs(path); readErr == nil {
				tmdbID = nfoIDs.TMDBID
				if imdbID == 0 {
					imdbID = nfoIDs.IMDBID
				}
				tvdbID = nfoIDs.TVDBID
				tvmazeID = nfoIDs.TVmazeID
				malID = nfoIDs.MALID
				service = nfoIDs.Service
				serviceLongName = nfoIDs.ServiceLongName
			}
		}
	}

	return SceneResult{
		IsScene:         true,
		SceneName:       strings.TrimSpace(result.Release),
		TMDBID:          tmdbID,
		IMDBID:          imdbID,
		TVDBID:          tvdbID,
		TVmazeID:        tvmazeID,
		MALID:           malID,
		Service:         service,
		ServiceLongName: serviceLongName,
		NFOPath:         nfoPath,
		NFONew:          nfoNew,
	}, nil
}

type srrdbResponse struct {
	ResultsCount int `json:"resultsCount"`
	Results      []struct {
		Release string `json:"release"`
		IMDBID  string `json:"imdbId"`
		HasNFO  string `json:"hasNFO"`
	} `json:"results"`
}

type srrdbDetailsResponse struct {
	Files []struct {
		Name string `json:"name"`
	} `json:"files"`
}

type srrdbIMDBResponse struct {
	Releases []struct {
		IMDB string `json:"imdb"`
	} `json:"releases"`
}

type nfoExternalIDs struct {
	TMDBID          int
	IMDBID          int
	TVDBID          int
	TVmazeID        int
	MALID           int
	Service         string
	ServiceLongName string
}

var nfoURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

func (r srrdbIMDBResponse) firstIMDbID() int {
	for _, release := range r.Releases {
		if id := parseSRRDBIMDbID(release.IMDB); id != 0 {
			return id
		}
	}
	return 0
}

func parseSRRDBIMDbID(raw string) int {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(strings.ToLower(trimmed), "tt")
	if trimmed == "" {
		return 0
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0
	}
	return parsed
}

func parseNFOExternalIDs(path string) (nfoExternalIDs, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nfoExternalIDs{}, err
	}
	return parseNFOExternalIDsText(string(data)), nil
}

func parseNFOExternalIDsText(text string) nfoExternalIDs {
	var ids nfoExternalIDs
	if service, longName := parseNFOService(text); service != "" {
		ids.Service = service
		ids.ServiceLongName = longName
	}
	for _, raw := range nfoURLPattern.FindAllString(text, -1) {
		resolution, err := resolveSourceLookupURL(strings.TrimRight(raw, ".,;:)"))
		if err != nil {
			continue
		}
		if ids.TMDBID == 0 {
			ids.TMDBID = resolution.TMDBID
		}
		if ids.IMDBID == 0 {
			ids.IMDBID = resolution.IMDBID
		}
		if ids.TVDBID == 0 {
			ids.TVDBID = resolution.TVDBID
		}
		if ids.TVmazeID == 0 {
			ids.TVmazeID = resolution.TVmazeID
		}
		if ids.MALID == 0 {
			ids.MALID = resolution.MALID
		}
	}
	return ids
}

func parseNFOService(text string) (string, string) {
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Source") {
			continue
		}
		if service, longName := resolveServiceValue(value); service != "" {
			return service, longName
		}
	}
	return "", ""
}

func sceneBase(meta api.PreparedMetadata) string {
	candidate := strings.TrimSpace(meta.VideoPath)
	if candidate == "" {
		candidate = strings.TrimSpace(meta.SourcePath)
	}
	if candidate == "" {
		return ""
	}

	base := pathutil.Base(candidate)
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return strings.TrimSpace(base)
}

func (d *srrdbDetector) fetchNFO(ctx context.Context, release string) (string, bool, error) {
	trimmed := strings.TrimSpace(release)
	if trimmed == "" {
		return "", false, nil
	}
	fileBase := strings.ToLower(trimmed)
	if details, err := d.fetchDetails(ctx, trimmed); err == nil {
		for _, file := range details.Files {
			name := strings.TrimSpace(file.Name)
			if strings.HasSuffix(strings.ToLower(name), ".nfo") {
				base := strings.TrimSuffix(name, filepath.Ext(name))
				if strings.TrimSpace(base) != "" {
					fileBase = strings.ToLower(base)
				}
				break
			}
		}
	}

	cacheDir := d.cacheDir
	if cacheDir == "" {
		return "", false, errors.New("scene: nfo cache: missing cache dir")
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", false, fmt.Errorf("scene: nfo cache: %w", err)
	}
	nfoDir := d.nfoDir
	if nfoDir == "" {
		return "", false, errors.New("scene: nfo cache: missing nfo dir")
	}
	if err := os.MkdirAll(nfoDir, 0o700); err != nil {
		return "", false, fmt.Errorf("scene: nfo dir: %w", err)
	}
	path := filepath.Join(nfoDir, fileBase+".nfo")
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	}

	nfoURL := fmt.Sprintf("https://www.srrdb.com/download/file/%s/%s.nfo", url.PathEscape(trimmed), url.PathEscape(fileBase))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nfoURL, nil)
	if err != nil {
		return "", false, fmt.Errorf("scene: build nfo request: %w", err)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("scene: nfo request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, fmt.Errorf("scene: read nfo: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", false, fmt.Errorf("scene: write nfo: %w", err)
	}
	return path, true, nil
}

func (d *srrdbDetector) fetchDetails(ctx context.Context, release string) (srrdbDetailsResponse, error) {
	cacheDir := d.cacheDir
	cachePath := ""
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o700); err == nil {
			cachePath = filepath.Join(cacheDir, strings.ReplaceAll(release, " ", ".")+".json")
			if cached, err := os.ReadFile(cachePath); err == nil {
				var payload srrdbDetailsResponse
				if err := json.Unmarshal(cached, &payload); err == nil {
					return payload, nil
				}
			}
		}
	}
	endpoint := fmt.Sprintf("%s/v1/details/%s", strings.TrimRight(d.baseURL, "/"), url.PathEscape(release))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return srrdbDetailsResponse{}, fmt.Errorf("scene: build details request: %w", err)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return srrdbDetailsResponse{}, fmt.Errorf("scene: details request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return srrdbDetailsResponse{}, nil
	}
	var payload srrdbDetailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return srrdbDetailsResponse{}, fmt.Errorf("scene: decode details: %w", err)
	}
	if cachePath != "" {
		if data, err := json.Marshal(payload); err == nil {
			_ = os.WriteFile(cachePath, data, 0o600)
		}
	}
	return payload, nil
}

func (d *srrdbDetector) fetchIMDB(ctx context.Context, release string) (srrdbIMDBResponse, error) {
	trimmed := strings.TrimSpace(release)
	if trimmed == "" {
		return srrdbIMDBResponse{}, nil
	}
	endpoint := fmt.Sprintf("%s/v1/imdb/%s", strings.TrimRight(d.baseURL, "/"), url.PathEscape(trimmed))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return srrdbIMDBResponse{}, fmt.Errorf("scene: build imdb request: %w", err)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return srrdbIMDBResponse{}, fmt.Errorf("scene: imdb request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return srrdbIMDBResponse{}, nil
	}
	var payload srrdbIMDBResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return srrdbIMDBResponse{}, fmt.Errorf("scene: decode imdb: %w", err)
	}
	return payload, nil
}
