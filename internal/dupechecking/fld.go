package dupechecking

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type fldHandler struct {
	cfg     config.Config
	http    *http.Client
	baseURL string
}

func (h fldHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	_, apiKey, ok := trackerCfgWithAPIKey(h.cfg, "FLD")
	if !ok {
		return nil, []string{noteSkip("missing api_key for tracker FLD")}, nil
	}

	tmdb := meta.ExternalIDs.TMDBID
	if tmdb == 0 {
		return nil, []string{noteSkip("missing tmdb id for FLD dupe search")}, nil
	}

	category := strings.ToUpper(strings.TrimSpace(meta.ExternalIDs.Category))
	if category == "" {
		category = strings.ToUpper(strings.TrimSpace(meta.MediaInfoCategory))
	}

	params := url.Values{}
	if category == "TV" {
		params.Set("tmdb_id", fmt.Sprintf("tv/%d", tmdb))
		if meta.SeasonInt > 0 {
			params.Set("show_season_number", strconv.Itoa(meta.SeasonInt))
		}
		if meta.EpisodeInt > 0 {
			params.Set("show_episode_number", strconv.Itoa(meta.EpisodeInt))
		}
	} else {
		params.Set("tmdb_id", fmt.Sprintf("movie/%d", tmdb))
	}

	headers := map[string]string{
		"Authorization": "Bearer " + apiKey,
	}

	apiURL := "https://flood.st/api/torrents"
	if h.baseURL != "" {
		apiURL = h.baseURL
	}

	status, payload, err := doJSONGetAny(ctx, h.http, apiURL, params, headers)
	if err != nil || status < 200 || status >= 300 {
		return nil, []string{noteSkip("FLD search failed")}, nil
	}

	rawMap, ok := payload.(map[string]any)
	if !ok {
		return nil, nil, nil
	}

	itemsSlice, ok := anyToSlice(rawMap["items"])
	if !ok {
		return nil, nil, nil
	}

	entries := make([]api.DupeEntry, 0, len(itemsSlice))
	for _, raw := range itemsSlice {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := stringFromAny(item["id"])
		name := stringFromAny(item["name"])
		mainURL := stringFromAny(item["main_url"])
		size := intFromAny(item["size"])

		entry := api.DupeEntry{
			Name: name,
			ID:   id,
			Link: mainURL,
		}
		if size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		entries = append(entries, entry)
	}

	return entries, nil, nil
}
