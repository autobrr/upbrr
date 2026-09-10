// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	imagehostpolicy "github.com/autobrr/upbrr/internal/imagehosting/policy"
	"github.com/autobrr/upbrr/internal/languageutil"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliOptions struct {
	LiveTestMaxImages          int
	LiveTest                   bool
	ConfigPath                 string
	ShowVersion                bool
	QueueName                  string
	LimitQueue                 int
	SiteCheck                  bool
	SiteUpload                 string
	Trackers                   string
	TrackersRemove             string
	Debug                      bool
	LogLevel                   string
	ConsoleLogLevel            string
	Screens                    int
	NoSeed                     bool
	SkipAutoTorrent            bool
	KeepFolder                 bool
	OnlyID                     bool
	UploadOnly                 bool
	Category                   string
	Type                       string
	Source                     string
	SourceLookup               string
	Resolution                 string
	Tag                        string
	Service                    string
	Distributor                string
	OriginalLanguage           string
	Edition                    string
	Season                     string
	Episode                    string
	EpisodeTitle               string
	ManualYear                 int
	ManualDate                 string
	NoSeason                   bool
	NoYear                     bool
	NoAKA                      bool
	NoTag                      bool
	NoEpisodeTitle             bool
	NoDistributor              bool
	NoEdition                  bool
	NoDub                      bool
	NoDual                     bool
	DualAudio                  bool
	Region                     string
	CreateAuth                 bool
	ExportConfigPath           string
	ExportConfigPlaintext      bool
	ImportConfigPath           string
	DeleteTmp                  bool
	Cleanup                    bool
	TMDB                       string
	TVDB                       string
	TVmaze                     string
	IMDb                       string
	MAL                        string
	Unattended                 bool
	UnattendedConfirm          bool
	SkipDupeCheck              bool
	SkipDupeAsActual           bool
	DoubleDupeCheck            bool
	Commentary                 bool
	PersonalRelease            bool
	StreamOptimized            bool
	WebDV                      bool
	ConfirmBDMVRescan          bool
	NotAnime                   bool
	Anime                      bool
	UseSeasonEpisode           bool
	InputOnly                  bool
	Title                      string
	AlternateTitle             string
	OriginalTitle              string
	Genres                     string
	AudioLanguages             string
	SubtitleLanguages          string
	HardcodedSubtitleLanguages string
	HardcodedSubs              bool
	TrackLanguages             []string
	ResetInput                 []string
	ConfirmInput               []string
	TrackerInput               []string
	Anon                       bool
	Draft                      bool
	ModQ                       bool
	Channel                    string
	PTP                        string
	BLU                        string
	Aither                     string
	LST                        string
	OE                         string
	HDB                        string
	BTN                        string
	BHD                        string
	ULCX                       string
	DescriptionFile            string
	DescriptionLink            string
	Client                     string
	QbitTag                    string
	QbitCategory               string
	ForceRecheck               bool
	Foreign                    bool
	Opera                      bool
	Asian                      bool
	DiscType                   string
	ImageHost                  string
	SkipImageUpload            bool
	ManualFrames               string
	Comparison                 string
	ComparisonIndex            int
	MenuImages                 string
	GetDVDMenus                bool
	InfoHash                   string
	MaxPieceSize               int
	NoHash                     bool
	Rehash                     bool
}

type serveOptions struct {
	LiveTestMaxImages int
	LiveTest          bool
	ConfigPath        string
	Addr              string
	Host              string
	Port              int
	BaseURL           string
	PersistListen     bool
	PersistWebConfig  bool
	DevNoAuth         bool
}

func bindUploadFlags(fs *pflag.FlagSet, opts *cliOptions) {
	fs.IntVar(&opts.LiveTestMaxImages, "live-test-max-images", 0, "Maximum journaled image uploads in this live-test run (zero keeps captures local)")
	fs.BoolVar(&opts.LiveTest, "live-test", false, "Use an isolated live-test profile with tracker and client writes disabled")
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to config file")
	fs.BoolVar(&opts.ShowVersion, "version", false, "Show version and exit")
	fs.StringVar(&opts.QueueName, "queue", "", "Process an entire folder queue")
	fs.IntVar(&opts.LimitQueue, "limit-queue", 0, "Limit the number of queued items to process")
	fs.IntVar(&opts.LimitQueue, "lq", 0, "Limit the number of queued items to process")
	fs.BoolVar(&opts.SiteCheck, "site-check", false, "Search/check sites without uploading")
	fs.BoolVar(&opts.SiteCheck, "sc", false, "Search/check sites without uploading")
	fs.StringVar(&opts.SiteUpload, "site-upload", "", "Process a single tracker upload flow")
	fs.StringVar(&opts.SiteUpload, "su", "", "Process a single tracker upload flow")
	fs.StringVar(&opts.Trackers, "trackers", "", "Upload to these trackers (comma-separated)")
	fs.StringVar(&opts.Trackers, "tk", "", "Upload to these trackers (comma-separated)")
	fs.StringVar(&opts.TrackersRemove, "trackers-remove", "", "Remove these trackers (comma-separated)")
	fs.StringVar(&opts.TrackersRemove, "rtk", "", "Remove these trackers (comma-separated)")
	fs.BoolVar(&opts.Debug, "debug", false, "Enable debug mode")
	fs.StringVar(&opts.LogLevel, "log-level", "", "Set application log level for this run (error, warn, info, debug, trace)")
	fs.StringVar(
		&opts.ConsoleLogLevel,
		"console-log-level",
		"",
		"Set console log level for this run without changing application logs (error, warn, info, debug, trace)",
	)
	fs.StringVar(&opts.ConsoleLogLevel, "cll", "", "Set console log level for this run without changing application logs (error, warn, info, debug, trace)")
	fs.IntVar(&opts.Screens, "screens", -1, "Number of screenshots to take")
	fs.IntVar(&opts.Screens, "s", -1, "Number of screenshots to take")
	fs.BoolVar(&opts.NoSeed, "no-seed", false, "Do not inject torrent into clients")
	fs.BoolVar(&opts.NoSeed, "ns", false, "Do not inject torrent into clients")
	fs.BoolVar(&opts.SkipAutoTorrent, "skip_auto_torrent", false, "Skip automated torrent client searching")
	fs.BoolVar(&opts.SkipAutoTorrent, "sat", false, "Skip automated torrent client searching")
	fs.BoolVar(&opts.KeepFolder, "keep-folder", false, "Keep a supplied folder instead of processing its selected video file directly")
	fs.BoolVar(&opts.KeepFolder, "kf", false, "Keep a supplied folder instead of processing its selected video file directly")
	fs.BoolVar(&opts.OnlyID, "onlyID", false, "Only grab tracker metadata IDs")
	fs.BoolVar(&opts.UploadOnly, "upload-only", false, "Upload using prepared metadata cache only")
	fs.StringVar(&opts.Category, "category", "", "Override category")
	fs.StringVar(&opts.Category, "c", "", "Override category")
	fs.StringVar(&opts.Type, "type", "", "Override release type")
	fs.StringVar(&opts.Type, "t", "", "Override release type")
	fs.StringVar(&opts.Source, "source", "", "Override source")
	fs.StringVar(&opts.Resolution, "resolution", "", "Override resolution")
	fs.StringVar(&opts.Resolution, "res", "", "Override resolution")
	fs.StringVar(&opts.Tag, "tag", "", "Override group tag")
	fs.StringVar(&opts.Tag, "g", "", "Override group tag")
	fs.StringVar(&opts.Service, "service", "", "Override streaming service")
	fs.StringVar(&opts.Service, "serv", "", "Override streaming service")
	fs.StringVar(&opts.Distributor, "distributor", "", "Override distributor")
	fs.StringVar(&opts.Distributor, "dist", "", "Override distributor")
	fs.StringVar(&opts.OriginalLanguage, "original-language", "", "Override original language")
	fs.StringVar(&opts.OriginalLanguage, "ol", "", "Override original language")
	fs.StringVar(&opts.Edition, "edition", "", "Override edition text")
	fs.StringVar(&opts.Edition, "repack", "", "Override edition text")
	fs.StringVar(&opts.Season, "season", "", "Override season value (single token such as 5 or S05)")
	fs.StringVar(&opts.Episode, "episode", "", "Override episode value (single token such as 5 or E05)")
	fs.StringVar(&opts.EpisodeTitle, "episode-title", "", "Override episode title")
	fs.StringVar(&opts.EpisodeTitle, "manual-episode-title", "", "Override episode title")
	fs.StringVar(&opts.EpisodeTitle, "met", "", "Override episode title")
	fs.IntVar(&opts.ManualYear, "manual-year", 0, "Override release year")
	fs.IntVar(&opts.ManualYear, "year", 0, "Override release year")
	fs.StringVar(&opts.ManualDate, "daily", "", "Set daily episode air date (YYYY-MM-DD)")
	fs.BoolVar(&opts.NoSeason, "no-season", false, "Remove season and episode from name")
	fs.BoolVar(&opts.NoYear, "no-year", false, "Remove year from name")
	fs.BoolVar(&opts.NoAKA, "no-aka", false, "Remove AKA from name")
	fs.BoolVar(&opts.NoTag, "no-tag", false, "Remove group tag from name")
	fs.BoolVar(&opts.NoEpisodeTitle, "no-episode-title", false, "Remove episode title from name")
	fs.BoolVar(&opts.NoEpisodeTitle, "net", false, "Remove episode title from name")
	fs.BoolVar(&opts.NoDistributor, "no-distributor", false, "Remove distributor")
	fs.BoolVar(&opts.NoDistributor, "ndist", false, "Remove distributor")
	fs.BoolVar(&opts.NoEdition, "no-edition", false, "Remove edition from name")
	fs.BoolVar(&opts.NoEdition, "ne", false, "Remove edition from name")
	fs.BoolVar(&opts.NoDub, "no-dub", false, "Remove dubbed tag from audio name")
	fs.BoolVar(&opts.NoDual, "no-dual", false, "Remove dual-audio tag from audio name")
	fs.BoolVar(&opts.DualAudio, "dual-audio", false, "Add dual-audio tag to audio name")
	fs.BoolVar(&opts.CreateAuth, "create-auth", false, "Create web-auth.json beside the active database and exit")
	fs.StringVar(&opts.ExportConfigPath, "export-config", "", "Export SQLite config to YAML file and exit")
	fs.BoolVar(&opts.ExportConfigPlaintext, "export-config-plaintext", false, "Export config with plaintext secrets (requires --export-config)")
	fs.StringVar(&opts.ImportConfigPath, "import-config", "", "Import config file (.py, .yaml, .yml, .json) and exit")
	fs.BoolVar(&opts.DeleteTmp, "dtmp", false, "Delete stored database content for each input path before upload")
	fs.BoolVar(&opts.DeleteTmp, "delete-tmp", false, "Delete stored database content for each input path before upload")
	fs.BoolVar(&opts.Cleanup, "cleanup", false, "Delete all stored database content for all releases and exit")
	fs.StringVar(&opts.Region, "region", "", "Override disc region")
	fs.StringVar(&opts.Region, "reg", "", "Override disc region")
	fs.StringVar(&opts.TMDB, "tmdb", "", "Override TMDB id; empty or 0 clears and skips this provider")
	fs.StringVar(&opts.IMDb, "imdb", "", "Override IMDb id; empty or 0 clears and skips this provider")
	fs.StringVar(&opts.MAL, "mal", "", "Override MAL id; empty or 0 clears and skips this provider")
	fs.StringVar(&opts.TVDB, "tvdb", "", "Override TVDB id; empty or 0 clears and skips this provider")
	fs.StringVar(&opts.TVmaze, "tvmaze", "", "Override TVmaze id; empty or 0 clears and skips this provider")
	fs.StringVar(&opts.PTP, "ptp", "", "PTP torrent id or URL")
	fs.StringVar(&opts.BLU, "blu", "", "BLU torrent id or URL")
	fs.StringVar(&opts.Aither, "aither", "", "Aither torrent id or URL")
	fs.StringVar(&opts.LST, "lst", "", "LST torrent id or URL")
	fs.StringVar(&opts.OE, "oe", "", "OE torrent id or URL")
	fs.StringVar(&opts.HDB, "hdb", "", "HDB torrent id or URL")
	fs.StringVar(&opts.BTN, "btn", "", "BTN torrent id or URL")
	fs.StringVar(&opts.BHD, "bhd", "", "BHD torrent id or URL")
	fs.StringVar(&opts.ULCX, "ulcx", "", "ULCX torrent id or URL")
	fs.StringVar(&opts.DescriptionFile, "descfile", "", "Custom description file path")
	fs.StringVar(&opts.DescriptionFile, "df", "", "Custom description file path")
	fs.StringVar(&opts.DescriptionLink, "desclink", "", "Custom description link")
	fs.StringVar(&opts.DescriptionLink, "pb", "", "Custom description link")
	fs.StringVar(&opts.Client, "client", "", "Override torrent client")
	fs.StringVar(&opts.QbitTag, "qbit-tag", "", "Override qBittorrent tag")
	fs.StringVar(&opts.QbitTag, "qbt", "", "Override qBittorrent tag")
	fs.StringVar(&opts.QbitCategory, "qbit-cat", "", "Override qBittorrent category")
	fs.StringVar(&opts.QbitCategory, "qbc", "", "Override qBittorrent category")
	fs.BoolVar(&opts.ForceRecheck, "force-recheck", false, "Force recheck matched qBittorrent torrents before validation")
	fs.BoolVar(&opts.ForceRecheck, "frc", false, "Force recheck matched qBittorrent torrents before validation")
	fs.BoolVar(&opts.Foreign, "foreign", false, "Mark TIK release as foreign")
	fs.BoolVar(&opts.Opera, "opera", false, "Mark TIK release as opera or musical")
	fs.BoolVar(&opts.Asian, "asian", false, "Mark TIK release as asian")
	fs.StringVar(&opts.DiscType, "disctype", "", "Override TIK disc type")
	fs.StringVar(&opts.ImageHost, "imghost", "", "Override image host")
	fs.StringVar(&opts.ImageHost, "ih", "", "Override image host")
	fs.BoolVar(&opts.SkipImageUpload, "skip-imagehost-upload", false, "Skip automatic image host uploads")
	fs.BoolVar(&opts.SkipImageUpload, "siu", false, "Skip automatic image host uploads")
	fs.StringVar(&opts.ManualFrames, "manual_frames", "", "Comma-separated frame numbers to use for screenshots")
	fs.StringVar(&opts.ManualFrames, "mf", "", "Comma-separated frame numbers to use for screenshots")
	fs.StringVar(&opts.Comparison, "comparison", "", "Comparison folder path or comma-separated paths")
	fs.StringVar(&opts.Comparison, "comps", "", "Comparison folder path or comma-separated paths")
	fs.IntVar(&opts.ComparisonIndex, "comparison_index", 0, "Primary comparison index")
	fs.IntVar(&opts.ComparisonIndex, "comps_index", 0, "Primary comparison index")
	fs.StringVar(&opts.MenuImages, "menu-images", "", "Path to manually captured disc menu screenshots (Disc releases only)")
	fs.BoolVar(&opts.GetDVDMenus, "get-dvd-menus", false, "Capture distinct menus from an extracted DVD VIDEO_TS (requires compatible FFmpeg)")
	fs.StringVar(&opts.InfoHash, "torrenthash", "", "Reuse an existing torrent info hash")
	fs.StringVar(&opts.InfoHash, "th", "", "Reuse an existing torrent info hash")
	fs.StringVar(&opts.InfoHash, "infohash", "", "Override v1 info hash")
	fs.IntVar(&opts.MaxPieceSize, "max-piece-size", 0, "Set maximum torrent piece size in MiB")
	fs.IntVar(&opts.MaxPieceSize, "mps", 0, "Set maximum torrent piece size in MiB")
	fs.BoolVar(&opts.NoHash, "nohash", false, "Reuse existing torrents only without generating a new one")
	fs.BoolVar(&opts.NoHash, "nh", false, "Reuse existing torrents only without generating a new one")
	fs.BoolVar(&opts.Rehash, "rehash", false, "Force generation of a fresh torrent")
	fs.BoolVar(&opts.Rehash, "rh", false, "Force generation of a fresh torrent")
	fs.BoolVar(&opts.Unattended, "ua", false, "Unattended mode")
	fs.BoolVar(&opts.Unattended, "unattended", false, "Unattended mode")
	fs.BoolVar(&opts.UnattendedConfirm, "uac", false, "Unattended mode with prompts")
	fs.BoolVar(&opts.UnattendedConfirm, "unattended_confirm", false, "Unattended mode with prompts")
	fs.BoolVar(&opts.SkipDupeCheck, "sdc", false, "Skip dupe check")
	fs.BoolVar(&opts.SkipDupeCheck, "skip-dupe-check", false, "Skip dupe check")
	fs.BoolVar(&opts.SkipDupeAsActual, "sda", false, "Skip dupe asking")
	fs.BoolVar(&opts.SkipDupeAsActual, "skip-dupe-asking", false, "Skip dupe asking")
	fs.BoolVar(&opts.DoubleDupeCheck, "ddc", false, "Double dupe check")
	fs.BoolVar(&opts.DoubleDupeCheck, "double-dupe-check", false, "Double dupe check")
	fs.BoolVar(&opts.Commentary, "mc", false, "Mark release as containing commentary")
	fs.BoolVar(&opts.Commentary, "commentary", false, "Mark release as containing commentary")
	fs.BoolVar(&opts.PersonalRelease, "pr", false, "Mark release as personal")
	fs.BoolVar(&opts.PersonalRelease, "personalrelease", false, "Mark release as personal")
	fs.BoolVar(&opts.StreamOptimized, "st", false, "Mark release as stream optimized")
	fs.BoolVar(&opts.StreamOptimized, "stream", false, "Mark release as stream optimized")
	fs.BoolVar(&opts.WebDV, "webdv", false, "Mark release as WEB-DV")
	fs.BoolVar(&opts.NotAnime, "not-anime", false, "Force release to be treated as not anime")
	fs.BoolVar(&opts.Anime, "anime", false, "Force release to be treated as anime")
	fs.BoolVar(&opts.UseSeasonEpisode, "use-season-episode", false, "Use the explicit season and episode values")
	fs.BoolVar(&opts.InputOnly, "input-only", false, "Prepare and evaluate local tracker input without remote tracker work")
	fs.StringVar(&opts.SourceLookup, "source-lookup", "", "Use a tracker source URL for metadata lookup")
	fs.StringVar(&opts.Title, "title", "", "Override title")
	fs.StringVar(&opts.AlternateTitle, "alternate-title", "", "Override alternate title")
	fs.StringVar(&opts.OriginalTitle, "original-title", "", "Override original title")
	fs.StringVar(&opts.Genres, "genres", "", "Override genres (comma-separated)")
	fs.StringVar(&opts.AudioLanguages, "audio-languages", "", "Override audio languages (comma-separated)")
	fs.StringVar(&opts.SubtitleLanguages, "subtitle-languages", "", "Override subtitle languages (comma-separated)")
	fs.StringVar(&opts.HardcodedSubtitleLanguages, "hardcoded-subtitle-languages", "", "Override hardcoded subtitle languages (comma-separated)")
	fs.BoolVar(&opts.HardcodedSubs, "hardcoded-subs", false, "Mark release as containing hardcoded subtitles")
	fs.BoolVar(&opts.HardcodedSubs, "hc", false, "Mark release as containing hardcoded subtitles")
	fs.StringArrayVar(&opts.TrackLanguages, "track-languages", nil, "Override track languages as track-id=language[,language]")
	fs.StringArrayVar(&opts.ResetInput, "reset-input", nil, "Reset a saved correction field or field:track-id")
	fs.StringArrayVar(&opts.ConfirmInput, "confirm-input", nil, "Confirm a saved content correction field or field:track-id")
	fs.StringArrayVar(&opts.TrackerInput, "tracker-input", nil, "Set tracker input as TRACKER:field=yes|no|auto")
	fs.BoolVar(&opts.Anon, "a", false, "Upload anonymously")
	fs.BoolVar(&opts.Anon, "anon", false, "Upload anonymously")
	fs.BoolVar(&opts.Draft, "dr", false, "Send uploads to drafts where supported")
	fs.BoolVar(&opts.Draft, "draft", false, "Send uploads to drafts where supported")
	fs.BoolVar(&opts.ModQ, "mq", false, "Opt into mod queue where supported")
	fs.BoolVar(&opts.ModQ, "modq", false, "Opt into mod queue where supported")
	fs.StringVar(&opts.Channel, "ch", "", "Override SPD channel")
	fs.StringVar(&opts.Channel, "channel", "", "Override SPD channel")
	for alias := range cliFlagAliases() {
		_ = fs.MarkHidden(alias)
	}
}

func parseCLIOptions(args []string) (cliOptions, map[string]bool, []string, error) {
	var opts cliOptions
	fs := pflag.NewFlagSet("upbrr", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.SetInterspersed(false)
	bindUploadFlags(fs, &opts)

	flagArgs, positionalArgs := partitionUploadArgs(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return cliOptions{}, nil, nil, fmt.Errorf("parse CLI options: %w", err)
		}
		return cliOptions{}, nil, nil, fmt.Errorf("parse CLI options: %w", translatePFlagError(err))
	}

	visited := canonicalChangedFlags(fs, cliFlagAliases())
	if err := validateCLIInputFlagOccurrences(fs, flagArgs); err != nil {
		return cliOptions{}, nil, nil, err
	}
	if err := normalizeCLIOptions(&opts, visited); err != nil {
		return cliOptions{}, nil, nil, err
	}

	return opts, visited, positionalArgs, nil
}

func normalizeCLIOptions(opts *cliOptions, visited map[string]bool) error {
	if opts.UnattendedConfirm {
		opts.Unattended = true
		visited["unattended"] = true
		visited["unattended_confirm"] = true
	}
	if visited["infohash"] {
		if _, err := parseInfoHash(opts.InfoHash); err != nil {
			return err
		}
	}
	if visited["imghost"] {
		normalized, err := parseImageHost(opts.ImageHost)
		if err != nil {
			return err
		}
		opts.ImageHost = normalized
	}
	if visited["disctype"] {
		normalized, err := parseTIKDiscType(opts.DiscType)
		if err != nil {
			return err
		}
		opts.DiscType = normalized
	}
	if visited["max-piece-size"] {
		if err := validateMaxPieceSize(opts.MaxPieceSize); err != nil {
			return err
		}
	}
	if visited["nohash"] && visited["rehash"] {
		return errors.New("nohash and rehash cannot be used together")
	}
	if visited["anime"] && visited["not-anime"] {
		return errors.New("anime and not-anime cannot be used together")
	}
	if visited["manual_frames"] {
		if _, err := parseManualFrames(opts.ManualFrames); err != nil {
			return err
		}
	}
	if visited["comparison"] {
		if _, err := parseComparisonPaths(opts.Comparison); err != nil {
			return err
		}
	}
	if visited["comparison_index"] {
		if err := validateComparisonIndex(opts.ComparisonIndex); err != nil {
			return err
		}
	}
	if visited["log-level"] {
		normalized, err := api.ParseLogLevel(opts.LogLevel)
		if err != nil {
			return fmt.Errorf("upbrr: %w", err)
		}
		opts.LogLevel = normalized
	}
	if visited["console-log-level"] {
		normalized, err := api.ParseLogLevel(opts.ConsoleLogLevel)
		if err != nil {
			return fmt.Errorf("upbrr: %w", err)
		}
		opts.ConsoleLogLevel = normalized
	}
	if visited["site-upload"] {
		normalized := strings.ToUpper(strings.TrimSpace(opts.SiteUpload))
		if normalized == "" {
			return errors.New("site-upload requires a tracker")
		}
		opts.SiteUpload = normalized
	}
	if visited["limit-queue"] && opts.LimitQueue < 0 {
		return errors.New("limit-queue must be >= 0")
	}
	if visited["queue"] {
		trimmed := strings.TrimSpace(opts.QueueName)
		if trimmed == "" {
			return errors.New("--queue requires a non-empty queue name")
		}
		opts.QueueName = trimmed
	}
	if opts.ExportConfigPlaintext && !visited["export-config"] {
		return errors.New("--export-config-plaintext requires --export-config")
	}
	if opts.ExportConfigPlaintext && strings.TrimSpace(opts.ExportConfigPath) == "" {
		return errors.New("--export-config must have a non-empty value when --export-config-plaintext is used")
	}
	if _, err := buildTrackerIDOverrides(*opts, visited); err != nil {
		return err
	}
	if _, err := buildExternalIDOverrides(*opts, visited); err != nil {
		return err
	}
	if _, err := buildCLITrackerInput(opts.TrackerInput); err != nil {
		return err
	}
	if err := validateCLITrackLanguageInputs(opts.TrackLanguages); err != nil {
		return err
	}
	if _, err := buildCLIInputCorrectionPatch(*opts, visited, nil); err != nil {
		return err
	}
	if len(opts.TrackerInput) > 0 && hasCLIInputCorrections(visited) {
		return errors.New("tracker-input cannot be combined with release corrections")
	}
	return nil
}

func canonicalChangedFlags(fs *pflag.FlagSet, aliases map[string]string) map[string]bool {
	visited := make(map[string]bool)
	fs.Visit(func(f *pflag.Flag) {
		name := f.Name
		if canonical, ok := aliases[name]; ok {
			name = canonical
		}
		visited[name] = true
	})
	return visited
}

func partitionUploadArgs(fs *pflag.FlagSet, args []string) ([]string, []string) {
	flagArgs := make([]string, 0, len(args))
	positionalArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionalArgs = append(positionalArgs, args[i+1:]...)
			break
		}
		name, normalized, ok := normalizeLegacyFlag(arg)
		if !ok {
			positionalArgs = append(positionalArgs, arg)
			continue
		}
		if (name == "help" || name == "h") && !strings.HasPrefix(arg, "---") {
			flagArgs = append(flagArgs, "--help")
			break
		}
		flagDef := fs.Lookup(name)
		if flagDef == nil {
			flagArgs = append(flagArgs, normalized)
			continue
		}

		flagArgs = append(flagArgs, normalized)
		if strings.Contains(arg, "=") || isBoolFlag(flagDef) {
			continue
		}
		if i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return flagArgs, positionalArgs
}

func normalizeNonInterspersedArgs(fs *pflag.FlagSet, args []string) []string {
	normalized := make([]string, 0, len(args))
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			return append(normalized, args...)
		}
		name, normalizedArg, ok := normalizeLegacyFlag(arg)
		if !ok {
			return append(normalized, args...)
		}
		args = args[1:]
		if (name == "help" || name == "h") && !strings.HasPrefix(arg, "---") {
			return append(normalized, "--help")
		}
		normalized = append(normalized, normalizedArg)
		flagDef := fs.Lookup(name)
		if flagDef == nil || strings.Contains(arg, "=") || isBoolFlag(flagDef) {
			continue
		}
		if len(args) > 0 {
			normalized = append(normalized, args[0])
			args = args[1:]
		}
	}
	return normalized
}

func normalizeLegacyFlag(arg string) (string, string, bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" {
		return "", arg, false
	}
	dashes := 0
	for dashes < len(arg) && arg[dashes] == '-' {
		dashes++
	}
	trimmed := arg[dashes:]
	if trimmed == "" {
		return "", arg, false
	}
	name := trimmed
	if before, _, ok := strings.Cut(trimmed, "="); ok {
		name = before
	}
	if name == "" {
		return "", arg, false
	}
	if dashes == 1 && !isLongOnlyCLIFlag(name) {
		return name, "--" + trimmed, true
	}
	return name, arg, true
}

func isLongOnlyCLIFlag(name string) bool {
	return name == "console-log-level"
}

func cliFlagAliases() map[string]string {
	return map[string]string{
		"tk":                   "trackers",
		"lq":                   "limit-queue",
		"sc":                   "site-check",
		"su":                   "site-upload",
		"rtk":                  "trackers-remove",
		"dtmp":                 "delete-tmp",
		"cll":                  "console-log-level",
		"s":                    "screens",
		"ns":                   "no-seed",
		"sat":                  "skip_auto_torrent",
		"kf":                   "keep-folder",
		"c":                    "category",
		"t":                    "type",
		"res":                  "resolution",
		"g":                    "tag",
		"serv":                 "service",
		"dist":                 "distributor",
		"ol":                   "original-language",
		"repack":               "edition",
		"manual-episode-title": "episode-title",
		"met":                  "episode-title",
		"net":                  "no-episode-title",
		"ndist":                "no-distributor",
		"ne":                   "no-edition",
		"year":                 "manual-year",
		"reg":                  "region",
		"df":                   "descfile",
		"pb":                   "desclink",
		"qbt":                  "qbit-tag",
		"qbc":                  "qbit-cat",
		"frc":                  "force-recheck",
		"ih":                   "imghost",
		"siu":                  "skip-imagehost-upload",
		"mf":                   "manual_frames",
		"comps":                "comparison",
		"comps_index":          "comparison_index",
		"th":                   "infohash",
		"torrenthash":          "infohash",
		"mps":                  "max-piece-size",
		"nh":                   "nohash",
		"rh":                   "rehash",
		"ua":                   "unattended",
		"uac":                  "unattended_confirm",
		"sdc":                  "skip-dupe-check",
		"sda":                  "skip-dupe-asking",
		"ddc":                  "double-dupe-check",
		"mc":                   "commentary",
		"pr":                   "personalrelease",
		"st":                   "stream",
		"a":                    "anon",
		"dr":                   "draft",
		"mq":                   "modq",
		"ch":                   "channel",
		"hc":                   "hardcoded-subs",
	}
}

func bindServeFlags(fs *pflag.FlagSet, opts *serveOptions) {
	fs.IntVar(&opts.LiveTestMaxImages, "live-test-max-images", 0, "Maximum journaled image uploads in this live-test run (zero keeps captures local)")
	fs.BoolVar(&opts.LiveTest, "live-test", false, "Use an isolated live-test profile with tracker and client writes disabled")
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to config file")
	fs.StringVar(&opts.Addr, "addr", "", "Web UI listen address (host:port)")
	fs.StringVar(&opts.Host, "host", "", "Web UI host to bind")
	fs.Var(&decimalPortValue{target: &opts.Port}, "port", "Web UI port to bind")
	fs.StringVar(&opts.BaseURL, "base-url", "", "External Web UI base URL or path, for example https://example.test/upbrr/ or /upbrr/")
	fs.BoolVar(&opts.PersistListen, "persist-listen", false, "Persist Web UI listen host and port to web-config.json")
	fs.BoolVar(&opts.PersistWebConfig, "persist-web-config", false, "Persist supplied Web UI serve settings to web-config.json")
	fs.BoolVar(&opts.DevNoAuth, "dev-no-auth", false, "Development only: serve web UI without web authentication on loopback hosts")
}

// parseServeOptions parses serve-only flags and returns the set of flags the
// caller supplied so config defaults are not overwritten by zero values.
func parseServeOptions(args []string) (serveOptions, map[string]bool, error) {
	var opts serveOptions
	fs := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.SetInterspersed(false)
	bindServeFlags(fs, &opts)
	normalized := normalizeNonInterspersedArgs(fs, args)

	if err := fs.Parse(normalized); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return serveOptions{}, nil, fmt.Errorf("parse serve options: %w", err)
		}
		return serveOptions{}, nil, fmt.Errorf("parse serve options: %w", translatePFlagError(err))
	}

	visited := make(map[string]bool)
	fs.Visit(func(f *pflag.Flag) {
		visited[f.Name] = true
	})

	return opts, visited, nil
}

func formatFlagUsage(fs *pflag.FlagSet, usage string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Usage: %s\n", usage)
	if fs.Name() == "upbrr" {
		fmt.Fprint(&builder, "\nCommands:\n")
		fmt.Fprint(&builder, "  serve [options]\n")
		fmt.Fprint(&builder, "      Start the embedded web UI server\n")
		fmt.Fprint(&builder, "      Options: --addr, --host, --port, --base-url, --persist-web-config, --dev-no-auth\n")
		fmt.Fprint(&builder, "  api-token <list|revoke> [options]\n")
		fmt.Fprint(&builder, "      List and revoke persistent API bearer tokens\n")
		fmt.Fprint(&builder, "  auth <password|browse-roots> [options]\n")
		fmt.Fprint(&builder, "      Manage the WebUI password and browse policy locally\n")
		fmt.Fprint(&builder, "  live-test <init|cleanup> [options]\n")
		fmt.Fprint(&builder, "      Create an isolated profile or clean up its owned hosted images\n")
	}
	fmt.Fprint(&builder, "\nOptions:\n")
	sections := cliHelpSections(fs.Name())
	aliasesByCanonical := cliAliasesByCanonical()
	seen := make(map[string]bool)
	for _, section := range sections {
		wroteHeader := false
		for _, name := range section.names {
			f := fs.Lookup(name)
			if f == nil || seen[name] {
				continue
			}
			if !wroteHeader {
				fmt.Fprintf(&builder, "\n%s:\n", section.title)
				wroteHeader = true
			}
			formatHelpFlag(&builder, f, aliasesByCanonical[name])
			seen[name] = true
			for _, alias := range aliasesByCanonical[name] {
				seen[alias] = true
			}
		}
	}

	var remaining []*pflag.Flag
	fs.VisitAll(func(f *pflag.Flag) {
		if !seen[f.Name] && f.Name != "help" && !f.Hidden {
			remaining = append(remaining, f)
		}
	})
	if len(remaining) > 0 {
		sort.Slice(remaining, func(i, j int) bool {
			return remaining[i].Name < remaining[j].Name
		})
		fmt.Fprintln(&builder, "\nOther:")
		for _, f := range remaining {
			formatHelpFlag(&builder, f, nil)
		}
	}
	return builder.String()
}

type helpSection struct {
	title string
	names []string
}

func cliHelpSections(name string) []helpSection {
	if name == "serve" {
		return []helpSection{
			{title: "Config", names: []string{"config"}},
			{title: "Server", names: []string{"addr", "host", "port", "base-url", "persist-listen", "persist-web-config"}},
			{title: "Development", names: []string{"dev-no-auth", "live-test", "live-test-max-images"}},
		}
	}
	return []helpSection{
		{title: "Config", names: []string{"config", "export-config", "export-config-plaintext", "import-config", "create-auth"}},
		{title: "Application", names: []string{"version", "cleanup"}},
		{title: "Execution", names: []string{
			"queue",
			"limit-queue",
			"site-check",
			"site-upload",
			"debug",
			"live-test",
			"live-test-max-images",
			"log-level",
			"console-log-level",
			"upload-only",
			"input-only",
			"delete-tmp",
			"unattended",
			"unattended_confirm",
		}},
		{title: "Tracker Selection", names: []string{"trackers", "trackers-remove"}},
		{title: "Tracker IDs", names: []string{"ptp", "blu", "aither", "lst", "oe", "hdb", "btn", "bhd", "ulcx"}},
		{title: "Release Overrides", names: []string{
			"category", "type", "source", "resolution", "tag", "service", "distributor", "original-language",
			"edition", "season", "episode", "episode-title", "manual-year", "daily", "region", "use-season-episode", "no-season", "no-year",
			"no-aka", "no-tag", "no-episode-title", "no-distributor", "no-edition", "no-dub", "no-dual", "dual-audio",
			"title", "alternate-title", "original-title", "genres", "audio-languages", "subtitle-languages", "hardcoded-subs", "hardcoded-subtitle-languages",
		}},
		{title: "Input Corrections", names: []string{"source-lookup", "track-languages", "reset-input", "confirm-input", "tracker-input"}},
		{title: "Metadata IDs", names: []string{"tmdb", "imdb", "mal", "tvdb", "tvmaze"}},
		{title: "Tracker Overrides", names: []string{
			"skip-dupe-check", "skip-dupe-asking", "double-dupe-check", "foreign", "opera", "asian", "disctype",
			"commentary", "personalrelease", "stream", "webdv", "not-anime", "anime", "anon", "draft", "modq", "channel",
		}},
		{title: "Screenshots and Images", names: []string{
			"screens", "manual_frames", "comparison", "comparison_index", "menu-images", "get-dvd-menus", "imghost", "skip-imagehost-upload",
			"descfile", "desclink",
		}},
		{title: "Client and Torrent", names: []string{
			"client", "qbit-tag", "qbit-cat", "force-recheck", "no-seed", "skip_auto_torrent", "keep-folder", "onlyID", "infohash",
			"max-piece-size", "nohash", "rehash",
		}},
	}
}

func cliAliasesByCanonical() map[string][]string {
	result := make(map[string][]string)
	for alias, canonical := range cliFlagAliases() {
		result[canonical] = append(result[canonical], alias)
	}
	for canonical := range result {
		sort.Strings(result[canonical])
	}
	return result
}

func formatHelpFlag(builder *strings.Builder, f *pflag.Flag, aliases []string) {
	valueName, usage := pflag.UnquoteUsage(f)
	if _, ok := f.Value.(*decimalPortValue); ok {
		valueName = "int"
	}
	names := make([]string, 0, 2+len(aliases))
	if !isLongOnlyCLIFlag(f.Name) {
		names = append(names, "-"+f.Name)
	}
	names = append(names, "--"+f.Name)
	for _, alias := range aliases {
		names = append(names, "-"+alias)
	}
	suffix := ""
	if !isBoolFlag(f) && valueName != "" {
		suffix = " " + valueName
	}
	fmt.Fprintf(builder, "  %s%s\n", strings.Join(names, ", "), suffix)
	fmt.Fprintf(builder, "      %s\n", usage)
}

// decimalPortValue parses --port from raw flag text so leading-zero values use
// decimal syntax instead of the integer-literal rules used by flag.IntVar.
type decimalPortValue struct {
	target *int
}

func (v *decimalPortValue) Set(value string) error {
	port, err := parseServePortValue(value)
	if err != nil {
		return err
	}
	*v.target = port
	return nil
}

func (v *decimalPortValue) String() string {
	if v == nil || v.target == nil {
		return "0"
	}
	return strconv.Itoa(*v.target)
}

func (v *decimalPortValue) Type() string {
	return "int"
}

func isBoolFlag(f *pflag.Flag) bool {
	return f != nil && f.NoOptDefVal != ""
}

func (o cliOptions) interactionMode() api.InteractionMode {
	if o.UnattendedConfirm {
		return api.InteractionModeUnattendedConfirm
	}
	if o.Unattended {
		return api.InteractionModeUnattended
	}
	return api.InteractionModeInteractive
}

func buildCLIRequest(opts cliOptions, visited map[string]bool, paths []string, screens int) (api.Request, error) {
	runLogLevel := ""
	if visited["log-level"] {
		normalized, err := api.ParseLogLevel(opts.LogLevel)
		if err != nil {
			return api.Request{}, fmt.Errorf("upbrr: %w", err)
		}
		runLogLevel = normalized
	}

	sourcePath := ""
	if len(paths) == 1 {
		sourcePath = paths[0]
	}
	req := api.Request{
		SourcePath:      sourcePath,
		SourceLookupURL: strings.TrimSpace(opts.SourceLookup),
		Execution: api.ExecutionOptions{
			QueueName:         strings.TrimSpace(opts.QueueName),
			QueueLimit:        opts.LimitQueue,
			SiteCheck:         opts.SiteCheck,
			SiteUploadTracker: strings.ToUpper(strings.TrimSpace(opts.SiteUpload)),
		},
		Trackers:       splitCSV(opts.Trackers),
		TrackersRemove: splitCSV(opts.TrackersRemove),
		Options: api.UploadOptions{
			RunLogLevel:     runLogLevel,
			Screens:         max(screens, 0),
			NoSeed:          opts.NoSeed,
			SkipAutoTorrent: opts.SkipAutoTorrent,
			KeepFolder:      opts.KeepFolder,
			OnlyID:          opts.OnlyID,
			CaptureDVDMenus: opts.GetDVDMenus,
			InteractionMode: opts.interactionMode(),
		},
		ReleaseNameOverrides: buildReleaseNameOverrides(visited, releaseOverrideInput{
			Category:         opts.Category,
			Type:             opts.Type,
			Source:           opts.Source,
			Resolution:       opts.Resolution,
			Tag:              opts.Tag,
			Service:          opts.Service,
			Edition:          opts.Edition,
			Season:           opts.Season,
			Episode:          opts.Episode,
			EpisodeTitle:     opts.EpisodeTitle,
			ManualYear:       opts.ManualYear,
			ManualDate:       opts.ManualDate,
			UseSeasonEpisode: opts.UseSeasonEpisode,
			NoSeason:         opts.NoSeason,
			NoYear:           opts.NoYear,
			NoAKA:            opts.NoAKA,
			NoTag:            opts.NoTag,
			NoEpisodeTitle:   opts.NoEpisodeTitle,
			NoDistributor:    opts.NoDistributor,
			NoEdition:        opts.NoEdition,
			NoDub:            opts.NoDub,
			NoDual:           opts.NoDual,
			DualAudio:        opts.DualAudio,
			Region:           opts.Region,
		}),
		SkipDupeCheck:               opts.SkipDupeCheck,
		SkipDupeAsActual:            opts.SkipDupeAsActual,
		DoubleDupeCheck:             opts.DoubleDupeCheck,
		DescriptionOverrideURL:      strings.TrimSpace(opts.DescriptionLink),
		MetadataOverrides:           buildMetadataOverrides(opts, visited),
		TrackerQuestionnaireAnswers: mustBuildCLITrackerInput(opts.TrackerInput),
		TrackerConfigOverrides:      buildTrackerConfigOverrides(opts, visited),
		TrackerSiteOverrides:        buildTrackerSiteOverrides(opts, visited),
		ClientOverrides:             buildClientOverrides(opts, visited),
		ImageHostOverrides:          buildImageHostOverrides(opts, visited),
		ScreenshotOverrides:         buildScreenshotOverrides(opts, visited),
		TorrentOverrides:            buildTorrentOverrides(opts, visited),
		ConfirmBDMVRescan:           opts.ConfirmBDMVRescan,
	}
	if req.Execution.SiteUploadTracker != "" {
		req.Trackers = []string{req.Execution.SiteUploadTracker}
	}

	if visited["descfile"] {
		descriptionRaw, err := os.ReadFile(strings.TrimSpace(opts.DescriptionFile))
		if err != nil {
			return api.Request{}, fmt.Errorf("read description file: %w", err)
		}
		req.DescriptionOverrideRaw = string(descriptionRaw)
	}

	ids, err := buildExternalIDOverrides(opts, visited)
	if err != nil {
		return api.Request{}, err
	}
	req.ExternalIDOverrides = ids
	trackerIDs, err := buildTrackerIDOverrides(opts, visited)
	if err != nil {
		return api.Request{}, err
	}
	req.TrackerIDOverrides = trackerIDs
	if visited["tmdb"] {
		_, category, err := parseTMDBID(opts.TMDB)
		if err != nil {
			return api.Request{}, err
		}
		if category != "" {
			req.ReleaseNameOverrides.Category = stringPtr(category)
		}
	}
	return req, nil
}

func buildMetadataOverrides(opts cliOptions, visited map[string]bool) api.MetadataOverrides {
	overrides := api.MetadataOverrides{}
	if visited["distributor"] {
		overrides.Distributor = stringPtr(opts.Distributor)
	}
	if visited["original-language"] {
		overrides.OriginalLanguage = stringPtr(opts.OriginalLanguage)
	}
	if visited["commentary"] {
		overrides.Commentary = boolPtr(opts.Commentary)
	}
	if visited["personalrelease"] {
		overrides.PersonalRelease = boolPtr(opts.PersonalRelease)
	}
	if visited["stream"] {
		overrides.StreamOptimized = boolPtr(opts.StreamOptimized)
	}
	if visited["webdv"] {
		overrides.WebDV = boolPtr(opts.WebDV)
	}
	if visited["not-anime"] {
		overrides.Anime = boolPtr(false)
	}
	if visited["anime"] {
		overrides.Anime = boolPtr(opts.Anime)
	}
	if visited["title"] {
		overrides.Title = stringPtr(opts.Title)
	}
	if visited["alternate-title"] {
		overrides.AlternateTitle = stringPtr(opts.AlternateTitle)
	}
	if visited["original-title"] {
		overrides.OriginalTitle = stringPtr(opts.OriginalTitle)
	}
	if visited["genres"] {
		values := splitCSV(opts.Genres)
		overrides.Genres = &values
	}
	if visited["audio-languages"] {
		values := languageutil.NormalizeLanguageList([]string{opts.AudioLanguages})
		overrides.AudioLanguages = &values
	}
	if visited["subtitle-languages"] {
		values := languageutil.NormalizeLanguageList([]string{opts.SubtitleLanguages})
		overrides.SubtitleLanguages = &values
	}
	if visited["hardcoded-subs"] {
		overrides.HardcodedSubs = boolPtr(opts.HardcodedSubs)
	}
	if visited["hardcoded-subtitle-languages"] {
		values := languageutil.NormalizeLanguageList([]string{opts.HardcodedSubtitleLanguages})
		overrides.HardcodedSubtitleLanguages = &values
	}
	return overrides
}

func hasCLIInputCorrections(visited map[string]bool) bool {
	for name, changed := range visited {
		if changed && isCLIInputCorrectionFlag(name) {
			return true
		}
	}
	return false
}

func isCLIInputCorrectionFlag(name string) bool {
	return slices.Contains([]string{
		"tmdb", "imdb", "tvdb", "tvmaze", "mal", "category", "type", "source", "resolution", "tag", "service", "edition",
		"season", "episode", "episode-title", "manual-year", "daily", "use-season-episode", "no-season", "no-year", "no-aka", "no-tag",
		"no-episode-title", "no-distributor", "no-edition", "no-dub", "no-dual", "dual-audio", "region", "distributor",
		"original-language", "commentary", "personalrelease", "stream", "webdv", "not-anime", "anime", "title", "alternate-title",
		"original-title", "genres", "audio-languages", "subtitle-languages", "hardcoded-subs", "hardcoded-subtitle-languages",
		"track-languages", "reset-input", "confirm-input",
	}, name)
}

func validateCLIInputFlagOccurrences(fs *pflag.FlagSet, flagArgs []string) error {
	aliases := cliFlagAliases()
	seen := make(map[string]bool)
	for i := 0; i < len(flagArgs); i++ {
		name, _, _ := normalizeLegacyFlag(flagArgs[i])
		flag := fs.Lookup(name)
		if flag == nil {
			continue
		}
		if canonical, exists := aliases[name]; exists {
			name = canonical
		}
		if isCLIInputCorrectionFlag(name) && flag.Value.Type() != "stringArray" {
			if seen[name] {
				return fmt.Errorf("duplicate input correction flag --%s", name)
			}
			seen[name] = true
		}
		if !strings.Contains(flagArgs[i], "=") && !isBoolFlag(flag) {
			i++
		}
	}
	return nil
}

func mergeCLIInputEditArgs(currentArgs, editArgs []string) ([]string, error) {
	_, changed, _, err := parseCLIOptions(editArgs)
	if err != nil {
		return nil, err
	}
	var opts cliOptions
	fs := pflag.NewFlagSet("upbrr", pflag.ContinueOnError)
	bindUploadFlags(fs, &opts)
	flags, paths := partitionUploadArgs(fs, currentArgs)
	aliases := cliFlagAliases()
	merged := make([]string, 0, len(currentArgs)+len(editArgs)+1)
	for i := 0; i < len(flags); i++ {
		start := i
		name, _, _ := normalizeLegacyFlag(flags[i])
		flag := fs.Lookup(name)
		if flag != nil && !strings.Contains(flags[i], "=") && !isBoolFlag(flag) {
			i++
		}
		if canonical, exists := aliases[name]; exists {
			name = canonical
		}
		if !changed[name] {
			merged = append(merged, flags[start:i+1]...)
		}
	}
	merged = append(merged, editArgs...)
	merged = append(merged, "--")
	return append(merged, paths...), nil
}

func mustBuildCLITrackerInput(values []string) map[string]map[string]string {
	answers, err := buildCLITrackerInput(values)
	if err != nil {
		return nil
	}
	for _, fields := range answers {
		for field, value := range fields {
			if value == "auto" {
				delete(fields, field)
			}
		}
	}
	return answers
}

func buildCLITrackerInput(values []string) (map[string]map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	answers := make(map[string]map[string]string)
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		trackerAndField, value, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("invalid tracker-input %q", raw)
		}
		tracker, field, ok := strings.Cut(strings.TrimSpace(trackerAndField), ":")
		tracker = strings.ToUpper(strings.TrimSpace(tracker))
		field = strings.TrimSpace(field)
		if !ok || tracker == "" || field == "" {
			return nil, fmt.Errorf("invalid tracker-input %q", raw)
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "yes" && value != "no" && value != "auto" {
			return nil, fmt.Errorf("invalid tracker-input value %q", raw)
		}
		key := tracker + "\x00" + field
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("duplicate tracker-input target %s:%s", tracker, field)
		}
		seen[key] = struct{}{}
		if answers[tracker] == nil {
			answers[tracker] = make(map[string]string)
		}
		answers[tracker][field] = value
	}
	return answers, nil
}

func buildCLIInputCorrectionPatch(opts cliOptions, visited map[string]bool, tracks []api.MediaTrackFacts) (*api.ReleaseCorrectionPatch, error) {
	patch := api.ReleaseCorrectionPatch{}
	seen := make(map[string]struct{})
	for _, raw := range opts.ResetInput {
		ref, err := parseCLIInputFieldRef(raw)
		if err != nil {
			return nil, err
		}
		key := string(ref.Field) + "\x00" + ref.TrackID
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("duplicate input correction target %s", raw)
		}
		seen[key] = struct{}{}
		patch.ResetFields = append(patch.ResetFields, ref)
	}
	for _, raw := range opts.ConfirmInput {
		ref, err := parseCLIInputFieldRef(raw)
		if err != nil {
			return nil, err
		}
		key := string(ref.Field) + "\x00" + ref.TrackID
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("duplicate input correction target %s", raw)
		}
		seen[key] = struct{}{}
		patch.ConfirmFields = append(patch.ConfirmFields, ref)
	}
	if len(opts.TrackLanguages) > 0 && tracks != nil {
		corrections, err := buildCLITrackLanguageCorrections(opts.TrackLanguages, tracks)
		if err != nil {
			return nil, err
		}
		patch.Values.Metadata.TrackLanguages = corrections
		for _, correction := range corrections {
			key := string(api.CorrectionFieldMetadataTrackLanguages) + "\x00" + correction.TrackID
			if _, duplicate := seen[key]; duplicate {
				return nil, fmt.Errorf("duplicate input correction target %s:%s", api.CorrectionFieldMetadataTrackLanguages, correction.TrackID)
			}
			seen[key] = struct{}{}
		}
	}
	if len(patch.ResetFields) == 0 && len(patch.ConfirmFields) == 0 && len(patch.Values.Metadata.TrackLanguages) == 0 {
		return nil, nil
	}
	if len(patch.ConfirmFields) > 0 {
		patch.ExpectedRevision = new(uint64)
	}
	validation := patch
	values, err := buildCLIInputCorrectionValues(opts, visited)
	if err != nil {
		return nil, err
	}
	validation.Values = values
	if err := validation.Validate(); err != nil {
		return nil, fmt.Errorf("invalid input correction: %w", err)
	}
	return &patch, nil
}

func buildCLIInputCorrectionValues(opts cliOptions, visited map[string]bool) (api.ReleaseCorrectionValues, error) {
	identity, err := buildExternalIDOverrides(opts, visited)
	if err != nil {
		return api.ReleaseCorrectionValues{}, err
	}
	releaseName := buildReleaseNameOverrides(visited, releaseOverrideInput{
		Category:         opts.Category,
		Type:             opts.Type,
		Source:           opts.Source,
		Resolution:       opts.Resolution,
		Tag:              opts.Tag,
		Service:          opts.Service,
		Edition:          opts.Edition,
		Season:           opts.Season,
		Episode:          opts.Episode,
		EpisodeTitle:     opts.EpisodeTitle,
		ManualYear:       opts.ManualYear,
		ManualDate:       opts.ManualDate,
		UseSeasonEpisode: opts.UseSeasonEpisode,
		NoSeason:         opts.NoSeason,
		NoYear:           opts.NoYear,
		NoAKA:            opts.NoAKA,
		NoTag:            opts.NoTag,
		NoEpisodeTitle:   opts.NoEpisodeTitle,
		NoDistributor:    opts.NoDistributor,
		NoEdition:        opts.NoEdition,
		NoDub:            opts.NoDub,
		NoDual:           opts.NoDual,
		DualAudio:        opts.DualAudio,
		Region:           opts.Region,
	})
	if visited["tmdb"] {
		_, category, err := parseTMDBID(opts.TMDB)
		if err != nil {
			return api.ReleaseCorrectionValues{}, err
		}
		if category != "" {
			releaseName.Category = stringPtr(category)
		}
	}
	return api.ReleaseCorrectionValues{
		Identity:    identity,
		ReleaseName: releaseName,
		Metadata:    buildMetadataOverrides(opts, visited),
	}, nil
}

func parseCLIInputFieldRef(raw string) (api.CorrectionFieldRef, error) {
	field, trackID, hasTrack := strings.Cut(strings.TrimSpace(raw), ":")
	field = strings.TrimSpace(field)
	trackID = strings.TrimSpace(trackID)
	if field == "" || (hasTrack && trackID == "") {
		return api.CorrectionFieldRef{}, fmt.Errorf("invalid input correction target %q", raw)
	}
	return api.CorrectionFieldRef{Field: api.CorrectionField(field), TrackID: trackID}, nil
}

func validateCLITrackLanguageInputs(values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		trackID, _, ok := strings.Cut(raw, "=")
		trackID = strings.TrimSpace(trackID)
		if !ok || trackID == "" {
			return fmt.Errorf("invalid track-languages %q", raw)
		}
		if _, duplicate := seen[trackID]; duplicate {
			return fmt.Errorf("duplicate track-languages target %q", trackID)
		}
		seen[trackID] = struct{}{}
	}
	return nil
}

func buildCLITrackLanguageCorrections(values []string, inputTracks []api.MediaTrackFacts) ([]api.TrackLanguageCorrection, error) {
	tracks := make(map[string]api.MediaTrackFacts, len(inputTracks))
	for _, track := range inputTracks {
		tracks[track.ID] = track
	}
	corrections := make([]api.TrackLanguageCorrection, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		trackID, languages, ok := strings.Cut(raw, "=")
		trackID = strings.TrimSpace(trackID)
		if !ok || trackID == "" {
			return nil, fmt.Errorf("invalid track-languages %q", raw)
		}
		if _, duplicate := seen[trackID]; duplicate {
			return nil, fmt.Errorf("duplicate track-languages target %q", trackID)
		}
		seen[trackID] = struct{}{}
		track, exists := tracks[trackID]
		if !exists || strings.TrimSpace(track.ManifestFingerprint) == "" {
			return nil, fmt.Errorf("track-languages requires a prepared manifest track %q; run --input-only first", trackID)
		}
		corrections = append(corrections, api.TrackLanguageCorrection{
			TrackID:             trackID,
			Languages:           languageutil.NormalizeLanguageList([]string{languages}),
			ManifestFingerprint: track.ManifestFingerprint,
		})
	}
	return corrections, nil
}

func buildTrackerConfigOverrides(opts cliOptions, visited map[string]bool) api.TrackerConfigOverrides {
	overrides := api.TrackerConfigOverrides{}
	if visited["anon"] {
		overrides.Anon = boolPtr(opts.Anon)
	}
	if visited["draft"] {
		overrides.Draft = boolPtr(opts.Draft)
	}
	if visited["modq"] {
		overrides.ModQ = boolPtr(opts.ModQ)
	}
	if visited["channel"] {
		overrides.Channel = stringPtr(opts.Channel)
	}
	return overrides
}

func buildClientOverrides(opts cliOptions, visited map[string]bool) api.ClientOverrides {
	overrides := api.ClientOverrides{}
	if visited["client"] {
		overrides.Client = stringPtr(opts.Client)
	}
	if visited["qbit-tag"] {
		overrides.QbitTag = stringPtr(opts.QbitTag)
	}
	if visited["qbit-cat"] {
		overrides.QbitCategory = stringPtr(opts.QbitCategory)
	}
	if visited["force-recheck"] {
		overrides.ForceRecheck = boolPtr(opts.ForceRecheck)
	}
	return overrides
}

func buildTrackerSiteOverrides(opts cliOptions, visited map[string]bool) api.TrackerSiteOverrides {
	overrides := api.TrackerSiteOverrides{}
	if visited["foreign"] {
		overrides.TIK.Foreign = boolPtr(opts.Foreign)
	}
	if visited["opera"] {
		overrides.TIK.Opera = boolPtr(opts.Opera)
	}
	if visited["asian"] {
		overrides.TIK.Asian = boolPtr(opts.Asian)
	}
	if visited["disctype"] {
		overrides.TIK.DiscType = stringPtr(opts.DiscType)
	}
	return overrides
}

func buildImageHostOverrides(opts cliOptions, visited map[string]bool) api.ImageHostOverrides {
	overrides := api.ImageHostOverrides{}
	if visited["imghost"] {
		overrides.PreferredHost = stringPtr(opts.ImageHost)
	}
	if visited["skip-imagehost-upload"] {
		overrides.SkipUpload = boolPtr(opts.SkipImageUpload)
	}
	return overrides
}

func buildScreenshotOverrides(opts cliOptions, visited map[string]bool) api.ScreenshotOverrides {
	overrides := api.ScreenshotOverrides{}
	if visited["manual_frames"] {
		frames, err := parseManualFrames(opts.ManualFrames)
		if err == nil {
			overrides.ManualFrames = frames
		}
	}
	if visited["comparison"] {
		paths, err := parseComparisonPaths(opts.Comparison)
		if err == nil {
			overrides.ComparisonPaths = paths
		}
	}
	if visited["menu-images"] {
		paths, err := parseComparisonPaths(opts.MenuImages)
		if err == nil {
			overrides.MenuPaths = paths
		}
	}
	if visited["comparison_index"] {
		value := opts.ComparisonIndex
		overrides.ComparisonPrimaryIndex = &value
	}
	return overrides
}

func buildTorrentOverrides(opts cliOptions, visited map[string]bool) api.TorrentOverrides {
	overrides := api.TorrentOverrides{}
	if visited["infohash"] {
		normalized, err := parseInfoHash(opts.InfoHash)
		if err == nil {
			overrides.InfoHash = stringPtr(normalized)
		}
	}
	if visited["max-piece-size"] {
		value := opts.MaxPieceSize
		overrides.MaxPieceSizeMiB = &value
	}
	if visited["nohash"] {
		overrides.NoHash = boolPtr(opts.NoHash)
	}
	if visited["rehash"] {
		overrides.Rehash = boolPtr(opts.Rehash)
	}
	return overrides
}

func buildTrackerIDOverrides(opts cliOptions, visited map[string]bool) (map[string]string, error) {
	inputs := []struct {
		visitedName string
		tracker     string
		value       string
	}{
		{
			visitedName: "ptp",
			tracker:     "ptp",
			value:       opts.PTP,
		},
		{
			visitedName: "blu",
			tracker:     "blu",
			value:       opts.BLU,
		},
		{
			visitedName: "aither",
			tracker:     "aither",
			value:       opts.Aither,
		},
		{
			visitedName: "lst",
			tracker:     "lst",
			value:       opts.LST,
		},
		{
			visitedName: "oe",
			tracker:     "oe",
			value:       opts.OE,
		},
		{
			visitedName: "hdb",
			tracker:     "hdb",
			value:       opts.HDB,
		},
		{
			visitedName: "btn",
			tracker:     "btn",
			value:       opts.BTN,
		},
		{
			visitedName: "bhd",
			tracker:     "bhd",
			value:       opts.BHD,
		},
		{
			visitedName: "ulcx",
			tracker:     "ulcx",
			value:       opts.ULCX,
		},
	}

	overrides := make(map[string]string)
	for _, input := range inputs {
		if !visited[input.visitedName] {
			continue
		}
		id, err := parseTrackerIDOverride(input.tracker, input.value)
		if err != nil {
			return nil, err
		}
		overrides[input.tracker] = id
	}
	if len(overrides) == 0 {
		return nil, nil
	}
	return overrides, nil
}

// buildExternalIDOverrides parses only explicitly supplied provider flags.
// Omitted flags leave nil fields; blank or zero values produce explicit provider clears.
// Invalid input returns an empty override set and an error.
func buildExternalIDOverrides(opts cliOptions, visited map[string]bool) (api.ExternalIDOverrides, error) {
	overrides := api.ExternalIDOverrides{}
	if visited["tmdb"] {
		id, _, err := parseTMDBID(opts.TMDB)
		if err != nil {
			return api.ExternalIDOverrides{}, err
		}
		overrides.TMDBID = intPtr(id)
	}
	for _, input := range []struct {
		name   string
		value  string
		target **int
	}{
		{
			name:   "tvdb",
			value:  opts.TVDB,
			target: &overrides.TVDBID,
		},
		{
			name:   "tvmaze",
			value:  opts.TVmaze,
			target: &overrides.TVmazeID,
		},
		{
			name:   "mal",
			value:  opts.MAL,
			target: &overrides.MALID,
		},
	} {
		if !visited[input.name] {
			continue
		}
		var id int64
		if value := strings.TrimSpace(input.value); value != "" {
			var err error
			id, err = strconv.ParseInt(value, 0, strconv.IntSize)
			if err != nil || id < 0 {
				return api.ExternalIDOverrides{}, fmt.Errorf("invalid %s id %q", input.name, input.value)
			}
		}
		*input.target = intPtr(int(id))
	}
	if visited["imdb"] {
		if strings.TrimSpace(opts.IMDb) == "" {
			overrides.IMDBID = intPtr(0)
			return overrides, nil
		}
		id, err := parseIMDbID(opts.IMDb)
		if err != nil {
			return api.ExternalIDOverrides{}, err
		}
		overrides.IMDBID = intPtr(id)
	}
	return overrides, nil
}

// parseTMDBID accepts a positive ID, a movie/ or tv/ prefix, or a URL ending in an ID.
// It returns a category hint when present. Blank or literal zero input clears the ID
// without a category hint; other invalid input returns an error.
func parseTMDBID(raw string) (int, string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" || trimmed == "0" {
		return 0, "", nil
	}

	category := ""
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return 0, "", fmt.Errorf("invalid tmdb id %q", raw)
		}
		path := strings.Trim(parsed.Path, "/")
		parts := strings.Split(path, "/")
		if len(parts) >= 2 {
			typePart := parts[len(parts)-2]
			switch typePart {
			case "tv":
				category = "TV"
			case "movie":
				category = "MOVIE"
			}
			trimmed = parts[len(parts)-1]
		}
	}

	switch {
	case strings.HasPrefix(trimmed, "tv/"):
		category = "TV"
		trimmed = strings.TrimPrefix(trimmed, "tv/")
	case strings.HasPrefix(trimmed, "movie/"):
		category = "MOVIE"
		trimmed = strings.TrimPrefix(trimmed, "movie/")
	}

	id, err := strconv.Atoi(trimmed)
	if err != nil || id <= 0 {
		return 0, "", fmt.Errorf("invalid tmdb id %q", raw)
	}
	return id, category, nil
}

func parseIMDbID(raw string) (int, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	trimmed = strings.TrimPrefix(trimmed, "tt")
	if trimmed == "" {
		return 0, fmt.Errorf("invalid imdb id %q", raw)
	}
	id, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid imdb id %q", raw)
	}
	return id, nil
}

func parseInfoHash(raw string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if len(trimmed) != 40 {
		return "", fmt.Errorf("invalid infohash %q", raw)
	}
	for _, ch := range trimmed {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return "", fmt.Errorf("invalid infohash %q", raw)
		}
	}
	return trimmed, nil
}

func parseImageHost(raw string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if imagehostpolicy.IsUploadHost(trimmed) {
		registry, err := trackerimpl.NewRegistry()
		if err == nil && registry.OwnerForImageHost(trimmed) == "" {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf("invalid imghost %q", raw)
}

func parseManualFrames(raw string) ([]int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("invalid manual_frames %q", raw)
	}
	parts := strings.Split(trimmed, ",")
	frames := make([]int, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		frame, err := strconv.Atoi(value)
		if err != nil || frame <= 0 {
			return nil, fmt.Errorf("invalid manual_frames %q", raw)
		}
		frames = append(frames, frame)
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("invalid manual_frames %q", raw)
	}
	return frames, nil
}

func parseComparisonPaths(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("invalid comparison %q", raw)
	}
	parts := strings.Split(trimmed, ",")
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		absPath, err := filepath.Abs(value)
		if err != nil {
			return nil, fmt.Errorf("invalid comparison %q", raw)
		}
		paths = append(paths, absPath)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("invalid comparison %q", raw)
	}
	return paths, nil
}

func validateComparisonIndex(value int) error {
	if value <= 0 {
		return fmt.Errorf("invalid comparison_index %d", value)
	}
	return nil
}

func parseTIKDiscType(raw string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	switch trimmed {
	case "BD100", "BD66", "BD50", "BD25", "NTSC DVD9", "NTSC DVD5", "PAL DVD9", "PAL DVD5", "CUSTOM", "3D":
		return trimmed, nil
	default:
		return "", fmt.Errorf("invalid disctype %q", raw)
	}
}

func validateMaxPieceSize(value int) error {
	switch value {
	case 1, 2, 4, 8, 16, 32, 64, 128:
		return nil
	default:
		return fmt.Errorf("invalid max-piece-size %d", value)
	}
}

func parseTrackerIDOverride(tracker string, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("invalid %s tracker id %q", tracker, raw)
	}
	if !strings.HasPrefix(strings.ToLower(trimmed), "http://") && !strings.HasPrefix(strings.ToLower(trimmed), "https://") {
		return trimmed, nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid %s tracker id %q", tracker, raw)
	}
	path := strings.TrimSpace(parsed.Path)
	trimmedPath := strings.TrimRight(path, "/")

	switch strings.ToLower(strings.TrimSpace(tracker)) {
	case "ptp":
		value := strings.TrimSpace(parsed.Query().Get("torrentid"))
		if value == "" {
			return "", fmt.Errorf("invalid %s tracker id %q", tracker, raw)
		}
		return value, nil
	case "hdb", "btn":
		value := strings.TrimSpace(parsed.Query().Get("id"))
		if value == "" {
			return "", fmt.Errorf("invalid %s tracker id %q", tracker, raw)
		}
		return value, nil
	case "bhd":
		lastSegment := pathLastSegment(trimmedPath)
		if lastSegment == "" {
			return "", fmt.Errorf("invalid %s tracker id %q", tracker, raw)
		}
		if strings.Contains(trimmedPath, "/download/") || strings.Contains(trimmedPath, "/torrents/") {
			if idx := strings.LastIndex(lastSegment, "."); idx >= 0 && idx < len(lastSegment)-1 {
				candidate := strings.TrimSpace(lastSegment[idx+1:])
				if candidate != "" {
					return candidate, nil
				}
			}
		}
		return lastSegment, nil
	default:
		lastSegment := pathLastSegment(trimmedPath)
		if lastSegment == "" {
			return "", fmt.Errorf("invalid %s tracker id %q", tracker, raw)
		}
		return lastSegment, nil
	}
}

func pathLastSegment(path string) string {
	trimmed := strings.Trim(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	return strings.TrimSpace(parts[len(parts)-1])
}
