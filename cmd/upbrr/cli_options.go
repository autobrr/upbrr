// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	imagehostpolicy "github.com/autobrr/upbrr/internal/imagehosting/policy"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliOptions struct {
	ConfigPath            string
	ShowVersion           bool
	QueueName             string
	LimitQueue            int
	SiteCheck             bool
	SiteUpload            string
	Trackers              string
	TrackersRemove        string
	Debug                 bool
	LogLevel              string
	Screens               int
	NoSeed                bool
	SkipAutoTorrent       bool
	KeepFolder            bool
	OnlyID                bool
	UploadOnly            bool
	Category              string
	Type                  string
	Source                string
	Resolution            string
	Tag                   string
	Service               string
	Distributor           string
	OriginalLanguage      string
	Edition               string
	Season                string
	Episode               string
	EpisodeTitle          string
	ManualYear            int
	ManualDate            string
	NoSeason              bool
	NoYear                bool
	NoAKA                 bool
	NoTag                 bool
	NoEdition             bool
	NoDub                 bool
	NoDual                bool
	DualAudio             bool
	Region                string
	CreateAuth            bool
	ExportConfigPath      string
	ExportConfigPlaintext bool
	ImportConfigPath      string
	DeleteTmp             bool
	Cleanup               bool
	TMDB                  string
	TVDB                  int
	TVmaze                int
	IMDb                  string
	MAL                   int
	Unattended            bool
	UnattendedConfirm     bool
	SkipDupeCheck         bool
	SkipDupeAsActual      bool
	DoubleDupeCheck       bool
	Commentary            bool
	PersonalRelease       bool
	StreamOptimized       bool
	WebDV                 bool
	ConfirmBDMVRescan     bool
	NotAnime              bool
	Anon                  bool
	Draft                 bool
	ModQ                  bool
	Channel               string
	PTP                   string
	BLU                   string
	Aither                string
	LST                   string
	OE                    string
	HDB                   string
	BTN                   string
	BHD                   string
	ULCX                  string
	DescriptionFile       string
	DescriptionLink       string
	Client                string
	QbitTag               string
	QbitCategory          string
	ForceRecheck          bool
	Foreign               bool
	Opera                 bool
	Asian                 bool
	DiscType              string
	ImageHost             string
	SkipImageUpload       bool
	ManualFrames          string
	Comparison            string
	ComparisonIndex       int
	MenuImages            string
	GetDVDMenus           bool
	InfoHash              string
	MaxPieceSize          int
	NoHash                bool
	Rehash                bool
}

type serveOptions struct {
	ConfigPath       string
	Addr             string
	Host             string
	Port             int
	BaseURL          string
	PersistListen    bool
	PersistWebConfig bool
	DevNoAuth        bool
}

type cliHelpError struct {
	usage string
}

func (e *cliHelpError) Error() string {
	return "help requested"
}

func (e *cliHelpError) Usage() string {
	if e == nil {
		return ""
	}
	return e.usage
}

func parseCLIOptions(args []string) (cliOptions, map[string]bool, []string, error) {
	var opts cliOptions
	fs := flag.NewFlagSet("upbrr", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

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
	fs.StringVar(&opts.LogLevel, "log-level", "", "Set run log level (error, warn, info, debug, trace)")
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
	fs.StringVar(&opts.Season, "season", "", "Override season value")
	fs.StringVar(&opts.Episode, "episode", "", "Override episode value")
	fs.StringVar(&opts.EpisodeTitle, "episode-title", "", "Override episode title")
	fs.StringVar(&opts.EpisodeTitle, "manual-episode-title", "", "Override episode title")
	fs.StringVar(&opts.EpisodeTitle, "met", "", "Override episode title")
	fs.IntVar(&opts.ManualYear, "manual-year", 0, "Override release year")
	fs.IntVar(&opts.ManualYear, "year", 0, "Override release year")
	fs.StringVar(&opts.ManualDate, "daily", "", "Set daily episode air date")
	fs.BoolVar(&opts.NoSeason, "no-season", false, "Remove season and episode from name")
	fs.BoolVar(&opts.NoYear, "no-year", false, "Remove year from name")
	fs.BoolVar(&opts.NoAKA, "no-aka", false, "Remove AKA from name")
	fs.BoolVar(&opts.NoTag, "no-tag", false, "Remove group tag from name")
	fs.BoolVar(&opts.NoEdition, "no-edition", false, "Remove edition from name")
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
	fs.StringVar(&opts.TMDB, "tmdb", "", "Override TMDB id")
	fs.StringVar(&opts.IMDb, "imdb", "", "Override IMDb id")
	fs.IntVar(&opts.MAL, "mal", 0, "Override MAL id")
	fs.IntVar(&opts.TVDB, "tvdb", 0, "Override TVDB id")
	fs.IntVar(&opts.TVmaze, "tvmaze", 0, "Override TVmaze id")
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
	fs.BoolVar(&opts.Anon, "a", false, "Upload anonymously")
	fs.BoolVar(&opts.Anon, "anon", false, "Upload anonymously")
	fs.BoolVar(&opts.Draft, "dr", false, "Send uploads to drafts where supported")
	fs.BoolVar(&opts.Draft, "draft", false, "Send uploads to drafts where supported")
	fs.BoolVar(&opts.ModQ, "mq", false, "Opt into mod queue where supported")
	fs.BoolVar(&opts.ModQ, "modq", false, "Opt into mod queue where supported")
	fs.StringVar(&opts.Channel, "ch", "", "Override SPD channel")
	fs.StringVar(&opts.Channel, "channel", "", "Override SPD channel")

	flagArgs, positionalArgs := splitInterspersedCLIFlags(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cliOptions{}, nil, nil, &cliHelpError{usage: formatFlagUsage(fs, "upbrr [options] <input path>...")}
		}
		return cliOptions{}, nil, nil, fmt.Errorf("parse CLI options: %w", err)
	}

	visited := make(map[string]bool)
	aliases := cliFlagAliases()
	fs.Visit(func(f *flag.Flag) {
		name := f.Name
		if canonical, ok := aliases[name]; ok {
			name = canonical
		}
		visited[name] = true
	})

	if opts.UnattendedConfirm {
		opts.Unattended = true
		visited["unattended"] = true
		visited["unattended_confirm"] = true
	}

	if visited["imdb"] {
		if trimmed := strings.TrimSpace(opts.IMDb); trimmed != "" {
			if _, err := parseIMDbID(trimmed); err != nil {
				return cliOptions{}, nil, nil, err
			}
		}
	}
	if visited["infohash"] {
		if _, err := parseInfoHash(opts.InfoHash); err != nil {
			return cliOptions{}, nil, nil, err
		}
	}
	if visited["imghost"] {
		normalized, err := parseImageHost(opts.ImageHost)
		if err != nil {
			return cliOptions{}, nil, nil, err
		}
		opts.ImageHost = normalized
	}
	if visited["disctype"] {
		normalized, err := parseTIKDiscType(opts.DiscType)
		if err != nil {
			return cliOptions{}, nil, nil, err
		}
		opts.DiscType = normalized
	}
	if visited["max-piece-size"] {
		if err := validateMaxPieceSize(opts.MaxPieceSize); err != nil {
			return cliOptions{}, nil, nil, err
		}
	}
	if visited["nohash"] && visited["rehash"] {
		return cliOptions{}, nil, nil, errors.New("nohash and rehash cannot be used together")
	}
	if visited["manual_frames"] {
		if _, err := parseManualFrames(opts.ManualFrames); err != nil {
			return cliOptions{}, nil, nil, err
		}
	}
	if visited["comparison"] {
		if _, err := parseComparisonPaths(opts.Comparison); err != nil {
			return cliOptions{}, nil, nil, err
		}
	}
	if visited["comparison_index"] {
		if err := validateComparisonIndex(opts.ComparisonIndex); err != nil {
			return cliOptions{}, nil, nil, err
		}
	}
	if visited["log-level"] {
		if _, err := api.ParseLogLevel(opts.LogLevel); err != nil {
			return cliOptions{}, nil, nil, fmt.Errorf("upbrr: %w", err)
		}
	}
	if visited["tmdb"] {
		if trimmed := strings.TrimSpace(opts.TMDB); trimmed != "" {
			if _, _, err := parseTMDBID(trimmed); err != nil {
				return cliOptions{}, nil, nil, err
			}
		}
	}
	if visited["site-upload"] {
		normalized := strings.ToUpper(strings.TrimSpace(opts.SiteUpload))
		if normalized == "" {
			return cliOptions{}, nil, nil, errors.New("site-upload requires a tracker")
		}
		opts.SiteUpload = normalized
	}
	if visited["limit-queue"] && opts.LimitQueue < 0 {
		return cliOptions{}, nil, nil, errors.New("limit-queue must be >= 0")
	}
	if visited["queue"] {
		trimmed := strings.TrimSpace(opts.QueueName)
		if trimmed == "" {
			return cliOptions{}, nil, nil, errors.New("--queue requires a non-empty queue name")
		}
		opts.QueueName = trimmed
	}
	if opts.ExportConfigPlaintext && !visited["export-config"] {
		return cliOptions{}, nil, nil, errors.New("--export-config-plaintext requires --export-config")
	}
	if opts.ExportConfigPlaintext && strings.TrimSpace(opts.ExportConfigPath) == "" {
		return cliOptions{}, nil, nil, errors.New("--export-config must have a non-empty value when --export-config-plaintext is used")
	}
	if _, err := buildTrackerIDOverrides(opts, visited); err != nil {
		return cliOptions{}, nil, nil, err
	}

	return opts, visited, positionalArgs, nil
}

func splitInterspersedCLIFlags(fs *flag.FlagSet, args []string) ([]string, []string) {
	flagArgs := make([]string, 0, len(args))
	positionalArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionalArgs = append(positionalArgs, args[i+1:]...)
			break
		}
		name, ok := cliFlagName(arg)
		if !ok {
			positionalArgs = append(positionalArgs, arg)
			continue
		}
		flagDef := fs.Lookup(name)
		if flagDef == nil {
			flagArgs = append(flagArgs, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)
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

func cliFlagName(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" {
		return "", false
	}
	trimmed := strings.TrimLeft(arg, "-")
	if trimmed == "" {
		return "", false
	}
	if before, _, ok := strings.Cut(trimmed, "="); ok {
		trimmed = before
	}
	return trimmed, trimmed != ""
}

func cliFlagAliases() map[string]string {
	return map[string]string{
		"tk":                   "trackers",
		"lq":                   "limit-queue",
		"sc":                   "site-check",
		"su":                   "site-upload",
		"rtk":                  "trackers-remove",
		"dtmp":                 "delete-tmp",
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
	}
}

// parseServeOptions parses serve-only flags and returns the set of flags the
// caller supplied so config defaults are not overwritten by zero values.
func parseServeOptions(args []string) (serveOptions, map[string]bool, error) {
	var opts serveOptions
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&opts.ConfigPath, "config", "", "Path to config file")
	fs.StringVar(&opts.Addr, "addr", "", "Web UI listen address (host:port)")
	fs.StringVar(&opts.Host, "host", "", "Web UI host to bind")
	fs.Var(&decimalPortValue{target: &opts.Port}, "port", "Web UI port to bind")
	fs.StringVar(&opts.BaseURL, "base-url", "", "External Web UI base URL or path, for example https://example.test/upbrr/ or /upbrr/")
	fs.BoolVar(&opts.PersistListen, "persist-listen", false, "Persist Web UI listen host and port to web-config.json")
	fs.BoolVar(&opts.PersistWebConfig, "persist-web-config", false, "Persist supplied Web UI serve settings to web-config.json")
	fs.BoolVar(&opts.DevNoAuth, "dev-no-auth", false, "Development only: serve web UI without web authentication on loopback hosts")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return serveOptions{}, nil, &cliHelpError{usage: formatFlagUsage(fs, "upbrr serve [options]")}
		}
		return serveOptions{}, nil, fmt.Errorf("parse serve options: %w", err)
	}

	visited := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})

	return opts, visited, nil
}

func formatFlagUsage(fs *flag.FlagSet, usage string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Usage: %s\n", usage)
	if fs.Name() == "upbrr" {
		fmt.Fprint(&builder, "\nCommands:\n")
		fmt.Fprint(&builder, "  serve [options]\n")
		fmt.Fprint(&builder, "      Start the embedded web UI server\n")
		fmt.Fprint(&builder, "      Options: --addr, --host, --port, --base-url, --persist-web-config, --dev-no-auth\n")
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

	var remaining []*flag.Flag
	fs.VisitAll(func(f *flag.Flag) {
		if !seen[f.Name] {
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
			{title: "Development", names: []string{"dev-no-auth"}},
		}
	}
	return []helpSection{
		{title: "Config", names: []string{"config", "export-config", "export-config-plaintext", "import-config", "create-auth"}},
		{title: "Application", names: []string{"version", "cleanup"}},
		{title: "Execution", names: []string{
			"queue", "limit-queue", "site-check", "site-upload", "debug", "log-level", "upload-only",
			"delete-tmp", "unattended", "unattended_confirm",
		}},
		{title: "Tracker Selection", names: []string{"trackers", "trackers-remove"}},
		{title: "Tracker IDs", names: []string{"ptp", "blu", "aither", "lst", "oe", "hdb", "btn", "bhd", "ulcx"}},
		{title: "Release Overrides", names: []string{
			"category", "type", "source", "resolution", "tag", "service", "distributor", "original-language",
			"edition", "season", "episode", "episode-title", "manual-year", "daily", "region", "no-season", "no-year",
			"no-aka", "no-tag", "no-edition", "no-dub", "no-dual", "dual-audio",
		}},
		{title: "Metadata IDs", names: []string{"tmdb", "imdb", "mal", "tvdb", "tvmaze"}},
		{title: "Tracker Overrides", names: []string{
			"skip-dupe-check", "skip-dupe-asking", "double-dupe-check", "foreign", "opera", "asian", "disctype",
			"commentary", "personalrelease", "stream", "webdv", "not-anime", "anon", "draft", "modq", "channel",
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

func formatHelpFlag(builder *strings.Builder, f *flag.Flag, aliases []string) {
	valueName, usage := flag.UnquoteUsage(f)
	if _, ok := f.Value.(*decimalPortValue); ok {
		valueName = "int"
	}
	names := make([]string, 0, 2+len(aliases))
	names = append(names, "-"+f.Name, "--"+f.Name)
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

type boolFlag interface {
	IsBoolFlag() bool
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

func isBoolFlag(f *flag.Flag) bool {
	boolValue, ok := f.Value.(boolFlag)
	return ok && boolValue.IsBoolFlag()
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
		SourcePath: sourcePath,
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
			Screens:         screens,
			NoSeed:          opts.NoSeed,
			SkipAutoTorrent: opts.SkipAutoTorrent,
			KeepFolder:      opts.KeepFolder,
			OnlyID:          opts.OnlyID,
			CaptureDVDMenus: opts.GetDVDMenus,
			InteractionMode: opts.interactionMode(),
		},
		ReleaseNameOverrides: buildReleaseNameOverrides(visited, releaseOverrideInput{
			Category:     opts.Category,
			Type:         opts.Type,
			Source:       opts.Source,
			Resolution:   opts.Resolution,
			Tag:          opts.Tag,
			Service:      opts.Service,
			Edition:      opts.Edition,
			Season:       opts.Season,
			Episode:      opts.Episode,
			EpisodeTitle: opts.EpisodeTitle,
			ManualYear:   opts.ManualYear,
			ManualDate:   opts.ManualDate,
			NoSeason:     opts.NoSeason,
			NoYear:       opts.NoYear,
			NoAKA:        opts.NoAKA,
			NoTag:        opts.NoTag,
			NoEdition:    opts.NoEdition,
			NoDub:        opts.NoDub,
			NoDual:       opts.NoDual,
			DualAudio:    opts.DualAudio,
			Region:       opts.Region,
		}),
		SkipDupeCheck:          opts.SkipDupeCheck,
		SkipDupeAsActual:       opts.SkipDupeAsActual,
		DoubleDupeCheck:        opts.DoubleDupeCheck,
		DescriptionOverrideURL: strings.TrimSpace(opts.DescriptionLink),
		MetadataOverrides:      buildMetadataOverrides(opts, visited),
		TrackerConfigOverrides: buildTrackerConfigOverrides(opts, visited),
		TrackerSiteOverrides:   buildTrackerSiteOverrides(opts, visited),
		ClientOverrides:        buildClientOverrides(opts, visited),
		ImageHostOverrides:     buildImageHostOverrides(opts, visited),
		ScreenshotOverrides:    buildScreenshotOverrides(opts, visited),
		TorrentOverrides:       buildTorrentOverrides(opts, visited),
		ConfirmBDMVRescan:      opts.ConfirmBDMVRescan,
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
	return overrides
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

func buildExternalIDOverrides(opts cliOptions, visited map[string]bool) (api.ExternalIDOverrides, error) {
	overrides := api.ExternalIDOverrides{}
	if visited["tmdb"] {
		id, _, err := parseTMDBID(opts.TMDB)
		if err != nil {
			return api.ExternalIDOverrides{}, err
		}
		overrides.TMDBID = intPtr(id)
	}
	if visited["tvdb"] {
		overrides.TVDBID = intPtr(opts.TVDB)
	}
	if visited["tvmaze"] {
		overrides.TVmazeID = intPtr(opts.TVmaze)
	}
	if visited["mal"] {
		overrides.MALID = intPtr(opts.MAL)
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

func parseTMDBID(raw string) (int, string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return 0, "", fmt.Errorf("invalid tmdb id %q", raw)
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
