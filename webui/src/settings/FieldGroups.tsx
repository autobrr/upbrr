// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import { cn } from "../utils/cn";
import { settingsStyle } from "./style";

const fieldGroups: Record<string, Array<{ title: string; keys: string[] }>> = {
  MainSettings: [
    { title: "Notifications", keys: ["UpdateNotification", "VerboseNotification"] },
    {
      title: "Metadata and tracker checks",
      keys: ["TMDBAPI", "SceneDetection", "TrackerPassChecks"],
    },
    {
      title: "Interface and input history",
      keys: ["UseFavicons", "FaviconOnly", "InputHistoryLimit"],
    },
    { title: "Storage", keys: ["DBPath"] },
  ],
  Metadata: [
    {
      title: "Provider lookups",
      keys: [
        "BTNAPI",
        "OnlyID",
        "PingUnit3D",
        "SkipAutoTorrent",
        "SkipTrackerFilenameLookup",
        "CheckPredb",
      ],
    },
    {
      title: "Blu-ray matching",
      keys: ["GetBlurayInfo", "UseLargestPlaylist", "BlurayScore", "BluraySingleScore"],
    },
    { title: "Overrides and retained images", keys: ["UserOverrides", "KeepImages"] },
  ],
  ScreenshotHandling: [
    { title: "Capture targets", keys: ["Screens", "MaxMenuItems", "CutoffScreens"] },
    { title: "Image uploads", keys: ["MinSuccessfulUploads", "MaxConcurrentUploads"] },
    { title: "Overlays", keys: ["FrameOverlay", "OverlayTextSize"] },
    { title: "Tone mapping", keys: ["ToneMap", "UseLibplacebo", "TonemapAlgorithm", "Desat"] },
    {
      title: "Processing and compression",
      keys: ["ProcessLimit", "FFmpegLimit", "FFmpegCompression"],
    },
  ],
  Description: [
    {
      title: "Content",
      keys: [
        "AddLogo",
        "EpisodeOverview",
        "AddBlurayLink",
        "UseBlurayImages",
        "LogoSize",
        "LogoLanguage",
        "BlurayImageSize",
      ],
    },
    {
      title: "Screenshot layout",
      keys: ["ThumbnailSize", "ScreensPerRow", "MultiScreens", "PackThumbSize"],
    },
    {
      title: "Custom headers",
      keys: [
        "TonemappedHeader",
        "CustomDescriptionHeader",
        "ScreenshotHeader",
        "DiscMenuHeader",
        "CustomSignature",
      ],
    },
    { title: "Limits and processing", keys: ["CharLimit", "FileLimit", "ProcessLimit"] },
  ],
  ArrIntegration: [
    {
      title: "Sonarr",
      keys: [
        "UseSonarr",
        "SonarrURL",
        "SonarrAPIKey",
        "SonarrURL1",
        "SonarrAPIKey1",
        "SonarrURL2",
        "SonarrAPIKey2",
        "SonarrURL3",
        "SonarrAPIKey3",
      ],
    },
    {
      title: "Radarr",
      keys: [
        "UseRadarr",
        "RadarrURL",
        "RadarrAPIKey",
        "RadarrURL1",
        "RadarrAPIKey1",
        "RadarrURL2",
        "RadarrAPIKey2",
        "RadarrURL3",
        "RadarrAPIKey3",
      ],
    },
    { title: "Media paths", keys: ["EmbyDir", "EmbyTVDir"] },
  ],
  PostUpload: [
    { title: "Tracker handling", keys: ["MaxConcurrentTrackers", "SearchRequests"] },
    {
      title: "Upload output",
      keys: ["ShowUploadDuration", "PrintTrackerMessages", "PrintTrackerLinks"],
    },
    { title: "Cross-seeding", keys: ["CrossSeeding", "CrossSeedCheckEverything"] },
    { title: "Client injection", keys: ["InjectDelay"] },
  ],
  ClientSetup: [
    { title: "Default client", keys: ["DefaultClient"] },
    { title: "Client selection", keys: ["InjectClients", "SearchClients"] },
  ],
  TorrentCreation: [
    { title: "Torrent construction", keys: ["MkbrrThreads", "PreferMax16"] },
    { title: "Rehash scheduling", keys: ["RehashCooldown"] },
  ],
  Trackers: [
    {
      title: "Connection and credentials",
      keys: [
        "APIKey",
        "ApiKey",
        "ApiUser",
        "Username",
        "Password",
        "Passkey",
        "AnnounceURL",
        "MyAnnounceURL",
        "BhdRSSKey",
        "OTPURI",
        "LoginQuestion",
        "LoginAnswer",
        "UserID",
      ],
    },
    {
      title: "Upload preferences",
      keys: [
        "Anon",
        "ShowGroupIfAnon",
        "UploaderName",
        "UploaderStatus",
        "CheckForRules",
        "ModQ",
        "Draft",
        "DraftDefault",
        "CheckRequests",
        "APIUpload",
        "Exclusive",
        "Channel",
      ],
    },
    {
      title: "Images and descriptions",
      keys: [
        "ImageHost",
        "ImgRehost",
        "ImageCount",
        "ImgAPI",
        "FullMediainfo",
        "CustomLayout",
        "TagForCustomRelease",
        "UseSpanishTitle",
        "UseItalianTitle",
        "PTGenAPI",
        "AddWebSourceToDesc",
        "UseMetadataName",
        "PronfoAPIKey",
        "PronfoTheme",
        "PronfoRAPIID",
        "FaviconURL",
      ],
    },
    {
      title: "Release groups",
      keys: ["DupeBypassGroups", "PersonalReleaseGroups", "InternalGroups"],
    },
    {
      title: "Client handling",
      keys: ["TorrentClient", "LinkDirName", "SkipIfRehash", "InjectDelay"],
    },
  ],
  TorrentClients: [
    {
      title: "Connection",
      keys: [
        "Type",
        "QuiProxyURL",
        "QbitDirect",
        "QbitURL",
        "QbitPort",
        "QbitUser",
        "QbitPass",
        "VerifyWebUICertificate",
      ],
    },
    { title: "Folders", keys: ["WatchFolder", "StorageDir"] },
    {
      title: "Labels and tags",
      keys: [
        "QbitCategoryValue",
        "QbitTag",
        "QbitCrossCategory",
        "QbitCrossTag",
        "UseTrackerAsTag",
      ],
    },
    {
      title: "Linking and path mapping",
      keys: [
        "Linking",
        "AllowFallback",
        "LinkedFolder",
        "LocalPath",
        "RemotePath",
        "AutomaticManagementPaths",
      ],
    },
  ],
  Logging: [
    { title: "Output", keys: ["Level", "FileEnabled"] },
    { title: "Log retention", keys: ["MaxTotalSizeMB", "MaxFiles"] },
  ],
};

/** Groups rendered controls without changing their values, handlers, or visibility. */
export function SettingsFieldGroups({
  section,
  fields,
}: Readonly<{
  section: string;
  fields: ReadonlyArray<readonly [string, ReactNode]>;
}>) {
  const definitions = fieldGroups[section] ?? [];
  const visibleFields = fields.filter(([, field]) => field != null && field !== false);
  const groups = definitions.map(({ title, keys }) => ({
    title,
    fields: keys.flatMap((key) => visibleFields.filter(([fieldKey]) => fieldKey === key)),
  }));
  const otherFields = visibleFields.filter(
    ([key]) => !definitions.some((group) => group.keys.includes(key)),
  );
  if (otherFields.length) groups.push({ title: "Other settings", fields: otherFields });
  return (
    <div className={settingsStyle.form}>
      {groups
        .filter((group) => group.fields.length > 0)
        .map(({ title, fields }) => (
          <section className={settingsStyle.subgroup} key={title} aria-label={title}>
            <h3 className="m-0 text-sm font-semibold">{title}</h3>
            <div
              className={cn(
                settingsStyle.grid,
                ((section === "Description" && title === "Content") ||
                  section === "ArrIntegration") &&
                  "[&>label:has([role=switch])]:col-span-full [&>label:has([role=switch])]:border-b [&>label:has([role=switch])]:border-foreground/10",
              )}
            >
              {fields.map(([, field]) => field)}
            </div>
          </section>
        ))}
    </div>
  );
}
