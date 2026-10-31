// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { FieldMeta } from "../types";

type Presentation = Pick<FieldMeta, "label" | "help">;
const field = (label: string, help: string): Presentation => ({ label, help });

/** Controls omitted from Settings; stored values and secret metadata remain intact. */
export const hiddenSettingsFields: Record<string, readonly string[]> = {
  Metadata: ["BTNAPI"],
  Description: ["MultiScreens", "PackThumbSize", "CharLimit", "FileLimit", "ProcessLimit"],
  ArrIntegration: ["EmbyDir", "EmbyTVDir"],
};

/** Display-only metadata: configuration keys and values remain owned by the existing schema. */
export const settingsFieldPresentation: Record<string, Record<string, Presentation>> = {
  MainSettings: {
    UpdateNotification: field(
      "Update notifications (compatibility)",
      "Retained for configuration compatibility. This switch does not currently control update notifications.",
    ),
    VerboseNotification: field(
      "Detailed notifications (compatibility)",
      "Retained for configuration compatibility. This switch does not currently change notification detail.",
    ),
    TMDBAPI: field(
      "TMDB API key",
      "Enables TMDB metadata and artwork lookups. Empty leaves other providers and supplied IDs available. Leave a stored [REDACTED] value unchanged to keep the key.",
    ),
    TrackerPassChecks: field(
      "Tracker check passes (compatibility)",
      "Retained for configuration compatibility. This number does not currently change tracker-check attempts.",
    ),
    InputHistoryLimit: field(
      "Remembered source paths",
      "Maximum source paths retained in this browser. Default: 20. Zero disables and clears retained history; negative values are rejected.",
    ),
    DBPath: field(
      "Database path",
      "SQLite path selected when the server starts. Saving this field cannot move the running instance or its database; select the database location at startup.",
    ),
    UseFavicons: field(
      "Show tracker icons",
      "Fetch and display tracker icons on supported workflow pages. Enabled by default.",
    ),
    FaviconOnly: field(
      "Show icons without tracker names",
      "Hide tracker names where an icon is shown. Has no effect when Show tracker icons is disabled.",
    ),
    SceneDetection: field(
      "Detect scene releases with SRRDB",
      "Use SRRDB evidence during preparation to identify scene releases. Disabling prevents these SRRDB requests; enabled by default.",
    ),
  },
  Metadata: {
    SkipAutoTorrent: field(
      "Skip automatic torrent-client search",
      "Do not automatically search configured torrent clients for reusable torrents during preparation.",
    ),
    SkipTrackerFilenameLookup: field(
      "Skip filename-based tracker lookup",
      "Do not use the source filename for tracker-data lookup. Known tracker IDs can still be looked up.",
    ),
    UseLargestPlaylist: field(
      "Automatically select largest Blu-ray playlist",
      "Lets CLI preparation select the largest Blu-ray playlist instead of asking. This does not replace Web UI playlist selection.",
    ),
    KeepImages: field(
      "Collect existing tracker images",
      "Collect eligible images from supported tracker descriptions for later reuse. Eligible images are included automatically on Screenshots, where you can review or remove them. Collecting them does not capture new frames. Imported comparison images remain in descriptions.",
    ),
    OnlyID: field(
      "Limit tracker lookup to only metadata ids",
      "Request identity fields rather than full description metadata from supported trackers. Image collection is controlled separately; supported trackers can still provide images when both settings are enabled.",
    ),
    UserOverrides: field(
      "User overrides (compatibility)",
      "Retained for configuration compatibility. This switch does not enable or disable the current release correction controls.",
    ),
    PingUnit3D: field(
      "UNIT3D ping preference (compatibility)",
      "Retained for configuration compatibility. This switch does not currently enable additional UNIT3D requests.",
    ),
    GetBlurayInfo: field(
      "Look up Blu-ray.com releases",
      "Find disc-release candidates for BDMV or DVD sources with an IMDb ID. Blu-ray link or cover-image options also enable this lookup.",
    ),
    BlurayScore: field(
      "Blu-ray match threshold",
      "Minimum candidate score for automatic selection when multiple releases are found. Default: 94.5. A non-positive value uses 100.",
    ),
    BluraySingleScore: field(
      "Single-candidate Blu-ray threshold",
      "Minimum score for automatic selection when one release is found. Default: 89.5. A non-positive value uses 100.",
    ),
    CheckPredb: field(
      "PreDB check preference (compatibility)",
      "Retained for configuration compatibility. This switch does not currently enable PreDB lookups; SRRDB scene detection is configured separately.",
    ),
  },
  ScreenshotHandling: {
    Screens: field(
      "Automatic screenshot count",
      "Number requested for automatic screenshot generation. Default: 4; must be greater than zero. Tracker-specific requirements can affect the final selection.",
    ),
    MaxMenuItems: field(
      "Maximum DVD menu images",
      "Maximum distinct images from opt-in automatic DVD menu capture. Range: 0–32; zero uses the default of 6.",
    ),
    MinSuccessfulUploads: field(
      "Minimum successful images per host",
      "Accept a partially failed image-host batch once this many images have published. Default: 3. Zero or negative requires every image to succeed; tracker image requirements still apply.",
    ),
    CutoffScreens: field(
      "Screenshot cutoff (compatibility)",
      "Retained for configuration compatibility. This value does not currently shorten captures or change the screenshot count.",
    ),
    FrameOverlay: field(
      "Show frame information on captures",
      "Add frame number, picture type and applicable HDR tone-mapping information to new captures. This does not change which frame is selected.",
    ),
    OverlayTextSize: field(
      "Overlay text size",
      "Text size relative to a 1080-line frame, scaled to the source resolution. Default: 18; non-positive values use 18. Requires frame information overlay.",
    ),
    ProcessLimit: field(
      "Concurrent capture processes",
      "Maximum FFmpeg screenshot processes when Limit capture concurrency is enabled. Default: 2; non-positive values use one process.",
    ),
    MaxConcurrentUploads: field(
      "Concurrent image uploads",
      "Maximum simultaneous uploads in an image-host batch. Default: 6; non-positive values use one worker.",
    ),
    FFmpegLimit: field(
      "Limit capture concurrency",
      "Apply the Concurrent capture processes limit. When disabled, screenshot jobs may run concurrently without that cap.",
    ),
    ToneMap: field(
      "Tone map HDR screenshots",
      "Convert new HDR captures for SDR display. Does not alter saved images or the source video. Enabled by default.",
    ),
    UseLibplacebo: field(
      "Use Vulkan/libplacebo tone mapping",
      "Needed for Dolby Vision Profile 5 inputs to avoid green/purple screenshots. Uses FFmpeg Vulkan/libplacebo when tone mapping is enabled and frame overlay is disabled. Requires a compatible FFmpeg/Vulkan setup; failed captures can fall back to software tone mapping.",
    ),
    FFmpegCompression: field(
      "PNG compression level",
      "FFmpeg PNG compression from 0 to 9; default 6. Higher compression trades processing time for smaller lossless images, not lower image quality.",
    ),
    TonemapAlgorithm: field(
      "Software tone-mapping algorithm",
      "FFmpeg software tonemap filter algorithm. Empty uses mobius. This choice is not used by the libplacebo path, which uses hable.",
    ),
    Desat: field(
      "Software highlight desaturation",
      "Desaturation value passed to the FFmpeg software tonemap filter. Default: 10.0. Does not control libplacebo tone mapping.",
    ),
  },
  Description: {
    AddLogo: field(
      "Include title logo",
      "Include a TMDB title logo where available and supported by the tracker description builder. Requires matching TMDB artwork.",
    ),
    LogoSize: field(
      "Logo width (pixels)",
      "Rendered logo width in the shared description builder. Default: 300; non-positive values use 300. Tracker-specific layouts may override it.",
    ),
    LogoLanguage: field(
      "Preferred logo languages",
      "Comma- or semicolon-separated TMDB language codes for logo lookup. Empty leaves the normal provider selection in place.",
    ),
    ThumbnailSize: field(
      "Screenshot thumbnail width (pixels)",
      "Width of screenshot and DVD-menu thumbnails in shared descriptions. Default: 350; non-positive values use 350. Tracker rules may override it.",
    ),
    ScreensPerRow: field(
      "Screenshots per description row",
      "Positive whole number of images per shared description row. Empty, invalid or non-positive values use 2. Tracker-specific layout rules can override it.",
    ),
    EpisodeOverview: field(
      "Include episode overview",
      "Include available episode overview text in description builders that honor this preference. Some trackers use their own required layout.",
    ),
    TonemappedHeader: field(
      "Tone-mapped screenshot notice",
      "Tracker markup used when every included screenshot is known to be tone mapped. Mixed or unknown processing uses a generated evidence summary instead; the current capture setting alone is not proof.",
    ),
    CustomDescriptionHeader: field(
      "Description introduction",
      "Custom tracker markup prepended by supported description builders. Empty adds no custom introduction. Preview each tracker before submission.",
    ),
    ScreenshotHeader: field(
      "Screenshot section heading",
      "Custom tracker markup before ordinary screenshots in supported builders. Empty leaves that builder's normal heading behavior.",
    ),
    DiscMenuHeader: field(
      "DVD menu section heading",
      "Custom tracker markup before DVD menu images in supported builders. Empty leaves the builder's normal heading behavior.",
    ),
    CustomSignature: field(
      "Description signature",
      "Custom tracker markup appended by supported description builders. Empty adds no custom signature. Unsupported markup is not made portable automatically.",
    ),
    AddBlurayLink: field(
      "Include Blu-ray.com release link",
      "Include the selected Blu-ray.com release URL when available in shared descriptions. Also enables disc-release lookup.",
    ),
    UseBlurayImages: field(
      "Include Blu-ray.com cover images",
      "Include available cover images for the selected disc release in shared descriptions. Also enables disc-release lookup.",
    ),
    BlurayImageSize: field(
      "Disc cover width (pixels)",
      "Rendered Blu-ray.com cover width. These images are usually taller than they are wide; use a smaller width to reduce their height in descriptions. Default: 250; non-positive values use 250. Requires available selected-release cover images.",
    ),
  },
  ArrIntegration: {
    UseSonarr: field(
      "Look up TV sources in Sonarr",
      "Match TV source paths against configured Sonarr instances for IDs and release evidence. Failed or empty lookups do not block preparation.",
    ),
    UseRadarr: field(
      "Look up movie sources in Radarr",
      "Match movie source paths against configured Radarr instances for IDs and release evidence. Failed or empty lookups do not block preparation.",
    ),
  },
  PostUpload: {
    InjectDelay: field(
      "Client injection delay (seconds)",
      "Wait before each torrent-client injection. Default: 0; non-positive values do not wait. A tracker-specific delay can override this.",
    ),
    MaxConcurrentTrackers: field(
      "Concurrent tracker uploads",
      "Maximum simultaneous tracker uploads. Default: 4; zero uses 4 and negative values are rejected.",
    ),
    ShowUploadDuration: field(
      "Show upload duration (compatibility)",
      "Retained for configuration compatibility. This switch does not currently control duration reporting.",
    ),
    PrintTrackerMessages: field(
      "Print tracker messages (compatibility)",
      "Retained for configuration compatibility. This switch does not currently change tracker response output.",
    ),
    PrintTrackerLinks: field(
      "Print tracker links (compatibility)",
      "Retained for configuration compatibility. This switch does not currently control result links.",
    ),
    SearchRequests: field(
      "Search tracker requests (compatibility)",
      "Retained for configuration compatibility. This switch does not currently enable request searches.",
    ),
    CrossSeeding: field(
      "Cross-seeding preference (compatibility)",
      "Retained for configuration compatibility. This switch does not currently enable or disable cross-seeding behavior.",
    ),
    CrossSeedCheckEverything: field(
      "Check all cross-seed candidates (compatibility)",
      "Retained for configuration compatibility. This switch does not currently broaden torrent searches or duplicate checks.",
    ),
  },
  TorrentCreation: {
    MkbrrThreads: field(
      "Torrent hashing threads (compatibility)",
      "Retained for configuration compatibility. This value does not currently control mkbrr hashing concurrency.",
    ),
    PreferMax16: field(
      "Prefer reusable torrents with pieces up to 16 MiB",
      "Prefer a validated reusable client torrent with piece size at most 16 MiB. Does not impose that piece size on newly generated torrents.",
    ),
    RehashCooldown: field(
      "Rehash-dependent upload delay (seconds)",
      "Wait after reusable-torrent submissions finish before starting rehash-dependent tracker uploads. Default: 0; negative values behave as zero.",
    ),
  },
};

for (const service of ["Sonarr", "Radarr"]) {
  for (const suffix of ["", "1", "2", "3"]) {
    const instance = suffix ? `additional instance ${suffix}` : "primary instance";
    settingsFieldPresentation.ArrIntegration[`${service}URL${suffix}`] = field(
      `${service} ${instance} URL`,
      `Base URL reachable from upbrr, without an API route. Requires this instance's API key and enabled ${service} lookup. Instances are tried primary, 1, 2, 3 until useful evidence is found; incomplete pairs are ignored.`,
    );
    settingsFieldPresentation.ArrIntegration[`${service}APIKey${suffix}`] = field(
      `${service} ${instance} API key`,
      `API key for the matching ${service} URL. Leave [REDACTED] unchanged to retain the stored secret; clear it to disable this URL/key pair.`,
    );
  }
}

settingsFieldPresentation.TorrentClients = {
  Type: {
    label: "Client type",
    help: "Choose a qBittorrent API connection or a watch folder that receives local torrent files.",
  },
  WatchFolder: {
    label: "Torrent watch folder",
    help: "Existing directory accessible to upbrr where local .torrent files are copied. Watch folders cannot consume URL-only results.",
  },
  StorageDir: {
    label: "Torrent storage directory (compatibility)",
    help: "Retained for compatibility; not currently used to change watch-folder copying or torrent storage.",
  },
  QuiProxyURL: {
    label: "qui proxy URL",
    help: "qui-provided qBittorrent-compatible endpoint. A nonempty proxy takes precedence over the direct URL and port. This URL may contain credentials; keep it private.",
  },
  QbitURL: {
    label: "qBittorrent WebUI URL",
    help: "Direct WebUI address. A missing scheme becomes HTTP. Ignored while a qui proxy URL is configured.",
  },
  QbitPort: {
    label: "qBittorrent WebUI port",
    help: "Port added only when the direct URL has no explicit port. Zero adds no separate port.",
  },
  QbitUser: {
    label: "qBittorrent username",
    help: "Username for direct WebUI login. Passwordless access must already be configured in qBittorrent; this does not change server authentication.",
  },
  QbitPass: {
    label: "qBittorrent password",
    help: "Password for direct WebUI login. Leave [REDACTED] unchanged to preserve the saved password; clear it only when the server permits passwordless access.",
  },
  QbitCategoryValue: {
    label: "Upload category",
    help: "Category assigned to injected torrents unless a nonempty cross-seed category applies.",
  },
  QbitTag: {
    label: "Upload tags",
    help: "Comma-separated tags assigned to injected torrents unless nonempty cross-seed tags apply. Explicit tags take precedence over tracker-name fallback.",
  },
  QbitCrossCategory: {
    label: "Cross-seed category",
    help: "Category for cross-seed injection. Empty uses the regular upload category.",
  },
  QbitCrossTag: {
    label: "Cross-seed tags",
    help: "Comma-separated tags for cross-seed injection. Empty uses regular upload tags, then the tracker-name fallback if enabled.",
  },
  UseTrackerAsTag: {
    label: "Use tracker name when tags are empty",
    help: "Use the tracker name as a tag only when the effective regular or cross-seed tag list is empty.",
  },
  Linking: {
    label: "File linking method",
    help: "Stage selected source files with hard links, reflinks or symbolic links before injection. None uses the original source location. Requires link staging folders and filesystem support.",
  },
  AllowFallback: {
    label: "Allow client fallback",
    help: "Allow original-path injection for supported failed or unavailable link-staging cases. Also permits this client in URL-only fallback when selected clients cannot consume URLs. Does not bypass path validation. Enabled by default.",
  },
  LinkedFolder: {
    label: "Link staging folders",
    help: "Candidate roots for staged file links; a tracker subfolder is added. Windows hard links and reflinks require a source-compatible volume.",
  },
  LocalPath: {
    label: "Local path mapping roots",
    help: "Paths as seen by upbrr, paired by item position with client path roots. Needed when upbrr and qBittorrent use different path references, such as Docker setups. Blank or unpaired entries are ignored; the most-specific matching root wins.",
  },
  RemotePath: {
    label: "Client path mapping roots",
    help: "Corresponding paths as seen by qBittorrent. Needed when upbrr and qBittorrent use different path references, such as Docker setups. Keep these in the same item order as local path roots.",
  },
  AutomaticManagementPaths: {
    label: "Automatic-management source roots",
    help: "Enable qBittorrent automatic management for unlinked injections whose local save folder is beneath one of these roots. Empty disables it; linked staging disables it regardless.",
  },
  VerifyWebUICertificate: {
    label: "Verify connection TLS certificate",
    help: "Verify HTTPS server identity for direct or proxy connections. Enabled by default. Disabling weakens connection security; legacy TLS settings retain their existing precedence.",
  },
};

settingsFieldPresentation.ClientSetup = {
  DefaultClient: {
    label: "Default torrent client",
    help: "Fallback for empty injection or search lists. If also empty, injection uses the sole configured client only, while search considers all qBittorrent/qui clients. Release and tracker overrides can take precedence.",
  },
  InjectClients: {
    label: "Clients receiving torrents",
    help: "Global torrent-injection list. Empty uses the default client, then the sole configured client. An explicit selection that resolves to no client does not silently choose another.",
  },
  SearchClients: {
    label: "Clients searched for existing torrents",
    help: "Search this client list for reusable torrents. Empty uses the default client, then all qBittorrent/qui clients. An explicit list is authoritative.",
  },
};

settingsFieldPresentation.ImageHosting = {
  ZiplineURL: field(
    "Zipline upload endpoint URL",
    "Full endpoint that receives image uploads. Used exactly as entered; no upload route is appended. Requires its API key.",
  ),
  ZiplineAPIKey: field(
    "Zipline API key",
    "Authorization credential sent to the configured Zipline upload endpoint. Required when Zipline is used.",
  ),
  ShareXURL: field(
    "ShareX upload endpoint URL",
    "Full endpoint that receives image uploads. Empty uses https://img.digitalcore.club/api/upload. Requires the matching API key; this is not a ShareX configuration-file import.",
  ),
  ShareXAPIKey: field(
    "ShareX API key",
    "Authorization credential sent to the configured ShareX upload endpoint. Required when this host is used.",
  ),
};
for (const [key, host] of Object.entries({
  ImgBBAPI: "ImgBB",
  LensdumpAPI: "Lensdump",
  PTScreensAPI: "PTScreens",
  OnlyImageAPI: "OnlyImage",
  DalexniAPI: "Dalexni",
  PassTheImageAPI: "PassTheImage",
  SeedpoolCDNAPI: "Seedpool CDN",
  UTPPMAPI: "UTPPM",
  LostimgAPI: "Lostimg",
  ReelflixAPI: "ReelFliX",
  SamaritanoAPI: "Samaritano",
})) {
  settingsFieldPresentation.ImageHosting[key] = field(
    `${host} API key`,
    `Account API key sent to ${host} when uploading images there. Required when this host is used. Leave [REDACTED] unchanged to keep the stored key.`,
  );
}
