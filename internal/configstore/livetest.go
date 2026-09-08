// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package configstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/config/importer"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/livetest"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
)

// LiveTestProfileOptions controls explicit, clone-only live-test adjustments.
type LiveTestProfileOptions struct {
	PreferDeletableHosts bool
	Registry             *trackers.Registry
}

// LoadLiveTestConfig loads an already published profile without applying the
// current environment. Runtime and cleanup therefore use the configuration
// captured at initialization, including the original image-host credentials.
func LoadLiveTestConfig(ctx context.Context, profile livetest.Profile) (*config.Config, error) {
	ready, err := livetest.LoadProfile(profile.RunDir)
	if err != nil {
		return nil, fmt.Errorf("live-test validate ready profile: %w", err)
	}
	if ready.Version != profile.Version || ready.RunID != profile.RunID || ready.RunDir != profile.RunDir ||
		ready.DBPath != profile.DBPath || ready.ConfigPath != profile.ConfigPath || ready.SourceKind != profile.SourceKind ||
		ready.SourceFingerprint != profile.SourceFingerprint || !slices.Equal(ready.DefaultTrackers, profile.DefaultTrackers) {
		return nil, errors.New("live-test ready profile identity mismatch")
	}
	loaded, err := loadFromDBPath(ctx, ready.DBPath, false)
	if err != nil {
		return nil, fmt.Errorf("live-test load captured config: %w", err)
	}
	ApplyLiveTestPaths(loaded, ready)
	return loaded, nil
}

// ApplyLiveTestPaths pins writable configuration paths to a validated profile.
// Callers validate the profile before runtime activation; construction uses
// this same helper before publishing it. Discovery mappings remain unchanged.
func ApplyLiveTestPaths(cfg *config.Config, profile livetest.Profile) {
	if cfg == nil {
		return
	}
	cfg.MainSettings.DBPath = profile.DBPath
	for name, client := range cfg.TorrentClients {
		client.WatchFolder = filepath.Join(profile.RunDir, "artifacts", "watch")
		client.StorageDir = filepath.Join(profile.RunDir, "artifacts", "torrents")
		if len(client.LinkedFolder) != 0 {
			client.LinkedFolder = config.StringList{filepath.Join(profile.RunDir, "artifacts", "links")}
		}
		cfg.TorrentClients[name] = client
	}
}

// ValidateLiveTestTrackerConfigNames rejects ambiguous tracker aliases before
// a live runtime can bind provider credentials or policy inputs.
func ValidateLiveTestTrackerConfigNames(trackers map[string]config.TrackerConfig) error {
	names := slices.Sorted(maps.Keys(trackers))
	for index, name := range names {
		for _, other := range names[index+1:] {
			if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(other)) {
				return fmt.Errorf("live-test tracker configuration has duplicate case-insensitive names %q and %q", name, other)
			}
		}
	}
	return nil
}

// CreateLiveTestProfile snapshots real configuration and matching auth material
// into a fresh private run. Only the clone is migrated or mutated. Backup failures
// remove the new run directory; later construction failures leave a restricted
// directory without a runnable ready marker.
func CreateLiveTestProfile(ctx context.Context, sourceConfigPath string, sourceProvided bool, runDir string) (livetest.Profile, error) {
	return CreateLiveTestProfileWithOptions(ctx, sourceConfigPath, sourceProvided, runDir, LiveTestProfileOptions{})
}

// CreateLiveTestProfileWithOptions snapshots real configuration and applies
// explicit live-test preferences only to the isolated clone.
func CreateLiveTestProfileWithOptions(
	ctx context.Context,
	sourceConfigPath string,
	sourceProvided bool,
	runDir string,
	options LiveTestProfileOptions,
) (livetest.Profile, error) {
	if ctx == nil {
		return livetest.Profile{}, errors.New("live-test context is required")
	}
	if options.PreferDeletableHosts && options.Registry == nil {
		return livetest.Profile{}, errors.New("live-test deletable image-host preference requires a tracker registry")
	}
	runDir, err := livetest.ValidateRunDir(runDir)
	if err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test validate destination: %w", err)
	}
	sourcePath, providedData, imported, err := liveTestSource(sourceConfigPath, sourceProvided)
	if err != nil {
		return livetest.Profile{}, err
	}
	if pathing.IsWithinRoot(runDir, sourcePath) || pathing.IsWithinRoot(filepath.Dir(sourcePath), runDir) {
		return livetest.Profile{}, errors.New("live-test source and destination overlap")
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test source database is required: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return livetest.Profile{}, errors.New("live-test source database is not a regular file")
	}
	record, err := authmaterial.LoadRecordFromDBPath(sourcePath)
	if err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test source auth material: %w", err)
	}
	if record.PendingUpgrade != nil {
		return livetest.Profile{}, errors.New("live-test source has a pending auth upgrade")
	}
	authBefore, err := os.ReadFile(authmaterial.AuthFilePath(sourcePath))
	if err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test read auth snapshot: %w", err)
	}
	var matching authmaterial.Record
	if err := json.Unmarshal(authBefore, &matching); err != nil {
		return livetest.Profile{}, errors.New("live-test auth snapshot is invalid")
	}
	if matching.AuthMaterial() != record.AuthMaterial() || matching.PendingUpgrade != nil {
		return livetest.Profile{}, errors.New("live-test auth material changed during snapshot")
	}
	if err := os.MkdirAll(filepath.Dir(runDir), 0o700); err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test create runs root: %w", err)
	}
	if err := os.Mkdir(runDir, 0o700); err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test requires a new run directory: %w", err)
	}
	profileDir := filepath.Join(runDir, "profile")
	if err := os.Mkdir(profileDir, 0o700); err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test create profile directory: %w", err)
	}
	p := livetest.Profile{
		Version:    1,
		RunID:      filepath.Base(runDir),
		RunDir:     runDir,
		DBPath:     filepath.Join(profileDir, "db.sqlite"),
		ConfigPath: filepath.Join(profileDir, DefaultConfigFileName),
		SourceKind: "database",
	}
	if sourceProvided {
		p.SourceKind = "provided_config"
	}
	if err := db.BackupReadOnly(ctx, sourcePath, p.DBPath); err != nil {
		if cleanupErr := os.RemoveAll(runDir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove incomplete run directory: %w", cleanupErr))
		}
		return livetest.Profile{}, fmt.Errorf("live-test snapshot database: %w", err)
	}
	p.SourceFingerprint, err = liveTestSnapshotFingerprint(p.DBPath)
	if err != nil {
		return livetest.Profile{}, err
	}
	if err := cloneLegacyCookies(sourcePath, p.DBPath); err != nil {
		return livetest.Profile{}, err
	}
	authAfter, err := os.ReadFile(authmaterial.AuthFilePath(sourcePath))
	if err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test recheck auth snapshot: %w", err)
	}
	sourceAfter, err := os.Stat(sourcePath)
	if err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test recheck source identity: %w", err)
	}
	if !bytes.Equal(authBefore, authAfter) || !os.SameFile(sourceInfo, sourceAfter) {
		return livetest.Profile{}, errors.New("live-test source identity changed during snapshot")
	}
	if sourceProvided {
		after, err := os.ReadFile(sourceConfigPath)
		if err != nil {
			return livetest.Profile{}, fmt.Errorf("live-test recheck provided config: %w", err)
		}
		if !bytes.Equal(providedData, after) {
			return livetest.Profile{}, errors.New("live-test provided config changed during snapshot")
		}
	}
	record = matching
	record.APIKeys = nil
	record.AllowUnencryptedExport = false
	record.BrowseRoot = runDir
	record.AllowUnrestrictedBrowse = false
	encoded, err := json.Marshal(record)
	if err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test encode private auth: %w", err)
	}
	if err := os.WriteFile(authmaterial.AuthFilePath(p.DBPath), encoded, 0o600); err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test write private auth: %w", err)
	}
	if err := prepareLiveTestClone(ctx, p, sourceConfigPath, providedData, imported, options); err != nil {
		return livetest.Profile{}, err
	}
	loaded, err := loadFromDBPath(ctx, p.DBPath, false)
	if err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test reopen cloned config: %w", err)
	}
	p.DefaultTrackers = slices.Clone([]string(loaded.Trackers.DefaultTrackers))
	for i, id := range p.DefaultTrackers {
		p.DefaultTrackers[i] = strings.ToUpper(strings.TrimSpace(id))
	}
	if err := config.ExportToYAML(loaded, p.ConfigPath); err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test export cloned config: %w", err)
	}
	if err := livetest.PublishProfile(p); err != nil {
		return livetest.Profile{}, fmt.Errorf("live-test publish profile: %w", err)
	}
	return p, nil
}

func liveTestSnapshotFingerprint(dbPath string) (string, error) {
	file, err := os.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("live-test open snapshot fingerprint: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("live-test fingerprint snapshot: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// cloneLegacyCookies copies only regular cookie files into the private DB
// directory. Runtime migration may delete these copies, never the originals.
func cloneLegacyCookies(sourceDBPath, cloneDBPath string) error {
	sourceDir := filepath.Join(filepath.Dir(sourceDBPath), "cookies")
	info, err := os.Lstat(sourceDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("live-test inspect legacy cookies: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("live-test legacy cookie directory has an unsafe identity")
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("live-test list legacy cookies: %w", err)
	}
	cloneDir := filepath.Join(filepath.Dir(cloneDBPath), "cookies")
	if err := os.Mkdir(cloneDir, 0o700); err != nil {
		return fmt.Errorf("live-test create legacy cookie directory: %w", err)
	}
	cloneRoot, err := os.OpenRoot(cloneDir)
	if err != nil {
		return fmt.Errorf("live-test open legacy cookie destination: %w", err)
	}
	defer cloneRoot.Close()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(entry.Name())); ext != ".json" && ext != ".txt" {
			continue
		}
		path := filepath.Join(sourceDir, entry.Name())
		before, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("live-test inspect legacy cookie: %w", err)
		}
		if !before.Mode().IsRegular() {
			return errors.New("live-test legacy cookie is not a regular file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("live-test read legacy cookie: %w", err)
		}
		after, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("live-test recheck legacy cookie: %w", err)
		}
		if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return errors.New("live-test legacy cookies changed during snapshot")
		}
		if err := cloneRoot.WriteFile(entry.Name(), data, 0o600); err != nil {
			return fmt.Errorf("live-test copy legacy cookie: %w", err)
		}
	}
	return nil
}

func liveTestSource(configPath string, provided bool) (string, []byte, *config.Config, error) {
	if !provided {
		path, err := db.DefaultPath()
		if err != nil {
			return "", nil, nil, fmt.Errorf("live-test resolve database: %w", err)
		}
		return path, nil, nil, nil
	}
	path, err := ResolveYAMLPath(configPath, true)
	if err != nil {
		return "", nil, nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, fmt.Errorf("live-test read provided config: %w", err)
	}
	imported, _, err := importer.ImportFromContent(path, data)
	if err != nil {
		return "", nil, nil, fmt.Errorf("live-test import provided config: %w", err)
	}
	dbPath, err := resolveDBPath(imported)
	return dbPath, data, imported, err
}

func prepareLiveTestClone(
	ctx context.Context,
	p livetest.Profile,
	configPath string,
	data []byte,
	imported *config.Config,
	options LiveTestProfileOptions,
) error {
	repo, err := db.OpenContext(ctx, p.DBPath)
	if err != nil {
		return fmt.Errorf("live-test open clone: %w", err)
	}
	defer repo.Close()
	if err := repo.ValidateLiveTestSchema(ctx); err != nil {
		return fmt.Errorf("live-test validate schema: %w", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		return fmt.Errorf("live-test migrate clone: %w", err)
	}
	// Rebase before decrypting so config loading cannot fall back to source helpers.
	if _, err := repo.RawDB().ExecContext(
		ctx,
		`UPDATE config_settings SET data = json_set(data, '$.DBPath', ?) WHERE section = 'MainSettings'`,
		p.DBPath,
	); err != nil {
		return fmt.Errorf("live-test rebase config helper: %w", err)
	}
	var trackersJSON string
	if err := repo.RawDB().QueryRowContext(ctx, "SELECT data FROM config_settings WHERE section = 'Trackers'").Scan(&trackersJSON); err != nil {
		return fmt.Errorf("live-test source tracker configuration is required: %w", err)
	}
	var trackers config.TrackersConfig
	if err := json.Unmarshal([]byte(trackersJSON), &trackers); err != nil {
		return errors.New("live-test source tracker configuration is invalid")
	}
	loaded, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		return fmt.Errorf("live-test decrypt cloned config: %w", err)
	}
	// Never obtain test scope from embedded defaults; explicit YAML or env
	// overrides may supply a scope when the stored list is empty.
	loaded.Trackers.DefaultTrackers = slices.Clone(trackers.DefaultTrackers)
	if imported != nil {
		loaded, err = mergeProvidedConfig(loaded, configPath, data, imported)
		if err != nil {
			return err
		}
	}
	config.ApplyEnvOverrides(loaded)
	ApplyLiveTestPaths(loaded, p)
	if len(loaded.Trackers.DefaultTrackers) == 0 {
		return errors.New("live-test effective default tracker list is empty")
	}
	if err := ValidateLiveTestTrackerConfigNames(loaded.Trackers.Trackers); err != nil {
		return err
	}
	if options.PreferDeletableHosts {
		preferLiveTestDeletableHost(loaded, options.Registry)
	}
	if err := validateLiveTestAuth(ctx, repo, p.DBPath); err != nil {
		return err
	}
	if err := repo.PruneLiveTestState(ctx); err != nil {
		return fmt.Errorf("live-test prune snapshot: %w", err)
	}
	if err := SaveToRepository(ctx, loaded, repo, p.DBPath); err != nil {
		return fmt.Errorf("live-test persist isolated config: %w", err)
	}
	return nil
}

func preferLiveTestDeletableHost(cfg *config.Config, registry *trackers.Registry) {
	const host = "lostimg"
	configured, known := cfg.ImageHosting.ConditionalHostConfigured(host)
	if !known || !configured {
		return
	}
	owner := registry.OwnerForImageHost(host)
	selectedTrackers := slices.Clone([]string(cfg.Trackers.DefaultTrackers))
	for configuredTracker := range cfg.Trackers.Trackers {
		selectedTrackers = append(selectedTrackers, configuredTracker)
	}
	for _, selected := range selectedTrackers {
		tracker := strings.ToUpper(strings.TrimSpace(selected))
		if tracker == "" || (owner != "" && !strings.EqualFold(owner, tracker)) {
			continue
		}
		declared, ok := registry.LookupImageHostPolicy(tracker)
		if !ok || (!strings.EqualFold(strings.TrimSpace(declared.ConditionalHost), host) &&
			!slices.ContainsFunc(declared.AllowedHosts, func(candidate string) bool { return strings.EqualFold(strings.TrimSpace(candidate), host) })) {
			continue
		}
		key := tracker
		for configuredTracker := range cfg.Trackers.Trackers {
			if strings.EqualFold(strings.TrimSpace(configuredTracker), tracker) {
				key = configuredTracker
				break
			}
		}
		if cfg.Trackers.Trackers == nil {
			cfg.Trackers.Trackers = make(map[string]config.TrackerConfig)
		}
		trackerCfg := cfg.Trackers.Trackers[key]
		trackerCfg.ImageHost = host
		cfg.Trackers.Trackers[key] = trackerCfg
	}
}

func validateLiveTestAuth(ctx context.Context, repo *db.SQLiteRepository, dbPath string) error {
	key, err := cookies.NewKeyManager(repo.RawDB()).InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("live-test cloned auth does not match snapshot: %w", err)
	}
	store, err := cookies.NewCookieStore(repo.RawDB())
	if err != nil {
		return fmt.Errorf("live-test cookie store: %w", err)
	}
	stateStore, err := trackerauth.NewAuthStateStore(repo.RawDB())
	if err != nil {
		return fmt.Errorf("live-test auth state store: %w", err)
	}
	for _, table := range []string{"tracker_cookies", "tracker_auth_state"} {
		column := "cookie_name"
		if table == "tracker_auth_state" {
			column = "state_key"
		}
		rows, err := repo.RawDB().QueryContext(ctx, "SELECT tracker_id, "+column+" FROM "+table)
		if err != nil {
			return fmt.Errorf("live-test read auth identities: %w", err)
		}
		defer rows.Close()
		var identities [][2]string
		for rows.Next() {
			var pair [2]string
			if err := rows.Scan(&pair[0], &pair[1]); err != nil {
				_ = rows.Close()
				return fmt.Errorf("live-test read auth identity: %w", err)
			}
			identities = append(identities, pair)
		}
		rowErr := rows.Err()
		_ = rows.Close()
		if rowErr != nil {
			return fmt.Errorf("live-test read auth rows: %w", rowErr)
		}
		for _, pair := range identities {
			if strings.TrimSpace(pair[0]) == "" {
				return errors.New("live-test auth has invalid tracker identity")
			}
			if table == "tracker_cookies" {
				_, err = store.GetCookie(ctx, pair[0], pair[1], key)
			} else {
				_, err = stateStore.Load(ctx, pair[0], pair[1], key)
			}
			if err != nil {
				return errors.New("live-test encrypted auth validation failed")
			}
		}
	}
	return nil
}
