// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/autobrr/upbrr/internal/imagehostpolicy"
)

type Config struct {
	MainSettings       MainSettingsConfig             `yaml:"main_settings"`
	ImageHosting       ImageHostingConfig             `yaml:"image_hosting"`
	Metadata           MetadataConfig                 `yaml:"metadata"`
	ScreenshotHandling ScreenshotHandlingConfig       `yaml:"screenshot_handling"`
	Description        DescriptionSettingsConfig      `yaml:"description_settings"`
	ClientSetup        ClientSetupConfig              `yaml:"client_setup"`
	ArrIntegration     ArrIntegrationConfig           `yaml:"arr_integration"`
	TorrentCreation    TorrentCreationConfig          `yaml:"torrent_creation"`
	PostUpload         PostUploadConfig               `yaml:"post_upload"`
	Logging            LoggingConfig                  `yaml:"logging"`
	Trackers           TrackersConfig                 `yaml:"trackers"`
	TorrentClients     map[string]TorrentClientConfig `yaml:"torrent_clients"`
}

const DefaultInputHistoryLimit = 20

type MainSettingsConfig struct {
	UpdateNotification  bool   `yaml:"update_notification"`
	VerboseNotification bool   `yaml:"verbose_notification"`
	TMDBAPI             string `yaml:"tmdb_api"`
	TrackerPassChecks   int    `yaml:"tracker_pass_checks"`
	InputHistoryLimit   int    `yaml:"input_history_limit"`
	DBPath              string `yaml:"db_path"`
}

type mainSettingsConfigAlias MainSettingsConfig

func (c *MainSettingsConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw mainSettingsConfigAlias
	raw.InputHistoryLimit = DefaultInputHistoryLimit
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("config: decode main settings yaml: %w", err)
	}
	*c = MainSettingsConfig(raw)
	return nil
}

func (c *MainSettingsConfig) UnmarshalJSON(data []byte) error {
	var raw mainSettingsConfigAlias
	raw.InputHistoryLimit = DefaultInputHistoryLimit
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("config: decode main settings json: %w", err)
	}
	*c = MainSettingsConfig(raw)
	return nil
}

type ImageHostingConfig struct {
	Host1           string `yaml:"img_host_1"`
	Host2           string `yaml:"img_host_2"`
	Host3           string `yaml:"img_host_3"`
	Host4           string `yaml:"img_host_4"`
	Host5           string `yaml:"img_host_5"`
	Host6           string `yaml:"img_host_6"`
	ImgBBAPI        string `yaml:"imgbb_api"`
	LensdumpAPI     string `yaml:"lensdump_api"`
	PTScreensAPI    string `yaml:"ptscreens_api"`
	OnlyImageAPI    string `yaml:"onlyimage_api"`
	DalexniAPI      string `yaml:"dalexni_api"`
	PassTheImageAPI string `yaml:"passtheima_ge_api"`
	ZiplineURL      string `yaml:"zipline_url"`
	ZiplineAPIKey   string `yaml:"zipline_api_key"`
	SeedpoolCDNAPI  string `yaml:"seedpool_cdn_api"`
	ShareXURL       string `yaml:"sharex_url"`
	ShareXAPIKey    string `yaml:"sharex_api_key"`
	UTPPMAPI        string `yaml:"utppm_api"`
}

type MetadataConfig struct {
	BTNAPI                    string  `yaml:"btn_api"`
	SkipAutoTorrent           bool    `yaml:"skip_auto_torrent"`
	SkipTrackerFilenameLookup bool    `yaml:"skip_tracker_filename_lookup"`
	UseLargestPlaylist        bool    `yaml:"use_largest_playlist"`
	KeepImages                bool    `yaml:"keep_images"`
	OnlyID                    bool    `yaml:"only_id"`
	UserOverrides             bool    `yaml:"user_overrides"`
	PingUnit3D                bool    `yaml:"ping_unit3d"`
	GetBlurayInfo             bool    `yaml:"get_bluray_info"`
	BlurayScore               float64 `yaml:"bluray_score"`
	BluraySingleScore         float64 `yaml:"bluray_single_score"`
	CheckPredb                bool    `yaml:"check_predb"`
}

type ScreenshotHandlingConfig struct {
	Screens              int     `yaml:"screens"`
	MinSuccessfulUploads int     `yaml:"min_successful_image_uploads"`
	CutoffScreens        int     `yaml:"cutoff_screens"`
	FrameOverlay         bool    `yaml:"frame_overlay"`
	OverlayTextSize      int     `yaml:"overlay_text_size"`
	ProcessLimit         int     `yaml:"process_limit"`
	MaxConcurrentUploads int     `yaml:"max_concurrent_uploads"`
	FFmpegLimit          bool    `yaml:"ffmpeg_limit"`
	ToneMap              bool    `yaml:"tone_map"`
	UseLibplacebo        bool    `yaml:"use_libplacebo"`
	FFmpegCompression    int     `yaml:"ffmpeg_compression"`
	TonemapAlgorithm     string  `yaml:"algorithm"`
	Desat                float64 `yaml:"desat"`
}

type DescriptionSettingsConfig struct {
	AddLogo                 bool   `yaml:"add_logo"`
	LogoSize                int    `yaml:"logo_size"`
	LogoLanguage            string `yaml:"logo_language"`
	ThumbnailSize           int    `yaml:"thumbnail_size"`
	ScreensPerRow           string `yaml:"screens_per_row"`
	EpisodeOverview         bool   `yaml:"episode_overview"`
	TonemappedHeader        string `yaml:"tonemapped_header"`
	MultiScreens            int    `yaml:"multiScreens"`
	PackThumbSize           int    `yaml:"pack_thumb_size"`
	CharLimit               int    `yaml:"charLimit"`
	FileLimit               int    `yaml:"fileLimit"`
	ProcessLimit            int    `yaml:"processLimit"`
	CustomDescriptionHeader string `yaml:"custom_description_header"`
	ScreenshotHeader        string `yaml:"screenshot_header"`
	DiscMenuHeader          string `yaml:"disc_menu_header"`
	CustomSignature         string `yaml:"custom_signature"`
	AddBlurayLink           bool   `yaml:"add_bluray_link"`
	UseBlurayImages         bool   `yaml:"use_bluray_images"`
	BlurayImageSize         int    `yaml:"bluray_image_size"`
}

type ClientSetupConfig struct {
	DefaultClient string  `yaml:"default_torrent_client"`
	InjectClients CSVList `yaml:"injecting_client_list"`
	SearchClients CSVList `yaml:"searching_client_list"`
}

type ArrIntegrationConfig struct {
	UseSonarr     bool   `yaml:"use_sonarr"`
	SonarrURL     string `yaml:"sonarr_url"`
	SonarrAPIKey  string `yaml:"sonarr_api_key"`
	SonarrURL1    string `yaml:"sonarr_url_1"`
	SonarrAPIKey1 string `yaml:"sonarr_api_key_1"`
	SonarrURL2    string `yaml:"sonarr_url_2"`
	SonarrAPIKey2 string `yaml:"sonarr_api_key_2"`
	SonarrURL3    string `yaml:"sonarr_url_3"`
	SonarrAPIKey3 string `yaml:"sonarr_api_key_3"`
	UseRadarr     bool   `yaml:"use_radarr"`
	RadarrURL     string `yaml:"radarr_url"`
	RadarrAPIKey  string `yaml:"radarr_api_key"`
	RadarrURL1    string `yaml:"radarr_url_1"`
	RadarrAPIKey1 string `yaml:"radarr_api_key_1"`
	RadarrURL2    string `yaml:"radarr_url_2"`
	RadarrAPIKey2 string `yaml:"radarr_api_key_2"`
	RadarrURL3    string `yaml:"radarr_url_3"`
	RadarrAPIKey3 string `yaml:"radarr_api_key_3"`
	EmbyDir       string `yaml:"emby_dir"`
	EmbyTVDir     string `yaml:"emby_tv_dir"`
}

type TorrentCreationConfig struct {
	MkbrrThreads   int  `yaml:"mkbrr_threads"`
	PreferMax16    bool `yaml:"prefer_max_16_torrent"`
	RehashCooldown int  `yaml:"rehash_cooldown"`
}

type PostUploadConfig struct {
	InjectDelay              int  `yaml:"inject_delay"`
	ShowUploadDuration       bool `yaml:"show_upload_duration"`
	PrintTrackerMessages     bool `yaml:"print_tracker_messages"`
	PrintTrackerLinks        bool `yaml:"print_tracker_links"`
	MaxConcurrentTrackers    int  `yaml:"max_concurrent_tracker_uploads"`
	SearchRequests           bool `yaml:"search_requests"`
	CrossSeeding             bool `yaml:"cross_seeding"`
	CrossSeedCheckEverything bool `yaml:"cross_seed_check_everything"`
}

type LoggingConfig struct {
	Level          string `yaml:"level"`
	FileEnabled    bool   `yaml:"file_enabled"`
	MaxTotalSizeMB int    `yaml:"max_total_size_mb"`
	MaxFiles       int    `yaml:"max_files"`
}

type TrackersConfig struct {
	DefaultTrackers  CSVList                  `yaml:"default_trackers" json:"DefaultTrackers"`
	PreferredTracker string                   `yaml:"preferred_tracker" json:"PreferredTracker"`
	Trackers         map[string]TrackerConfig `yaml:"-" json:"Trackers"`
}

type TrackerConfig struct {
	LinkDirName         string                 `yaml:"link_dir_name" json:"LinkDirName"`
	APIKey              string                 `yaml:"api_key" json:"APIKey"`
	PTPAPIUser          string                 `yaml:"ApiUser" json:"ApiUser"`
	PTPAPIKey           string                 `yaml:"ApiKey" json:"ApiKey"`
	Username            string                 `yaml:"username" json:"Username"`
	Password            string                 `yaml:"password" json:"Password"`
	Passkey             string                 `yaml:"passkey" json:"Passkey"`
	AnnounceURL         string                 `yaml:"announce_url" json:"AnnounceURL"`
	MyAnnounceURL       string                 `yaml:"my_announce_url" json:"MyAnnounceURL"`
	URL                 string                 `yaml:"url" json:"URL"`
	UploaderStatus      bool                   `yaml:"uploader_status" json:"UploaderStatus"`
	CustomLayout        string                 `yaml:"custom_layout" json:"CustomLayout"`
	TagForCustomRelease string                 `yaml:"tag_for_custom_release" json:"TagForCustomRelease"`
	CheckForRules       bool                   `yaml:"check_for_rules" json:"CheckForRules"`
	ModQ                bool                   `yaml:"modq" json:"ModQ"`
	Draft               bool                   `yaml:"draft" json:"Draft"`
	DraftDefault        bool                   `yaml:"draft_default" json:"DraftDefault"`
	Anon                bool                   `yaml:"anon" json:"Anon"`
	ShowGroupIfAnon     bool                   `yaml:"show_group_if_anon" json:"ShowGroupIfAnon"`
	BhdRSSKey           string                 `yaml:"bhd_rss_key" json:"BhdRSSKey"`
	CheckRequests       bool                   `yaml:"check_requests" json:"CheckRequests"`
	FullMediainfo       bool                   `yaml:"full_mediainfo" json:"FullMediainfo"`
	UploaderName        string                 `yaml:"uploader_name" json:"UploaderName"`
	ImgRehost           bool                   `yaml:"img_rehost" json:"ImgRehost"`
	ImageHost           string                 `yaml:"image_host" json:"ImageHost"`
	UseSpanishTitle     bool                   `yaml:"use_spanish_title" json:"UseSpanishTitle"`
	UseItalianTitle     bool                   `yaml:"use_italian_title" json:"UseItalianTitle"`
	OTPURI              string                 `yaml:"otp_uri" json:"OTPURI"`
	SkipIfRehash        bool                   `yaml:"skip_if_rehash" json:"SkipIfRehash"`
	PreferMTV           bool                   `yaml:"prefer_mtv_torrent" json:"PreferMTV"`
	PTGenAPI            string                 `yaml:"ptgen_api" json:"PTGenAPI"`
	AddWebSourceToDesc  bool                   `yaml:"add_web_source_to_desc" json:"AddWebSourceToDesc"`
	UseMetadataName     bool                   `yaml:"use_metadata_name" json:"UseMetadataName"`
	InjectDelay         *int                   `yaml:"inject_delay" json:"InjectDelay"`
	ImageCount          int                    `yaml:"image_count" json:"ImageCount"`
	Channel             string                 `yaml:"channel" json:"Channel"`
	ImgAPI              string                 `yaml:"img_api" json:"ImgAPI"`
	PronfoAPIKey        string                 `yaml:"pronfo_api_key" json:"PronfoAPIKey"`
	PronfoTheme         string                 `yaml:"pronfo_theme" json:"PronfoTheme"`
	PronfoRAPIID        string                 `yaml:"pronfo_rapi_id" json:"PronfoRAPIID"`
	APIUpload           bool                   `yaml:"api_upload" json:"APIUpload"`
	Exclusive           bool                   `yaml:"exclusive" json:"Exclusive"`
	LoginQuestion       string                 `yaml:"login_question" json:"LoginQuestion"`
	LoginAnswer         string                 `yaml:"login_answer" json:"LoginAnswer"`
	UserID              string                 `yaml:"user_id" json:"UserID"`
	Filebrowser         string                 `yaml:"filebrowser" json:"Filebrowser"`
	Internal            bool                   `yaml:"internal" json:"Internal"`
	InternalGroups      []string               `yaml:"internal_groups" json:"InternalGroups"`
	Unknown             map[string]interface{} `yaml:"-" json:"-"`
}

type trackerConfigAlias TrackerConfig

var (
	trackerTagOnce       sync.Once
	trackerKnownYAMLKeys map[string]struct{}
	trackerKnownJSONKeys map[string]struct{}
	trackerYAMLToJSON    map[string]string
	trackerJSONToYAML    map[string]string

	trackerSchemaOnce sync.Once
	trackerSchema     map[string]map[string]struct{}
)

func initTrackerTagMetadata() {
	trackerTagOnce.Do(func() {
		trackerKnownYAMLKeys = make(map[string]struct{})
		trackerKnownJSONKeys = make(map[string]struct{})
		trackerYAMLToJSON = make(map[string]string)
		trackerJSONToYAML = make(map[string]string)

		t := reflect.TypeOf(TrackerConfig{})
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			yamlTag := strings.TrimSpace(strings.Split(field.Tag.Get("yaml"), ",")[0])
			jsonTag := strings.TrimSpace(strings.Split(field.Tag.Get("json"), ",")[0])
			if yamlTag == "" || yamlTag == "-" || jsonTag == "" || jsonTag == "-" {
				continue
			}
			trackerKnownYAMLKeys[yamlTag] = struct{}{}
			trackerKnownJSONKeys[jsonTag] = struct{}{}
			trackerYAMLToJSON[yamlTag] = jsonTag
			trackerJSONToYAML[jsonTag] = yamlTag
		}
	})
}

func initTrackerSchema() {
	trackerSchemaOnce.Do(func() {
		trackerSchema = make(map[string]map[string]struct{})

		var root struct {
			Trackers map[string]interface{} `yaml:"trackers"`
		}
		if err := yaml.Unmarshal(EmbeddedExampleYAML(), &root); err != nil {
			return
		}

		for trackerName, raw := range root.Trackers {
			if strings.EqualFold(trackerName, "default_trackers") || strings.EqualFold(trackerName, "preferred_tracker") {
				continue
			}
			entry, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			keys := make(map[string]struct{}, len(entry))
			for key := range entry {
				keys[key] = struct{}{}
			}
			trackerSchema[trackerName] = keys
		}
	})
}

func trackerAllowedYAMLKeys(trackerName string) map[string]struct{} {
	initTrackerSchema()
	addGlobal := func(keys map[string]struct{}) map[string]struct{} {
		keys["image_host"] = struct{}{}
		return keys
	}
	if len(trackerSchema) == 0 {
		return nil
	}
	if keys, ok := trackerSchema[trackerName]; ok {
		clone := make(map[string]struct{}, len(keys)+1)
		for key := range keys {
			clone[key] = struct{}{}
		}
		return addGlobal(clone)
	}
	return nil
}

func trackerAllowedJSONKeys(trackerName string) map[string]struct{} {
	yamlKeys := trackerAllowedYAMLKeys(trackerName)
	if len(yamlKeys) == 0 {
		return nil
	}
	initTrackerTagMetadata()
	converted := make(map[string]struct{}, len(yamlKeys))
	for yamlKey := range yamlKeys {
		if jsonKey, ok := trackerYAMLToJSON[yamlKey]; ok {
			converted[jsonKey] = struct{}{}
		}
	}
	return converted
}

func filterMapByAllowedKeys(source map[string]interface{}, allowed map[string]struct{}) map[string]interface{} {
	if len(source) == 0 {
		return map[string]interface{}{}
	}
	if len(allowed) == 0 {
		clone := make(map[string]interface{}, len(source))
		for key, value := range source {
			clone[key] = value
		}
		return clone
	}
	filtered := make(map[string]interface{}, len(allowed))
	for key := range allowed {
		if value, ok := source[key]; ok {
			filtered[key] = value
		}
	}
	return filtered
}

func mergeUnknownKeys(target map[string]interface{}, unknown map[string]interface{}) {
	if len(unknown) == 0 {
		return
	}
	for key, value := range unknown {
		if _, exists := target[key]; exists {
			continue
		}
		target[key] = value
	}
}

func trackerConfigToJSONMap(cfg TrackerConfig) (map[string]interface{}, error) {
	alias := trackerConfigAlias(cfg)
	alias.Unknown = nil
	// Export paths encrypt known secrets first unless explicitly called for plaintext export.
	//nolint:gosec // TrackerConfig intentionally serializes API key fields for config export.
	payload, err := json.Marshal(alias)
	if err != nil {
		return nil, fmt.Errorf("config: marshal tracker config to json map: %w", err)
	}
	result := map[string]interface{}{}
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("config: unmarshal tracker config json map: %w", err)
	}
	return result, nil
}

func trackerConfigToYAMLMap(cfg TrackerConfig) (map[string]interface{}, error) {
	alias := trackerConfigAlias(cfg)
	alias.Unknown = nil
	// Export paths encrypt known secrets first unless explicitly called for plaintext export.
	//nolint:gosec // TrackerConfig intentionally serializes API key fields for config export.
	payload, err := yaml.Marshal(alias)
	if err != nil {
		return nil, fmt.Errorf("config: marshal tracker config to yaml map: %w", err)
	}
	result := map[string]interface{}{}
	if err := yaml.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("config: unmarshal tracker config yaml map: %w", err)
	}
	return result, nil
}

func parseDefaultTrackersValue(raw interface{}) CSVList {
	if raw == nil {
		return CSVList{}
	}
	switch value := raw.(type) {
	case []interface{}:
		result := make([]string, 0, len(value))
		for _, item := range value {
			trimmed := strings.TrimSpace(fmt.Sprintf("%v", item))
			if trimmed == "" {
				continue
			}
			result = append(result, trimmed)
		}
		return CSVList(result)
	case []string:
		result := make([]string, 0, len(value))
		for _, item := range value {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				continue
			}
			result = append(result, trimmed)
		}
		return CSVList(result)
	case string:
		items := strings.Split(value, ",")
		result := make([]string, 0, len(items))
		for _, item := range items {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				continue
			}
			result = append(result, trimmed)
		}
		return CSVList(result)
	default:
		return CSVList{}
	}
}

func extractTrackerUnknown(raw map[string]interface{}) map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	initTrackerTagMetadata()
	unknown := make(map[string]interface{})
	for key, value := range raw {
		if _, ok := trackerKnownJSONKeys[key]; ok {
			continue
		}
		if _, ok := trackerKnownYAMLKeys[key]; ok {
			continue
		}
		unknown[key] = value
	}
	if len(unknown) == 0 {
		return nil
	}
	return unknown
}

func decodeTrackerConfigFromJSON(raw map[string]interface{}) (TrackerConfig, error) {
	payload, err := json.Marshal(raw)
	if err != nil {
		return TrackerConfig{}, fmt.Errorf("config: marshal tracker config from json: %w", err)
	}
	var cfg TrackerConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return TrackerConfig{}, fmt.Errorf("config: unmarshal tracker config from json: %w", err)
	}
	cfg.Unknown = extractTrackerUnknown(raw)
	return cfg, nil
}

func decodeTrackerConfigFromYAML(raw map[string]interface{}) (TrackerConfig, error) {
	payload, err := yaml.Marshal(raw)
	if err != nil {
		return TrackerConfig{}, fmt.Errorf("config: marshal tracker config from yaml: %w", err)
	}
	var cfg TrackerConfig
	if err := yaml.Unmarshal(payload, &cfg); err != nil {
		return TrackerConfig{}, fmt.Errorf("config: unmarshal tracker config from yaml: %w", err)
	}
	cfg.Unknown = extractTrackerUnknown(raw)
	return cfg, nil
}

func (t TrackersConfig) MarshalJSON() ([]byte, error) {
	trackers := make(map[string]map[string]interface{}, len(t.Trackers))
	for trackerName, trackerCfg := range t.Trackers {
		jsonMap, err := trackerConfigToJSONMap(trackerCfg)
		if err != nil {
			return nil, err
		}
		filtered := filterMapByAllowedKeys(jsonMap, trackerAllowedJSONKeys(trackerName))
		mergeUnknownKeys(filtered, trackerCfg.Unknown)
		trackers[trackerName] = filtered
	}

	type trackersJSON struct {
		DefaultTrackers  CSVList                           `json:"DefaultTrackers"`
		PreferredTracker string                            `json:"PreferredTracker"`
		Trackers         map[string]map[string]interface{} `json:"Trackers"`
	}

	defaultTrackers := t.DefaultTrackers
	if defaultTrackers == nil {
		defaultTrackers = CSVList{}
	}
	preferredTracker := strings.TrimSpace(t.PreferredTracker)

	payload, err := json.Marshal(trackersJSON{DefaultTrackers: defaultTrackers, PreferredTracker: preferredTracker, Trackers: trackers})
	if err != nil {
		return nil, fmt.Errorf("config: marshal trackers config: %w", err)
	}
	return payload, nil
}

func (t *TrackersConfig) UnmarshalJSON(data []byte) error {
	if t == nil {
		return errors.New("trackers: nil target")
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("config: unmarshal trackers config root: %w", err)
	}

	t.DefaultTrackers = CSVList{}
	t.PreferredTracker = ""
	t.Trackers = map[string]TrackerConfig{}

	if raw, ok := root["DefaultTrackers"]; ok {
		var list []string
		if err := json.Unmarshal(raw, &list); err == nil {
			t.DefaultTrackers = CSVList(list)
		}
	}
	if raw, ok := root["default_trackers"]; ok {
		var list []string
		if err := json.Unmarshal(raw, &list); err == nil {
			t.DefaultTrackers = CSVList(list)
		}
	}
	if raw, ok := root["PreferredTracker"]; ok {
		var preferred string
		if err := json.Unmarshal(raw, &preferred); err == nil {
			t.PreferredTracker = strings.TrimSpace(preferred)
		}
	}
	if raw, ok := root["preferred_tracker"]; ok {
		var preferred string
		if err := json.Unmarshal(raw, &preferred); err == nil {
			t.PreferredTracker = strings.TrimSpace(preferred)
		}
	}

	var rawTrackers map[string]json.RawMessage
	if raw, ok := root["Trackers"]; ok {
		if err := json.Unmarshal(raw, &rawTrackers); err != nil {
			return fmt.Errorf("config: unmarshal trackers map: %w", err)
		}
	} else {
		rawTrackers = make(map[string]json.RawMessage)
		for key, raw := range root {
			if key == "DefaultTrackers" || key == "default_trackers" || key == "PreferredTracker" || key == "preferred_tracker" {
				continue
			}
			rawTrackers[key] = raw
		}
	}

	for trackerName, raw := range rawTrackers {
		entry := map[string]interface{}{}
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		cfg, err := decodeTrackerConfigFromJSON(entry)
		if err != nil {
			return err
		}
		t.Trackers[trackerName] = cfg
	}

	return nil
}

func (t TrackersConfig) MarshalYAML() (interface{}, error) {
	root := map[string]interface{}{}
	defaultTrackers := t.DefaultTrackers
	if defaultTrackers == nil {
		defaultTrackers = CSVList{}
	}
	root["default_trackers"] = []string(defaultTrackers)
	root["preferred_tracker"] = strings.TrimSpace(t.PreferredTracker)

	for trackerName, trackerCfg := range t.Trackers {
		yamlMap, err := trackerConfigToYAMLMap(trackerCfg)
		if err != nil {
			return nil, err
		}
		filtered := filterMapByAllowedKeys(yamlMap, trackerAllowedYAMLKeys(trackerName))
		mergeUnknownKeys(filtered, trackerCfg.Unknown)
		root[trackerName] = filtered
	}

	return root, nil
}

func (t *TrackersConfig) UnmarshalYAML(value *yaml.Node) error {
	if t == nil {
		return errors.New("trackers: nil target")
	}

	var root map[string]interface{}
	if err := value.Decode(&root); err != nil {
		return fmt.Errorf("config: decode trackers yaml: %w", err)
	}

	t.DefaultTrackers = parseDefaultTrackersValue(root["default_trackers"])
	t.PreferredTracker = strings.TrimSpace(fmt.Sprintf("%v", root["preferred_tracker"]))
	if strings.EqualFold(t.PreferredTracker, "<nil>") {
		t.PreferredTracker = ""
	}
	t.Trackers = map[string]TrackerConfig{}

	if nestedRaw, ok := root["trackers"]; ok {
		if nested, ok := nestedRaw.(map[string]interface{}); ok {
			for key, value := range nested {
				root[key] = value
			}
		}
	}

	for key, raw := range root {
		if strings.EqualFold(key, "default_trackers") || strings.EqualFold(key, "preferred_tracker") || strings.EqualFold(key, "trackers") {
			continue
		}
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		cfg, err := decodeTrackerConfigFromYAML(entry)
		if err != nil {
			return err
		}
		t.Trackers[key] = cfg
	}

	return nil
}

type TorrentClientConfig struct {
	Type          string     `yaml:"type"`
	TorrentClient string     `yaml:"torrent_client"`
	URL           string     `yaml:"url"`
	QuiProxyURL   string     `yaml:"qui_proxy_url"`
	WatchFolder   string     `yaml:"watch_folder"`
	StorageDir    string     `yaml:"torrent_storage_dir"`
	Username      string     `yaml:"username"`
	Password      string     `yaml:"password"`
	Category      string     `yaml:"category"`
	Tags          []string   `yaml:"tags"`
	TLSSkipVerify *bool      `yaml:"tls_skip_verify"`
	Linking       string     `yaml:"linking"`
	AllowFallback *bool      `yaml:"allow_fallback"`
	LinkedFolder  StringList `yaml:"linked_folder"`
	LocalPath     StringList `yaml:"local_path"`
	RemotePath    StringList `yaml:"remote_path"`

	QbitURL                string   `yaml:"qbit_url"`
	QbitPort               int      `yaml:"qbit_port"`
	QbitUser               string   `yaml:"qbit_user"`
	QbitPass               string   `yaml:"qbit_pass"`
	QbitCategoryValue      string   `yaml:"qbit_cat"`
	QbitTag                string   `yaml:"qbit_tag"`
	QbitTagsValue          []string `yaml:"qbit_tags"`
	QbitCrossCategory      string   `yaml:"qbit_cross_cat"`
	QbitCrossTag           string   `yaml:"qbit_cross_tag"`
	UseTrackerAsTag        bool     `yaml:"use_tracker_as_tag"`
	VerifyWebUICertificate *bool    `yaml:"verify_webui_certificate"`
}

func (c TorrentClientConfig) MarshalJSON() ([]byte, error) {
	payload := torrentClientConfigJSONMap(c)
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("config: marshal torrent client config: %w", err)
	}
	return data, nil
}

func (c TorrentClientConfig) MarshalYAML() (interface{}, error) {
	return torrentClientConfigYAMLMap(c), nil
}

func torrentClientConfigJSONMap(c TorrentClientConfig) map[string]interface{} {
	out := map[string]interface{}{}
	addString := func(key string, value string) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out[key] = trimmed
		}
	}
	addStringList := func(key string, values StringList) {
		if items := nonEmptyStrings(values); len(items) > 0 {
			out[key] = items
		}
	}

	addString("QuiProxyURL", c.QuiProxyURL)
	if strings.TrimSpace(c.QuiProxyURL) == "" {
		addString("QbitURL", firstNonEmpty(c.QbitURL, c.URL))
		if c.QbitPort != 0 {
			out["QbitPort"] = c.QbitPort
		}
		addString("QbitUser", c.QbitUsername())
		addString("QbitPass", c.QbitPassword())
	}
	addString("QbitCategoryValue", c.QbitCategory())
	addString("QbitTag", c.QbitTags())
	addString("QbitCrossCategory", c.QbitCrossCategory)
	addString("QbitCrossTag", c.QbitCrossTag)
	if c.UseTrackerAsTag {
		out["UseTrackerAsTag"] = c.UseTrackerAsTag
	}
	addString("Linking", c.LinkingMode())
	if c.AllowFallback != nil {
		out["AllowFallback"] = *c.AllowFallback
	}
	addStringList("LinkedFolder", c.LinkedFolder)
	addStringList("LocalPath", c.LocalPath)
	addStringList("RemotePath", c.RemotePath)
	if c.VerifyWebUICertificate != nil {
		out["VerifyWebUICertificate"] = *c.VerifyWebUICertificate
	}

	return out
}

func torrentClientConfigYAMLMap(c TorrentClientConfig) map[string]interface{} {
	out := map[string]interface{}{}
	addString := func(key string, value string) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out[key] = trimmed
		}
	}
	addStringList := func(key string, values StringList) {
		if items := nonEmptyStrings(values); len(items) > 0 {
			out[key] = items
		}
	}

	addString("qui_proxy_url", c.QuiProxyURL)
	if strings.TrimSpace(c.QuiProxyURL) == "" {
		addString("qbit_url", firstNonEmpty(c.QbitURL, c.URL))
		if c.QbitPort != 0 {
			out["qbit_port"] = c.QbitPort
		}
		addString("qbit_user", c.QbitUsername())
		addString("qbit_pass", c.QbitPassword())
	}
	addString("qbit_cat", c.QbitCategory())
	addString("qbit_tag", c.QbitTags())
	addString("qbit_cross_cat", c.QbitCrossCategory)
	addString("qbit_cross_tag", c.QbitCrossTag)
	if c.UseTrackerAsTag {
		out["use_tracker_as_tag"] = c.UseTrackerAsTag
	}
	addString("linking", c.LinkingMode())
	if c.AllowFallback != nil {
		out["allow_fallback"] = *c.AllowFallback
	}
	addStringList("linked_folder", c.LinkedFolder)
	addStringList("local_path", c.LocalPath)
	addStringList("remote_path", c.RemotePath)
	if c.VerifyWebUICertificate != nil {
		out["verify_webui_certificate"] = *c.VerifyWebUICertificate
	}

	return out
}

func (c Config) Validate() error {
	if c.MainSettings.TMDBAPI == "" {
		return errors.New("config: main_settings.tmdb_api is required")
	}
	if c.MainSettings.InputHistoryLimit < 0 {
		return errors.New("config: main_settings.input_history_limit must be zero or greater")
	}
	if c.ScreenshotHandling.Screens <= 0 {
		return errors.New("config: screenshot_handling.screens must be greater than zero")
	}
	if c.PostUpload.MaxConcurrentTrackers < 0 {
		return errors.New("config: post_upload.max_concurrent_tracker_uploads must be zero or greater")
	}
	if c.Logging.FileEnabled {
		if c.Logging.MaxTotalSizeMB <= 0 {
			return errors.New("config: logging.max_total_size_mb must be greater than zero")
		}
		if c.Logging.MaxFiles <= 0 {
			return errors.New("config: logging.max_files must be greater than zero")
		}
	}

	for name, client := range c.TorrentClients {
		clientType := strings.TrimSpace(client.ClientType())
		switch {
		case strings.EqualFold(clientType, "watch"):
			if strings.TrimSpace(client.WatchFolder) == "" {
				return fmt.Errorf("config: torrent_clients.%s.watch_folder is required", name)
			}
		case strings.EqualFold(clientType, "qbit"), strings.EqualFold(clientType, "qbittorrent"):
			if strings.TrimSpace(client.QbitHost()) == "" {
				return fmt.Errorf("config: torrent_clients.%s.url or qbit_url is required", name)
			}
			if !client.UsesQuiProxy() {
				if strings.TrimSpace(client.QbitUsername()) == "" {
					return fmt.Errorf("config: torrent_clients.%s.username or qbit_user is required", name)
				}
				if strings.TrimSpace(client.QbitPassword()) == "" {
					return fmt.Errorf("config: torrent_clients.%s.password or qbit_pass is required", name)
				}
			}
			switch strings.ToLower(strings.TrimSpace(client.Linking)) {
			case "", "none", "disabled", "symlink", "hardlink", "reflink":
			default:
				return fmt.Errorf("config: torrent_clients.%s.linking must be symlink, hardlink, reflink, or empty", name)
			}
			linkMode := strings.ToLower(strings.TrimSpace(client.Linking))
			if linkMode == "symlink" || linkMode == "hardlink" || linkMode == "reflink" {
				if len(nonEmptyStrings(client.LinkedFolder)) == 0 {
					return fmt.Errorf("config: torrent_clients.%s.linked_folder is required when linking is enabled", name)
				}
			}
		case strings.EqualFold(clientType, "qui"):
			if !client.UsesQuiProxy() {
				return fmt.Errorf("config: torrent_clients.%s.qui_proxy_url is required", name)
			}
		}
	}

	for trackerName, trackerCfg := range c.Trackers.Trackers {
		imageHost := strings.ToLower(strings.TrimSpace(trackerCfg.ImageHost))
		if imageHost != "" {
			if !imagehostpolicy.IsUploadHost(imageHost) {
				return fmt.Errorf("config: trackers.%s.image_host %q is not supported", trackerName, trackerCfg.ImageHost)
			}
			if owner := imagehostpolicy.OwnerForHost(imageHost); owner != "" && !strings.EqualFold(strings.TrimSpace(trackerName), owner) {
				return fmt.Errorf("config: trackers.%s.image_host %q is owned by %s", trackerName, trackerCfg.ImageHost, owner)
			}
			policy := imagehostpolicy.ForTracker(trackerName, trackerCfg.ImgRehost, trackerCfg.ImgAPI)
			if len(policy.AllowedHosts) > 0 && !imagehostpolicy.HostAllowed(imageHost, policy.AllowedHosts) {
				return fmt.Errorf("config: trackers.%s.image_host %q is not allowed for this tracker", trackerName, trackerCfg.ImageHost)
			}
		}
		if !trackerCfg.ImgRehost {
			continue
		}
		policy := imagehostpolicy.ForTracker(trackerName, true, trackerCfg.ImgAPI)
		if len(policy.AllowedHosts) == 0 {
			return fmt.Errorf("config: trackers.%s.img_rehost requires a tracker image-host policy, but none is defined", trackerName)
		}
	}

	return nil
}

func DisableUnsupportedTrackerImageRehosts(cfg *Config) []string {
	if cfg == nil || len(cfg.Trackers.Trackers) == 0 {
		return nil
	}

	disabled := make([]string, 0)
	for trackerName, trackerCfg := range cfg.Trackers.Trackers {
		if !trackerCfg.ImgRehost {
			continue
		}
		policy := imagehostpolicy.ForTracker(trackerName, true, trackerCfg.ImgAPI)
		if len(policy.AllowedHosts) != 0 {
			continue
		}
		trackerCfg.ImgRehost = false
		cfg.Trackers.Trackers[trackerName] = trackerCfg
		disabled = append(disabled, trackerName)
	}
	return disabled
}

func ResolveBTNAPIToken(cfg Config) string {
	if trackerCfg, ok := cfg.Trackers.Trackers["BTN"]; ok {
		token := strings.TrimSpace(trackerCfg.APIKey)
		if token != "" {
			return token
		}
	}
	token := strings.TrimSpace(cfg.Metadata.BTNAPI)
	return token
}

// MergeMissingTrackerDefaults backfills tracker stubs from the embedded example
// config so older saved configs can discover newly added trackers in the GUI.
func MergeMissingTrackerDefaults(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	if cfg.Trackers.Trackers == nil {
		cfg.Trackers.Trackers = map[string]TrackerConfig{}
	}
	if cfg.Trackers.DefaultTrackers == nil {
		cfg.Trackers.DefaultTrackers = CSVList{}
	}
	defaults, err := loadEmbeddedDefaultConfigRaw()
	if err != nil || defaults == nil || len(defaults.Trackers.Trackers) == 0 {
		if err != nil {
			return fmt.Errorf("load embedded tracker defaults: %w", err)
		}
		return errors.New("load embedded tracker defaults: embedded default trackers missing")
	}
	for trackerName, trackerCfg := range defaults.Trackers.Trackers {
		if _, ok := cfg.Trackers.Trackers[trackerName]; ok {
			continue
		}
		cfg.Trackers.Trackers[trackerName] = trackerCfg
	}
	if token := strings.TrimSpace(cfg.Metadata.BTNAPI); token != "" {
		btnCfg := cfg.Trackers.Trackers["BTN"]
		if strings.TrimSpace(btnCfg.APIKey) == "" {
			btnCfg.APIKey = token
			cfg.Trackers.Trackers["BTN"] = btnCfg
		}
	}
	return nil
}

func (c TorrentClientConfig) QbitHost() string {
	if strings.TrimSpace(c.QuiProxyURL) != "" {
		host := strings.TrimSpace(c.QuiProxyURL)
		if !strings.Contains(host, "://") {
			host = "http://" + host
		}
		return host
	}
	host := strings.TrimSpace(c.URL)
	if host == "" {
		host = strings.TrimSpace(c.QbitURL)
	}
	if host == "" {
		return ""
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	parsed, err := url.Parse(host)
	if err != nil {
		return host
	}
	if c.QbitPort != 0 && parsed.Port() == "" {
		parsed.Host = net.JoinHostPort(parsed.Hostname(), strconv.Itoa(c.QbitPort))
	}
	return parsed.String()
}

func (c TorrentClientConfig) ClientType() string {
	if strings.TrimSpace(c.Type) != "" {
		return strings.TrimSpace(c.Type)
	}
	if strings.TrimSpace(c.TorrentClient) != "" {
		return strings.TrimSpace(c.TorrentClient)
	}
	return "qbit"
}

func (c TorrentClientConfig) QbitUsername() string {
	if strings.TrimSpace(c.QbitUser) != "" {
		return strings.TrimSpace(c.QbitUser)
	}
	return strings.TrimSpace(c.Username)
}

func (c TorrentClientConfig) QbitPassword() string {
	if strings.TrimSpace(c.QbitPass) != "" {
		return strings.TrimSpace(c.QbitPass)
	}
	return strings.TrimSpace(c.Password)
}

func (c TorrentClientConfig) QbitCategory() string {
	if strings.TrimSpace(c.QbitCategoryValue) != "" {
		return strings.TrimSpace(c.QbitCategoryValue)
	}
	return strings.TrimSpace(c.Category)
}

func (c TorrentClientConfig) QbitTags() string {
	if strings.TrimSpace(c.QbitTag) != "" {
		return strings.TrimSpace(c.QbitTag)
	}
	if len(c.QbitTagsValue) > 0 {
		items := make([]string, 0, len(c.QbitTagsValue))
		for _, value := range c.QbitTagsValue {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			items = append(items, trimmed)
		}
		return strings.Join(items, ",")
	}
	if len(c.Tags) > 0 {
		items := make([]string, 0, len(c.Tags))
		for _, value := range c.Tags {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			items = append(items, trimmed)
		}
		return strings.Join(items, ",")
	}
	return ""
}

func (c TorrentClientConfig) LinkingMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.Linking))
	switch mode {
	case "none", "disabled":
		return ""
	default:
		return mode
	}
}

func (c TorrentClientConfig) FallbackAllowed() bool {
	return c.AllowFallback == nil || *c.AllowFallback
}

func (c TorrentClientConfig) QbitTLSSkipVerify() bool {
	if c.TLSSkipVerify != nil {
		return *c.TLSSkipVerify
	}
	if c.VerifyWebUICertificate != nil {
		return !*c.VerifyWebUICertificate
	}
	return false
}

func (c TorrentClientConfig) UsesQuiProxy() bool {
	return strings.TrimSpace(c.QuiProxyURL) != ""
}

func nonEmptyStrings[S ~[]string](values S) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
