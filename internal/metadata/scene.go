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

// formatSRRDBIMDbID renders an IMDb id in srrdb's required zero-padded tt form
// (12345 -> "tt0012345"). Integer storage drops leading zeroes and srrdb rejects
// an unpadded numeric id ("Invalid value for imdb"), so the query must re-pad.
// Returns "" for a non-positive id.
func formatSRRDBIMDbID(id int) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", id)
}

// sceneCandidates holds the local on-disk name signals used to select and verify
// a scene release. folders are release-folder basenames kept whole (so dotted
// tokens survive); files are media basenames with the media extension stripped;
// mediaFilename is the on-disk media basename WITH extension for the
// case-sensitive rename comparison.
type sceneCandidates struct {
	folders       []string
	files         []string
	mediaFilename string
}

func (c sceneCandidates) empty() bool {
	return len(c.folders) == 0 && len(c.files) == 0
}

// primaryLocalBase returns the most descriptive local name for scoring/foreign
// heuristics: the folder when known, else the file.
func (c sceneCandidates) primaryLocalBase() string {
	if len(c.folders) > 0 {
		return c.folders[0]
	}
	if len(c.files) > 0 {
		return c.files[0]
	}
	return ""
}

// sceneLocalCandidates derives folder and file name candidates from the prepared
// paths. VideoPath is the primary media file; SourcePath is the release root — a
// folder for folder releases (its basename is the canonical dotted name) or the
// media file itself for single-file releases.
func sceneLocalCandidates(meta api.PreparedMetadata) sceneCandidates {
	video := strings.TrimSpace(meta.VideoPath)
	source := strings.TrimSpace(meta.SourcePath)
	var c sceneCandidates

	addFile := func(base string) {
		if stem := stripSceneMediaExt(base); stem != "" && !containsFold(c.files, stem) {
			c.files = append(c.files, stem)
		}
	}

	if video != "" {
		base := pathutil.Base(video)
		c.mediaFilename = strings.TrimSpace(base)
		addFile(base)
	}
	if source != "" && !pathutil.SamePath(source, video) {
		base := pathutil.Base(source)
		switch {
		case looksLikeSceneMediaFile(base):
			addFile(base)
			if c.mediaFilename == "" {
				c.mediaFilename = strings.TrimSpace(base)
			}
		case base != "" && base != ".":
			if !containsFold(c.folders, base) {
				c.folders = append(c.folders, base)
			}
		}
	}
	return c
}

func (d *srrdbDetector) Detect(ctx context.Context, meta api.PreparedMetadata) (SceneResult, error) {
	cands := sceneLocalCandidates(meta)
	if cands.empty() {
		d.log().Tracef("metadata: scene detection skipped: no local candidates")
		return SceneResult{}, nil
	}
	imdbID := sceneIMDbID(meta)
	d.log().Debugf("metadata: scene detection start imdb=%d folders=%d files=%d media_present=%t", imdbID, len(cands.folders), len(cands.files), strings.TrimSpace(cands.mediaFilename) != "")

	// For TV releases, a word:<title SxxEyy|Sxx> search is the cheapest
	// high-signal path and avoids broad IMDb fan-out on long-running shows. TV
	// detection deliberately skips the IMDb fallback after the word search and
	// only tries exact r: names; non-TV releases still use IMDb before r:.
	if result, err := d.detectViaTVWord(ctx, meta, cands); err != nil {
		return result, err
	} else if result.IsScene {
		return result, nil
	}
	if isSRRDBTVCategory(meta) {
		return d.detectViaR(ctx, cands)
	}
	if imdbID > 0 {
		result, err := d.detectViaIMDB(ctx, meta, cands, imdbID)
		if err != nil {
			return result, err
		}
		if result.IsScene {
			return result, nil
		}
	}
	return d.detectViaR(ctx, cands)
}

// detectViaTVWord runs the low-fan-out SRRDB word search for parsed TV releases.
// It returns no match when the metadata cannot form a title/season query. Network
// and decode failures are soft unless they are context cancellation.
func (d *srrdbDetector) detectViaTVWord(ctx context.Context, meta api.PreparedMetadata, cands sceneCandidates) (SceneResult, error) {
	query := srrdbTVWordQuery(meta)
	if query == "" {
		return SceneResult{}, nil
	}
	d.log().Tracef("metadata: scene word search start query=%q", query)
	releases, err := d.searchWord(ctx, query)
	if err != nil {
		if isContextError(ctx, err) {
			return SceneResult{}, err
		}
		d.log().Debugf("metadata: scene word search soft-failed query=%q: %v", query, err)
		return SceneResult{}, nil
	}
	if len(releases) == 0 {
		d.log().Debugf("metadata: scene word search no results query=%q", query)
		return SceneResult{}, nil
	}
	d.log().Debugf("metadata: scene word search results query=%q count=%d", query, len(releases))

	best, source := selectSceneRelease(meta, cands, releases)
	if best == nil {
		d.log().Debugf("metadata: scene word no confident candidate query=%q candidates=%d folders=%d files=%d", query, len(releases), len(cands.folders), len(cands.files))
		return SceneResult{}, nil
	}
	return d.finishSceneMatch(ctx, cands, *best, source, "word")
}

// detectViaIMDB lists every scene release for the title, selects the one matching
// the local folder/filename, then verifies the media filename. Strictly
// best-effort: every failure returns a no-match (except context cancellation).
func (d *srrdbDetector) detectViaIMDB(ctx context.Context, meta api.PreparedMetadata, cands sceneCandidates, imdbID int) (SceneResult, error) {
	d.log().Tracef("metadata: scene imdb search start imdb=%d", imdbID)
	releases, err := d.fetchIMDBReleases(ctx, meta, imdbID)
	if err != nil {
		if isContextError(ctx, err) {
			return SceneResult{}, err
		}
		d.log().Debugf("metadata: scene imdb search soft-failed imdb=%d: %v", imdbID, err)
		return SceneResult{}, nil
	}
	if len(releases) == 0 {
		d.log().Debugf("metadata: scene imdb search no results imdb=%d", imdbID)
		return SceneResult{}, nil
	}
	d.log().Debugf("metadata: scene imdb search results imdb=%d count=%d", imdbID, len(releases))

	best, source := selectSceneRelease(meta, cands, releases)
	if best == nil {
		d.log().Debugf("metadata: scene imdb no confident candidate imdb=%d candidates=%d folders=%d files=%d", imdbID, len(releases), len(cands.folders), len(cands.files))
		return SceneResult{}, nil
	}
	return d.finishSceneMatch(ctx, cands, *best, source, "imdb")
}

// detectViaR is the no-IMDb fallback: an exact r: lookup over the folder
// candidates (the canonical dotted name) first, then the media filename.
func (d *srrdbDetector) detectViaR(ctx context.Context, cands sceneCandidates) (SceneResult, error) {
	names := make([]string, 0, len(cands.folders)+len(cands.files))
	names = append(names, cands.folders...)
	names = append(names, cands.files...)
	d.log().Debugf("metadata: scene r search start candidates=%d", len(names))
	for _, name := range names {
		release, ok, err := d.searchExactR(ctx, name)
		if err != nil {
			if isContextError(ctx, err) {
				return SceneResult{}, err
			}
			d.log().Debugf("metadata: scene r: search soft-failed name=%q: %v", name, err)
			return SceneResult{}, nil
		}
		if ok {
			return d.finishSceneMatch(ctx, cands, release, "r", "r")
		}
	}
	d.log().Debugf("metadata: scene r search no match candidates=%d", len(names))
	return SceneResult{}, nil
}

// searchExactR performs a single exact r: search and returns the first result.
func (d *srrdbDetector) searchExactR(ctx context.Context, name string) (srrdbSearchResult, bool, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return srrdbSearchResult{}, false, nil
	}
	d.log().Tracef("metadata: scene r search candidate=%q", trimmed)
	endpoint := fmt.Sprintf("%s/v1/search/r:%s", strings.TrimRight(d.baseURL, "/"), url.PathEscape(trimmed))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return srrdbSearchResult{}, false, fmt.Errorf("scene: build r search request: %w", err)
	}
	setSRRDBHeaders(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return srrdbSearchResult{}, false, fmt.Errorf("scene: r search request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		d.log().Tracef("metadata: scene r search candidate=%q status=%d", trimmed, resp.StatusCode)
		return srrdbSearchResult{}, false, nil
	}
	var payload srrdbResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return srrdbSearchResult{}, false, fmt.Errorf("scene: decode r search: %w", err)
	}
	if payload.ResultsCount <= 0 || len(payload.Results) == 0 {
		d.log().Tracef("metadata: scene r search candidate=%q results=0", trimmed)
		return srrdbSearchResult{}, false, nil
	}
	d.log().Tracef("metadata: scene r search candidate=%q results=%d selected=%q", trimmed, len(payload.Results), payload.Results[0].Release)
	return payload.Results[0], true, nil
}

// searchWord performs one SRRDB word:<query> search and returns the raw result
// list. Non-OK HTTP statuses are treated as no results; request and JSON decode
// failures are returned so the caller can apply the scene-detection soft-fail
// policy.
func (d *srrdbDetector) searchWord(ctx context.Context, query string) ([]srrdbSearchResult, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, nil
	}
	endpoint := fmt.Sprintf("%s/v1/search/word:%s", strings.TrimRight(d.baseURL, "/"), url.PathEscape(trimmed))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("scene: build word search request: %w", err)
	}
	setSRRDBHeaders(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scene: word search request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		d.log().Tracef("metadata: scene word search query=%q status=%d", trimmed, resp.StatusCode)
		return nil, nil
	}
	var payload srrdbResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("scene: decode word search: %w", err)
	}
	return payload.Results, nil
}

// selectSceneRelease picks the srrdb release that matches the local names, using
// case-insensitive equality (only for selecting the right release when local
// casing differs): exact folder match, then exact filename/release-name match,
// then metadata scoring to break ties / reject weak candidates.
func selectSceneRelease(meta api.PreparedMetadata, cands sceneCandidates, releases []srrdbSearchResult) (*srrdbSearchResult, string) {
	for _, folder := range cands.folders {
		for i := range releases {
			if strings.EqualFold(strings.TrimSpace(releases[i].Release), folder) {
				return &releases[i], "folder"
			}
		}
	}
	for _, file := range cands.files {
		for i := range releases {
			if strings.EqualFold(strings.TrimSpace(releases[i].Release), file) {
				return &releases[i], "filename"
			}
		}
	}
	if best, _ := bestSceneCandidate(meta, cands.primaryLocalBase(), releases); best != nil {
		return best, "score"
	}
	return nil, ""
}

// finishSceneMatch verifies the media filename against the selected release and
// builds the SceneResult.
func (d *srrdbDetector) finishSceneMatch(ctx context.Context, cands sceneCandidates, release srrdbSearchResult, matchSource, mode string) (SceneResult, error) {
	renamed := d.detectRenamed(ctx, release.Release, cands)
	reason := ""
	if renamed {
		reason = sceneRenamedReason
		d.log().Infof("metadata: scene release renamed or modified via=%s", matchSource)
	}
	d.log().Debugf("metadata: scene matched mode=%s via=%s release=%q folders=%v media=%q has_nfo=%t renamed=%t", mode, matchSource, release.Release, cands.folders, cands.mediaFilename, strings.EqualFold(release.HasNFO, "yes"), renamed)
	return d.buildSceneResult(ctx, release, renamed, reason)
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
	d.log().Tracef("metadata: scene result imdb=%d has_nfo=%q renamed=%t", scene.IMDBID, result.HasNFO, scene.Renamed)
	if strings.EqualFold(result.HasNFO, "yes") {
		path, downloaded, err := d.fetchNFO(ctx, result.Release)
		if err != nil && isContextError(ctx, err) {
			return SceneResult{}, err
		}
		if path != "" {
			scene.NFOPath = path
			scene.NFONew = downloaded
			d.log().Tracef("metadata: scene nfo attached downloaded=%t", downloaded)
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
				d.log().Tracef("metadata: scene nfo ids tmdb=%d imdb=%d tvdb=%d tvmaze=%d mal=%d service_present=%t", scene.TMDBID, scene.IMDBID, scene.TVDBID, scene.TVmazeID, scene.MALID, scene.Service != "")
			} else {
				d.log().Debugf("metadata: scene nfo id parse failed: %v", readErr)
			}
		}
		if err != nil {
			d.log().Debugf("metadata: scene nfo side effect failed: %v", err)
			return scene, newSceneNFOError(err)
		}
	} else {
		d.log().Debugf("metadata: scene nfo not advertised has_nfo=%q", result.HasNFO)
	}

	return scene, nil
}

// sceneRenamedReason is intentionally generic: it does not disclose the
// canonical scene name, so the fix is not simply "rename the file back". A
// modified release should be investigated (hash/provenance) rather than papered
// over with a rename.
const sceneRenamedReason = "source does not match its original scene release name (renamed or modified); verify the file hash and source provenance"

// detectRenamed reports whether the local media file was renamed/modified from
// the selected scene release. The local media filename is compared to the
// release's archived media filenames CASE-SENSITIVELY — srrdb archived names are
// authoritative, so a casing-only difference is a rename. It is conservative: no
// media file to compare, no archived media, or any details-fetch failure yields
// "not renamed".
func (d *srrdbDetector) detectRenamed(ctx context.Context, release string, cands sceneCandidates) bool {
	local := strings.TrimSpace(cands.mediaFilename)
	if local == "" {
		return false
	}
	details, err := d.fetchDetails(ctx, release)
	if err != nil {
		return false
	}
	renamed, matched := archivedMediaRenamed(details.ArchivedFiles, local)
	if !matched {
		return false
	}
	return renamed
}

// archivedMediaRenamed reports whether the local media filename fails to match
// (case-sensitively) any archived scene media filename. matched is false when the
// release has no identifiable media member to compare against.
func archivedMediaRenamed(archived []srrdbArchivedFile, localMediaFilename string) (renamed, matched bool) {
	want := strings.TrimSpace(localMediaFilename)
	found := false
	for _, file := range archived {
		base, ok := archivedMediaName(file.Name)
		if !ok {
			continue
		}
		found = true
		if base == want { // case-sensitive: srrdb is authoritative
			return false, true
		}
	}
	if !found {
		return false, false
	}
	return true, true
}

// archivedMediaName returns the original-case basename (with extension) of an
// archived media member, or ok=false for non-media members. srrdb archive names
// are slash-data, handled with plain string ops (no local-path API).
func archivedMediaName(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", false
	}
	if idx := strings.LastIndexAny(trimmed, "/\\"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	dot := strings.LastIndex(trimmed, ".")
	if dot <= 0 {
		return "", false
	}
	if _, isMedia := sceneMediaExtensions[strings.ToLower(trimmed[dot:])]; !isMedia {
		return "", false
	}
	return trimmed, true
}

// stripSceneMediaExt removes a recognized media extension from a basename,
// returning the stem; a name without a media extension is returned unchanged so
// folder-like names keep their dotted tokens.
func stripSceneMediaExt(base string) string {
	trimmed := strings.TrimSpace(base)
	if dot := strings.LastIndex(trimmed, "."); dot > 0 {
		if _, ok := sceneMediaExtensions[strings.ToLower(trimmed[dot:])]; ok {
			return strings.TrimSpace(trimmed[:dot])
		}
	}
	return trimmed
}

// looksLikeSceneMediaFile reports whether a basename ends in a recognized media
// extension.
func looksLikeSceneMediaFile(base string) bool {
	trimmed := strings.TrimSpace(base)
	dot := strings.LastIndex(trimmed, ".")
	if dot <= 0 {
		return false
	}
	_, ok := sceneMediaExtensions[strings.ToLower(trimmed[dot:])]
	return ok
}

// containsFold reports case-insensitive membership.
func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
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

func (d *srrdbDetector) fetchNFO(ctx context.Context, release string) (string, bool, error) {
	trimmed := strings.TrimSpace(release)
	if trimmed == "" {
		return "", false, nil
	}
	d.log().Tracef("metadata: scene nfo lookup start release=%q", trimmed)
	fileBase := strings.ToLower(trimmed)
	detailsNFO := false
	var detailsErr error
	if details, err := d.fetchDetails(ctx, trimmed); err == nil {
		d.log().Tracef("metadata: scene nfo details loaded release=%q files=%d", trimmed, len(details.Files))
		for _, file := range details.Files {
			name := strings.TrimSpace(file.Name)
			if strings.HasSuffix(strings.ToLower(name), ".nfo") {
				base := strings.TrimSuffix(name, filepath.Ext(name))
				if strings.TrimSpace(base) != "" {
					fileBase = strings.ToLower(base)
				}
				detailsNFO = true
				d.log().Tracef("metadata: scene nfo details selected release=%q file=%q", trimmed, name)
				break
			}
		}
	} else if isContextError(ctx, err) {
		return "", false, err
	} else {
		d.log().Debugf("metadata: scene nfo details failed release=%q: %v", trimmed, err)
		detailsErr = err
	}

	cacheDir := d.cacheDir
	if cacheDir == "" {
		d.log().Debugf("metadata: scene nfo cache unavailable release=%q reason=missing_cache_dir", trimmed)
		return "", false, errors.Join(detailsErr, errors.New("scene: nfo cache: missing cache dir"))
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		d.log().Debugf("metadata: scene nfo cache unavailable release=%q: %v", trimmed, err)
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: nfo cache: %w", err))
	}
	nfoDir := d.nfoDir
	if nfoDir == "" {
		d.log().Debugf("metadata: scene nfo dir unavailable release=%q reason=missing_nfo_dir", trimmed)
		return "", false, errors.Join(detailsErr, errors.New("scene: nfo cache: missing nfo dir"))
	}
	if err := os.MkdirAll(nfoDir, 0o700); err != nil {
		d.log().Debugf("metadata: scene nfo dir unavailable release=%q: %v", trimmed, err)
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: nfo dir: %w", err))
	}
	path := filepath.Join(nfoDir, fileBase+".nfo")
	if _, err := os.Stat(path); err == nil {
		d.log().Debugf("metadata: scene nfo cache hit release=%q path=%s details_error=%t", trimmed, path, detailsErr != nil)
		return path, false, detailsErr
	}

	d.log().Tracef("metadata: scene nfo downloading release=%q file=%q details_nfo=%t details_error=%t", trimmed, fileBase+".nfo", detailsNFO, detailsErr != nil)
	nfoURL := fmt.Sprintf("https://www.srrdb.com/download/file/%s/%s.nfo", url.PathEscape(trimmed), url.PathEscape(fileBase))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nfoURL, nil)
	if err != nil {
		d.log().Debugf("metadata: scene nfo request build failed release=%q: %v", trimmed, err)
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: build nfo request: %w", err))
	}
	setSRRDBHeaders(req)
	resp, err := d.client.Do(req)
	if err != nil {
		d.log().Debugf("metadata: scene nfo request failed release=%q: %v", trimmed, err)
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: nfo request: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		d.log().Debugf("metadata: scene nfo unavailable release=%q status=%d details_error=%t", trimmed, resp.StatusCode, detailsErr != nil)
		return "", false, detailsErr
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		d.log().Debugf("metadata: scene nfo read failed release=%q: %v", trimmed, err)
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: read nfo: %w", err))
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		d.log().Debugf("metadata: scene nfo write failed release=%q path=%s: %v", trimmed, path, err)
		return "", false, errors.Join(detailsErr, fmt.Errorf("scene: write nfo: %w", err))
	}
	d.log().Debugf("metadata: scene nfo downloaded release=%q path=%s bytes=%d details_error=%t", trimmed, path, len(data), detailsErr != nil)
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
					d.log().Tracef("metadata: scene details cache hit release=%q path=%s", release, cachePath)
					return payload, nil
				}
				d.log().Debugf("metadata: scene details cache invalid release=%q path=%s: %v", release, cachePath, err)
			}
		}
	}
	d.log().Tracef("metadata: scene details fetch release=%q", release)
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
		d.log().Tracef("metadata: scene details fetch status release=%q status=%d", release, resp.StatusCode)
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
	d.log().Tracef("metadata: scene details fetched release=%q files=%d archived=%d", release, len(payload.Files), len(payload.ArchivedFiles))
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
	// srrdbIMDBMaxPages is a safety backstop on the imdb: fan-out. Pagination
	// normally stops naturally once resultsCount is collected; this only bounds a
	// pathological response. It is set well above any real title's scene release
	// count (the largest observed are well under 100) so completeness is not the
	// limiting factor — 25 pages = 1000 releases.
	srrdbIMDBMaxPages = 25
)

// sceneIMDbID returns the resolved IMDb id on meta in precedence order. Detection
// runs in ApplyMediaDetails, after ResolveExternalIDs, so meta.ExternalIDs.IMDBID
// is normally populated; 0 means "no id known" and the imdb: search is skipped in
// favor of the r: fallback.
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

// srrdbTVWordQuery builds the low-fan-out TV query SRRDB accepts after word:.
// It returns "<title> SxxEyy" when an episode is known and "<title> Sxx" when
// only the season is known. A normalized TV category, title, and season are
// required so movie-like releases do not take the TV-only path.
func srrdbTVWordQuery(meta api.PreparedMetadata) string {
	if !isSRRDBTVCategory(meta) {
		return ""
	}
	title := strings.Join(strings.Fields(meta.Release.Title), " ")
	if title == "" {
		return ""
	}
	season, episode := meta.SeasonEpisodeWithParsedFallback()
	if season <= 0 {
		return ""
	}
	if episode <= 0 {
		return fmt.Sprintf("%s S%02d", title, season)
	}
	return fmt.Sprintf("%s S%02dE%02d", title, season, episode)
}

// isSRRDBTVCategory reports whether scene detection should use TV-specific SRRDB
// search behavior for the parsed release category.
func isSRRDBTVCategory(meta api.PreparedMetadata) bool {
	return api.NormalizeCategory(meta.Release.Category) == api.CategoryTV
}

// fetchIMDBReleases lists every scene release for an IMDb id via the imdb:
// search, paginating up to srrdbIMDBMaxPages. The id is a validated integer, so
// the URL cannot be influenced by parsed metadata (no SSRF surface).
func (d *srrdbDetector) fetchIMDBReleases(ctx context.Context, meta api.PreparedMetadata, imdbID int) ([]srrdbSearchResult, error) {
	if imdbID <= 0 {
		return nil, nil
	}
	all := make([]srrdbSearchResult, 0, srrdbIMDBPageSize)
	total := -1
	for page := 1; page <= srrdbIMDBMaxPages; page++ {
		payload, err := d.fetchIMDBReleasePage(ctx, meta, imdbID, page)
		if err != nil {
			return all, err
		}
		all = append(all, payload.Results...)
		total = payload.ResultsCount
		d.log().Tracef("metadata: scene imdb search page imdb=%d page=%d results=%d total=%d collected=%d", imdbID, page, len(payload.Results), total, len(all))
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

func (d *srrdbDetector) fetchIMDBReleasePage(ctx context.Context, meta api.PreparedMetadata, imdbID, page int) (srrdbIMDBSearchResponse, error) {
	imdb := formatSRRDBIMDbID(imdbID)
	if imdb == "" {
		return srrdbIMDBSearchResponse{}, nil
	}
	endpoint := fmt.Sprintf("%s/v1/search/imdb:%s%s/order:date/page:%d", strings.TrimRight(d.baseURL, "/"), imdb, srrdbIMDBForeignFilterPath(meta), page)
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
		d.log().Tracef("metadata: scene imdb search status imdb=%d page=%d status=%d", imdbID, page, resp.StatusCode)
		return srrdbIMDBSearchResponse{}, nil
	}
	var payload srrdbIMDBSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return srrdbIMDBSearchResponse{}, fmt.Errorf("scene: decode imdb search: %w", err)
	}
	return payload, nil
}

// srrdbIMDBForeignFilterPath narrows the broad imdb: release listing by the
// parsed release language. Empty or English-only release names search
// non-foreign entries; any explicit non-English marker searches foreign entries.
func srrdbIMDBForeignFilterPath(meta api.PreparedMetadata) string {
	for _, language := range meta.Release.Language {
		switch strings.ToLower(strings.TrimSpace(language)) {
		case "":
			continue
		case "english", "en", "eng":
			continue
		default:
			return "/foreign:yes"
		}
	}
	return "/foreign:no"
}

// sceneMediaExtensions are the archive members treated as the primary media file
// for rename detection.
var sceneMediaExtensions = map[string]struct{}{
	".mkv": {}, ".mp4": {}, ".avi": {}, ".ts": {}, ".m2ts": {},
	".vob": {}, ".iso": {}, ".wmv": {}, ".mov": {}, ".m4v": {},
}
