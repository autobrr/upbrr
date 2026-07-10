// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder for tracker images
	_ "image/jpeg" // register JPEG decoder for tracker images
	_ "image/png"  // register PNG decoder for tracker images
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path" //nolint:depguard // Builds Unit3D API URL paths, not local filesystem paths.
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	imageTimeout     = 15 * time.Second
	maxImageBytes    = 20 * 1024 * 1024
	imageConcurrency = 5
)

var unit3DImageBlockedIPRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

var unit3DCategoryNamesByID = map[string][]string{
	"1": {"MOVIE"},
	"2": {"TV"},
}

var unit3DTypeNamesByID = map[string][]string{
	"1": {"DISC"},
	"2": {"REMUX"},
	"3": {"ENCODE", "DVDRIP"},
	"4": {"WEBDL"},
	"5": {"WEBRIP"},
	"6": {"HDTV"},
}

var unit3DResolutionNamesByID = map[string][]string{
	"10": {"8640P"},
	"1":  {"4320P"},
	"2":  {"2160P"},
	"3":  {"1080P", "1440P"},
	"4":  {"1080I"},
	"5":  {"720P"},
	"6":  {"576P"},
	"7":  {"576I"},
	"8":  {"480P"},
	"9":  {"480I"},
}

func IsUnit3DTracker(tracker string) bool {
	return trackers.IsUnit3DTracker(tracker)
}

func IsUnit3DTrackerWithConfig(cfg config.Config, tracker string) bool {
	key := strings.ToUpper(strings.TrimSpace(tracker))
	if key == "" {
		return false
	}
	if IsUnit3DTracker(key) {
		return true
	}
	if trackers.IsKnownTracker(key) {
		return false
	}
	entry, ok := cfg.Trackers.Trackers[key]
	if !ok {
		for name, candidate := range cfg.Trackers.Trackers {
			if strings.EqualFold(name, key) {
				entry = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		return false
	}
	if strings.TrimSpace(entry.APIKey) == "" {
		return false
	}
	if strings.TrimSpace(entry.AnnounceURL) == "" {
		return false
	}
	if strings.TrimSpace(entry.Username) != "" || strings.TrimSpace(entry.Password) != "" || strings.TrimSpace(entry.Passkey) != "" {
		return false
	}
	if strings.TrimSpace(entry.PTPAPIUser) != "" || strings.TrimSpace(entry.PTPAPIKey) != "" {
		return false
	}
	return true
}

func CategoryID(category string) string {
	return reverseLookupCanonicalID(CanonicalUnit3DCategory(category), unit3DCategoryNamesByID)
}

func TypeID(typeValue string) string {
	return reverseLookupCanonicalID(CanonicalUnit3DType(typeValue), unit3DTypeNamesByID)
}

func ResolutionID(value string) string {
	return reverseLookupCanonicalID(CanonicalUnit3DResolution(value), unit3DResolutionNamesByID)
}

func CategoryName(id string) string {
	return firstCanonicalValue(CategoryNames(id))
}

func CategoryNames(id string) []string {
	return copyCanonicalValues(unit3DCategoryNamesByID[strings.TrimSpace(id)])
}

func TypeName(id string) string {
	return firstCanonicalValue(TypeNames(id))
}

func TypeNames(id string) []string {
	return copyCanonicalValues(unit3DTypeNamesByID[strings.TrimSpace(id)])
}

func ResolutionName(id string) string {
	return firstCanonicalValue(ResolutionNames(id))
}

func ResolutionNames(id string) []string {
	return copyCanonicalValues(unit3DResolutionNamesByID[strings.TrimSpace(id)])
}

func CanonicalUnit3DCategory(value string) string {
	switch normalizeUnit3DLookupKey(value) {
	case "MOVIE", "FILM":
		return "MOVIE"
	case "TV", "TELEVISION", "SHOW", "SERIES", "TVSHOW", "EPISODE":
		return "TV"
	default:
		return ""
	}
}

func CanonicalUnit3DType(value string) string {
	switch normalizeUnit3DLookupKey(value) {
	case "DISC":
		return "DISC"
	case "REMUX":
		return "REMUX"
	case "ENCODE":
		return "ENCODE"
	case "DVDRIP":
		return "DVDRIP"
	case "WEBDL":
		return "WEBDL"
	case "WEBRIP":
		return "WEBRIP"
	case "HDTV", "UHDTV":
		return "HDTV"
	default:
		return ""
	}
}

func CanonicalUnit3DResolution(value string) string {
	switch normalizeUnit3DLookupKey(value) {
	case "8640P":
		return "8640P"
	case "4320P":
		return "4320P"
	case "2160P":
		return "2160P"
	case "1440P":
		return "1440P"
	case "1080P":
		return "1080P"
	case "1080I":
		return "1080I"
	case "720P":
		return "720P"
	case "576P":
		return "576P"
	case "576I":
		return "576I"
	case "480P":
		return "480P"
	case "480I":
		return "480I"
	default:
		return ""
	}
}

func normalizeUnit3DLookupKey(value string) string {
	return strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToUpper(strings.TrimSpace(value)))
}

func reverseLookupCanonicalID(canonical string, namesByID map[string][]string) string {
	if canonical == "" {
		return ""
	}
	for id, names := range namesByID {
		if slices.Contains(names, canonical) {
			return id
		}
	}
	return ""
}

func firstCanonicalValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func copyCanonicalValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string{}, values...)
}

func TrackerAPIKey(cfg config.Config, tracker string) string {
	key := strings.ToUpper(strings.TrimSpace(tracker))
	if key == "" {
		return ""
	}
	if entry, ok := cfg.Trackers.Trackers[key]; ok {
		return strings.TrimSpace(entry.APIKey)
	}
	if entry, ok := cfg.Trackers.Trackers[strings.ToLower(key)]; ok {
		return strings.TrimSpace(entry.APIKey)
	}
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(name, key) {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}

func (c *Client) TorrentInfo(ctx context.Context, tracker string, id string, fileName string, onlyID bool, keepImages bool) (Result, error) {
	return c.lookupUnit3D(ctx, tracker, id, fileName, onlyID, keepImages)
}

func (c *Client) lookupUnit3D(ctx context.Context, tracker string, id string, fileName string, onlyID bool, keepImages bool) (Result, error) {
	baseURL, ok := baseURLForTracker(tracker)
	if !ok {
		return Result{}, fmt.Errorf("unit3d: unknown tracker %q", tracker)
	}

	apiKey := strings.TrimSpace(TrackerAPIKey(c.cfg, tracker))
	params := url.Values{}
	if apiKey == "" {
		c.logger.Debugf("unit3d: %s missing API key; request will be unauthenticated", tracker)
	}

	var endpoint string
	switch {
	case strings.TrimSpace(id) != "":
		endpoint = baseURL + "/api/torrents/" + strings.TrimSpace(id)
	case strings.TrimSpace(fileName) != "":
		endpoint = baseURL + "/api/torrents/filter"
		params.Set("file_name", fileName)
	default:
		return Result{}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, fmt.Errorf("unit3d: request: %w", err)
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	SetUnit3DAPIHeaders(req, apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("unit3d: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.logger.Debugf("unit3d: %s request failed (status=%d id=%q file=%q)", tracker, resp.StatusCode, strings.TrimSpace(id), strings.TrimSpace(fileName))
		return Result{}, nil
	}

	var payload unit3dResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("unit3d: decode: %w", err)
	}
	attrs := payload.extractAttributes(strings.TrimSpace(id) != "")
	if attrs == nil {
		c.logger.Debugf("unit3d: %s response contained no attributes (id=%q file=%q)", tracker, strings.TrimSpace(id), strings.TrimSpace(fileName))
		return Result{}, nil
	}

	result := Result{
		TrackerID: strings.TrimSpace(id),
		TMDBID:    attrs.tmdbID,
		IMDBID:    attrs.imdbID,
		TVDBID:    attrs.tvdbID,
		MALID:     attrs.malID,
		Category:  strings.TrimSpace(attrs.category),
		InfoHash:  strings.TrimSpace(attrs.infoHash),
		FileName:  attrs.fileName,
	}

	description := strings.TrimSpace(attrs.description)
	if description == "" {
		return result, nil
	}
	reports := make([]descriptionunit3d.Report, 0, 2)
	cleaned := ""
	if !onlyID {
		report := descriptionunit3d.CleanDescriptionBody(description, baseURL)
		reports = append(reports, report)
		cleaned = report.Description
	}
	images := []bbcode.Image(nil)
	if keepImages {
		report := descriptionunit3d.CleanDescriptionImages(description, baseURL)
		reports = append(reports, report)
		images = convertCleanedUnit3DImages(report.Images)
	}
	cleanedLen := len(cleaned)
	imageCount := len(images)
	validated := []bbcode.Image(nil)
	if keepImages {
		validated = validateImages(ctx, c.http, images)
		images = validated
	} else {
		images = nil
	}
	result.Description = cleaned
	result.Images = images
	result.Validated = validated
	c.logger.Debugf("unit3d: %s description raw=%d cleaned=%d images=%d validated=%d onlyID=%t keepImages=%t", tracker, len(description), cleanedLen, imageCount, len(validated), onlyID, keepImages)
	for _, report := range reports {
		for _, note := range report.Notes {
			c.logger.Debugf("unit3d: %s description note kind=%s msg=%s", tracker, note.Kind, note.Message)
		}
	}

	return result, nil
}

func convertCleanedUnit3DImages(images []descriptionunit3d.Image) []bbcode.Image {
	if len(images) == 0 {
		return nil
	}
	converted := make([]bbcode.Image, 0, len(images))
	for _, image := range images {
		converted = append(converted, bbcode.Image{
			ImgURL: image.ImgURL,
			RawURL: image.RawURL,
			WebURL: image.WebURL,
			Host:   image.Host,
		})
	}
	return converted
}

func (c *Client) SearchTorrents(ctx context.Context, tracker string, params url.Values, isDisc bool) ([]api.DupeEntry, string, error) {
	baseURL, ok := baseURLForTrackerWithConfig(c.cfg, tracker)
	if !ok {
		return nil, "", fmt.Errorf("unit3d: unknown tracker %q", tracker)
	}

	apiKey := strings.TrimSpace(TrackerAPIKey(c.cfg, tracker))
	if apiKey == "" && c.logger != nil {
		c.logger.Debugf("unit3d: %s missing API key; request will be unauthenticated", tracker)
	}

	endpoints := []unit3dSearchEndpoint{{
		url: strings.TrimRight(baseURL, "/") + path.Join("/", "api", "torrents", "filter"),
	}}
	if usesUnit3DPendingSearch(tracker) {
		tmdbID, _ := strconv.Atoi(strings.TrimSpace(params.Get("tmdbId")))
		endpoints = append(endpoints, unit3dSearchEndpoint{
			url:           strings.TrimRight(baseURL, "/") + path.Join("/", "api", "torrents", "pending"),
			pending:       true,
			filterTMDBID:  tmdbID,
			pendingWebURL: strings.TrimRight(baseURL, "/") + "/torrents/pending",
		})
	}

	var entries []api.DupeEntry
	for _, endpoint := range endpoints {
		endpointEntries, warning, err := c.searchUnit3DEndpoint(ctx, tracker, endpoint, params, apiKey, isDisc)
		if err != nil {
			return nil, "", err
		}
		if warning != "" {
			return entries, warning, nil
		}
		entries = append(entries, endpointEntries...)
	}

	return entries, "", nil
}

func (c *Client) searchUnit3DEndpoint(ctx context.Context, tracker string, endpoint unit3dSearchEndpoint, params url.Values, apiKey string, isDisc bool) ([]api.DupeEntry, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("unit3d: request: %w", err)
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	SetUnit3DAPIHeaders(req, apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("unit3d: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if c.logger != nil {
			c.logger.Warnf("unit3d: %s search failed (status=%d)", tracker, resp.StatusCode)
		}
		return nil, fmt.Sprintf("%s search failed (status=%d)", strings.ToUpper(strings.TrimSpace(tracker)), resp.StatusCode), nil
	}

	if endpoint.pending {
		var payload unit3dPendingSearchResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, "", fmt.Errorf("unit3d: decode: %w", err)
		}
		return buildUnit3DPendingEntries(payload.Data, endpoint, isDisc), "", nil
	}

	var payload unit3dSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", fmt.Errorf("unit3d: decode: %w", err)
	}
	return buildUnit3DSearchEntries(payload.Data, isDisc), "", nil
}

// SetUnit3DAPIHeaders applies the authentication and response format expected
// by every Unit3D API request.
func SetUnit3DAPIHeaders(req *http.Request, apiKey string) {
	if req == nil {
		return
	}
	req.Header.Set("Accept", "application/json")
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func buildUnit3DSearchEntries(items []unit3dSearchItem, isDisc bool) []api.DupeEntry {
	entries := make([]api.DupeEntry, 0, len(items))
	for _, item := range items {
		entry := api.DupeEntry{
			Name:        strings.TrimSpace(item.Attributes.Name),
			Trumpable:   item.Attributes.Trumpable,
			Link:        strings.TrimSpace(item.Attributes.DetailsLink),
			Download:    strings.TrimSpace(item.Attributes.DownloadLink),
			ID:          strings.TrimSpace(item.ID.String()),
			Type:        strings.TrimSpace(item.Attributes.Type),
			Res:         strings.TrimSpace(item.Attributes.Resolution),
			Internal:    item.Attributes.Internal,
			BDInfo:      strings.TrimSpace(item.Attributes.BDInfo),
			Description: strings.TrimSpace(item.Attributes.Description),
			Flags:       append([]string{}, item.Attributes.Flags...),
		}

		if sizeValue, err := parseNumberToInt64(item.Attributes.Size); err == nil {
			entry.SizeBytes = sizeValue
			entry.SizeKnown = sizeValue > 0
		} else if raw := strings.TrimSpace(item.Attributes.Size.String()); raw != "" {
			entry.SizeText = raw
		}

		if len(item.Attributes.Files) > 0 {
			entry.FileCount = len(item.Attributes.Files)
			if !isDisc {
				entry.Files = make([]string, 0, len(item.Attributes.Files))
				for _, file := range item.Attributes.Files {
					trimmed := strings.TrimSpace(file.Name)
					if trimmed != "" {
						entry.Files = append(entry.Files, trimmed)
					}
				}
			}
		}

		entries = append(entries, entry)
	}

	return entries
}

func buildUnit3DPendingEntries(items []unit3dPendingSearchItem, endpoint unit3dSearchEndpoint, isDisc bool) []api.DupeEntry {
	entries := make([]api.DupeEntry, 0, len(items))
	for _, item := range items {
		if endpoint.filterTMDBID > 0 && item.TMDBID != endpoint.filterTMDBID {
			continue
		}

		entry := api.DupeEntry{
			Name:        strings.TrimSpace(item.Name),
			Trumpable:   item.Trumpable,
			Link:        endpoint.pendingWebURL,
			Download:    strings.TrimSpace(item.DownloadLink),
			ID:          strings.TrimSpace(item.ID.String()),
			Type:        strings.TrimSpace(item.Type),
			Res:         strings.TrimSpace(item.Resolution),
			Internal:    item.Internal,
			BDInfo:      strings.TrimSpace(item.BDInfo),
			Description: strings.TrimSpace(item.Description),
			Flags:       append([]string{}, item.Flags...),
		}

		if sizeValue, err := parseNumberToInt64(item.Size); err == nil {
			entry.SizeBytes = sizeValue
			entry.SizeKnown = sizeValue > 0
		} else if raw := strings.TrimSpace(item.Size.String()); raw != "" {
			entry.SizeText = raw
		}

		if len(item.Files) > 0 {
			entry.FileCount = len(item.Files)
			if !isDisc {
				entry.Files = make([]string, 0, len(item.Files))
				for _, file := range item.Files {
					trimmed := strings.TrimSpace(file.Name)
					if trimmed != "" {
						entry.Files = append(entry.Files, trimmed)
					}
				}
			}
		}

		entries = append(entries, entry)
	}

	return entries
}

func usesUnit3DPendingSearch(tracker string) bool {
	return strings.EqualFold(tracker, "CBR")
}

func validateImages(ctx context.Context, client *http.Client, images []bbcode.Image) []bbcode.Image {
	if len(images) == 0 {
		return nil
	}
	client = Unit3DImageHTTPClient(client)

	results := make([]bbcode.Image, len(images))
	valid := make([]bool, len(images))
	sem := make(chan struct{}, imageConcurrency)
	var wg sync.WaitGroup

	for idx, img := range images {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if checkImage(ctx, client, img.RawURL) {
				results[idx] = img
				valid[idx] = true
			}
		})
	}
	wg.Wait()

	filtered := make([]bbcode.Image, 0, len(images))
	for idx, ok := range valid {
		if ok {
			filtered = append(filtered, results[idx])
		}
	}
	return filtered
}

func checkImage(ctx context.Context, client *http.Client, rawURL string) bool {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return false
	}
	if err := ValidateUnit3DImageURL(ctx, trimmed); err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trimmed, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	if resp.ContentLength > 0 && resp.ContentLength > maxImageBytes {
		return false
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "image") {
		return false
	}
	limited := io.LimitReader(resp.Body, maxImageBytes)
	if _, _, err := image.DecodeConfig(limited); err != nil {
		return false
	}
	return true
}

// Unit3DImageHTTPClient returns a clone that rejects non-public image redirects and,
// for standard transports, dials only public target IPs.
func Unit3DImageHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: imageTimeout}
	}
	cloned := *client
	if cloned.Timeout == 0 {
		cloned.Timeout = imageTimeout
	}
	if transport, ok := unit3DImagePublicTransport(cloned.Transport); ok {
		cloned.Transport = transport
	}
	checkRedirect := cloned.CheckRedirect
	cloned.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := ValidateUnit3DImageURL(req.Context(), req.URL.String()); err != nil {
			return err
		}
		if checkRedirect != nil {
			return checkRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &cloned
}

func unit3DImagePublicTransport(rt http.RoundTripper) (http.RoundTripper, bool) {
	var transport *http.Transport
	switch typed := rt.(type) {
	case nil:
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return rt, false
		}
		transport = defaultTransport.Clone()
	case *http.Transport:
		transport = typed.Clone()
	default:
		return rt, false
	}

	originalDial := transport.DialContext
	dialer := &net.Dialer{Timeout: imageTimeout}
	transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("parse address: %w", err)
		}
		addrs, err := resolveUnit3DImagePublicAddrs(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, addr := range addrs {
			target := net.JoinHostPort(addr.String(), port)
			var conn net.Conn
			if originalDial != nil {
				conn, err = originalDial(ctx, network, target)
			} else {
				conn, err = dialer.DialContext(ctx, network, target)
			}
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	return transport, true
}

// ValidateUnit3DImageURL rejects non-HTTP(S) or non-public Unit3D image targets.
func ValidateUnit3DImageURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return errors.New("missing host")
	}
	_, err = resolveUnit3DImagePublicAddrs(ctx, host)
	return err
}

func resolveUnit3DImagePublicAddrs(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return nil, errors.New("missing host")
	}
	lowerHost := strings.ToLower(host)
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") || strings.Contains(lowerHost, "%") {
		return nil, fmt.Errorf("blocked private image host %q", host)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if !isUnit3DImagePublicIP(addr) {
			return nil, fmt.Errorf("blocked private image address %q", addr)
		}
		return []netip.Addr{addr}, nil
	}

	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve host %q: %w", host, err)
	}
	addrs := make([]netip.Addr, 0, len(resolved))
	for _, item := range resolved {
		addr, ok := netip.AddrFromSlice(item.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if !isUnit3DImagePublicIP(addr) {
			return nil, fmt.Errorf("blocked private image address %q", addr)
		}
		addrs = append(addrs, addr)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("host %q resolved no public addresses", host)
	}
	return addrs, nil
}

func isUnit3DImagePublicIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	for _, blocked := range unit3DImageBlockedIPRanges {
		if blocked.Contains(addr) {
			return false
		}
	}
	return true
}

func baseURLForTracker(tracker string) (string, bool) {
	return unit3dmeta.BaseURL(tracker)
}

func baseURLForTrackerWithConfig(cfg config.Config, tracker string) (string, bool) {
	key := strings.ToUpper(strings.TrimSpace(tracker))
	if key != "" {
		if entry, ok := cfg.Trackers.Trackers[key]; ok {
			if base := baseFromAnnounce(entry.AnnounceURL); base != "" {
				return base, true
			}
		}
		if entry, ok := cfg.Trackers.Trackers[strings.ToLower(key)]; ok {
			if base := baseFromAnnounce(entry.AnnounceURL); base != "" {
				return base, true
			}
		}
		for name, entry := range cfg.Trackers.Trackers {
			if strings.EqualFold(name, key) {
				if base := baseFromAnnounce(entry.AnnounceURL); base != "" {
					return base, true
				}
			}
		}
	}
	return baseURLForTracker(tracker)
}

func baseFromAnnounce(announce string) string {
	trimmed := strings.TrimSpace(announce)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	parsed.Path = "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func parseNumberToInt64(value json.Number) (int64, error) {
	text := strings.TrimSpace(value.String())
	if text == "" {
		return 0, errors.New("empty number")
	}
	if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
		return parsed, nil
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("parse numeric JSON value %q: %w", text, err)
	}
	return int64(parsed), nil
}

type unit3dResponse struct {
	Data       json.RawMessage `json:"data"`
	Attributes json.RawMessage `json:"attributes"`
}

type unit3dDataItem struct {
	Attributes unit3dAttributes `json:"attributes"`
}

type unit3dAttributes struct {
	Category      string         `json:"category"`
	Description   string         `json:"description"`
	TMDBID        int            `json:"tmdb_id"`
	IMDBID        int            `json:"imdb_id"`
	TVDBID        int            `json:"tvdb_id"`
	MALID         int            `json:"mal_id"`
	InfoHash      string         `json:"info_hash"`
	Files         []unit3dFile   `json:"files"`
	RegionID      int            `json:"region_id"`
	DistributorID int            `json:"distributor_id"`
	RawFileName   string         `json:"file_name"`
	ExtraFiles    []unit3dFile   `json:"file"`
	Other         map[string]any `json:"-"`
}

type unit3dFile struct {
	Name string `json:"name"`
}

type parsedAttributes struct {
	category    string
	description string
	tmdbID      int
	imdbID      int
	tvdbID      int
	malID       int
	infoHash    string
	fileName    string
}

func (r unit3dResponse) extractAttributes(preferTopLevel bool) *parsedAttributes {
	if len(r.Data) > 0 {
		var dataString string
		if err := json.Unmarshal(r.Data, &dataString); err == nil {
			if strings.TrimSpace(dataString) == "404" {
				return nil
			}
		}
		var dataItems []unit3dDataItem
		if err := json.Unmarshal(r.Data, &dataItems); err == nil {
			if len(dataItems) > 0 {
				return parseAttributes(dataItems[0].Attributes)
			}
		}
	}

	if !preferTopLevel || len(r.Attributes) == 0 {
		return nil
	}
	var attrs unit3dAttributes
	if err := json.Unmarshal(r.Attributes, &attrs); err != nil {
		return nil
	}
	return parseAttributes(attrs)
}

func parseAttributes(attrs unit3dAttributes) *parsedAttributes {
	info := &parsedAttributes{
		category:    attrs.Category,
		description: attrs.Description,
		infoHash:    attrs.InfoHash,
	}
	info.tmdbID = normalizeID(attrs.TMDBID)
	info.imdbID = normalizeID(attrs.IMDBID)
	info.tvdbID = normalizeID(attrs.TVDBID)
	info.malID = normalizeID(attrs.MALID)

	fileNames := make([]string, 0, len(attrs.Files))
	for _, file := range attrs.Files {
		trimmed := strings.TrimSpace(file.Name)
		if trimmed == "" {
			continue
		}
		fileNames = append(fileNames, trimmed)
		if len(fileNames) >= 5 {
			break
		}
	}
	if len(fileNames) == 1 {
		info.fileName = fileNames[0]
	} else if len(fileNames) > 1 {
		info.fileName = strings.Join(fileNames, ", ")
	}
	if info.fileName == "" {
		info.fileName = strings.TrimSpace(attrs.RawFileName)
	}
	return info
}

func normalizeID(value int) int {
	if value <= 0 {
		return 0
	}
	return value
}

type unit3dSearchResponse struct {
	Data []unit3dSearchItem `json:"data"`
}

type unit3dSearchEndpoint struct {
	url           string
	pending       bool
	filterTMDBID  int
	pendingWebURL string
}

type unit3dSearchItem struct {
	ID         json.Number       `json:"id"`
	Attributes unit3dSearchAttrs `json:"attributes"`
}

type unit3dSearchAttrs struct {
	Name         string       `json:"name"`
	Size         json.Number  `json:"size"`
	Files        []unit3dFile `json:"files"`
	Trumpable    bool         `json:"trumpable"`
	DetailsLink  string       `json:"details_link"`
	DownloadLink string       `json:"download_link"`
	Type         string       `json:"type"`
	Resolution   string       `json:"resolution"`
	Internal     bool         `json:"internal"`
	BDInfo       string       `json:"bd_info"`
	Description  string       `json:"description"`
	Flags        []string     `json:"flags"`
}

type unit3dPendingSearchResponse struct {
	Data []unit3dPendingSearchItem `json:"data"`
}

type unit3dPendingSearchItem struct {
	ID           json.Number  `json:"id"`
	TMDBID       int          `json:"tmdb_id"`
	Name         string       `json:"name"`
	Size         json.Number  `json:"size"`
	Files        []unit3dFile `json:"files"`
	Trumpable    bool         `json:"trumpable"`
	DownloadLink string       `json:"download_link"`
	Type         string       `json:"type"`
	Resolution   string       `json:"resolution"`
	Internal     bool         `json:"internal"`
	BDInfo       string       `json:"bd_info"`
	Description  string       `json:"description"`
	Flags        []string     `json:"flags"`
}
