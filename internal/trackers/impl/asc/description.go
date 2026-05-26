// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

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
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type layoutData struct {
	Images map[string]string
}

func buildDescription(ctx context.Context, meta api.PreparedMetadata, cfg config.Config, assets trackers.DescriptionAssets, layoutID string) string {
	if assets.Override && strings.TrimSpace(assets.Description) != "" {
		return strings.TrimSpace(assets.Description)
	}
	layout, _ := fetchLayout(ctx, cfg.MainSettings.DBPath, meta, layoutID)
	parts := []string{"[center]"}

	for idx := 1; idx <= 3; idx++ {
		if image := layout.Images[fmt.Sprintf("BARRINHA_CUSTOM_T_%d", idx)]; image != "" {
			parts = append(parts, formatImage(image))
		}
	}
	if image := layout.Images["BARRINHA_APRESENTA"]; image != "" {
		parts = append(parts, formatImage(image))
	}
	parts = append(parts, "[size=3]"+resolveUploadTitle(meta)+"[/size]")
	appendSection := func(key string, content string) {
		if strings.TrimSpace(content) == "" {
			return
		}
		if image := layout.Images[key]; image != "" {
			parts = append(parts, formatImage(image))
		}
		parts = append(parts, content)
	}

	if poster := resolvePoster(meta); poster != "" {
		poster = strings.ReplaceAll(poster, "/t/p/original/", "/t/p/w500/")
		appendSection("BARRINHA_CAPA", formatImage(poster))
	}
	appendSection("BARRINHA_SINOPSE", resolveOverview(meta, questionnaireAnswers(meta)))
	appendSection("BARRINHA_FICHA_TECNICA", buildTechnicalSheet(meta))
	appendSection("BARRINHA_ELENCO", buildCastSection(meta))

	if media := buildMediaInfo(meta, cfg.MainSettings.DBPath); media != "" {
		parts = append(parts, "[spoiler=Informações do Arquivo]\n[left][font=Courier New]"+media+"[/font][/left][/spoiler]")
	}
	if notes := sanitizeDescriptionNotes(assets.Description); notes != "" {
		parts = append(parts, notes)
	}
	if customHeader := strings.TrimSpace(cfg.Description.CustomDescriptionHeader); customHeader != "" {
		parts = append(parts, customHeader)
	}

	for idx := 1; idx <= 3; idx++ {
		if image := layout.Images[fmt.Sprintf("BARRINHA_CUSTOM_B_%d", idx)]; image != "" {
			parts = append(parts, formatImage(image))
		}
	}
	parts = append(parts, "[/center]")
	parts = append(parts, "[center][url=https://github.com/autobrr/upbrr]Upload realizado via upbrr[/url][/center]")
	return strings.TrimSpace(strings.Join(filterEmpty(parts), "\n\n"))
}

func fetchLayout(ctx context.Context, dbPath string, meta api.PreparedMetadata, layoutID string) (layoutData, error) {
	cached, err := readLayoutCache(dbPath, layoutID)
	if err == nil {
		return cached, nil
	}
	cookies, _, err := LoadCookies(ctx, dbPath)
	if err != nil {
		return layoutData{}, err
	}
	form := url.Values{
		"imdb":   {firstNonEmpty(resolveIMDbIDText(meta), "tt0013442")},
		"layout": {firstNonEmpty(strings.TrimSpace(layoutID), "2")},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/search.php", strings.NewReader(form.Encode()))
	if err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC create layout request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(req)
	if err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC fetch layout: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return layoutData{}, fmt.Errorf("layout fetch status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC read layout response: %w", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC unmarshal layout response: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload["ASC"], &raw); err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC unmarshal layout data: %w", err)
	}
	layout := normalizeLayout(raw)
	_ = writeLayoutCache(dbPath, layoutID, payload["ASC"])
	return layout, nil
}

func normalizeLayout(raw map[string]any) layoutData {
	layout := layoutData{Images: make(map[string]string)}
	for key, value := range raw {
		if !strings.HasPrefix(key, "BARRINHA_") {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			layout.Images[key] = text
		}
	}
	return layout
}

func readLayoutCache(dbPath string, layoutID string) (layoutData, error) {
	payload, err := os.ReadFile(layoutCachePath(dbPath, layoutID))
	if err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC read layout cache: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC unmarshal layout cache: %w", err)
	}
	return normalizeLayout(raw), nil
}

func writeLayoutCache(dbPath string, layoutID string, payload []byte) error {
	path := layoutCachePath(dbPath, layoutID)
	if path == "" {
		return errors.New("missing layout cache path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("trackers: ASC create layout cache dir: %w", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("trackers: ASC write layout cache: %w", err)
	}
	return nil
}

func layoutCachePath(dbPath string, layoutID string) string {
	if strings.TrimSpace(dbPath) == "" {
		return ""
	}
	cacheRoot, err := db.Subdir(dbPath, "cache")
	if err != nil {
		return ""
	}
	return filepath.Join(cacheRoot, "asc_layout_"+firstNonEmpty(strings.TrimSpace(layoutID), "2")+".json")
}

func buildTechnicalSheet(meta api.PreparedMetadata) string {
	items := make([]string, 0, 4)
	if runtime := resolveRuntime(meta); runtime != "" {
		items = append(items, "Duração: "+runtime)
	}
	if countries := resolveCountries(meta); countries != "" {
		items = append(items, "País de Origem: "+countries)
	}
	if genres := resolveGenres(meta, questionnaireAnswers(meta)); genres != "" {
		items = append(items, "Gêneros: "+genres)
	}
	if releaseDate := resolveReleaseDate(meta); releaseDate != "" {
		items = append(items, "Data de Lançamento: "+releaseDate)
	}
	return strings.Join(items, "\n")
}

func buildCastSection(meta api.PreparedMetadata) string {
	names := resolveCast(meta)
	if len(names) == 0 {
		return ""
	}
	limit := len(names)
	if limit > 10 {
		limit = 10
	}
	parts := make([]string, 0, limit)
	for idx := 0; idx < limit; idx++ {
		parts = append(parts, "[size=2][b]"+names[idx]+"[/b][/size]")
	}
	return strings.Join(parts, "\n")
}

func buildMediaInfo(meta api.PreparedMetadata, dbPath string) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		text, _ := readBDSummary(meta, dbPath)
		return text
	case "DVD":
		return firstNonEmpty(strings.TrimSpace(meta.DVDVOBMediaInfoText), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
	default:
		return readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath))
	}
}

func readBDSummary(meta api.PreparedMetadata, dbPath string) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", err
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", err
	}
	return readTextFile(paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylist(meta)))
}

func sanitizeDescriptionNotes(value string) string {
	replacer := strings.NewReplacer(
		"[user]", "", "[/user]", "",
		"[align=left]", "", "[/align]", "",
		"[align=right]", "", "[/align]", "",
		"[alert]", "", "[/alert]", "",
		"[note]", "", "[/note]", "",
		"[h1]", "[u][b]", "[/h1]", "[/b][/u]",
		"[h2]", "[u][b]", "[/h2]", "[/b][/u]",
		"[h3]", "[u][b]", "[/h3]", "[/b][/u]",
	)
	return strings.TrimSpace(replacer.Replace(value))
}

func formatImage(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "[img]" + strings.TrimSpace(value) + "[/img]"
}

func filterEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}
