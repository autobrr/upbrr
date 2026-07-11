// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
)

type exportFormat int

const (
	exportFormatYAML exportFormat = iota
	exportFormatJSON
)

// ExportToYAML writes the config to a YAML file.
func ExportToYAML(cfg *Config, path string) error {
	return exportToFile(cfg, path, exportFormatYAML, true)
}

// ExportToPlaintextYAML writes the config to a YAML file without encrypting secret fields.
func ExportToPlaintextYAML(cfg *Config, path string) error {
	return exportToFile(cfg, path, exportFormatYAML, false)
}

func exportToFile(cfg *Config, path string, format exportFormat, encryptSecrets bool) error {
	if cfg == nil {
		return internalerrors.ErrInvalidInput
	}
	if path == "" {
		return errors.New("config export: empty path")
	}

	// Ensure directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("config export: mkdir: %w", err)
	}

	exportCfg, err := exportableConfig(cfg, encryptSecrets)
	if err != nil {
		return err
	}

	var data []byte
	switch format {
	case exportFormatYAML:
		data, err = yaml.Marshal(exportCfg)
		if err != nil {
			return fmt.Errorf("config export: marshal yaml: %w", err)
		}
		// TODO: exportFormatJSON is currently unused by public callers (they route through exportToJSON);
		// keep this branch so file-based JSON export can be re-enabled without duplicating marshal logic.
	case exportFormatJSON:
		data, err = json.MarshalIndent(exportCfg, "", "  ")
		if err != nil {
			return fmt.Errorf("config export: marshal json: %w", err)
		}
	default:
		return errors.New("config export: unknown format")
	}

	// Write to file.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("config export: write file: %w", err)
	}

	return nil
}

// ImportFromYAML reads the config from a YAML file.
func ImportFromYAML(path string) (*Config, error) {
	if path == "" {
		return nil, errors.New("config import: empty path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, internalerrors.ErrNotFound
		}
		return nil, fmt.Errorf("config import: read file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config import: unmarshal yaml: %w", err)
	}

	decryptedCfg, err := DecryptConfigSecrets(&cfg)
	if err != nil {
		return nil, fmt.Errorf("config import: decrypt secrets: %w", err)
	}

	return decryptedCfg, nil
}

// ExportToJSON serializes the config to a JSON string.
func ExportToJSON(cfg *Config) (string, error) {
	return exportToJSON(cfg, true)
}

// ExportToPlaintextJSON serializes the config to JSON without encrypting secret fields.
func ExportToPlaintextJSON(cfg *Config) (string, error) {
	return exportToJSON(cfg, false)
}

func exportToJSON(cfg *Config, encryptSecrets bool) (string, error) {
	if cfg == nil {
		return "", internalerrors.ErrInvalidInput
	}

	exportCfg, err := exportableConfig(cfg, encryptSecrets)
	if err != nil {
		return "", err
	}

	data, err := json.MarshalIndent(exportCfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("config export: marshal json: %w", err)
	}

	return string(data), nil
}

// ImportFromJSON deserializes plaintext JSON config (for example,
// ExportToPlaintextJSON output) without attempting secret decryption.
func ImportFromJSON(payload string) (*Config, error) {
	return importFromJSON(payload, false)
}

// ImportFromJSONEncrypted deserializes JSON config that contains encrypted
// secret envelopes (for example, ExportToJSON output) and decrypts secrets.
func ImportFromJSONEncrypted(payload string) (*Config, error) {
	return importFromJSON(payload, true)
}

func importFromJSON(payload string, decryptSecrets bool) (*Config, error) {
	if payload == "" {
		return nil, errors.New("config import: empty json")
	}

	var cfg Config
	if err := json.Unmarshal([]byte(payload), &cfg); err != nil {
		return nil, fmt.Errorf("config import: unmarshal json: %w", err)
	}
	if !decryptSecrets {
		return &cfg, nil
	}

	decryptedCfg, err := DecryptConfigSecrets(&cfg)
	if err != nil {
		return nil, fmt.Errorf("config import: decrypt secrets: %w", err)
	}

	return decryptedCfg, nil
}

// BackupToYAML creates a timestamped YAML backup of the current config.
// Returns the path to the backup file.
func BackupToYAML(cfg *Config, baseDir string) (string, error) {
	if cfg == nil {
		return "", internalerrors.ErrInvalidInput
	}
	if baseDir == "" {
		return "", errors.New("config backup: empty base directory")
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", fmt.Errorf("config backup: mkdir: %w", err)
	}

	// Create timestamped filename.
	backupDir := filepath.Join(baseDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", fmt.Errorf("config backup: mkdir backups: %w", err)
	}

	backupPath := filepath.Join(backupDir, "config.yaml")
	if err := ExportToYAML(cfg, backupPath); err != nil {
		return "", fmt.Errorf("config backup: export: %w", err)
	}

	return backupPath, nil
}

// fullConfigLoader reconstructs persisted config data into either the Config
// struct or a raw section map. Production repositories support both forms.
type fullConfigLoader interface {
	LoadFullConfig(ctx context.Context, dest any) error
}

// LoadFromDatabase loads the full config from the repository, overlaying saved
// sections onto embedded defaults so older persisted configs pick up newly
// added options while preserving explicit zero values and decrypted secrets.
func LoadFromDatabase(ctx context.Context, repo fullConfigLoader) (*Config, error) {
	cfg, _, err := LoadFromDatabaseWithDefaultBackfill(ctx, repo)
	return cfg, err
}

// LoadFromDatabaseWithDefaultBackfill returns a loaded config plus a changed
// flag indicating whether embedded defaults filled fields missing from storage.
// The flag is based on raw stored JSON key presence before Config unmarshaling,
// so explicit false, zero, and empty values do not look like missing fields.
func LoadFromDatabaseWithDefaultBackfill(ctx context.Context, repo fullConfigLoader) (*Config, bool, error) {
	if repo == nil {
		return nil, false, errors.New("config load: nil repository")
	}

	cfg, report, err := loadFullConfigOverlayingDefaults(ctx, repo)
	if err != nil {
		return nil, false, err
	}

	// Decrypt using the secret-encryption helper located next to the
	// repository's actual on-disk database, not the db_path persisted inside the
	// config (which may be empty or point at a pre-upgrade path). The returned
	// config keeps its stored db_path so callers still control path resolution.
	decryptedCfg, err := decryptConfigSecretsWithDBDir(&cfg, repoDBPath(repo))
	if err != nil {
		return nil, false, fmt.Errorf("config load from database: decrypt secrets: %w", err)
	}

	return decryptedCfg, report.BackfilledDefaults, nil
}

// DatabaseRepairReport describes load-time repairs needed after overlaying
// stored config onto embedded defaults. BackfilledDefaults reports missing
// defaults, invalid stored objects, or legacy fallback context repaired in
// memory. ChangedSections contains sorted JSON root section names that should
// be persisted; roots with invalid object-shaped input are excluded because the
// stored subtree was not safely reusable. InvalidPaths contains dotted
// raw-config paths whose stored value could not be used as an object.
type DatabaseRepairReport struct {
	BackfilledDefaults bool
	ChangedSections    []string
	InvalidPaths       []string
}

// LoadFromDatabaseWithRepairReport loads and decrypts full config from repo,
// overlaying stored raw sections onto embedded defaults. The returned report
// identifies repaired root sections; if raw section-map loading fails but
// legacy struct loading succeeds, the report carries repair context without
// scheduling section persistence. On error the returned config is nil.
func LoadFromDatabaseWithRepairReport(ctx context.Context, repo fullConfigLoader) (*Config, DatabaseRepairReport, error) {
	if repo == nil {
		return nil, DatabaseRepairReport{}, errors.New("config load: nil repository")
	}

	cfg, report, err := loadFullConfigOverlayingDefaults(ctx, repo)
	if err != nil {
		return nil, DatabaseRepairReport{}, err
	}

	decryptedCfg, err := decryptConfigSecretsWithDBDir(&cfg, repoDBPath(repo))
	if err != nil {
		return nil, DatabaseRepairReport{}, fmt.Errorf("config load from database: decrypt secrets: %w", err)
	}

	return decryptedCfg, report, nil
}

// loadFullConfigOverlayingDefaults overlays raw stored JSON sections onto the
// embedded default config and reports whether any default keys were absent from
// storage. Raw overlay is required because unmarshaled structs cannot
// distinguish omitted fields from explicit false, zero, or empty values.
func loadFullConfigOverlayingDefaults(ctx context.Context, repo fullConfigLoader) (Config, DatabaseRepairReport, error) {
	defaults, err := LoadEmbeddedDefaultConfig()
	if err != nil {
		return Config{}, DatabaseRepairReport{}, fmt.Errorf("config load from database: load defaults: %w", err)
	}
	// Template clients are examples, not persisted user configuration.
	defaults.TorrentClients = map[string]TorrentClientConfig{}

	base, err := configJSONMap(defaults)
	if err != nil {
		return Config{}, DatabaseRepairReport{}, fmt.Errorf("config load from database: marshal defaults: %w", err)
	}

	stored := map[string]any{}
	if err := repo.LoadFullConfig(ctx, &stored); err != nil {
		cfg := *defaults
		if err := repo.LoadFullConfig(ctx, &cfg); err != nil {
			return Config{}, DatabaseRepairReport{}, fmt.Errorf("config load from database: %w", err)
		}
		return cfg, DatabaseRepairReport{
			BackfilledDefaults: true,
			InvalidPaths:       []string{"raw-config"},
		}, nil
	}
	mergeReport, err := mergeStoredConfigMapWithReport(base, stored, "")
	if err != nil {
		return Config{}, DatabaseRepairReport{}, fmt.Errorf("config load from database: merge stored config: %w", err)
	}

	merged, err := json.Marshal(base)
	if err != nil {
		return Config{}, DatabaseRepairReport{}, fmt.Errorf("config load from database: marshal merged defaults: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(merged, &cfg); err != nil {
		return Config{}, DatabaseRepairReport{}, fmt.Errorf("config load from database: unmarshal merged defaults: %w", err)
	}
	report := DatabaseRepairReport{
		BackfilledDefaults: len(mergeReport.missingDefaultPaths) > 0 || len(mergeReport.invalidPaths) > 0,
		ChangedSections:    mergeReport.changedRootSections(),
		InvalidPaths:       append([]string(nil), mergeReport.invalidPaths...),
	}
	return cfg, report, nil
}

// configJSONMap converts cfg to the same exported-field JSON shape used by DB
// section storage and native JSON exports.
func configJSONMap(cfg *Config) (map[string]any, error) {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config json map: %w", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("unmarshal config json map: %w", err)
	}
	return out, nil
}

// mergeStoredConfigMap recursively overlays stored config onto defaults and
// returns default-key paths absent from the stored JSON shape. Raw presence is
// tracked before Config unmarshaling so explicit false, zero, and empty values
// are not confused with omitted fields. Duplicate case-folded tracker names or
// fields are rejected instead of choosing an order-dependent winner.
type storedConfigMergeReport struct {
	missingDefaultPaths []string
	invalidPaths        []string
	changedSections     map[string]struct{}
	invalidSections     map[string]struct{}
}

func newStoredConfigMergeReport() storedConfigMergeReport {
	return storedConfigMergeReport{
		changedSections: map[string]struct{}{},
		invalidSections: map[string]struct{}{},
	}
}

func (r *storedConfigMergeReport) append(other storedConfigMergeReport) {
	r.missingDefaultPaths = append(r.missingDefaultPaths, other.missingDefaultPaths...)
	r.invalidPaths = append(r.invalidPaths, other.invalidPaths...)
	for section := range other.changedSections {
		r.changedSections[section] = struct{}{}
	}
	for section := range other.invalidSections {
		r.invalidSections[section] = struct{}{}
	}
}

func (r *storedConfigMergeReport) markChanged(path string) {
	if section := configMapRoot(path); section != "" {
		r.changedSections[section] = struct{}{}
	}
}

func (r *storedConfigMergeReport) markInvalid(path string) {
	if section := configMapRoot(path); section != "" {
		r.invalidSections[section] = struct{}{}
	}
}

func (r *storedConfigMergeReport) changedRootSections() []string {
	changed := make(map[string]struct{}, len(r.changedSections))
	for section := range r.changedSections {
		if _, invalid := r.invalidSections[section]; invalid {
			continue
		}
		changed[section] = struct{}{}
	}
	return sortedStringSet(changed)
}

// mergeStoredConfigMapWithReport mutates base by applying overlay and reports
// missing defaults, invalid object-shaped overlays, and root sections changed by
// repair. It returns an error for ambiguous tracker name or field collisions.
func mergeStoredConfigMapWithReport(base map[string]any, overlay map[string]any, path string) (storedConfigMergeReport, error) {
	report := newStoredConfigMergeReport()
	for key := range base {
		if _, exists := overlay[key]; !exists {
			missingPath := configMapPath(path, key)
			report.missingDefaultPaths = append(report.missingDefaultPaths, missingPath)
			report.markChanged(missingPath)
		}
	}

	overlayKeys := make([]string, 0, len(overlay))
	for key := range overlay {
		if shouldDiscardStoredTrackerField(path, key, overlay) {
			invalidPath := configMapPath(path, key)
			report.invalidPaths = append(report.invalidPaths, invalidPath)
			report.markChanged(invalidPath)
			continue
		}
		overlayKeys = append(overlayKeys, key)
	}
	sort.Strings(overlayKeys)
	if err := validateStoredOverlayKeys(base, overlayKeys, path); err != nil {
		return report, err
	}

	for _, key := range overlayKeys {
		overlayValue := overlay[key]
		baseValue, exists := base[key]
		if !exists {
			if allowsStoredDynamicConfigEntry(path) || isStoredTrackerEntryPath(path) {
				childReport, err := mergeStoredDynamicConfigValue(base, key, overlayValue, path)
				if err != nil {
					return report, err
				}
				report.append(childReport)
			}
			continue
		}

		baseMap, baseOK := baseValue.(map[string]any)
		overlayMap, overlayOK := overlayValue.(map[string]any)
		if baseOK {
			if !overlayOK {
				invalidPath := configMapPath(path, key)
				report.invalidPaths = append(report.invalidPaths, invalidPath)
				report.markInvalid(invalidPath)
				continue
			}
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			childReport, err := mergeStoredConfigMapWithReport(baseMap, overlayMap, childPath)
			if err != nil {
				return report, err
			}
			report.append(childReport)
			continue
		}
		base[key] = overlayValue
	}
	return report, nil
}

// mergeStoredDynamicConfigValue inserts an object-shaped stored dynamic entry
// or folds it into a single existing ASCII-case tracker or field alias. Dynamic
// tracker and torrent-client entries that are not objects are skipped without
// marking the section changed.
func mergeStoredDynamicConfigValue(base map[string]any, key string, overlayValue any, path string) (storedConfigMergeReport, error) {
	report := newStoredConfigMergeReport()
	if usesASCIIStoredTrackerKeys(path) {
		existingKey, ok, err := asciiConfigMapKey(base, key)
		if err != nil {
			return report, err
		}
		if ok {
			baseMap, baseOK := base[existingKey].(map[string]any)
			overlayMap, overlayOK := overlayValue.(map[string]any)
			if baseOK {
				if !overlayOK {
					return report, nil
				}
				return mergeStoredConfigMapWithReport(baseMap, overlayMap, configMapPath(path, existingKey))
			}
			if !overlayOK {
				return report, nil
			}
			base[existingKey] = overlayValue
			report.markChanged(configMapPath(path, existingKey))
			return report, nil
		}
	}
	if isStoredTrackerEntryPath(path) {
		existingKey, ok, err := trackerFieldConfigMapKey(base, key)
		if err != nil {
			return report, err
		}
		if ok {
			base[existingKey] = overlayValue
			report.markChanged(configMapPath(path, existingKey))
			return report, nil
		}
	}

	if allowsStoredDynamicConfigEntry(path) {
		if _, overlayOK := overlayValue.(map[string]any); !overlayOK {
			return report, nil
		}
	}
	base[key] = overlayValue
	return report, nil
}

// asciiConfigMapKey returns the exact key or one deterministic ASCII-case key.
// It rejects multiple ASCII-case matches because callers cannot choose a safe
// winner without schema-specific precedence.
func asciiConfigMapKey(values map[string]any, key string) (string, bool, error) {
	if _, ok := values[key]; ok {
		return key, true, nil
	}
	matches := make([]string, 0, 1)
	for existingKey := range values {
		if existingKey != key && asciiEqualFold(existingKey, key) {
			matches = append(matches, existingKey)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return "", false, fmt.Errorf("ambiguous case-folded config key %q matches %v", key, matches)
	}
}

// trackerFieldConfigMapKey returns the exact tracker field or one ASCII-case
// match. It intentionally does not Unicode-fold fields such as APIKelvinKey into
// APIKey, and rejects ambiguous folded fields such as APIKey versus ApiKey.
func trackerFieldConfigMapKey(values map[string]any, key string) (string, bool, error) {
	if _, ok := values[key]; ok {
		return key, true, nil
	}
	matches := make([]string, 0, 1)
	for existingKey := range values {
		if existingKey != key && asciiEqualFold(existingKey, key) {
			matches = append(matches, existingKey)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return "", false, fmt.Errorf("ambiguous tracker field %q matches %v", key, matches)
	}
}

// shouldDiscardStoredTrackerField reports a schema-known field from a folded
// collision group that cannot apply to this tracker. Older saves could emit
// every TrackerConfig field, including APIKey and PTP-only ApiKey, for unknown
// trackers. A lone case alias remains eligible for legacy normalization.
func shouldDiscardStoredTrackerField(path string, key string, values map[string]any) bool {
	const trackerPathPrefix = "Trackers.Trackers."
	trackerName, ok := strings.CutPrefix(path, trackerPathPrefix)
	if !ok || trackerName == "" || strings.Contains(trackerName, ".") {
		return false
	}
	allowed := trackerAllowedJSONKeys(trackerName)
	if len(allowed) == 0 {
		return false
	}
	initTrackerTagMetadata()
	if _, known := trackerKnownJSONKeys[key]; !known {
		return false
	}
	if _, valid := allowed[key]; valid {
		return false
	}
	hasKnownFoldPeer := false
	for knownKey := range trackerKnownJSONKeys {
		if knownKey == key || !asciiEqualFold(knownKey, key) {
			continue
		}
		hasKnownFoldPeer = true
		if _, valid := allowed[knownKey]; valid {
			allowedValue, exactAllowedPresent := values[knownKey]
			return exactAllowedPresent && allowedValue != nil && !reflect.ValueOf(allowedValue).IsZero()
		}
	}
	return hasKnownFoldPeer
}

// validateStoredOverlayKeys rejects duplicate stored keys that would fold into
// the same tracker name or tracker field before mutation starts.
func validateStoredOverlayKeys(base map[string]any, keys []string, path string) error {
	seen := map[string]string{}
	for _, key := range keys {
		foldKey := key
		switch {
		case usesASCIIStoredTrackerKeys(path):
			existingKey, ok, err := asciiConfigMapKey(base, key)
			if err != nil {
				return err
			}
			if ok {
				foldKey = existingKey
			} else {
				foldKey = asciiFoldKey(key)
			}
		case isStoredTrackerEntryPath(path):
			existingKey, ok, err := trackerFieldConfigMapKey(base, key)
			if err != nil {
				return err
			}
			if ok {
				foldKey = existingKey
			}
		default:
			continue
		}
		if previous, ok := seen[foldKey]; ok && previous != key {
			if isRetainedStoredTrackerFieldAlias(path, previous, key) {
				continue
			}
			return fmt.Errorf("duplicate folded config keys %q and %q at %q", previous, key, path)
		}
		seen[foldKey] = key
	}
	return nil
}

// isRetainedStoredTrackerFieldAlias reports the unambiguous schema-known pair
// left after a populated legacy alias outranks its zero-valued allowed peer.
func isRetainedStoredTrackerFieldAlias(path, firstKey, secondKey string) bool {
	const trackerPathPrefix = "Trackers.Trackers."
	trackerName, ok := strings.CutPrefix(path, trackerPathPrefix)
	if !ok || trackerName == "" || strings.Contains(trackerName, ".") {
		return false
	}
	initTrackerTagMetadata()
	if _, known := trackerKnownJSONKeys[firstKey]; !known {
		return false
	}
	if _, known := trackerKnownJSONKeys[secondKey]; !known {
		return false
	}
	allowed := trackerAllowedJSONKeys(trackerName)
	_, firstAllowed := allowed[firstKey]
	_, secondAllowed := allowed[secondKey]
	return firstAllowed != secondAllowed
}

// usesASCIIStoredTrackerKeys reports paths whose dynamic child names are
// tracker IDs and may use ASCII-case aliases.
func usesASCIIStoredTrackerKeys(path string) bool {
	return path == "Trackers.Trackers"
}

// configMapPath appends key to a dotted raw-config path.
func configMapPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// configMapRoot returns the top-level JSON config section for a dotted path.
func configMapRoot(path string) string {
	if path == "" {
		return ""
	}
	if before, _, ok := strings.Cut(path, "."); ok {
		return before
	}
	return path
}

// allowsStoredDynamicConfigEntry reports raw JSON maps whose child keys are
// user-defined entries rather than fixed struct fields.
func allowsStoredDynamicConfigEntry(path string) bool {
	return path == "Trackers.Trackers" || path == "TorrentClients"
}

// isStoredTrackerEntryPath reports direct tracker-entry maps only; deeper
// extension maps are preserved without tracker-field case folding.
func isStoredTrackerEntryPath(path string) bool {
	const prefix = "Trackers.Trackers."
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	return !strings.Contains(strings.TrimPrefix(path, prefix), ".")
}

// asciiFoldKey lowercases ASCII letters without treating Unicode lookalikes as
// equivalent.
func asciiFoldKey(value string) string {
	out := []byte(value)
	for i, b := range out {
		if 'A' <= b && b <= 'Z' {
			out[i] = b + ('a' - 'A')
		}
	}
	return string(out)
}

// sortedStringSet returns map keys in stable order.
func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// sortedUniqueStrings removes blanks and duplicates, returning stable order.
func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		set[trimmed] = struct{}{}
	}
	return sortedStringSet(set)
}

// mergeStoredUnknownConfigValues copies stored object keys that are absent from
// current without overwriting known current values. It recurses using canonical
// tracker names and fields so selected-section repair saves preserve distinct
// future same-root fields without reintroducing folded aliases.
func mergeStoredUnknownConfigValues(current, stored any, path string) error {
	currentMap, ok := current.(map[string]any)
	if !ok {
		return nil
	}
	storedMap, ok := stored.(map[string]any)
	if !ok {
		return nil
	}
	for key, storedValue := range storedMap {
		if shouldDiscardStoredTrackerField(path, key, storedMap) {
			continue
		}
		currentKey, currentValue, exists, err := storedUnknownConfigMergeTarget(currentMap, key, path)
		if err != nil {
			return err
		}
		if !exists {
			currentMap[key] = storedValue
			continue
		}
		if err := mergeStoredUnknownConfigValues(currentValue, storedValue, configMapPath(path, currentKey)); err != nil {
			return err
		}
	}
	return nil
}

// storedUnknownConfigMergeTarget resolves a stored key to the current key that
// should receive recursive unknown-field preservation at path. Tracker names
// and direct tracker-entry fields use the same ASCII folding rules as load-time
// repair; all other paths require exact keys.
func storedUnknownConfigMergeTarget(current map[string]any, key, path string) (string, any, bool, error) {
	currentValue, exists := current[key]
	if exists {
		return key, currentValue, true, nil
	}

	var (
		currentKey string
		ok         bool
		err        error
	)
	switch {
	case usesASCIIStoredTrackerKeys(path):
		currentKey, ok, err = asciiConfigMapKey(current, key)
	case isStoredTrackerEntryPath(path):
		currentKey, ok, err = trackerFieldConfigMapKey(current, key)
	default:
		return key, nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	if !ok {
		return key, nil, false, nil
	}
	return currentKey, current[currentKey], true, nil
}

// preserveStoredUnknownConfigValues merges unknown stored fields into selected
// section payloads when the repository can load the full raw config. A missing
// stored config is treated as no data to preserve; other load errors abort
// before any section write.
func preserveStoredUnknownConfigValues(ctx context.Context, repo any, sections map[string]any) error {
	loader, ok := repo.(interface {
		LoadFullConfig(ctx context.Context, dest any) error
	})
	if !ok {
		return nil
	}

	stored := map[string]any{}
	if err := loader.LoadFullConfig(ctx, &stored); err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("load stored sections: %w", err)
	}
	for section, current := range sections {
		if err := mergeStoredUnknownConfigValues(current, stored[section], section); err != nil {
			return err
		}
	}
	return nil
}

// canonicalConfigSectionNames resolves requested root names to exported JSON
// section names before any persistence happens. It accepts exported field names
// plus yaml/json tag aliases from [Config], trims blanks, de-duplicates, and
// rejects the whole request if any section is unknown.
func canonicalConfigSectionNames(sectionData map[string]any, sections []string) ([]string, error) {
	aliases := configRootSectionAliases()
	canonicalSet := make(map[string]struct{}, len(sections))
	for _, section := range sections {
		trimmed := strings.TrimSpace(section)
		if trimmed == "" {
			continue
		}
		canonical := trimmed
		if alias, ok := aliases[trimmed]; ok {
			canonical = alias
		}
		if _, ok := sectionData[canonical]; !ok {
			return nil, fmt.Errorf("unknown section %q", section)
		}
		canonicalSet[canonical] = struct{}{}
	}
	return sortedStringSet(canonicalSet), nil
}

// configRootSectionAliases maps every supported root-section spelling to the
// exported field name used by database section storage.
func configRootSectionAliases() map[string]string {
	aliases := map[string]string{}
	t := reflect.TypeFor[Config]()
	for field := range t.Fields() {
		if !field.IsExported() {
			continue
		}
		aliases[field.Name] = field.Name

		yamlName := strings.TrimSpace(strings.Split(field.Tag.Get("yaml"), ",")[0])
		if yamlName != "" && yamlName != "-" {
			aliases[yamlName] = field.Name
		}

		jsonName := strings.TrimSpace(strings.Split(field.Tag.Get("json"), ",")[0])
		if jsonName != "" && jsonName != "-" {
			aliases[jsonName] = field.Name
		}
	}
	return aliases
}

// encryptConfigSecretsForSections prepares a copy for section persistence while
// leaving secrets outside the selected root sections empty. This avoids
// requiring unavailable helpers for unrelated encrypted envelopes but still
// validates and encrypts secrets that would be written.
func encryptConfigSecretsForSections(cfg *Config, sections []string) (*Config, error) {
	selected := make(map[string]struct{}, len(sections))
	for _, section := range sections {
		selected[section] = struct{}{}
	}

	encryptionCfg, err := cloneConfig(cfg)
	if err != nil {
		return nil, err
	}

	selectedSecret := false
	if err := walkSecretFields(encryptionCfg, func(path string, value *string) error {
		if value == nil {
			return nil
		}
		if _, ok := selected[secretPathRoot(path)]; ok {
			if strings.TrimSpace(*value) != "" {
				selectedSecret = true
			}
			return nil
		}
		*value = ""
		return nil
	}); err != nil {
		return nil, err
	}
	encryptionCfg.secretReencryptionRequired = cfg.secretReencryptionRequired && selectedSecret

	return EncryptConfigSecrets(encryptionCfg)
}

// secretPathRoot returns the top-level config section for a dotted or indexed
// secret field path.
func secretPathRoot(path string) string {
	end := len(path)
	if idx := strings.IndexByte(path, '.'); idx >= 0 && idx < end {
		end = idx
	}
	if idx := strings.IndexByte(path, '['); idx >= 0 && idx < end {
		end = idx
	}
	return path[:end]
}

// databasePathProvider is implemented by repositories that can expose the
// resolved on-disk database path used to locate sibling auth/helper files.
type databasePathProvider interface {
	DBPath() string
}

// repoDBPath returns the resolved on-disk database path for repositories that
// expose one (e.g. *db.SQLiteRepository), or "" for in-memory/path-less repos.
func repoDBPath(repo any) string {
	if r, ok := repo.(databasePathProvider); ok {
		return strings.TrimSpace(r.DBPath())
	}
	return ""
}

// decryptConfigSecretsWithDBDir decrypts cfg using the secret-encryption helper
// (web-auth.json) resolved from the config's stored db_path. If that helper is
// fails (e.g. the persisted db_path is empty, points at a pre-upgrade location,
// or has stale helper material), it retries anchored to runtimeDBPath, the
// repository's actual on-disk database, without changing the returned config's
// stored db_path. The stored-path-first order preserves existing behavior for callers
// that intentionally rely on the snapshot's db_path.
func decryptConfigSecretsWithDBDir(cfg *Config, runtimeDBPath string) (*Config, error) {
	decrypted, err := DecryptConfigSecrets(cfg)
	if err == nil {
		return decrypted, nil
	}

	runtimeDBPath = strings.TrimSpace(runtimeDBPath)
	if runtimeDBPath == "" || runtimeDBPath == strings.TrimSpace(cfg.MainSettings.DBPath) {
		return nil, err
	}

	probe := *cfg
	probe.MainSettings.DBPath = runtimeDBPath
	retried, retryErr := DecryptConfigSecrets(&probe)
	if retryErr != nil {
		return nil, errors.Join(err, fmt.Errorf("runtime db path decrypt retry: %w", retryErr))
	}
	retried.MainSettings.DBPath = cfg.MainSettings.DBPath
	return retried, nil
}

func encryptConfigSecretsForSectionsWithDBDir(cfg *Config, sections []string, runtimeDBPath string) (*Config, error) {
	runtimeDBPath = strings.TrimSpace(runtimeDBPath)
	if runtimeDBPath == "" || runtimeDBPath == strings.TrimSpace(cfg.MainSettings.DBPath) {
		return encryptConfigSecretsForSections(cfg, sections)
	}

	probe, err := cloneConfig(cfg)
	if err != nil {
		return nil, err
	}
	storedDBPath := probe.MainSettings.DBPath
	probe.MainSettings.DBPath = runtimeDBPath
	encrypted, err := encryptConfigSecretsForSections(probe, sections)
	if err != nil {
		return nil, err
	}
	encrypted.MainSettings.DBPath = storedDBPath
	return encrypted, nil
}

// SaveToDatabase persists the config to the repository.
func SaveToDatabase(ctx context.Context, cfg *Config, repo interface {
	SaveFullConfig(ctx context.Context, cfg any) error
}) error {
	if cfg == nil {
		return internalerrors.ErrInvalidInput
	}
	if repo == nil {
		return errors.New("config save: nil repository")
	}

	encryptedCfg, err := EncryptConfigSecrets(cfg)
	if err != nil {
		return fmt.Errorf("config save to database: encrypt secrets: %w", err)
	}

	if err := repo.SaveFullConfig(ctx, encryptedCfg); err != nil {
		return fmt.Errorf("config save to database: %w", err)
	}

	return nil
}

// SaveSectionsToDatabase persists only selected root config sections. Section
// names may use exported field names or root yaml/json tag aliases, and every
// requested name is canonicalized before any write. Only selected sections are
// prepared for secret encryption; repositories that support batch section saves
// receive all selected payloads in one call, preserving same-root unknown
// stored fields when the full raw config is available. Repositories without
// batch support can save one canonical section only; multi-section requests
// fail before any write.
func SaveSectionsToDatabase(ctx context.Context, cfg *Config, sections []string, repo interface {
	SaveConfigSection(ctx context.Context, section string, data any) error
}) error {
	if cfg == nil {
		return internalerrors.ErrInvalidInput
	}
	if repo == nil {
		return errors.New("config save sections: nil repository")
	}
	sections = sortedUniqueStrings(sections)
	if len(sections) == 0 {
		return nil
	}

	plainSectionData, err := configJSONMap(cfg)
	if err != nil {
		return fmt.Errorf("config save sections to database: section map: %w", err)
	}
	sections, err = canonicalConfigSectionNames(plainSectionData, sections)
	if err != nil {
		return fmt.Errorf("config save sections to database: %w", err)
	}

	encryptedCfg, err := encryptConfigSecretsForSectionsWithDBDir(cfg, sections, repoDBPath(repo))
	if err != nil {
		return fmt.Errorf("config save sections to database: encrypt secrets: %w", err)
	}
	sectionData, err := configJSONMap(encryptedCfg)
	if err != nil {
		return fmt.Errorf("config save sections to database: section map: %w", err)
	}
	sectionPayloads := make(map[string]any, len(sections))
	for _, section := range sections {
		data, ok := sectionData[section]
		if !ok {
			return fmt.Errorf("config save sections to database: unknown section %q", section)
		}
		sectionPayloads[section] = data
	}
	if repo, ok := repo.(interface {
		SaveConfigSections(ctx context.Context, sections map[string]any) error
	}); ok {
		if err := preserveStoredUnknownConfigValues(ctx, repo, sectionPayloads); err != nil {
			return fmt.Errorf("config save sections to database: preserve stored fields: %w", err)
		}
		if err := repo.SaveConfigSections(ctx, sectionPayloads); err != nil {
			return fmt.Errorf("config save sections to database: %w", err)
		}
		return nil
	}
	if len(sections) > 1 {
		return errors.New("config save sections to database: repository requires batch section save for multiple sections")
	}
	for _, section := range sections {
		data := sectionPayloads[section]
		if err := repo.SaveConfigSection(ctx, section, data); err != nil {
			return fmt.Errorf("config save section %s to database: %w", section, err)
		}
	}
	return nil
}

// SaveSectionToDatabase persists a single config section to the repository.
func SaveSectionToDatabase(ctx context.Context, section string, data any, repo interface {
	SaveConfigSection(ctx context.Context, section string, data any) error
}) error {
	if section == "" {
		return errors.New("config save section: empty section name")
	}
	if data == nil {
		return internalerrors.ErrInvalidInput
	}
	if repo == nil {
		return errors.New("config save section: nil repository")
	}

	if err := repo.SaveConfigSection(ctx, section, data); err != nil {
		return fmt.Errorf("config save section %s to database: %w", section, err)
	}

	return nil
}

// LoadSectionFromDatabase retrieves a single config section from the repository.
func LoadSectionFromDatabase(ctx context.Context, section string, dest any, repo interface {
	LoadConfigSection(ctx context.Context, section string, dest any) error
}) error {
	if section == "" {
		return errors.New("config load section: empty section name")
	}
	if dest == nil {
		return internalerrors.ErrInvalidInput
	}
	if repo == nil {
		return errors.New("config load section: nil repository")
	}

	if err := repo.LoadConfigSection(ctx, section, dest); err != nil {
		return fmt.Errorf("config load section %s from database: %w", section, err)
	}

	return nil
}

// ExportFromDatabaseToYAML loads config from database, applies environment overrides,
// and writes the resulting config to a YAML file.
func ExportFromDatabaseToYAML(ctx context.Context, outputPath string, repo interface {
	LoadFullConfig(ctx context.Context, dest any) error
}) error {
	return exportFromDatabaseToYAML(ctx, outputPath, repo, true)
}

// ExportFromDatabaseToPlaintextYAML loads config from database, applies environment overrides,
// and writes the resulting config to a YAML file without encrypting secret fields.
func ExportFromDatabaseToPlaintextYAML(ctx context.Context, outputPath string, repo interface {
	LoadFullConfig(ctx context.Context, dest any) error
}) error {
	return exportFromDatabaseToYAML(ctx, outputPath, repo, false)
}

func exportFromDatabaseToYAML(ctx context.Context, outputPath string, repo interface {
	LoadFullConfig(ctx context.Context, dest any) error
}, encryptSecrets bool) error {
	if strings.TrimSpace(outputPath) == "" {
		return errors.New("config export from database: empty output path")
	}

	cfg, err := LoadFromDatabase(ctx, repo)
	if err != nil {
		return fmt.Errorf("config export from database: load: %w", err)
	}

	ApplyEnvOverrides(cfg)
	var exportErr error
	if encryptSecrets {
		exportErr = ExportToYAML(cfg, outputPath)
	} else {
		exportErr = ExportToPlaintextYAML(cfg, outputPath)
	}
	if exportErr != nil {
		return fmt.Errorf("config export from database: %w", exportErr)
	}

	return nil
}

func exportableConfig(cfg *Config, encryptSecrets bool) (*Config, error) {
	if !encryptSecrets {
		return cloneConfig(cfg)
	}

	encryptedCfg, err := EncryptConfigSecrets(cfg)
	if err != nil {
		return nil, fmt.Errorf("config export: encrypt secrets: %w", err)
	}

	return encryptedCfg, nil
}
