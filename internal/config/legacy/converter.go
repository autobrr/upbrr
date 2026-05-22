// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package legacy

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"

	"gopkg.in/yaml.v3"
)

// legacyDefaultSectionByKey maps flat legacy DEFAULT keys to the config
// section they belong to. Mirrors LEGACY_DEFAULT_SECTION_BY_KEY from the
// Python converter.
var legacyDefaultSectionByKey = map[string]string{
	"update_notification":            "main_settings",
	"verbose_notification":           "main_settings",
	"tmdb_api":                       "main_settings",
	"tracker_pass_checks":            "main_settings",
	"db_path":                        "main_settings",
	"img_host_1":                     "image_hosting",
	"img_host_2":                     "image_hosting",
	"img_host_3":                     "image_hosting",
	"img_host_4":                     "image_hosting",
	"img_host_5":                     "image_hosting",
	"img_host_6":                     "image_hosting",
	"imgbb_api":                      "image_hosting",
	"ptpimg_api":                     "image_hosting",
	"lensdump_api":                   "image_hosting",
	"ptscreens_api":                  "image_hosting",
	"onlyimage_api":                  "image_hosting",
	"dalexni_api":                    "image_hosting",
	"passtheima_ge_api":              "image_hosting",
	"zipline_url":                    "image_hosting",
	"zipline_api_key":                "image_hosting",
	"seedpool_cdn_api":               "image_hosting",
	"sharex_url":                     "image_hosting",
	"sharex_api_key":                 "image_hosting",
	"utppm_api":                      "image_hosting",
	"btn_api":                        "metadata",
	"skip_auto_torrent":              "metadata",
	"skip_tracker_filename_lookup":   "metadata",
	"use_largest_playlist":           "metadata",
	"keep_images":                    "metadata",
	"only_id":                        "metadata",
	"user_overrides":                 "metadata",
	"ping_unit3d":                    "metadata",
	"get_bluray_info":                "metadata",
	"bluray_score":                   "metadata",
	"bluray_single_score":            "metadata",
	"check_predb":                    "metadata",
	"screens":                        "screenshot_handling",
	"min_successful_image_uploads":   "screenshot_handling",
	"cutoff_screens":                 "screenshot_handling",
	"frame_overlay":                  "screenshot_handling",
	"overlay_text_size":              "screenshot_handling",
	"process_limit":                  "screenshot_handling",
	"max_concurrent_uploads":         "screenshot_handling",
	"ffmpeg_limit":                   "screenshot_handling",
	"tone_map":                       "screenshot_handling",
	"use_libplacebo":                 "screenshot_handling",
	"ffmpeg_compression":             "screenshot_handling",
	"algorithm":                      "screenshot_handling",
	"desat":                          "screenshot_handling",
	"add_logo":                       "description_settings",
	"logo_size":                      "description_settings",
	"logo_language":                  "description_settings",
	"thumbnail_size":                 "description_settings",
	"screens_per_row":                "description_settings",
	"episode_overview":               "description_settings",
	"tonemapped_header":              "description_settings",
	"multiScreens":                   "description_settings",
	"pack_thumb_size":                "description_settings",
	"charLimit":                      "description_settings",
	"fileLimit":                      "description_settings",
	"processLimit":                   "description_settings",
	"custom_description_header":      "description_settings",
	"screenshot_header":              "description_settings",
	"disc_menu_header":               "description_settings",
	"custom_signature":               "description_settings",
	"add_bluray_link":                "description_settings",
	"use_bluray_images":              "description_settings",
	"bluray_image_size":              "description_settings",
	"default_torrent_client":         "client_setup",
	"injecting_client_list":          "client_setup",
	"searching_client_list":          "client_setup",
	"use_sonarr":                     "arr_integration",
	"sonarr_url":                     "arr_integration",
	"sonarr_api_key":                 "arr_integration",
	"sonarr_url_1":                   "arr_integration",
	"sonarr_api_key_1":               "arr_integration",
	"sonarr_url_2":                   "arr_integration",
	"sonarr_api_key_2":               "arr_integration",
	"sonarr_url_3":                   "arr_integration",
	"sonarr_api_key_3":               "arr_integration",
	"use_radarr":                     "arr_integration",
	"radarr_url":                     "arr_integration",
	"radarr_api_key":                 "arr_integration",
	"radarr_url_1":                   "arr_integration",
	"radarr_api_key_1":               "arr_integration",
	"radarr_url_2":                   "arr_integration",
	"radarr_api_key_2":               "arr_integration",
	"radarr_url_3":                   "arr_integration",
	"radarr_api_key_3":               "arr_integration",
	"emby_dir":                       "arr_integration",
	"emby_tv_dir":                    "arr_integration",
	"mkbrr_threads":                  "torrent_creation",
	"prefer_max_16_torrent":          "torrent_creation",
	"rehash_cooldown":                "torrent_creation",
	"inject_delay":                   "post_upload",
	"show_upload_duration":           "post_upload",
	"print_tracker_messages":         "post_upload",
	"print_tracker_links":            "post_upload",
	"max_concurrent_tracker_uploads": "post_upload",
	"search_requests":                "post_upload",
	"cross_seeding":                  "post_upload",
	"cross_seed_check_everything":    "post_upload",
}

// torrentClientKeyAliases maps legacy torrent client key names to current names.
var torrentClientKeyAliases = map[string]string{
	"rtorrent_url":             "url",
	"rtorrent_label":           "category",
	"VERIFY_WEBUI_CERTIFICATE": "verify_webui_certificate",
}

// Convert transforms a parsed legacy config into a new Config using the
// embedded template for structure and type information. It returns the
// converted config and a list of warnings for skipped/unmapped items.
func Convert(legacy *LegacyConfig, template *config.Config) (*config.Config, []string, error) {
	if legacy == nil {
		return nil, nil, errors.New("legacy config is nil")
	}
	if template == nil {
		return nil, nil, errors.New("template config is nil")
	}

	// Build the template as a map[string]map[string]any for field lookup.
	templateMap, err := configToSectionMaps(template)
	if err != nil {
		return nil, nil, fmt.Errorf("build template map: %w", err)
	}

	// Start with a deep copy of the template.
	out, err := deepCopyConfig(template)
	if err != nil {
		return nil, nil, fmt.Errorf("copy template: %w", err)
	}
	outMap, err := configToSectionMaps(out)
	if err != nil {
		return nil, nil, fmt.Errorf("build output map: %w", err)
	}

	var warnings []string

	// Migrate DEFAULT keys.
	for key, value := range legacy.Default {
		section, ok := legacyDefaultSectionByKey[key]
		if !ok {
			warnings = append(warnings, "skipped unknown DEFAULT key: "+key)
			continue
		}
		sectionTemplate, ok := templateMap[section]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skipped DEFAULT key %s: section %s not in template", key, section))
			continue
		}
		templateValue, ok := sectionTemplate[key]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skipped DEFAULT key %s: not found in template section %s", key, section))
			continue
		}
		outMap[section][key] = coerceValue(value, templateValue)
	}

	// Write section maps back to the config struct.
	if err := applySectionMaps(out, outMap); err != nil {
		return nil, nil, fmt.Errorf("apply section maps: %w", err)
	}

	// Migrate trackers.
	trackerWarnings := migrateTrackers(legacy.Trackers, template, out)
	warnings = append(warnings, trackerWarnings...)

	// Migrate torrent clients.
	clientWarnings := migrateTorrentClients(legacy.TorrentClients, out)
	warnings = append(warnings, clientWarnings...)

	return out, warnings, nil
}

// migrateTrackers copies tracker settings from the legacy config into out.
func migrateTrackers(legacyTrackers map[string]any, template *config.Config, out *config.Config) []string {
	var warnings []string

	if dt, ok := legacyTrackers["default_trackers"]; ok && dt != nil {
		out.Trackers.DefaultTrackers = coerceToStringSlice(dt)
	}

	if pt, ok := legacyTrackers["preferred_tracker"]; ok && pt != nil {
		out.Trackers.PreferredTracker = strings.TrimSpace(fmt.Sprintf("%v", pt))
	}

	// Get known tracker names from the template.
	knownTrackers := make(map[string]bool)
	for name := range template.Trackers.Trackers {
		knownTrackers[name] = true
	}

	for trackerName, raw := range legacyTrackers {
		if trackerName == "default_trackers" || trackerName == "preferred_tracker" {
			continue
		}
		trackerValues, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if !knownTrackers[trackerName] {
			warnings = append(warnings, "skipped unknown tracker: "+trackerName)
			continue
		}

		// Get the template tracker config for field/type reference.
		templateTracker, hasTemplate := template.Trackers.Trackers[trackerName]
		outTracker := out.Trackers.Trackers[trackerName]

		for key, value := range trackerValues {
			templateValue := getTrackerFieldTemplate(templateTracker, hasTemplate, key)
			if templateValue == nil {
				warnings = append(warnings, fmt.Sprintf("skipped unknown tracker key %s.%s", trackerName, key))
				continue
			}
			setTrackerField(&outTracker, key, coerceValue(value, templateValue))
		}

		out.Trackers.Trackers[trackerName] = outTracker
	}

	return warnings
}

// migrateTorrentClients copies torrent client settings from the legacy config.
func migrateTorrentClients(legacyClients map[string]any, out *config.Config) []string {
	var warnings []string

	if out.TorrentClients == nil {
		out.TorrentClients = make(map[string]config.TorrentClientConfig)
	}

	for clientName, raw := range legacyClients {
		clientValues, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		var tc config.TorrentClientConfig
		for key, value := range clientValues {
			mappedKey := key
			if alias, ok := torrentClientKeyAliases[key]; ok {
				mappedKey = alias
			}
			if !setTorrentClientField(&tc, mappedKey, value) {
				warnings = append(warnings, fmt.Sprintf("skipped unknown torrent client key %s.%s", clientName, key))
			}
		}

		out.TorrentClients[clientName] = tc
	}

	return warnings
}

// coerceValue converts a legacy value to match the type of the template value.
func coerceValue(value any, template any) any {
	if value == nil {
		if _, ok := template.(string); ok {
			return ""
		}
		return template
	}

	switch t := template.(type) {
	case bool:
		return coerceToBool(value)
	case int:
		return coerceToInt(value, t)
	case float64:
		return coerceToFloat(value, t)
	case []any:
		return coerceToSlice(value)
	case string:
		return fmt.Sprintf("%v", value)
	}

	return value
}

func coerceToBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		lowered := strings.ToLower(strings.TrimSpace(v))
		return lowered == "true" || lowered == "1" || lowered == "yes" || lowered == "on"
	case int:
		return v != 0
	case float64:
		return v != 0
	}
	return false
}

func coerceToInt(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		stripped := strings.TrimSpace(v)
		if stripped == "" {
			return fallback
		}
		if f, err := strconv.ParseFloat(stripped, 64); err == nil {
			return int(f)
		}
	}
	return fallback
}

func coerceToFloat(value any, fallback float64) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case bool:
		if v {
			return 1.0
		}
		return 0.0
	case string:
		stripped := strings.TrimSpace(v)
		if stripped == "" {
			return fallback
		}
		if f, err := strconv.ParseFloat(stripped, 64); err == nil {
			return f
		}
	}
	return fallback
}

func coerceToSlice(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case []string:
		result := make([]any, len(v))
		for i, s := range v {
			result[i] = s
		}
		return result
	case string:
		stripped := strings.TrimSpace(v)
		if stripped == "" {
			return []any{}
		}
		parts := strings.Split(stripped, ",")
		result := make([]any, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}
	return []any{value}
}

func coerceToStringSlice(value any) config.CSVList {
	switch v := value.(type) {
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			result = append(result, fmt.Sprintf("%v", item))
		}
		return config.CSVList(result)
	case []string:
		return config.CSVList(v)
	case string:
		stripped := strings.TrimSpace(v)
		if stripped == "" {
			return config.CSVList{}
		}
		parts := strings.Split(stripped, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return config.CSVList(result)
	}
	return config.CSVList{}
}

// configToSectionMaps marshals a Config to YAML and back to get section maps
// with YAML tag keys (matching legacy config keys).
func configToSectionMaps(cfg *config.Config) (map[string]map[string]any, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var raw map[string]map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	// Remove non-section keys that appear at root (trackers and torrent_clients
	// are handled specially).
	delete(raw, "trackers")
	delete(raw, "torrent_clients")
	return raw, nil
}

// applySectionMaps writes back the section maps into a Config struct.
func applySectionMaps(cfg *config.Config, sections map[string]map[string]any) error {
	// Build a partial YAML that only contains the simple sections (not
	// trackers/torrent_clients) and unmarshal it into the config.
	data, err := yaml.Marshal(sections)
	if err != nil {
		return err
	}

	// We only want to overwrite the simple scalar sections, preserving
	// trackers and torrent_clients which were handled separately.
	trackers := cfg.Trackers
	clients := cfg.TorrentClients

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return err
	}

	cfg.Trackers = trackers
	cfg.TorrentClients = clients
	return nil
}

// deepCopyConfig creates a deep copy of a Config via YAML round-trip.
func deepCopyConfig(cfg *config.Config) (*config.Config, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var out config.Config
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// getTrackerFieldTemplate returns the template's default value for a tracker
// field, used for type coercion. Returns nil if the field is unknown.
func getTrackerFieldTemplate(templateTracker config.TrackerConfig, hasTemplate bool, key string) any {
	if !hasTemplate {
		return getTrackerFieldDefault(key)
	}
	return getTrackerFieldValue(templateTracker, key)
}

// getTrackerFieldValue returns the value of a TrackerConfig field by its YAML tag.
func getTrackerFieldValue(tc config.TrackerConfig, yamlKey string) any {
	t := reflect.TypeOf(tc)
	v := reflect.ValueOf(tc)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.TrimSpace(strings.Split(field.Tag.Get("yaml"), ",")[0])
		if tag == yamlKey {
			return v.Field(i).Interface()
		}
	}
	return nil
}

// getTrackerFieldDefault returns a zero-value for known tracker fields.
func getTrackerFieldDefault(yamlKey string) any {
	t := reflect.TypeOf(config.TrackerConfig{})
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.TrimSpace(strings.Split(field.Tag.Get("yaml"), ",")[0])
		if tag == yamlKey {
			return reflect.Zero(field.Type).Interface()
		}
	}
	return nil
}

// setTrackerField sets a field on a TrackerConfig by its YAML tag name.
func setTrackerField(tc *config.TrackerConfig, yamlKey string, value any) {
	t := reflect.TypeOf(*tc)
	v := reflect.ValueOf(tc).Elem()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.TrimSpace(strings.Split(field.Tag.Get("yaml"), ",")[0])
		if tag != yamlKey {
			continue
		}
		fieldValue := v.Field(i)
		if !fieldValue.CanSet() {
			return
		}
		converted := convertToFieldType(value, field.Type)
		if converted.IsValid() {
			fieldValue.Set(converted)
		}
		return
	}
}

// setTorrentClientField sets a field on a TorrentClientConfig by its YAML tag.
// Returns false if the field is unknown.
func setTorrentClientField(tc *config.TorrentClientConfig, yamlKey string, value any) bool {
	t := reflect.TypeOf(*tc)
	v := reflect.ValueOf(tc).Elem()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.TrimSpace(strings.Split(field.Tag.Get("yaml"), ",")[0])
		if tag != yamlKey {
			continue
		}
		fieldValue := v.Field(i)
		if !fieldValue.CanSet() {
			return true
		}
		converted := convertToFieldType(value, field.Type)
		if converted.IsValid() {
			fieldValue.Set(converted)
		}
		return true
	}
	return false
}

// convertToFieldType converts an arbitrary value to the target reflect.Type.
func convertToFieldType(value any, targetType reflect.Type) reflect.Value {
	if value == nil {
		return reflect.Zero(targetType)
	}

	// Handle pointer types.
	if targetType.Kind() == reflect.Ptr {
		elemType := targetType.Elem()
		inner := convertToFieldType(value, elemType)
		if !inner.IsValid() {
			return reflect.Value{}
		}
		ptr := reflect.New(elemType)
		ptr.Elem().Set(inner)
		return ptr
	}
	switch targetType.Kind() { //nolint:exhaustive // only handle types relevant to config fields
	case reflect.String:
		return reflect.ValueOf(fmt.Sprintf("%v", value))
	case reflect.Bool:
		return reflect.ValueOf(coerceToBool(value))
	case reflect.Int, reflect.Int64:
		return reflect.ValueOf(coerceToInt(value, 0)).Convert(targetType)
	case reflect.Float64:
		return reflect.ValueOf(coerceToFloat(value, 0))
	case reflect.Slice:
		if targetType.Elem().Kind() == reflect.String {
			return reflect.ValueOf(coerceToStringList(value))
		}
	}

	// Fallback: try direct assignment.
	val := reflect.ValueOf(value)
	if val.Type().ConvertibleTo(targetType) {
		return val.Convert(targetType)
	}
	return reflect.Value{}
}

func coerceToStringList(value any) []string {
	switch v := value.(type) {
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			result = append(result, fmt.Sprintf("%v", item))
		}
		return result
	case []string:
		return v
	case string:
		stripped := strings.TrimSpace(v)
		if stripped == "" {
			return []string{}
		}
		parts := strings.Split(stripped, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}
	return []string{fmt.Sprintf("%v", value)}
}
