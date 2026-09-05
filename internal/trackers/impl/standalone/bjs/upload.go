// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://bj-share.info"
	uploadURL  = baseURL + "/upload.php"
	torrentURL = baseURL + "/torrents.php?torrentid="
	sourceFlag = "BJ"
)

var idPattern = regexp.MustCompile(`action=download&id=(\d+)|torrentid=(\d+)`)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	blockedReason string
	questionnaire *api.TrackerQuestionnaire
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	state, cookies, err := prepareUploadState(ctx, req, req.Intent != trackers.PreparationIntentUpload)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: BJS %s", state.blockedReason)
	}
	files := []commonhttp.FileField{{
		FieldName: "file_input",
		FileName:  filepath.Base(state.torrentPath),
		Path:      state.torrentPath,
	}}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, files)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "BJS")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, cookies, body, contentType, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	cookies []*http.Cookie,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BJS request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	httpReq.Header.Set("Referer", uploadURL)
	commonhttp.ApplyCookies(httpReq, cookies)
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BJS upload request: %w", err)
	}
	defer resp.Body.Close()
	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(
		resp,
		resp.StatusCode >= 200 && resp.StatusCode < 400,
		commonhttp.DefaultResponsePreviewBytes,
	)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BJS read upload response: %w", err)
	}
	match := idPattern.FindStringSubmatch(finalURL + "\n" + string(responseBody))
	id := metautil.FirstNonEmptyTrimmed(matchValue(match, 1), matchValue(match, 2))
	if resp.StatusCode >= 200 && resp.StatusCode < 400 && id != "" {
		tURL := torrentURL + id
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "BJS", state.torrentPath, artifactPath, announceURL, sourceFlag,
		)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "BJS",
				TorrentID:   id,
				TorrentURL:  tURL,
				TorrentPath: registeredPath,
			}},
		}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "BJS", "upload_failure", responsePreview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("BJS", resp.StatusCode, responsePreview)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	fields := maps.Clone(state.fields)
	if _, ok := fields["auth"]; ok {
		fields["auth"] = "[redacted]"
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "BJS",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "bjs",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          fields,
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput, dryRun bool) (uploadState, []*http.Cookie, error) {
	cookies, err := loadCookies(ctx, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	auth := "dry-run-auth"
	if !dryRun {
		auth, err = fetchAuth(ctx, cookies)
		if err != nil {
			return uploadState{}, nil, err
		}
	}
	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)
	fields := buildFields(req.Meta, description, auth, standalone.QuestionnaireAnswers(req.Meta, "BJS"))
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: BJS reviewed upload name: %w", err)
	}
	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
		fields:        fields,
		questionnaire: buildQuestionnaire(req.Meta, fields),
	}
	if reason := validateFields(fields); reason != "" {
		state.blockedReason = reason
	}
	return state, cookies, nil
}

func buildFields(meta api.UploadSubject, description string, auth string, answers map[string]string) map[string]string {
	width, height := resolveResolution(meta)
	runtimeMinutes := resolveRuntime(meta)
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)

	var tmdbOriginalTitle, tmdbTitle string
	if meta.ProviderMetadata.TMDB != nil {
		tmdbOriginalTitle = meta.ProviderMetadata.TMDB.OriginalTitle
		tmdbTitle = meta.ProviderMetadata.TMDB.Title
	}

	fields := map[string]string{
		"audio":            resolveAudio(meta),
		"auth":             auth,
		"codecaudio":       resolveAudioCodec(meta),
		"codecvideo":       resolveVideoCodec(meta),
		"duracaoHR":        strconv.Itoa(runtimeMinutes / 60),
		"duracaoMIN":       strconv.Itoa(runtimeMinutes % 60),
		"duracaotipo":      "selectbox",
		"fichatecnica":     description,
		"formato":          resolveContainer(meta),
		"idioma":           resolveLanguage(meta),
		"imdblink":         resolveIDLink(meta),
		"qualidade":        resolveQuality(meta),
		"release":          strings.TrimSpace(meta.ServiceLongName),
		"remaster_title":   resolveRemasterTitle(meta),
		"resolucaoh":       height,
		"resolucaow":       width,
		"sinopse":          metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["overview"]), resolveOverview(meta, ptBR)),
		"submit":           "true",
		"tags":             metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["tags"]), resolveTags(meta, ptBR)),
		"tipolegenda":      resolveSubtitle(meta),
		"title":            metautil.FirstNonEmptyTrimmed(tmdbOriginalTitle, meta.Release.Title),
		"titulobrasileiro": metautil.FirstNonEmptyTrimmed(ptBR.Title, tmdbTitle, meta.Release.Title),
		"traileryoutube":   resolveYouTube(meta, ptBR),
		"type":             resolveType(meta),
		"year":             resolveYearLabel(meta),
	}
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	if category == "MOVIE" {
		fields["adulto"] = resolveAdult(meta)
		fields["diretor"] = resolveDirectors(meta)
		fields["datalancamento"] = resolveReleaseDate(meta)
	}
	if category == "TV" {
		fields["diretor"] = resolveCreators(meta)
		if meta.TVPack {
			fields["tipo"] = "season"
		} else {
			fields["tipo"] = "episode"
		}
		fields["season"] = strconv.Itoa(meta.SeasonInt)
		fields["episode"] = strconv.Itoa(meta.EpisodeInt)
		fields["network"] = resolveNetworks(meta)

		numSeasons := 0
		if meta.ProviderMetadata.IMDB != nil {
			numSeasons = len(meta.ProviderMetadata.IMDB.SeasonsSummary)
		}
		fields["numtemporadas"] = strconv.Itoa(numSeasons)

		var originCountry []string
		if meta.ProviderMetadata.TMDB != nil {
			for _, code := range meta.ProviderMetadata.TMDB.OriginCountry {
				originCountry = append(originCountry, metautil.ISO3166PortugueseName(code, code))
			}
		}
		fields["pais"] = strings.Join(originCountry, ", ")

		fields["diretorserie"] = resolveDirectors(meta)
		fields["avaliacao"] = resolveIMDbRating(meta)
		fields["datalancamento"] = resolveReleaseDate(meta)
	}
	if !meta.Anime {
		fields["validimdb"] = "yes"
		fields["imdbrating"] = resolveIMDbRating(meta)
		fields["elenco"] = resolveCast(meta)
	}
	if meta.Anime && category == "MOVIE" {
		fields["tipo"] = "movie"
	}
	if meta.Anime && category == "TV" {
		fields["adulto"] = resolveAdult(meta)
	}
	if strings.TrimSpace(meta.Repack) != "" {
		fields["repack"] = "on"
	}
	if resolvePoster(meta) != "" {
		fields["image"] = resolvePoster(meta)
	}
	screens := resolveScreens(meta)
	if len(screens) > 0 {
		fields["screenshots[]"] = strings.Join(screens, ",")
	}
	return fields
}

func resolveIDLink(meta api.UploadSubject) string {
	if meta.Identity.IMDBID > 0 {
		return providerid.IMDb(meta.Identity.IMDBID).Prefixed()
	}
	if meta.Identity.TMDBID > 0 {
		if strings.EqualFold(categoryOf(meta), "TV") {
			return fmt.Sprintf("tv/%d", meta.Identity.TMDBID)
		}
		return fmt.Sprintf("movie/%d", meta.Identity.TMDBID)
	}
	return ""
}

// shouldUseScopedTVOverview reports whether BJS should prefer season or
// episode localized overview over title-level synopsis text.
func shouldUseScopedTVOverview(meta api.UploadSubject) bool {
	if meta.SeasonInt <= 0 {
		return false
	}
	if !isTVUpload(meta) {
		return false
	}
	if meta.TVPack {
		return true
	}
	return meta.EpisodeInt > 0
}

// isTVUpload reports whether canonical identity classifies the upload as TV.

func resolveYouTube(meta api.UploadSubject, ptBR api.TMDBLocalizedData) string {
	if ptBR.TrailerURL != "" {
		return strings.TrimSpace(ptBR.TrailerURL)
	}
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveNetworks(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.Networks) > 0 {
		names := make([]string, 0, len(meta.ProviderMetadata.TMDB.Networks))
		for _, n := range meta.ProviderMetadata.TMDB.Networks {
			if strings.TrimSpace(n.Name) != "" {
				names = append(names, strings.TrimSpace(n.Name))
			}
		}
		return strings.Join(names, ", ")
	}
	return ""
}

func resolveReleaseDate(meta api.UploadSubject) string {
	rawDate := ""
	if meta.ProviderMetadata.TMDB != nil {
		rawDate = metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.ReleaseDate, meta.ProviderMetadata.TMDB.FirstAirDate)
	}
	if rawDate == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", rawDate)
	if err == nil {
		return t.Format("02 Jan 2006")
	}
	return ""
}

func resolveYearLabel(meta api.UploadSubject) string {
	year := resolveYear(meta)
	if strings.EqualFold(categoryOf(meta), "TV") {
		if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.EndYear > 0 {
			return fmt.Sprintf("%d-%d", year, meta.ProviderMetadata.IMDB.EndYear)
		}
		return fmt.Sprintf("%d-", year)
	}
	return strconv.Itoa(year)
}

func resolveRemasterTitle(meta api.UploadSubject) string {
	var tags []string

	edition := strings.TrimSpace(meta.Edition)
	editionLower := strings.ToLower(edition)
	editionEntries := []struct{ keyword, label string }{
		{"director's cut", "Director's Cut"},
		{"extended", "Extended Edition"},
		{"imax", "IMAX"},
		{"open matte", "Open Matte"},
		{"noir", "Noir Edition"},
		{"theatrical", "Theatrical Cut"},
		{"uncut", "Uncut"},
		{"unrated", "Unrated"},
		{"uncensored", "Uncensored"},
	}
	for _, entry := range editionEntries {
		if strings.Contains(editionLower, entry.keyword) {
			tags = append(tags, entry.label)
			break
		}
	}

	audio := strings.ToUpper(strings.TrimSpace(meta.Audio))
	if strings.Contains(audio, "ATMOS") {
		tags = append(tags, "Dolby Atmos")
	}

	if meta.BitDepth == "10" {
		tags = append(tags, "10-bit")
	}

	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	if strings.Contains(hdr, "DV") || strings.Contains(hdr, "DOLBY VISION") {
		tags = append(tags, "Dolby Vision")
	}
	if strings.Contains(hdr, "HDR10+") {
		tags = append(tags, "HDR10+")
	}
	if strings.Contains(hdr, "HDR") && !strings.Contains(hdr, "HDR10+") {
		tags = append(tags, "HDR10")
	}

	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		tags = append(tags, "Remux")
	}

	if meta.HasCommentary {
		tags = append(tags, "Com comentários")
	}

	priority := []string{
		"Dolby Atmos",
		"Remux",
		"Director's Cut",
		"Extended Edition",
		"IMAX",
		"Open Matte",
		"Noir Edition",
		"Theatrical Cut",
		"Uncut",
		"Unrated",
		"Uncensored",
		"10-bit",
		"Dolby Vision",
		"HDR10+",
		"HDR10",
		"Com extras",
		"Com comentários",
	}

	tagSet := make(map[string]bool)
	for _, t := range tags {
		tagSet[t] = true
	}

	var ordered []string
	for _, p := range priority {
		if tagSet[p] {
			ordered = append(ordered, p)
		}
	}

	return strings.Join(ordered, " / ")
}

func resolveYear(meta api.UploadSubject) int {
	if meta.ProviderMetadata.TMDB != nil && meta.ProviderMetadata.TMDB.Year > 0 {
		return meta.ProviderMetadata.TMDB.Year
	}
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Year > 0 {
		return meta.ProviderMetadata.IMDB.Year
	}
	return meta.Release.Year
}

func resolveCreators(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.Creators) > 0 {
		return firstTrimmed(meta.ProviderMetadata.TMDB.Creators)
	}
	if meta.ProviderMetadata.IMDB != nil {
		names := make([]string, 0, len(meta.ProviderMetadata.IMDB.Creators))
		for _, p := range meta.ProviderMetadata.IMDB.Creators {
			if strings.TrimSpace(p.Name) != "" {
				names = append(names, strings.TrimSpace(p.Name))
			}
		}
		if len(names) > 0 {
			return names[0]
		}
	}
	return ""
}

func resolveCast(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.Cast) > 0 {
		return strings.Join(firstNTrimmed(meta.ProviderMetadata.TMDB.Cast, 5), ", ")
	}
	if meta.ProviderMetadata.IMDB != nil {
		names := make([]string, 0, len(meta.ProviderMetadata.IMDB.Stars))
		for _, p := range meta.ProviderMetadata.IMDB.Stars {
			if strings.TrimSpace(p.Name) != "" {
				names = append(names, strings.TrimSpace(p.Name))
			}
		}
		return strings.Join(firstNTrimmed(names, 5), ", ")
	}
	return ""
}

func resolveIMDbRating(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Rating > 0 {
		return strconv.FormatFloat(meta.ProviderMetadata.IMDB.Rating, 'f', 1, 64)
	}
	return ""
}

func resolveLogo(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo) != "" {
		return "https://image.tmdb.org/t/p/w300/" + strings.TrimPrefix(strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo), "/")
	}
	return ""
}

func firstTrimmed(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNTrimmed(values []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
		if len(out) == limit {
			break
		}
	}
	return out
}

func matchValue(values []string, idx int) string {
	if idx >= 0 && idx < len(values) {
		return values[idx]
	}
	return ""
}
