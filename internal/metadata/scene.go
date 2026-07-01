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

// SceneDetector resolves scene metadata from a prepared item. Implementations
// may return a populated result with an error when an optional side effect fails.
type SceneDetector interface {
	Detect(ctx context.Context, meta api.PreparedMetadata) (SceneResult, error)
}

// SceneResult captures scene metadata from external sources. NFO fields are
// best-effort side-effect outputs and may be empty on an otherwise valid match.
type SceneResult struct {
	IsScene   bool
	SceneName string
	TMDBID    int
	IMDBID    int
	TVDBID    int
	TVmazeID  int
	MALID     int
	// Service is the normalized service code parsed from a saved scene NFO.
	Service string
	// ServiceLongName is the display name matching Service when one is known.
	ServiceLongName string
	// NFOPath is the local filesystem path to a saved scene NFO.
	NFOPath string
	// NFONew reports whether NFOPath was downloaded during this detection run.
	NFONew bool
	// Renamed reports that a scene release was identified via the imdb: fallback
	// (the exact r: name missed) and the on-disk basename differs from the
	// canonical scene media filename, i.e. the release was renamed/modified.
	Renamed bool
	// RenamedReason is an operator-facing explanation set when Renamed is true.
	RenamedReason string
}

type sceneNFOError struct {
	err error
}

// newSceneNFOError marks optional NFO fetch or persistence failures so callers
// can keep the primary scene match while still reporting the side-effect error.
func newSceneNFOError(err error) error {
	if err == nil {
		return nil
	}
	return &sceneNFOError{err: err}
}

func (e *sceneNFOError) Error() string {
	return fmt.Sprintf("scene: nfo side effect: %v", e.err)
}

func (e *sceneNFOError) Unwrap() error {
	return e.err
}

// isSceneNFOError reports whether err is a recoverable NFO side-effect failure.
func isSceneNFOError(err error) bool {
	var nfoErr *sceneNFOError
	return errors.As(err, &nfoErr)
}

type srrdbDetector struct {
	client   *http.Client
	baseURL  string
	cacheDir string
	nfoDir   string
	logger   api.Logger
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
		logger:   api.NopLogger{},
	}
}

// log returns a non-nil logger for decision/diagnostic output.
func (d *srrdbDetector) log() api.Logger {
	if d.logger == nil {
		return api.NopLogger{}
	}
	return d.logger
}

// srrdbUserAgent identifies upbrr to srrdb (which sends no rate-limit headers and
// asks callers not to scrape); a descriptive agent keeps us a good citizen.
const srrdbUserAgent = "upbrr/scene-detector (+https://github.com/autobrr/upbrr)"

// setSRRDBHeaders applies the shared User-Agent to every srrdb request.
func setSRRDBHeaders(req *http.Request) {
	req.Header.Set("User-Agent", srrdbUserAgent)
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
	setSRRDBHeaders(req)

	resp, err := d.client.Do(req)
	if err != nil {
		// srrdb being unreachable/slow must never block an upload, so soft-fail the
		// r: search the same way the imdb: fallback does. Context cancellation still
		// propagates as fatal.
		if isContextError(ctx, err) {
			return SceneResult{}, fmt.Errorf("scene: srrdb request: %w", err)
		}
		d.log().Debugf("metadata: scene r: search soft-failed: %v", err)
		return SceneResult{}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return SceneResult{}, nil
	}

	var payload srrdbResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		// A truncated/malformed body is a transient srrdb failure, not an
		// upload-blocking condition; soft-fail unless the context was cancelled.
		if isContextError(ctx, err) {
			return SceneResult{}, fmt.Errorf("scene: decode response: %w", err)
		}
		d.log().Debugf("metadata: scene r: decode soft-failed: %v", err)
		return SceneResult{}, nil
	}

	if payload.ResultsCount <= 0 || len(payload.Results) == 0 {
		// The exact dotted-name search missed. A renamed/modified scene release
		// deviates from its canonical name, so fall back to an IMDb-keyed lookup
		// (independent of filename) and match locally. Stays entirely soft: srrdb
		// being unreachable or returning nothing must never block an upload.
		return d.detectViaIMDB(ctx, meta, base)
	}

	// The exact r: name matched, so the on-disk name is already canonical (case
	// differences aside) and is by definition not renamed.
	return d.buildSceneResult(ctx, payload.Results[0], false, "")
}

// buildSceneResult turns a matched srrdb release into a SceneResult, resolving the
// IMDb id and attaching the NFO/external IDs exactly as the r: path always has.
// renamed/renamedReason carry the imdb: fallback's canonical-name verdict.
func (d *srrdbDetector) buildSceneResult(ctx context.Context, result srrdbSearchResult, renamed bool, renamedReason string) (SceneResult, error) {
	imdbID := parseSRRDBIMDbID(result.IMDBID)
	if imdbID == 0 {
		if details, err := d.fetchIMDB(ctx, result.Release); err == nil {
			imdbID = details.firstIMDbID()
		}
	}

	scene := SceneResult{
		IsScene:       true,
		SceneName:     strings.TrimSpace(result.Release),
		IMDBID:        imdbID,
		Renamed:       renamed,
		RenamedReason: renamedReason,
	}
	if strings.EqualFold(result.HasNFO, "yes") {
		path, downloaded, err := d.fetchNFO(ctx, result.Release)
		if err != nil && isContextError(ctx, err) {
			return SceneResult{}, err
		}
		if path != "" {
			scene.NFOPath = path
			scene.NFONew = downloaded
			if nfoIDs, readErr := parseNFOExternalIDs(path); readErr == nil {
				scene.TMDBID = nfoIDs.TMDBID
				if scene.IMDBID == 0 {
					scene.IMDBID = nfoIDs.IMDBID
				}
				scene.TVDBID = nfoIDs.TVDBID
				scene.TVmazeID = nfoIDs.TVmazeID
				scene.MALID = nfoIDs.MALID
				scene.Service = nfoIDs.Service
				scene.ServiceLongName = nfoIDs.ServiceLongName
			}
		}
		if err != nil {
			return scene, newSceneNFOError(err)
		}
	}

	return scene, nil
}

// detectViaIMDB is the renamed-release fallback: keyed off an already-known IMDb
// id, it lists every scene release for the title, score-matches the best
// candidate against the parsed release tokens, then compares the canonical media
// filename to the on-disk basename to flag a rename. It is strictly best-effort —
// every failure path returns a no-match (except context cancellation), never an
// error that could block an upload.
func (d *srrdbDetector) detectViaIMDB(ctx context.Context, meta api.PreparedMetadata, localBase string) (SceneResult, error) {
	imdbID := sceneIMDbID(meta)
	if imdbID == 0 {
		return SceneResult{}, nil
	}

	releases, err := d.fetchIMDBReleases(ctx, imdbID)
	if err != nil {
		if isContextError(ctx, err) {
			return SceneResult{}, err
		}
		d.log().Debugf("metadata: scene imdb fallback soft-failed imdb=%d: %v", imdbID, err)
		return SceneResult{}, nil
	}
	if len(releases) == 0 {
		return SceneResult{}, nil
	}

	best, score := bestSceneCandidate(meta, localBase, releases)
	if best == nil {
		d.log().Debugf("metadata: scene imdb fallback no confident candidate imdb=%d candidates=%d", imdbID, len(releases))
		return SceneResult{}, nil
	}
	d.log().Debugf("metadata: scene imdb fallback matched imdb=%d candidates=%d release=%q score=%d", imdbID, len(releases), best.Release, score)

	renamed, reason := d.detectRename(ctx, best.Release, localBase)
	if renamed {
		d.log().Infof("metadata: scene release renamed or modified imdb=%d", imdbID)
	}
	return d.buildSceneResult(ctx, *best, renamed, reason)
}

// sceneRenamedReason is intentionally generic: it does not disclose the
// canonical scene name, so the fix is not simply "rename the file back". A
// modified release should be investigated (hash/provenance) rather than papered
// over with a rename.
const sceneRenamedReason = "source does not match its original scene release name (renamed or modified); verify the file hash and source provenance"

// detectRename compares the canonical media filename(s) recorded for a scene
// release against the on-disk basename. It is conservative: a name that matches
// any canonical media file (case differences tolerated, like srrdb's r: search),
// or any inability to read the release details, yields "not renamed".
func (d *srrdbDetector) detectRename(ctx context.Context, release, localBase string) (bool, string) {
	details, err := d.fetchDetails(ctx, release)
	if err != nil {
		return false, ""
	}
	if canonicalMediaBase(details.ArchivedFiles, localBase) == "" {
		return false, ""
	}
	return true, sceneRenamedReason
}

// isContextError reports cancellation and deadline errors from err or ctx.
func isContextError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
		return true
	}
	return false
}

// srrdbSearchResult is one entry from an srrdb search response. The r: and imdb:
// searches share this shape; imdb: additionally populates IsForeign and Size.
type srrdbSearchResult struct {
	Release   string `json:"release"`
	IMDBID    string `json:"imdbId"`
	HasNFO    string `json:"hasNFO"`
	IsForeign string `json:"isForeign"`
	Size      int64  `json:"size"`
}

type srrdbResponse struct {
	ResultsCount int                 `json:"resultsCount"`
	Results      []srrdbSearchResult `json:"results"`
}

// srrdbIMDBSearchResponse is the /v1/search/imdb:<id> payload: the full release
// list for a title keyed off its IMDb id, independent of the on-disk filename.
type srrdbIMDBSearchResponse struct {
	ResultsCount int                 `json:"resultsCount"`
	Results      []srrdbSearchResult `json:"results"`
}

type srrdbDetailsResponse struct {
	Files []struct {
		Name string `json:"name"`
	} `json:"files"`
	// ArchivedFiles lists the files inside the release archive; Name is the
	// canonical media filename used for rename detection, with CRC/Size kept for
	// the stronger content-tamper signal.
	ArchivedFiles []srrdbArchivedFile `json:"archived-files"`
}

type srrdbArchivedFile struct {
	Name string `json:"name"`
	CRC  string `json:"crc"`
	Size int64  `json:"size"`
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
		return nfoExternalIDs{}, fmt.Errorf("metadata: read NFO file: %w", err)
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
	for line := range strings.SplitSeq(text, "\n") {
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
	var detailsErr error
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
	} else if isContextError(ctx, err) {
		return "", false, err
	} else {
		detailsErr = err
	}

	cacheDir := d.cacheDir
	if cacheDir == "" {
		return "", false, errors.Join(detailsErr, errors.New("scene: nfo cache: missing cache dir"))
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: nfo cache: %w", err))
	}
	nfoDir := d.nfoDir
	if nfoDir == "" {
		return "", false, errors.Join(detailsErr, errors.New("scene: nfo cache: missing nfo dir"))
	}
	if err := os.MkdirAll(nfoDir, 0o700); err != nil {
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: nfo dir: %w", err))
	}
	path := filepath.Join(nfoDir, fileBase+".nfo")
	if _, err := os.Stat(path); err == nil {
		return path, false, detailsErr
	}

	nfoURL := fmt.Sprintf("https://www.srrdb.com/download/file/%s/%s.nfo", url.PathEscape(trimmed), url.PathEscape(fileBase))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nfoURL, nil)
	if err != nil {
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: build nfo request: %w", err))
	}
	setSRRDBHeaders(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: nfo request: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, detailsErr
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: read nfo: %w", err))
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: write nfo: %w", err))
	}
	return path, true, detailsErr
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
	setSRRDBHeaders(req)
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
	setSRRDBHeaders(req)
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

const (
	// srrdbIMDBPageSize is the observed per-page cap of the imdb: search.
	srrdbIMDBPageSize = 40
	// srrdbIMDBMaxPages bounds the imdb: fan-out so a title with a very large
	// release history can never trigger an unbounded number of requests.
	srrdbIMDBMaxPages = 5
)

// sceneIMDbID returns the IMDb id already resolved on meta at scene-detection
// time (Prepare runs before ResolveExternalIDs), in precedence order. A source
// lookup URL or a prior persisted resolution is what makes the rename fallback
// possible; 0 means "no id known" and the fallback is skipped.
func sceneIMDbID(meta api.PreparedMetadata) int {
	if id := meta.ExternalIDOverrides.IMDBID; id != nil && *id > 0 {
		return *id
	}
	if meta.ExternalIDs.IMDBID > 0 {
		return meta.ExternalIDs.IMDBID
	}
	if meta.ArrIMDBID > 0 {
		return meta.ArrIMDBID
	}
	return 0
}

// fetchIMDBReleases lists every scene release for an IMDb id via the imdb:
// search, paginating up to srrdbIMDBMaxPages. The id is a validated integer, so
// the URL cannot be influenced by parsed metadata (no SSRF surface).
func (d *srrdbDetector) fetchIMDBReleases(ctx context.Context, imdbID int) ([]srrdbSearchResult, error) {
	if imdbID <= 0 {
		return nil, nil
	}
	all := make([]srrdbSearchResult, 0, srrdbIMDBPageSize)
	total := -1
	for page := 1; page <= srrdbIMDBMaxPages; page++ {
		payload, err := d.fetchIMDBReleasePage(ctx, imdbID, page)
		if err != nil {
			return all, err
		}
		all = append(all, payload.Results...)
		total = payload.ResultsCount
		if len(payload.Results) < srrdbIMDBPageSize {
			break
		}
		if total >= 0 && len(all) >= total {
			break
		}
	}
	if total > len(all) {
		d.log().Debugf("metadata: scene imdb fallback truncated imdb=%d collected=%d total=%d cap=%d", imdbID, len(all), total, srrdbIMDBMaxPages)
	}
	return all, nil
}

func (d *srrdbDetector) fetchIMDBReleasePage(ctx context.Context, imdbID, page int) (srrdbIMDBSearchResponse, error) {
	endpoint := fmt.Sprintf("%s/v1/search/imdb:%d/order:date/page:%d", strings.TrimRight(d.baseURL, "/"), imdbID, page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return srrdbIMDBSearchResponse{}, fmt.Errorf("scene: build imdb search request: %w", err)
	}
	setSRRDBHeaders(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return srrdbIMDBSearchResponse{}, fmt.Errorf("scene: imdb search request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return srrdbIMDBSearchResponse{}, nil
	}
	var payload srrdbIMDBSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return srrdbIMDBSearchResponse{}, fmt.Errorf("scene: decode imdb search: %w", err)
	}
	return payload, nil
}

// sceneMediaExtensions are the archive members treated as the primary media file
// for rename detection.
var sceneMediaExtensions = map[string]struct{}{
	".mkv": {}, ".mp4": {}, ".avi": {}, ".ts": {}, ".m2ts": {},
	".vob": {}, ".iso": {}, ".wmv": {}, ".mov": {}, ".m4v": {},
}

// canonicalMediaBase reports the canonical media basename to flag as a rename, or
// "" when the on-disk basename matches a canonical media file (case differences
// tolerated, like srrdb's r: search) or no media file can be identified. The
// representative name returned is the largest matching media file.
func canonicalMediaBase(archived []srrdbArchivedFile, localBase string) string {
	wantBase := strings.TrimSpace(localBase)
	representative := ""
	var representativeSize int64 = -1
	for _, file := range archived {
		stem, ext, ok := sceneMediaStem(file.Name)
		if !ok {
			continue
		}
		if _, isMedia := sceneMediaExtensions[ext]; !isMedia {
			continue
		}
		if strings.EqualFold(stem, wantBase) {
			return ""
		}
		if file.Size > representativeSize {
			representativeSize = file.Size
			representative = stem
		}
	}
	return representative
}

// sceneMediaStem splits an srrdb archive member name (slash-data, never a local
// path) into its trimmed basename stem and lower-cased extension using plain
// string operations so no filesystem path API is applied to remote data.
func sceneMediaStem(name string) (stem, ext string, ok bool) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", "", false
	}
	if idx := strings.LastIndexAny(trimmed, "/\\"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	dot := strings.LastIndex(trimmed, ".")
	if dot <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(trimmed[:dot]), strings.ToLower(trimmed[dot:]), true
}
