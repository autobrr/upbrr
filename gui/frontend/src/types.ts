// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

/** Resolved external IDs returned by metadata resolution for the current source path. */
export type ExternalIDs = {
  TMDBID: number;
  IMDBID: number;
  TVDBID: number;
  TVmazeID: number;
  /** Canonical anime identifier used for MAL and MAL/AniList-compatible tracker fields. */
  MALID: number;
  Category: string;
  /** Resolver source labels for each provider ID, such as tracker, mediainfo, tmdb, or scene. */
  SourceTMDB: string;
  SourceIMDB: string;
  SourceTVDB: string;
  SourceTVmaze: string;
  /** Resolver source label for MALID. */
  SourceMAL: string;
};

/** External ID overrides use null/undefined for untouched fields and 0 for an explicit clear. */
export type ExternalIDOverrides = {
  TMDBID?: number | null;
  IMDBID?: number | null;
  TVDBID?: number | null;
  TVmazeID?: number | null;
  /** Canonical MAL/AniList-compatible anime identifier override. */
  MALID?: number | null;
};

export type ReleaseNameOverrides = {
  Category?: string | null;
  Type?: string | null;
  Source?: string | null;
  Resolution?: string | null;
  Tag?: string | null;
  Service?: string | null;
  Edition?: string | null;
  Season?: string | null;
  Episode?: string | null;
  EpisodeTitle?: string | null;
  ManualYear?: number | null;
  ManualDate?: string | null;
  UseSeasonEpisode?: boolean | null;
  NoSeason?: boolean | null;
  NoYear?: boolean | null;
  NoAKA?: boolean | null;
  NoTag?: boolean | null;
  NoEdition?: boolean | null;
  NoDub?: boolean | null;
  NoDual?: boolean | null;
  DualAudio?: boolean | null;
  Region?: string | null;
};

export type ExternalIDInfo = {
  Provider: string;
  ID: number;
  Source: string;
};

export type ExternalIDCandidate = {
  Provider: string;
  ID: number;
  Title: string;
  OriginalTitle: string;
  Year: number;
  Category: string;
  MediaType: string;
  Overview: string;
  PosterURL: string;
  Similarity: number;
};

export type ExternalIDCandidates = {
  TMDB: ExternalIDCandidate[];
  IMDB: ExternalIDCandidate[];
  TMDBAutoSelected: boolean;
  IMDBAutoSelected: boolean;
};

export type TMDBCompany = {
  ID: number;
  Name: string;
  LogoPath: string;
  OriginCountry: string;
};

export type TMDBCountry = {
  ISO3166: string;
  Name: string;
};

export type TMDBNetwork = {
  ID: number;
  Name: string;
  LogoPath: string;
  OriginCountry: string;
};

export type TMDBMetadata = {
  TMDBID: number;
  IMDBID: number;
  TVDBID: number;
  Category: string;
  Title: string;
  OriginalTitle: string;
  Year: number;
  ReleaseDate: string;
  FirstAirDate: string;
  LastAirDate: string;
  OriginCountry: string[];
  OriginalLanguage: string;
  Overview: string;
  Poster: string;
  TMDBPosterPath: string;
  Logo: string;
  TMDBLogo: string;
  Backdrop: string;
  TMDBType: string;
  Runtime: number;
  Genres: string;
  GenreIDs: string;
  Creators: string[];
  Directors: string[];
  Cast: string[];
  MALID: number;
  Anime: boolean;
  Demographic: string;
  RetrievedAKA: string;
  Keywords: string;
  /** Maps TMDB translation language keys, such as "de" or "pt-BR", to localized titles. */
  LocalizedTitles: Record<string, string>;
  YouTube: string;
  Certification: string;
  ProductionCompanies: TMDBCompany[];
  ProductionCountries: TMDBCountry[];
  Networks: TMDBNetwork[];
  IMDbMismatch: boolean;
  MismatchedIMDbID: number;
};

export type IMDBPerson = {
  ID: string;
  Name: string;
};

export type IMDBEditionDetail = {
  DisplayName: string;
  Seconds: number;
  Minutes: number;
  Attributes: string[];
};

export type IMDBAKA = {
  Title: string;
  Country: string;
  Language: string;
  Attributes: string[];
};

export type IMDBReleaseDate = {
  Year: number;
  Month: number;
  Day: number;
};

export type IMDBEpisode = {
  ID: string;
  Title: string;
  ReleaseYear: number;
  ReleaseDate: IMDBReleaseDate;
  Season: number;
  EpisodeText: string;
};

export type IMDBSeasonSummary = {
  Season: number;
  Year: number;
  YearRange: string;
};

export type IMDBMetadata = {
  IMDBID: number;
  IMDbIDText: string;
  IMDbURL: string;
  Title: string;
  Year: number;
  EndYear: number;
  AKA: string;
  Type: string;
  Plot: string;
  Rating: number;
  RatingCount: number;
  RatingText: string;
  RuntimeMinutes: number;
  RuntimeText: string;
  Genres: string;
  Country: string;
  CountryList: string;
  Cover: string;
  Directors: IMDBPerson[];
  Creators: IMDBPerson[];
  Writers: IMDBPerson[];
  Stars: IMDBPerson[];
  Editions: string[];
  EditionDetails: Record<string, IMDBEditionDetail>;
  Akas: IMDBAKA[];
  Episodes: IMDBEpisode[];
  SeasonsSummary: IMDBSeasonSummary[];
  SoundMixes: string[];
  TVYear: number;
  OriginalLanguage: string;
};

export type TVDBMetadata = {
  TVDBID: number;
  Name: string;
  NameEnglish: string;
  Overview: string;
  OverviewEnglish: string;
  FirstAired: string;
  Year: number;
  /** True when Year is naming-eligible for TV release names. */
  YearFromAlias: boolean;
  /** TVDB source used for Year, such as first_aired, translation_name, translation_alias, extended_alias, or slug. */
  YearSource: string;
  /** "high" for explicit title or alias years; "low" for guarded slug-derived naming years. */
  YearConfidence: string;
  Type: string;
  Status: string;
  Network: string;
  OriginalCountry: string;
  OriginalLanguage: string;
  HasEnglish: boolean;
  Genres: string;
  Poster: string;
  Aliases: string[];
  EpisodeSeason: number;
  EpisodeNumber: number;
  EpisodeName: string;
  EpisodeNameEnglish: string;
  EpisodeOverview: string;
  EpisodeOverviewEnglish: string;
  EpisodeAired: string;
  /** Selected episode image URL when TVDB returned one. */
  EpisodeImage: string;
  /** Fetched TVDB episode entries, usually for the selected season. */
  Episodes: TVDBEpisodeMetadata[];
};

/** One TVDB episode entry used by tracker descriptions. */
export type TVDBEpisodeMetadata = {
  ID: number;
  SeasonNumber: number;
  EpisodeNumber: number;
  EpisodeName: string;
  EpisodeNameEnglish: string;
  EpisodeOverview: string;
  EpisodeOverviewEnglish: string;
  /** TVDB air date string used in tracker descriptions. */
  EpisodeAired: string;
  /** Episode image URL when TVDB returned one. */
  EpisodeImage: string;
};

export type TVmazeMetadata = {
  TVmazeID: number;
  Name: string;
  Premiered: string;
  Ended: string;
  Summary: string;
  Status: string;
  Type: string;
  Language: string;
  Genres: string;
  Runtime: number;
  AverageRuntime: number;
  Rating: number;
  Weight: number;
  OfficialSite: string;
  Country: string;
  Network: string;
  NetworkCountry: string;
  NetworkLogo: string;
  WebChannel: string;
  WebCountry: string;
  WebLogo: string;
  Poster: string;
  PosterMedium: string;
  Backdrop: string;
  BackdropMedium: string;
  IMDBID: number;
  TVDBID: number;
};

export type WebAuthStatus = {
  path: string;
  exists: boolean;
  usable: boolean;
  canCreate: boolean;
  username: string;
  allowUnencryptedExport: boolean;
  browseRoot: string;
  allowUnrestrictedBrowse: boolean;
  encryptionEnabled: boolean;
  message: string;
};

export type ApplicationInfo = {
  version: string;
  buildIdentifier: string;
  goVersion: string;
  goos: string;
  goarch: string;
  uptime: string;
  uptimeSeconds: number;
};

/** Tracker auth support metadata returned by the app bridge. */
export type TrackerAuthCapability = {
  /** Normalized tracker code used in tracker auth bridge calls. */
  trackerID: string;
  displayName: string;
  /** Compact capability label such as "cookies", "credential_login", or "api_key_cookies_login". */
  authKind: string;
  supportsCookieFile: boolean;
  supportsLogin: boolean;
  supportsAutoLogin: boolean;
  supportsTOTP: boolean;
  supportsManual2FA: boolean;
  requiresAPIKey: boolean;
  requiresPasskey: boolean;
  /** Optional tracker-specific UI notes; Go bridge may serialize a nil slice as null. */
  notes?: string[] | null;
};

/** Current tracker auth state returned after status, import, login, validation, 2FA, or delete actions. */
export type TrackerAuthStatus = {
  trackerID: string;
  displayName: string;
  /** Backend state string such as "configured", "has_cookies", or "login_required". */
  state: string;
  cookieCount: number;
  /** RFC3339 UTC timestamp generated when the backend evaluated the status. */
  lastCheckedAt: string;
  /** Redacted failure detail from the most recent local or remote auth check. */
  lastError: string;
  encryptedStorage: boolean;
  needs2FA: boolean;
  /** Opaque manual-2FA continuation token; empty unless needs2FA is true. */
  challengeID: string;
  /** Stable user-facing status summary or next step. */
  message: string;
};

/** Optional login data for tracker auth flows. */
export type TrackerAuthLoginRequest = {
  /** One-time 2FA code for adapters that accept it during login. */
  code?: string;
};

export type ExternalPreview = {
  Provider: string;
  ID: number;
  Source: string;
  Title: string;
  Year: number;
  Overview: string;
  PosterURL: string;
  BackdropURL: string;
  Category: string;
  OriginalTitle: string;
  ReleaseDate: string;
  FirstAirDate: string;
  LastAirDate: string;
  OriginalLanguage: string;
  TMDBType: string;
  Runtime: number;
  Genres: string;
  Keywords: string;
  YouTube: string;
  IMDBType: string;
  Rating: number;
  RatingCount: number;
  RuntimeMinutes: number;
  Country: string;
  Premiered: string;
  IMDBID: number;
  TVDBID: number;
  TMDB?: TMDBMetadata;
  IMDB?: IMDBMetadata;
  TVDB?: TVDBMetadata;
  TVmaze?: TVmazeMetadata;
};

export type BlurayImage = {
  Kind: string;
  URL: string;
};

export type BluraySpecs = {
  Video: {
    Codec: string;
    Resolution: string;
  };
  Audio: string[];
  Subtitles: string[];
  Discs: {
    Type: string;
    Count: number;
    Format: string;
  };
  Playback: {
    Region: string;
    RegionNotes: string;
  };
};

export type BlurayReleaseCandidate = {
  ReleaseID: string;
  ProductID: string;
  MovieTitle: string;
  MovieYear: string;
  Title: string;
  URL: string;
  Price: string;
  Publisher: string;
  Country: string;
  Region: string;
  Score: number;
  Accepted: boolean;
  Warnings: string[];
  MatchNotes: string[];
  Specs: BluraySpecs;
  CoverImages: BlurayImage[];
  GenericDisc: boolean;
  SpecsMissing: boolean;
};

export type BlurayMetadata = {
  SourcePath: string;
  IMDBID: number;
  SearchURL: string;
  SelectedReleaseID: string;
  SelectedURL: string;
  AutoSelected: boolean;
  SelectionReason: string;
  BestScore: number;
  Threshold: number;
  Candidates: BlurayReleaseCandidate[];
  UpdatedAt: string;
};

export type TrackerPreview = {
  Tracker: string;
  TrackerID: string;
  TorrentURL: string;
  InfoHash: string;
  TMDBID: number;
  IMDBID: number;
  TVDBID: number;
  MALID: number;
  Category: string;
  Description: string;
  DescriptionHTML: string;
  ImageURLs: string[];
  Filename: string;
  Matched: boolean;
  UpdatedAt: string;
};

/** Upload rule failure attached to a tracker before dupe checking or upload. */
export type RuleFailure = {
  Rule: string;
  Reason: string;
};

export type DupeEntry = {
  Name: string;
  SizeBytes: number;
  SizeKnown: boolean;
  SizeText: string;
  Files: string[];
  FileCount: number;
  Trumpable: boolean;
  Link: string;
  Download: string;
  Flags: string[];
  ID: string;
  Type: string;
  Res: string;
  Internal: boolean;
  BDInfo: string;
  Description: string;
};

export type DupeEpisodeMatch = {
  ID: string;
  Name: string;
  Link: string;
  Tracker: string;
  Internal: boolean;
};

export type DupeMatch = {
  FilenameMatch: string;
  FileCountMatch: number;
  SizeMatch: string;
  TrumpableID: string;
  MatchedID: string;
  MatchedName: string;
  MatchedLink: string;
  MatchedDownload: string;
  MatchedReason: string;
  SeasonPackExists: boolean;
  SeasonPackName: string;
  SeasonPackLink: string;
  SeasonPackID: string;
  SeasonPackContainsEpisode: boolean;
  MatchedEpisodeIDs: DupeEpisodeMatch[];
};

/**
 * Duplicate-search outcome for one tracker. Raw contains tracker results before
 * filtering, Filtered contains blocking matches, and skipped or failed checks
 * carry Status plus SkipReason or Error. SkipCode and SkipRules expose stable
 * backend skip metadata for typed UI callers.
 */
export type DupeCheckResult = {
  Tracker: string;
  Raw: DupeEntry[];
  Filtered: DupeEntry[];
  HasDupes: boolean;
  ContentFail: boolean;
  Match: DupeMatch;
  Notes: string[];
  Skipped: boolean;
  SkipReason: string;
  /** Stable machine-readable skip reason emitted by the backend. */
  SkipCode: string;
  /** Upload rule keys that produced a rule-failure skip. */
  SkipRules: string[];
  Status: string;
  Error: string;
  CheckedAt: string;
};

export type DupeCheckSummary = {
  SourcePath: string;
  Results: DupeCheckResult[];
  Notes: string[];
};

export type DupeCheckTrackerState = {
  tracker: string;
  status: string;
  message: string;
  result: DupeCheckResult;
  startedAt: string;
  finishedAt: string;
};

export type DupeCheckSnapshot = {
  jobID: string;
  sourcePath: string;
  status: string;
  trackers: DupeCheckTrackerState[];
  completedCount: number;
  totalCount: number;
  summary: DupeCheckSummary;
  error: string;
  startedAt: string;
  finishedAt: string;
};

export type MetadataPreview = {
  SourcePath: string;
  TrackerName: string;
  ReleaseName: string;
  Warnings: string[];
  ReleaseNameOverrides: ReleaseNameOverrides;
  ExternalIDs: ExternalIDs;
  ExternalIDCandidates: ExternalIDCandidates;
  ExternalIDInfo: ExternalIDInfo[];
  ExternalPreview: ExternalPreview[];
  Bluray?: BlurayMetadata;
  TrackerData: TrackerPreview[];
  /** Preview-time upload rule failures keyed by normalized tracker code. */
  TrackerRuleFailures?: Record<string, RuleFailure[]>;
};

export type MetadataProgressUpdate = {
  path: string;
  phase: string;
  message: string;
  status: string;
  level: string;
  timestamp: string;
};

export type PreparationDescription = {
  GroupKey: string;
  Trackers: string[];
  RawDescription: string;
  RawDescriptionHTML: string;
  Description: string;
  DescriptionHTML: string;
  HasOverride: boolean;
  ImageHost: ImageHostFeedback;
};

export type PreparationPreview = {
  SourcePath: string;
  Descriptions: PreparationDescription[];
};

export type ImageHostFeedback = {
  Status: string;
  SelectedHost: string;
  AllowedHosts: string[];
  Warnings?: ImageHostWarning[];
  Reuploaded: boolean;
  Message: string;
};

export type ImageHostWarning = {
  Host: string;
  Message: string;
};

export type DescriptionBuilderGroup = {
  GroupKey: string;
  Trackers: string[];
  Description: string;
  DescriptionHTML: string;
  RawDescription: string;
  RawDescriptionHTML: string;
  HasOverride: boolean;
  ImageHost: ImageHostFeedback;
};

export type DescriptionBuilderPreview = {
  SourcePath: string;
  Groups: DescriptionBuilderGroup[];
};

export type ScreenshotPurpose = "preview" | "final";

export type ScreenshotSelection = {
  Index: number;
  TimestampSeconds: number;
  Frame: number;
  Source: string;
};

export type ScreenshotImage = {
  Index: number;
  TimestampSeconds: number;
  Path: string;
  Width: number;
  Height: number;
  SizeBytes: number;
  Host?: string;
  ImgURL?: string;
  RawURL?: string;
  WebURL?: string;
  UploadedAt?: string;
};

export type ScreenshotError = {
  Index: number;
  Message: string;
};

export type ScreenshotResult = {
  SourcePath: string;
  Purpose: ScreenshotPurpose;
  Images: ScreenshotImage[];
  Tonemapped: boolean;
  UsedLibplacebo: boolean;
  Errors: ScreenshotError[];
};

export type ScreenshotPlan = {
  SourcePath: string;
  DiscType: string;
  DurationSeconds: number;
  FrameRate: number;
  SuggestedSelections: ScreenshotSelection[];
  ExistingScreenshots: ScreenshotImage[];
  ExistingTrackerScreenshots: ScreenshotImage[];
  FinalSelections: ScreenshotImage[];
  TrackerImageLinks: ScreenshotLinkedImage[];
  PreviewImages: ScreenshotImage[];
  MetadataTimestamp: string;
  RequiresManualFrames: boolean;
};

export type ScreenshotLinkedImage = {
  Tracker: string;
  URL: string;
  Path: string;
  Host?: string;
};

export type UploadedImageLink = {
  SourcePath: string;
  ImagePath: string;
  Host: string;
  UsageScope: string;
  ImgURL: string;
  RawURL: string;
  WebURL: string;
  SizeBytes: number;
  UploadedAt: string;
};

export type UploadImageHostFailure = {
  Host: string;
  UsageScope: string;
  Trackers: string[];
  Message: string;
};

export type UploadImagesResult = {
  Links: UploadedImageLink[];
  Failures: UploadImageHostFailure[];
};

export type HistoryEntry = {
  SourcePath: string;
  ReleaseTitle: string;
  ReleaseSource: string;
  ReleaseResolution: string;
  MetadataUpdatedAt: string;
  LatestUploadStatus: string;
  LatestUploadAt: string;
  RuleFailureCount: number;
};

export type HistoryUploadRecord = {
  Tracker: string;
  Status: string;
  CreatedAt: string;
  SourcePath: string;
};

export type HistoryTrackerMetadata = {
  SourcePath: string;
  Tracker: string;
  TrackerID: string;
  InfoHash: string;
  TMDBID: number;
  IMDBID: number;
  TVDBID: number;
  MALID: number;
  Category: string;
  Description: string;
  ImageURLs: string[];
  Filename: string;
  Matched: boolean;
  UpdatedAt: string;
};

export type HistoryRuleFailure = {
  SourcePath: string;
  Tracker: string;
  Rule: string;
  Reason: string;
  CreatedAt: string;
};

export type HistoryOverview = {
  SourcePath: string;
  ReleaseTitle: string;
  ReleaseSource: string;
  ReleaseResolution: string;
  MetadataUpdatedAt: string;
  LatestUploadStatus: string;
  LatestUploadAt: string;
  StatusLabel: string;
  Metadata: Record<string, unknown>;
  ExternalIDs: ExternalIDs;
  ExternalMetadata: Record<string, unknown>;
  ReleaseNameOverrides: ReleaseNameOverrides;
  DescriptionOverride: {
    SourcePath: string;
    GroupKey: string;
    Description: string;
    UpdatedAt: string;
  };
  DescriptionOverrides: Array<{
    SourcePath: string;
    GroupKey: string;
    Description: string;
    UpdatedAt: string;
  }>;
  PlaylistSelection: {
    SourcePath: string;
    SelectedPlaylists: string[];
    UseAll: boolean;
    UpdatedAt: string;
  };
  TrackerMetadata: HistoryTrackerMetadata[];
  TrackerRuleFailures: HistoryRuleFailure[];
  Screenshots: Array<{
    SourcePath: string;
    ImagePath: string;
    Purpose: string;
    CapturedAt: string;
  }>;
  FinalSelections: Array<{
    SourcePath: string;
    ImagePath: string;
    Order: number;
    Source: string;
    SelectedAt: string;
  }>;
  UploadedImages: UploadedImageLink[];
  UploadHistory: HistoryUploadRecord[];
};

export type ScreenshotPreviewImage = {
  image: ScreenshotImage;
  dataUri: string;
};

export type TrackerUploadItem = {
  name: string;
  config: ConfigMap;
};

export type TrackerUploadTrackerState = {
  tracker: string;
  status: string;
  task: string;
  taskStatus: string;
  message: string;
  completedPieces: number;
  totalPieces: number;
  percent: number;
  hashRateMiB: number;
  uploadedCount: number;
  startedAt: string;
  finishedAt: string;
};

export type UploadProgressUpdate = {
  sourcePath: string;
  tracker: string;
  task: string;
  status: string;
  message: string;
  completedPieces: number;
  totalPieces: number;
  percent: number;
  hashRateMiB: number;
  timestamp: string;
};

export type TrackerUploadSnapshot = {
  jobID: string;
  sourcePath: string;
  status: string;
  currentTask: string;
  currentTaskStatus: string;
  currentMessage: string;
  currentCompletedPieces: number;
  currentTotalPieces: number;
  currentPercent: number;
  currentHashRateMiB: number;
  trackers: TrackerUploadTrackerState[];
  failedTrackers: string[];
  uploadedCount: number;
  error: string;
  startedAt: string;
  finishedAt: string;
};

export type TrackerDryRunFile = {
  Field: string;
  Path: string;
  Present: boolean;
};

export type TrackerQuestionnaireField = {
  Key: string;
  Label: string;
  Kind: string;
  Options?: string[];
  Value: string;
  Placeholder: string;
  Help: string;
  Required: boolean;
};

export type TrackerQuestionnaire = {
  Tracker: string;
  Fields: TrackerQuestionnaireField[];
};

export type TrackerDryRunEntry = {
  Tracker: string;
  Status: string;
  Message: string;
  ReleaseName: string;
  OriginalReleaseName: string;
  UploadReleaseName: string;
  ReleaseNameChanged: boolean;
  ReleaseNameChangeReason: string;
  DescriptionGroup: string;
  Description: string;
  Endpoint: string;
  Payload: Record<string, string>;
  Files: TrackerDryRunFile[];
  /** Optional staged diagnostics for trackers that expose intermediate dry-run payloads. */
  DebugSections?: TrackerDryRunDebugSection[] | null;
  Questionnaire?: TrackerQuestionnaire | null;
  ImageHost: ImageHostFeedback;
};

/** One named diagnostic payload rendered inside a tracker dry-run preview. */
export type TrackerDryRunDebugSection = {
  Title: string;
  Endpoint: string;
  Payload: Record<string, string>;
  Files: TrackerDryRunFile[];
};

export type TrackerDryRunPreview = {
  SourcePath: string;
  Trackers: TrackerDryRunEntry[];
};

export type ConfigValue = string | number | boolean | null | ConfigMap | ConfigValue[];
export type ConfigMap = { [key: string]: ConfigValue };
export type ImageHostPolicyMetadata = {
  UploadHosts?: string[];
  TrackerUploadHosts?: Record<string, string[]>;
  OwnedHosts?: Record<string, string>;
};
export type FieldType = "string" | "number" | "boolean";
export type FieldMeta = {
  key: string;
  label?: string;
  type?: FieldType;
  advanced?: boolean;
  sensitive?: boolean;
  options?: Array<{ value: string; label: string }>;
};

export type ReleaseNameEditState = {
  category: string;
  type: string;
  source: string;
  resolution: string;
  tag: string;
  service: string;
  edition: string;
  season: string;
  episode: string;
  episodeTitle: string;
  manualYear: string;
  manualDate: string;
  useSeasonEpisode: boolean;
  noSeason: boolean;
  noYear: boolean;
  noAKA: boolean;
  noTag: boolean;
  noEdition: boolean;
  noDub: boolean;
  noDual: boolean;
  dualAudio: boolean;
  region: string;
};

export type ReleaseNameTouchedState = {
  category: boolean;
  type: boolean;
  source: boolean;
  resolution: boolean;
  tag: boolean;
  service: boolean;
  edition: boolean;
  season: boolean;
  episode: boolean;
  episodeTitle: boolean;
  manualYear: boolean;
  manualDate: boolean;
  useSeasonEpisode: boolean;
  noSeason: boolean;
  noYear: boolean;
  noAKA: boolean;
  noTag: boolean;
  noEdition: boolean;
  noDub: boolean;
  noDual: boolean;
  dualAudio: boolean;
  region: boolean;
};

export type ReleaseNameIDEditState = {
  tmdb: string;
  imdb: string;
  tvdb: string;
  tvmaze: string;
};

export type DetailItem = {
  label: string;
  value: string;
  mono?: boolean;
  blocks?: DetailBlock[];
};

export type DetailBlock = {
  text?: string;
  imageUrl?: string;
  imageAlt?: string;
};

export type PlaylistItem = {
  file: string;
  size: number;
};

export type PlaylistInfo = {
  file: string;
  duration: number;
  items: PlaylistItem[];
  score: number;
  edition: string;
};

export type PlaylistSelection = {
  SourcePath: string;
  SelectedPlaylists: string[];
  UseAll: boolean;
  UpdatedAt: string;
};

export type BrowseDirectoryEntry = {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  modifiedAt: string;
};

export type BrowseDirectoryResponse = {
  currentPath: string;
  parentPath: string;
  mode: "file" | "folder";
  entries: BrowseDirectoryEntry[];
};

export type BrowseDirectoryRequest = {
  path: string;
  mode: "file" | "folder";
};
