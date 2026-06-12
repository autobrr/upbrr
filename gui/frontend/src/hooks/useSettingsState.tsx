// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useMemo, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { Button } from "../components/ui/button";
import { PillCheckbox } from "../components/ui/checkbox";
import { Switch } from "../components/ui/switch";
import type { ConfigMap, ConfigValue, FieldMeta, ImageHostPolicyMetadata } from "../types";
import { formatLabel, normalizeDefaultTrackerList } from "../utils/settings";

type SettingsSection = { key: string; jsonKey: string; label: string };

const settingsInputClass =
  "h-8 rounded-md border border-white/10 bg-slate-950/45 px-2.5 text-sm text-[var(--text)] outline-none transition placeholder:text-[var(--muted)] focus:border-[var(--accent-2)] focus:ring-2 focus:ring-[rgba(53,194,193,0.18)]";

const settingsSelectClass = `${settingsInputClass} cursor-pointer`;

type UseSettingsStateOptions = {
  activeTab: string;
};

type UseSettingsStateResult = {
  configData: ConfigMap | null;
  settingsLoading: boolean;
  settingsDirty: boolean;
  settingsSaved: string;
  settingsError: string;
  settingsSection: string;
  settingsSections: SettingsSection[];
  showAdvancedToggle: boolean;
  advancedOpen: boolean;
  setSettingsSection: Dispatch<SetStateAction<string>>;
  setSettingsAdvanced: Dispatch<SetStateAction<Record<string, boolean>>>;
  loadSettings: () => void;
  handleSaveSettings: () => void;
  renderImageHostingSection: () => JSX.Element | null;
  renderTrackerSection: (advancedOpen: boolean) => JSX.Element | null;
  renderMapSection: (
    sectionKey: string,
    sectionValue: ConfigMap,
    options?: {
      entriesKey?: string;
      defaultKey?: string;
      fieldMeta?: Record<string, FieldMeta>;
      advancedOpen?: boolean;
    },
  ) => JSX.Element;
  renderField: (label: string, value: ConfigValue, path: string[], meta?: FieldMeta) => JSX.Element;
  sectionFieldMeta: Record<string, Record<string, FieldMeta>>;
  updateConfigValue: (path: string[], value: ConfigValue) => void;
  updateScreenshotConfigValue: (key: string, value: ConfigValue) => void;
  configuredImageHosts: string[];
  screenshotConfig: ConfigMap | null;
  buildSavePayload: () => string | null;
  clearSettingsStatus: () => void;
  markSettingsSaved: (message: string) => void;
  setSettingsSavedMessage: (message: string) => void;
  setSettingsErrorMessage: (message: string) => void;
  resolveImageHostLabel: (value: string) => string;
  knownTrackersLoading: boolean;
  trackerSelectionNames: string[];
};

const settingsSections: SettingsSection[] = [
  { key: "main_settings", jsonKey: "MainSettings", label: "Main" },
  { key: "image_hosting", jsonKey: "ImageHosting", label: "Image Hosting" },
  { key: "metadata", jsonKey: "Metadata", label: "Metadata" },
  { key: "screenshot_handling", jsonKey: "ScreenshotHandling", label: "Screens" },
  { key: "description_settings", jsonKey: "Description", label: "Description" },
  { key: "client_setup", jsonKey: "ClientSetup", label: "Clients" },
  { key: "arr_integration", jsonKey: "ArrIntegration", label: "Arr" },
  { key: "torrent_creation", jsonKey: "TorrentCreation", label: "Torrent" },
  { key: "post_upload", jsonKey: "PostUpload", label: "Post Upload" },
  { key: "trackers", jsonKey: "Trackers", label: "Trackers" },
  { key: "torrent_clients", jsonKey: "TorrentClients", label: "Torrent Clients" },
];

const imageHostOptions = [
  { value: "", label: "None" },
  { value: "imgbb", label: "ImgBB" },
  { value: "imgbox", label: "ImgBox" },
  { value: "pixhost", label: "Pixhost" },
  { value: "lensdump", label: "Lensdump" },
  { value: "ptscreens", label: "PTScreens" },
  { value: "onlyimage", label: "OnlyImage" },
  { value: "dalexni", label: "Dalexni" },
  { value: "zipline", label: "Zipline" },
  { value: "passtheimage", label: "PassTheImage" },
  { value: "seedpool_cdn", label: "Seedpool CDN" },
  { value: "sharex", label: "ShareX" },
  { value: "utppm", label: "UTPPM" },
];

const trackerImageHostOptions = [...imageHostOptions, { value: "hdb", label: "HDB" }];
const torrentClientLinkingOptions = [
  { value: "", label: "None" },
  { value: "hardlink", label: "Hardlink" },
  { value: "symlink", label: "Symlink" },
];
const imageHostOptionLabels = new Map(
  trackerImageHostOptions.map((option) => [option.value, option.label]),
);
const defaultOwnedImageHosts: Record<string, string> = { hdb: "HDB" };
const normalizeImageHostValue = (value: string) => value.trim().toLowerCase();
const imageHostOptionFor = (host: string) => {
  const value = normalizeImageHostValue(host);
  return { value, label: imageHostOptionLabels.get(value) ?? value };
};

const imageHostKeyMap: Record<string, string[]> = {
  imgbb: ["ImgBBAPI"],
  lensdump: ["LensdumpAPI"],
  ptscreens: ["PTScreensAPI"],
  onlyimage: ["OnlyImageAPI"],
  dalexni: ["DalexniAPI"],
  zipline: ["ZiplineURL", "ZiplineAPIKey"],
  passtheimage: ["PassTheImageAPI"],
  seedpool_cdn: ["SeedpoolCDNAPI"],
  sharex: ["ShareXURL", "ShareXAPIKey"],
  utppm: ["UTPPMAPI"],
};

const stringField = (key: string, meta: Omit<FieldMeta, "key" | "type"> = {}): FieldMeta => ({
  key,
  type: "string",
  ...meta,
});
const boolField = (key: string, meta: Omit<FieldMeta, "key" | "type"> = {}): FieldMeta => ({
  key,
  type: "boolean",
  ...meta,
});
const numberField = (key: string, meta: Omit<FieldMeta, "key" | "type"> = {}): FieldMeta => ({
  key,
  type: "number",
  ...meta,
});

const trackerFieldMeta: Record<string, FieldMeta> = {
  LinkDirName: stringField("LinkDirName", { label: "Link dir name", advanced: true }),
  APIKey: stringField("APIKey", { label: "API key", sensitive: true }),
  ApiKey: stringField("ApiKey", { label: "API key", sensitive: true }),
  ApiUser: stringField("ApiUser", { label: "API user", sensitive: true }),
  Username: stringField("Username", { label: "Username" }),
  Password: stringField("Password", { label: "Password", sensitive: true }),
  Passkey: stringField("Passkey", { label: "Passkey", sensitive: true }),
  AnnounceURL: stringField("AnnounceURL", {
    label: "Announce URL",
    sensitive: true,
    advanced: true,
  }),
  MyAnnounceURL: stringField("MyAnnounceURL", {
    label: "My announce URL",
    sensitive: true,
    advanced: true,
  }),
  URL: stringField("URL", { label: "URL", advanced: true }),
  UploaderName: stringField("UploaderName", { label: "Uploader name" }),
  UploaderStatus: boolField("UploaderStatus", { label: "Uploader status", advanced: true }),
  CustomLayout: stringField("CustomLayout", { label: "Custom layout", advanced: true }),
  TagForCustomRelease: stringField("TagForCustomRelease", { label: "Tag for custom release" }),
  CheckForRules: boolField("CheckForRules", { label: "Check for rules", advanced: true }),
  ModQ: boolField("ModQ", { label: "Mod queue", advanced: true }),
  Draft: boolField("Draft", { label: "Draft", advanced: true }),
  DraftDefault: boolField("DraftDefault", { label: "Draft default", advanced: true }),
  Anon: boolField("Anon", { label: "Anonymous" }),
  ShowGroupIfAnon: boolField("ShowGroupIfAnon", { label: "Show group if anon", advanced: true }),
  BhdRSSKey: stringField("BhdRSSKey", { label: "BHD RSS key", sensitive: true, advanced: true }),
  CheckRequests: boolField("CheckRequests", { label: "Check requests", advanced: true }),
  FullMediainfo: boolField("FullMediainfo", { label: "Full mediainfo", advanced: true }),
  ImgRehost: boolField("ImgRehost", { label: "Image rehost", advanced: true }),
  ImageHost: stringField("ImageHost", {
    label: "Image host",
    options: imageHostOptions,
  }),
  UseSpanishTitle: boolField("UseSpanishTitle", { label: "Use Spanish title", advanced: true }),
  UseItalianTitle: boolField("UseItalianTitle", { label: "Use Italian title", advanced: true }),
  OTPURI: stringField("OTPURI", { label: "OTP URI", sensitive: true, advanced: true }),
  SkipIfRehash: boolField("SkipIfRehash", { label: "Skip if rehash", advanced: true }),
  PreferMTV: boolField("PreferMTV", { label: "Prefer MTV torrent", advanced: true }),
  PTGenAPI: stringField("PTGenAPI", { label: "PTGen API", sensitive: true, advanced: true }),
  AddWebSourceToDesc: boolField("AddWebSourceToDesc", {
    label: "Add web source to desc",
    advanced: true,
  }),
  ImageCount: numberField("ImageCount", { label: "Image count", advanced: true }),
  Channel: stringField("Channel", { label: "Channel", advanced: true }),
  ImgAPI: stringField("ImgAPI", { label: "Image API", sensitive: true, advanced: true }),
  PronfoAPIKey: stringField("PronfoAPIKey", {
    label: "Pronfo API key",
    sensitive: true,
    advanced: true,
  }),
  PronfoTheme: stringField("PronfoTheme", { label: "Pronfo theme", advanced: true }),
  PronfoRAPIID: stringField("PronfoRAPIID", { label: "Pronfo RAPI ID", advanced: true }),
  APIUpload: boolField("APIUpload", { label: "API upload", advanced: true }),
  Exclusive: boolField("Exclusive", { label: "Exclusive", advanced: true }),
  LoginQuestion: stringField("LoginQuestion", {
    label: "Login question",
    sensitive: true,
    advanced: true,
  }),
  LoginAnswer: stringField("LoginAnswer", {
    label: "Login answer",
    sensitive: true,
    advanced: true,
  }),
  UserID: stringField("UserID", { label: "User ID", sensitive: true, advanced: true }),
  Filebrowser: stringField("Filebrowser", { label: "Filebrowser", advanced: true }),
};

const trackerSchemas: Record<string, FieldMeta[]> = {
  A4K: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  ACM: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  AITHER: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  ANT: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  AR: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.AnnounceURL,
  ],
  ASC: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.UploaderStatus,
    trackerFieldMeta.CustomLayout,
    trackerFieldMeta.AnnounceURL,
  ],
  AZ: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.CheckForRules,
  ],
  BHD: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.BhdRSSKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.DraftDefault,
    trackerFieldMeta.Anon,
  ],
  BHDTV: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.MyAnnounceURL,
    trackerFieldMeta.Anon,
  ],
  BJS: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ShowGroupIfAnon,
  ],
  BLU: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  BT: [trackerFieldMeta.LinkDirName, trackerFieldMeta.AnnounceURL, trackerFieldMeta.Anon],
  BTN: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.URL,
    trackerFieldMeta.OTPURI,
  ],
  CBR: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
    trackerFieldMeta.TagForCustomRelease,
  ],
  CZ: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.CheckForRules,
  ],
  DC: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  DP: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  EMUW: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.UseSpanishTitle,
  ],
  FF: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.CheckRequests,
    trackerFieldMeta.FullMediainfo,
  ],
  FL: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Passkey,
    trackerFieldMeta.UploaderName,
    trackerFieldMeta.Anon,
  ],
  FRIKI: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey],
  GPW: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.AnnounceURL],
  HDB: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Passkey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.ImgRehost,
  ],
  HDS: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.FullMediainfo,
  ],
  HDT: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.URL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.FullMediainfo,
  ],
  HHD: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  IHD: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  IS: [trackerFieldMeta.LinkDirName, trackerFieldMeta.AnnounceURL, trackerFieldMeta.Anon],
  ITT: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  LCD: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  LDU: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  LST: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
    trackerFieldMeta.Draft,
  ],
  LT: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  LUME: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  MTV: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.OTPURI,
    trackerFieldMeta.SkipIfRehash,
    trackerFieldMeta.PreferMTV,
  ],
  NBL: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.AnnounceURL],
  OE: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  OTW: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.ModQ,
    trackerFieldMeta.Anon,
  ],
  PHD: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.CheckForRules,
  ],
  PT: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  PTP: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AddWebSourceToDesc,
    trackerFieldMeta.ApiUser,
    trackerFieldMeta.ApiKey,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.AnnounceURL,
  ],
  PTS: [trackerFieldMeta.LinkDirName, trackerFieldMeta.AnnounceURL],
  PTT: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  R4E: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  RAS: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  RF: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  RTF: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  SAM: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.TagForCustomRelease,
  ],
  SHRI: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.UseItalianTitle,
  ],
  SP: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey],
  SPD: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Channel],
  STC: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  THR: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.ImgAPI,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.PronfoAPIKey,
    trackerFieldMeta.PronfoTheme,
    trackerFieldMeta.PronfoRAPIID,
    trackerFieldMeta.Anon,
  ],
  TIK: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  TL: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIUpload,
    trackerFieldMeta.Passkey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ImgRehost,
    trackerFieldMeta.FullMediainfo,
  ],
  TLZ: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  TOS: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.Exclusive,
  ],
  TTR: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  TVC: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.ImageCount,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  ULCX: [
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  UTP: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  YUS: [trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey, trackerFieldMeta.Anon],
  MANUAL: [trackerFieldMeta.Filebrowser],
};

const trackerHasAdvancedFields = Object.values(trackerSchemas).some((fields) =>
  fields.some((field) => field.advanced),
);

const REDACTED_VALUE = "[REDACTED]";
const sensitiveKeyHints = [
  "password",
  "passkey",
  "token",
  "api",
  "key",
  "secret",
  "cookie",
  "session",
  "otp",
  "announce_url",
  "announceurl",
];

const sectionFieldMeta: Record<string, Record<string, FieldMeta>> = {
  MainSettings: {
    TrackerPassChecks: { key: "TrackerPassChecks", advanced: true },
    InputHistoryLimit: { key: "InputHistoryLimit", label: "Input history limit", type: "number" },
    DBPath: { key: "DBPath", advanced: true },
  },
  Metadata: {
    SkipTrackerFilenameLookup: { key: "SkipTrackerFilenameLookup", advanced: true },
    UserOverrides: { key: "UserOverrides", advanced: true },
    PingUnit3D: { key: "PingUnit3D", advanced: true },
    GetBlurayInfo: { key: "GetBlurayInfo", advanced: true },
    BlurayScore: { key: "BlurayScore", advanced: true },
    BluraySingleScore: { key: "BluraySingleScore", advanced: true },
    CheckPredb: { key: "CheckPredb", advanced: true },
  },
  ScreenshotHandling: {
    ProcessLimit: { key: "ProcessLimit", advanced: true },
    MaxConcurrentUploads: { key: "MaxConcurrentUploads", advanced: true },
    FFmpegLimit: { key: "FFmpegLimit", advanced: true },
    UseLibplacebo: { key: "UseLibplacebo", advanced: true },
    FFmpegCompression: { key: "FFmpegCompression", advanced: true },
    TonemapAlgorithm: { key: "TonemapAlgorithm", advanced: true },
    Desat: { key: "Desat", advanced: true },
  },
  Description: {
    TonemappedHeader: { key: "TonemappedHeader", advanced: true },
    MultiScreens: { key: "MultiScreens", advanced: true },
    PackThumbSize: { key: "PackThumbSize", advanced: true },
    CharLimit: { key: "CharLimit", advanced: true },
    FileLimit: { key: "FileLimit", advanced: true },
    ProcessLimit: { key: "ProcessLimit", advanced: true },
    CustomDescriptionHeader: { key: "CustomDescriptionHeader", advanced: true },
    ScreenshotHeader: { key: "ScreenshotHeader", advanced: true },
    DiscMenuHeader: { key: "DiscMenuHeader", advanced: true },
    CustomSignature: { key: "CustomSignature", advanced: true },
    BlurayImageSize: { key: "BlurayImageSize", advanced: true },
  },
  ArrIntegration: {
    SonarrURL1: { key: "SonarrURL1", advanced: true },
    SonarrAPIKey1: { key: "SonarrAPIKey1", advanced: true, sensitive: true },
    SonarrURL2: { key: "SonarrURL2", advanced: true },
    SonarrAPIKey2: { key: "SonarrAPIKey2", advanced: true, sensitive: true },
    SonarrURL3: { key: "SonarrURL3", advanced: true },
    SonarrAPIKey3: { key: "SonarrAPIKey3", advanced: true, sensitive: true },
    RadarrURL1: { key: "RadarrURL1", advanced: true },
    RadarrAPIKey1: { key: "RadarrAPIKey1", advanced: true, sensitive: true },
    RadarrURL2: { key: "RadarrURL2", advanced: true },
    RadarrAPIKey2: { key: "RadarrAPIKey2", advanced: true, sensitive: true },
    RadarrURL3: { key: "RadarrURL3", advanced: true },
    RadarrAPIKey3: { key: "RadarrAPIKey3", advanced: true, sensitive: true },
    EmbyDir: { key: "EmbyDir", advanced: true },
    EmbyTVDir: { key: "EmbyTVDir", advanced: true },
  },
  TorrentCreation: {
    MkbrrThreads: { key: "MkbrrThreads", advanced: true },
    PreferMax16: { key: "PreferMax16", advanced: true },
    RehashCooldown: { key: "RehashCooldown", advanced: true },
  },
  PostUpload: {
    PrintTrackerMessages: { key: "PrintTrackerMessages", advanced: true },
    PrintTrackerLinks: { key: "PrintTrackerLinks", advanced: true },
    SearchRequests: { key: "SearchRequests", advanced: true },
    CrossSeedCheckEverything: { key: "CrossSeedCheckEverything", advanced: true },
  },
  Logging: {
    MaxTotalSizeMB: { key: "MaxTotalSizeMB", advanced: true },
    MaxFiles: { key: "MaxFiles", advanced: true },
  },
  TorrentClients: {
    Linking: stringField("Linking", {
      label: "Linking",
      options: torrentClientLinkingOptions,
    }),
  },
};

const isSensitiveKeyName = (key: string) => {
  const lower = key.toLowerCase();
  return sensitiveKeyHints.some((hint) => lower.includes(hint));
};

const buildPathKey = (path: string[]) => path.join(".");

const maskSensitiveConfig = (input: ConfigMap) => {
  const originals: Record<string, string> = {};
  const walk = (value: ConfigValue, path: string[]): ConfigValue => {
    if (value === null || value === undefined) return value;
    if (Array.isArray(value)) {
      return value.map((entry, index) => walk(entry, [...path, String(index)]));
    }
    if (typeof value === "object") {
      const next: ConfigMap = {};
      Object.entries(value).forEach(([key, child]) => {
        next[key] = walk(child, [...path, key]);
      });
      return next;
    }
    if (typeof value === "string") {
      const key = path[path.length - 1] || "";
      if (value && isSensitiveKeyName(key)) {
        originals[buildPathKey(path)] = value;
        return REDACTED_VALUE;
      }
      return value;
    }
    return value;
  };

  return { masked: walk(input, []) as ConfigMap, originals };
};

const restoreSensitiveConfig = (input: ConfigMap, originals: Record<string, string>) => {
  const walk = (value: ConfigValue, path: string[]): ConfigValue => {
    if (value === null || value === undefined) return value;
    if (Array.isArray(value)) {
      return value.map((entry, index) => walk(entry, [...path, String(index)]));
    }
    if (typeof value === "object") {
      const next: ConfigMap = {};
      Object.entries(value).forEach(([key, child]) => {
        next[key] = walk(child, [...path, key]);
      });
      return next;
    }
    if (typeof value === "string") {
      if (value === REDACTED_VALUE) {
        const original = originals[buildPathKey(path)];
        if (original !== undefined) {
          return original;
        }
      }
      return value;
    }
    return value;
  };

  return walk(input, []) as ConfigMap;
};

export const useSettingsState = (options: UseSettingsStateOptions): UseSettingsStateResult => {
  const { activeTab } = options;
  const [configData, setConfigData] = useState<ConfigMap | null>(null);
  const [defaultConfig, setDefaultConfig] = useState<ConfigMap | null>(null);
  const [knownTrackers, setKnownTrackers] = useState<string[]>([]);
  const [imageHostPolicyMetadata, setImageHostPolicyMetadata] =
    useState<ImageHostPolicyMetadata | null>(null);
  const [knownTrackersLoading, setKnownTrackersLoading] = useState(false);
  const [trackerAddSelection, setTrackerAddSelection] = useState("");
  const [manualTrackerEntries, setManualTrackerEntries] = useState<Record<string, boolean>>({});
  const [settingsTrackerPanels, setSettingsTrackerPanels] = useState<Record<string, boolean>>({});
  const [defaultTrackersPanelOpen, setDefaultTrackersPanelOpen] = useState(false);
  const [settingsLoading, setSettingsLoading] = useState(false);
  const [settingsError, setSettingsError] = useState("");
  const [settingsSaved, setSettingsSaved] = useState("");
  const [settingsDirty, setSettingsDirty] = useState(false);
  const [settingsSection, setSettingsSection] = useState(settingsSections[0].key);
  const [settingsAdvanced, setSettingsAdvanced] = useState<Record<string, boolean>>({});
  const [sensitiveValues, setSensitiveValues] = useState<Record<string, string>>({});

  const configuredImageHosts = useMemo(() => {
    if (!configData || !configData.ImageHosting || typeof configData.ImageHosting !== "object") {
      return [] as string[];
    }
    if (Array.isArray(configData.ImageHosting)) {
      return [] as string[];
    }
    const imageCfg = configData.ImageHosting as ConfigMap;
    const hostFields = ["Host1", "Host2", "Host3", "Host4", "Host5", "Host6"];
    const hosts: string[] = [];
    hostFields.forEach((field) => {
      const value = String(imageCfg[field] ?? "").trim();
      if (!value) return;
      if (!hosts.includes(value)) {
        hosts.push(value);
      }
    });
    return hosts;
  }, [configData]);

  const screenshotConfig = useMemo(() => {
    if (
      !configData ||
      !configData.ScreenshotHandling ||
      typeof configData.ScreenshotHandling !== "object"
    ) {
      return null;
    }
    if (Array.isArray(configData.ScreenshotHandling)) {
      return null;
    }
    return configData.ScreenshotHandling as ConfigMap;
  }, [configData]);

  const setSettingsErrorMessage = (message: string) => {
    setSettingsError(message);
  };

  const clearSettingsStatus = useCallback(() => {
    setSettingsError("");
    setSettingsSaved("");
  }, []);

  const markSettingsSaved = (message: string) => {
    setSettingsSaved(message);
    setSettingsDirty(false);
  };

  const setSettingsSavedMessage = (message: string) => {
    setSettingsSaved(message);
  };

  const buildSavePayload = () => {
    if (!configData) {
      return null;
    }
    const restored = restoreSensitiveConfig(configData, sensitiveValues);
    return JSON.stringify(restored, null, 2);
  };

  const resolveImageHostLabel = (value: string) => {
    const option = imageHostOptions.find((entry) => entry.value === value);
    return option ? option.label : value;
  };

  const buildImageHostOptions = useCallback((hosts: string[]) => {
    const allowed = new Set(
      hosts.map((host) => normalizeImageHostValue(host)).filter((host) => host.length > 0),
    );
    const ordered = imageHostOptions.filter(
      (option) => option.value === "" || allowed.has(option.value),
    );
    const known = new Set(ordered.map((option) => option.value));
    const extras = Array.from(allowed)
      .filter((host) => !known.has(host))
      .sort((left, right) => left.localeCompare(right))
      .map(imageHostOptionFor);
    return [...ordered, ...extras];
  }, []);

  const trackerOptionsForImageHost = useCallback(
    (trackerName: string) => {
      const trackerKey = trackerName.trim().toUpperCase();
      if (!imageHostPolicyMetadata) {
        return trackerKey === "HDB" ? trackerImageHostOptions : imageHostOptions;
      }

      const policyHosts = imageHostPolicyMetadata.TrackerUploadHosts?.[trackerKey];
      const fallbackHosts =
        imageHostPolicyMetadata.UploadHosts?.map((host) => normalizeImageHostValue(host)) ??
        imageHostOptions.filter((option) => option.value).map((option) => option.value);
      const ownerByHost = imageHostPolicyMetadata.OwnedHosts ?? defaultOwnedImageHosts;
      const hosts = (policyHosts ?? fallbackHosts).filter((host) => {
        const normalizedHost = normalizeImageHostValue(host);
        const owner = ownerByHost[normalizedHost];
        return !owner || owner.trim().toUpperCase() === trackerKey;
      });

      return buildImageHostOptions(hosts);
    },
    [buildImageHostOptions, imageHostPolicyMetadata],
  );

  const updateConfigValue = (path: string[], value: ConfigValue) => {
    setConfigData((prev) => {
      if (!prev) return prev;
      const clone = structuredClone(prev) as ConfigMap;
      let cursor: ConfigMap = clone;
      for (let i = 0; i < path.length - 1; i += 1) {
        const key = path[i];
        const next = cursor[key];
        if (!next || typeof next !== "object" || Array.isArray(next)) {
          cursor[key] = {};
        }
        cursor = cursor[key] as ConfigMap;
      }
      cursor[path[path.length - 1]] = value;
      setSettingsDirty(true);
      return clone;
    });

    const key = path[path.length - 1] || "";
    if (typeof value === "string" && isSensitiveKeyName(key)) {
      setSensitiveValues((prev) => {
        const next = { ...prev };
        const pathKey = buildPathKey(path);
        if (value === REDACTED_VALUE) {
          return prev;
        }
        if (!value) {
          delete next[pathKey];
          return next;
        }
        next[pathKey] = value;
        return next;
      });
    }
  };

  const updateScreenshotConfigValue = (key: string, value: ConfigValue) => {
    updateConfigValue(["ScreenshotHandling", key], value);
  };

  const removeConfigKey = (path: string[], key: string) => {
    setConfigData((prev) => {
      if (!prev) return prev;
      const clone = structuredClone(prev) as ConfigMap;
      let cursor: ConfigMap = clone;
      for (let i = 0; i < path.length; i += 1) {
        cursor = cursor[path[i]] as ConfigMap;
        if (!cursor || typeof cursor !== "object" || Array.isArray(cursor)) {
          return prev;
        }
      }
      if (!Object.prototype.hasOwnProperty.call(cursor, key)) {
        return prev;
      }
      delete cursor[key];
      setSettingsDirty(true);
      return clone;
    });
  };

  const addConfigKey = (path: string[], key: string, value: ConfigValue) => {
    if (!key.trim()) return;
    setConfigData((prev) => {
      if (!prev) return prev;
      const clone = structuredClone(prev) as ConfigMap;
      let cursor: ConfigMap = clone;
      for (let i = 0; i < path.length; i += 1) {
        const step = path[i];
        const next = cursor[step];
        if (!next || typeof next !== "object" || Array.isArray(next)) {
          cursor[step] = {};
        }
        cursor = cursor[step] as ConfigMap;
      }
      if (cursor[key] !== undefined) return prev;
      cursor[key] = value;
      setSettingsDirty(true);
      return clone;
    });
  };

  const loadSettings = useCallback(async () => {
    clearSettingsStatus();
    const getConfig = globalThis.go?.guiapp?.App?.GetConfig;
    if (!getConfig) {
      setSettingsError("Settings are unavailable in this build.");
      return;
    }
    setSettingsLoading(true);
    try {
      const result = await getConfig();
      const parsed = JSON.parse(result) as ConfigMap;
      const masked = maskSensitiveConfig(parsed);
      setConfigData(masked.masked);
      setSensitiveValues(masked.originals);
      setManualTrackerEntries({});
      setSettingsDirty(false);
    } catch (err) {
      setSettingsError(String(err));
    } finally {
      setSettingsLoading(false);
    }
  }, [clearSettingsStatus]);

  const loadDefaultConfig = useCallback(async () => {
    const getDefaultConfig = globalThis.go?.guiapp?.App?.GetDefaultConfig;
    if (!getDefaultConfig) {
      return;
    }
    try {
      const result = await getDefaultConfig();
      const parsed = JSON.parse(result) as ConfigMap;
      setDefaultConfig(parsed);
    } catch (_err) {
      // Silently fail if we can't load default config; it's optional for tracker comparison
    }
  }, []);

  const loadKnownTrackers = useCallback(async () => {
    if (knownTrackersLoading) return;
    const listKnown = globalThis.go?.guiapp?.App?.ListKnownTrackers;
    if (!listKnown) return;
    setKnownTrackersLoading(true);
    try {
      const result = await listKnown();
      if (Array.isArray(result)) {
        setKnownTrackers(result.map((entry) => String(entry)));
      }
    } catch (err) {
      setSettingsError(String(err));
    } finally {
      setKnownTrackersLoading(false);
    }
  }, [knownTrackersLoading]);

  const loadImageHostPolicyMetadata = useCallback(async () => {
    const getMetadata = globalThis.go?.guiapp?.App?.GetImageHostPolicyMetadata;
    if (!getMetadata) return;
    try {
      const result = await getMetadata();
      if (result && typeof result === "object") {
        setImageHostPolicyMetadata(result);
      }
    } catch (err) {
      setSettingsError(String(err));
    }
  }, []);

  const handleSaveSettings = async () => {
    clearSettingsStatus();
    const saveConfig = globalThis.go?.guiapp?.App?.SaveConfig;
    if (!saveConfig) {
      setSettingsError("Settings are unavailable in this build.");
      return;
    }
    const payload = buildSavePayload();
    if (!payload) {
      setSettingsError("Settings are not loaded.");
      return;
    }
    setSettingsLoading(true);
    try {
      await saveConfig(payload);
      markSettingsSaved("Settings saved and applied.");
    } catch (err) {
      setSettingsError(String(err));
    } finally {
      setSettingsLoading(false);
    }
  };

  useEffect(() => {
    if (
      (activeTab === "input" ||
        activeTab === "tracker" ||
        activeTab === "settings" ||
        activeTab === "logging" ||
        activeTab === "upload" ||
        activeTab === "upload_images") &&
      !configData
    ) {
      loadSettings();
    }
  }, [activeTab, configData, loadSettings]);

  useEffect(() => {
    if (
      (activeTab === "settings" || activeTab === "input" || activeTab === "upload") &&
      !defaultConfig
    ) {
      loadDefaultConfig();
    }
  }, [activeTab, defaultConfig, loadDefaultConfig]);

  useEffect(() => {
    if (activeTab === "settings" && knownTrackers.length === 0 && !knownTrackersLoading) {
      loadKnownTrackers();
    }
  }, [activeTab, knownTrackers.length, knownTrackersLoading, loadKnownTrackers]);

  useEffect(() => {
    if (activeTab === "settings" && !imageHostPolicyMetadata) {
      loadImageHostPolicyMetadata();
    }
  }, [activeTab, imageHostPolicyMetadata, loadImageHostPolicyMetadata]);

  useEffect(() => {
    if ((activeTab === "screenshots" || activeTab === "upload_images") && !configData) {
      loadSettings();
    }
  }, [activeTab, configData, loadSettings]);

  const advancedOpen = settingsAdvanced[settingsSection] ?? false;
  const showAdvancedToggle = (() => {
    if (settingsSection === "trackers") return trackerHasAdvancedFields;
    const section = settingsSections.find((item) => item.key === settingsSection);
    if (!section) return false;
    const meta = sectionFieldMeta[section.jsonKey];
    if (!meta) return false;
    return Object.values(meta).some((field) => field.advanced);
  })();

  const renderArrayEditor = (value: ConfigValue[], path: string[]) => {
    return (
      <div className="settings-array">
        {value.map((entry, index) => (
          <div className="settings-array-row" key={`${path.join(".")}-${index}`}>
            <input
              className={settingsInputClass}
              value={entry === null ? "" : String(entry)}
              onChange={(event) => {
                const updated = [...value];
                updated[index] = event.target.value;
                updateConfigValue(path, updated);
              }}
            />
            <Button
              type="button"
              onClick={() => {
                const updated = [...value];
                updated.splice(index, 1);
                updateConfigValue(path, updated);
              }}
            >
              Remove
            </Button>
          </div>
        ))}
        <Button type="button" onClick={() => updateConfigValue(path, [...value, ""])}>
          Add item
        </Button>
      </div>
    );
  };

  const renderField = (label: string, value: ConfigValue, path: string[], meta?: FieldMeta) => {
    const displayLabel = meta?.label ?? formatLabel(label);
    const typeHint = meta?.type;
    if (meta?.options && meta.options.length > 0) {
      return (
        <label className="settings-field" key={path.join(".")}>
          <span>{displayLabel}</span>
          <select
            className={settingsSelectClass}
            value={value === null ? "" : String(value ?? "")}
            onChange={(event) => updateConfigValue(path, event.target.value)}
          >
            {meta.options.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
      );
    }
    if (typeHint === "boolean" || typeof value === "boolean") {
      return (
        <div className="settings-switch-row" key={path.join(".")}>
          <span>{displayLabel}</span>
          <Switch
            aria-label={displayLabel}
            checked={Boolean(value)}
            onChange={(event) => updateConfigValue(path, event.target.checked)}
          />
        </div>
      );
    }
    if (typeHint === "number" || typeof value === "number") {
      const numericValue = typeof value === "number" && Number.isFinite(value) ? value : 0;
      return (
        <label className="settings-field" key={path.join(".")}>
          <span>{displayLabel}</span>
          <input
            className={settingsInputClass}
            type="number"
            value={numericValue}
            onChange={(event) => updateConfigValue(path, Number(event.target.value))}
          />
        </label>
      );
    }
    if (Array.isArray(value)) {
      return (
        <div className="settings-field" key={path.join(".")}>
          <span>{displayLabel}</span>
          {renderArrayEditor(value, path)}
        </div>
      );
    }
    if (value && typeof value === "object") {
      return (
        <div className="settings-subgroup" key={path.join(".")}>
          <div className="settings-subgroup__title">{displayLabel}</div>
          <div className="settings-grid">
            {Object.entries(value).map(([childKey, childValue]) =>
              renderField(childKey, childValue, [...path, childKey]),
            )}
          </div>
        </div>
      );
    }

    return (
      <label className="settings-field" key={path.join(".")}>
        <span>{displayLabel}</span>
        <input
          className={settingsInputClass}
          value={value === null ? "" : String(value ?? "")}
          onChange={(event) => updateConfigValue(path, event.target.value)}
        />
      </label>
    );
  };

  const renderMapSection = (
    sectionKey: string,
    sectionValue: ConfigMap,
    options?: {
      entriesKey?: string;
      defaultKey?: string;
      fieldMeta?: Record<string, FieldMeta>;
      advancedOpen?: boolean;
    },
  ) => {
    const entriesRoot = options?.entriesKey
      ? (sectionValue[options.entriesKey] as ConfigMap) || {}
      : sectionValue;
    const entries = Object.entries(entriesRoot).filter(
      ([, value]) => value && typeof value === "object" && !Array.isArray(value),
    ) as Array<[string, ConfigMap]>;
    const defaultKey = options?.defaultKey;
    const fieldMeta = options?.fieldMeta || {};
    const advancedOpen = options?.advancedOpen ?? false;

    return (
      <div className="settings-map">
        {defaultKey ? (
          <div className="settings-subgroup">
            <div className="settings-subgroup__title">{formatLabel(defaultKey)}</div>
            {renderField(defaultKey, sectionValue[defaultKey] as ConfigValue, [
              sectionKey,
              defaultKey,
            ])}
          </div>
        ) : null}

        <div className="settings-map__header">
          <p className="label">Entries</p>
          <Button
            type="button"
            onClick={() => {
              const name = globalThis.prompt("New entry name");
              if (!name) return;
              if (options?.entriesKey) {
                addConfigKey([sectionKey, options.entriesKey], name, {});
                return;
              }
              addConfigKey([sectionKey], name, {});
            }}
          >
            Add entry
          </Button>
        </div>

        <div className="settings-map__grid">
          {entries.length === 0 ? (
            <p className="muted">No entries yet.</p>
          ) : (
            entries.map(([key, value]) => (
              <div className="settings-card" key={`${sectionKey}-${key}`}>
                <div className="settings-card__header">
                  <p className="value">{key}</p>
                  <Button
                    type="button"
                    onClick={() => {
                      if (options?.entriesKey) {
                        removeConfigKey([sectionKey, options.entriesKey], key);
                        return;
                      }
                      removeConfigKey([sectionKey], key);
                    }}
                  >
                    Remove
                  </Button>
                </div>
                <div className="settings-grid">
                  {Object.entries(value)
                    .filter(([childKey]) => {
                      const meta = fieldMeta[childKey];
                      if (meta?.advanced && !advancedOpen) return false;
                      return true;
                    })
                    .map(([childKey, childValue]) =>
                      renderField(
                        childKey,
                        childValue,
                        [sectionKey, options?.entriesKey || "", key, childKey].filter(Boolean),
                        fieldMeta[childKey],
                      ),
                    )}
                </div>
              </div>
            ))
          )}
        </div>
      </div>
    );
  };

  /**
   * Check if a tracker is configured by comparing it to the baseline default config.
   * A tracker is considered configured if:
   * - It has any field different from the baseline
   * - The baseline doesn't have the tracker
   * - It has Unknown keys (custom fields not in the schema)
   */
  const isTrackerConfigured = useCallback(
    (trackerName: string, trackerValue: ConfigMap): boolean => {
      if (!defaultConfig || typeof defaultConfig !== "object" || Array.isArray(defaultConfig)) {
        return true; // If we can't load defaults, assume it's configured
      }

      const defaultTrackersCfg = defaultConfig.Trackers;
      if (
        !defaultTrackersCfg ||
        typeof defaultTrackersCfg !== "object" ||
        Array.isArray(defaultTrackersCfg)
      ) {
        return true; // If defaults don't have Trackers section, assume all are configured
      }

      const defaultTrackersMap = defaultTrackersCfg.Trackers;
      if (
        !defaultTrackersMap ||
        typeof defaultTrackersMap !== "object" ||
        Array.isArray(defaultTrackersMap)
      ) {
        return true; // If defaults don't have tracker entries, assume all are configured
      }

      const baselineTracker = (defaultTrackersMap as ConfigMap)[trackerName];
      if (baselineTracker === undefined) {
        // Not in baseline = configured (user added this tracker)
        return true;
      }

      if (typeof baselineTracker !== "object" || Array.isArray(baselineTracker)) {
        // Baseline has invalid type = configured
        return true;
      }

      // Compare field-by-field
      const currentKeys = new Set(Object.keys(trackerValue));
      const baselineKeys = new Set(Object.keys(baselineTracker as ConfigMap));

      // Check if any current key differs from baseline
      for (const key of currentKeys) {
        const currentValue = trackerValue[key];
        const baselineValue = (baselineTracker as ConfigMap)[key];

        // If key not in baseline, or values differ, it's configured
        if (baselineValue === undefined) {
          return true; // User added a new field
        }

        // Deep comparison for objects, shallow for primitives
        if (typeof currentValue === "object" && typeof baselineValue === "object") {
          if (JSON.stringify(currentValue) !== JSON.stringify(baselineValue)) {
            return true;
          }
        } else if (currentValue !== baselineValue) {
          return true;
        }
      }

      // Check if baseline has keys not in current (field was removed from baseline template)
      for (const key of baselineKeys) {
        if (!currentKeys.has(key)) {
          // User may have intentionally removed a field; this is also a configuration
          return true;
        }
      }

      // All fields match baseline exactly
      return false;
    },
    [defaultConfig],
  );

  const trackerSelectionNames = useMemo(() => {
    if (
      !configData ||
      !configData.Trackers ||
      typeof configData.Trackers !== "object" ||
      Array.isArray(configData.Trackers)
    ) {
      return [] as string[];
    }

    const trackerRoot = configData.Trackers as ConfigMap;
    const rawEntries = trackerRoot.Trackers;
    const entriesRoot =
      rawEntries && typeof rawEntries === "object" && !Array.isArray(rawEntries)
        ? (rawEntries as ConfigMap)
        : {};
    const allEntries = Object.entries(entriesRoot).filter(
      ([, value]) => value && typeof value === "object" && !Array.isArray(value),
    ) as Array<[string, ConfigMap]>;
    const manualTrackerSet = new Set(
      Object.entries(manualTrackerEntries)
        .filter(([, enabled]) => enabled)
        .map(([name]) => name),
    );

    return allEntries
      .filter(
        ([trackerName, trackerValue]) =>
          isTrackerConfigured(trackerName, trackerValue) || manualTrackerSet.has(trackerName),
      )
      .map(([trackerName]) => trackerName)
      .sort((left, right) => left.localeCompare(right));
  }, [configData, manualTrackerEntries, isTrackerConfigured]);

  const renderTrackerSection = (advancedOpen: boolean) => {
    try {
      if (
        !configData ||
        !configData.Trackers ||
        typeof configData.Trackers !== "object" ||
        Array.isArray(configData.Trackers)
      ) {
        return null;
      }

      const trackerRoot = configData.Trackers as ConfigMap;
      const defaultTrackers = (trackerRoot.DefaultTrackers as ConfigValue) ?? [];
      const rawEntries = trackerRoot.Trackers;
      const entriesRoot =
        rawEntries && typeof rawEntries === "object" && !Array.isArray(rawEntries)
          ? (rawEntries as ConfigMap)
          : {};

      const allEntries = Object.entries(entriesRoot).filter(
        ([, value]) => value && typeof value === "object" && !Array.isArray(value),
      ) as Array<[string, ConfigMap]>;
      const visibleTrackerSet = new Set(trackerSelectionNames);
      const visibleEntries = allEntries.filter(([trackerName]) =>
        visibleTrackerSet.has(trackerName),
      );

      const normalizedDefaultTrackers = normalizeDefaultTrackerList(defaultTrackers);
      const preferredTrackerRaw = trackerRoot.PreferredTracker;
      const preferredTracker =
        typeof preferredTrackerRaw === "string" ? preferredTrackerRaw.trim() : "";
      const trackerNames = trackerSelectionNames;
      const normalizedTrackerNameSet = new Set(trackerNames.map((name) => name.toLowerCase()));
      const selectedDefaultTrackerCount = normalizedDefaultTrackers.filter((name) =>
        normalizedTrackerNameSet.has(name.toLowerCase()),
      ).length;
      const trackerOptions = (knownTrackers.length ? knownTrackers : Object.keys(trackerSchemas))
        .filter((name) => typeof name === "string" && name.trim().length > 0)
        .slice()
        .sort((a, b) => a.localeCompare(b));
      const allEntrySet = new Set(allEntries.map(([name]) => name));
      const availableTrackers = trackerOptions.filter((name) => !visibleTrackerSet.has(name));

      const trackerFallbackSchema = [
        trackerFieldMeta.LinkDirName,
        trackerFieldMeta.ImageHost,
        trackerFieldMeta.APIKey,
        trackerFieldMeta.AnnounceURL,
        trackerFieldMeta.Username,
        trackerFieldMeta.Password,
        trackerFieldMeta.Anon,
      ];
      const trackerSchemaFor = (name: string) => {
        const imageHostField = {
          ...trackerFieldMeta.ImageHost,
          options: trackerOptionsForImageHost(name),
        };
        const base = trackerSchemas[name] || trackerFallbackSchema;
        return [imageHostField, ...base.filter((meta) => meta.key !== "ImageHost")];
      };

      const buildDefault = (meta: FieldMeta) => {
        if (meta.type === "boolean") return false;
        if (meta.type === "number") return 0;
        return "";
      };

      const buildTrackerDefaults = (schema: FieldMeta[]) => {
        const defaults: ConfigMap = {};
        schema.forEach((meta) => {
          defaults[meta.key] = buildDefault(meta);
        });
        return defaults;
      };

      const toggleDefaultTracker = (name: string, enabled: boolean) => {
        const current = normalizeDefaultTrackerList(defaultTrackers);
        const next = current.filter((entry) => entry !== name);
        if (enabled) {
          next.push(name);
        }
        updateConfigValue(["Trackers", "DefaultTrackers"], next);
      };

      const updatePreferredTracker = (value: string) => {
        updateConfigValue(["Trackers", "PreferredTracker"], value.trim());
      };

      return (
        <div className="settings-map">
          <details
            className="settings-subgroup settings-subgroup--collapsible"
            open={defaultTrackersPanelOpen}
            onToggle={(event) => {
              const target = event.currentTarget as HTMLDetailsElement;
              setDefaultTrackersPanelOpen(target.open);
            }}
          >
            <summary className="settings-subgroup__title tracker-summary-heading">
              <span>Default trackers</span>
              <span className="tracker-summary-count">
                {selectedDefaultTrackerCount}/{trackerNames.length}
              </span>
            </summary>
            <div className="tracker-defaults-body">
              {trackerNames.length === 0 ? (
                <p className="muted">Add tracker entries to select defaults.</p>
              ) : (
                <div className="tracker-selection-container">
                  <div className="tracker-pills">
                    {trackerNames.map((tracker) => (
                      <PillCheckbox
                        key={tracker}
                        checked={normalizedDefaultTrackers.includes(tracker)}
                        onCheckedChange={(checked) => toggleDefaultTracker(tracker, checked)}
                      >
                        {tracker}
                      </PillCheckbox>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </details>

          <details className="settings-subgroup settings-subgroup--collapsible">
            <summary className="settings-subgroup__title">Preferred tracker data source</summary>
            <div style={{ paddingTop: "0.5rem" }}>
              <div className="settings-map__controls">
                <select
                  className={settingsSelectClass}
                  value={preferredTracker}
                  onChange={(event) => updatePreferredTracker(event.target.value)}
                >
                  <option value="">None</option>
                  {trackerOptions.map((name) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                </select>
                <Button
                  type="button"
                  disabled={preferredTracker === ""}
                  onClick={() => updatePreferredTracker("")}
                >
                  Clear
                </Button>
              </div>
              <p className="muted" style={{ marginTop: "0.5rem" }}>
                Moves the selected tracker to the top of tracker-data lookup and qBit tracker
                priority when present.
              </p>
            </div>
          </details>

          <div className="settings-map__header">
            <p className="label">Entries</p>
            <div className="settings-map__controls">
              <select
                className={settingsSelectClass}
                value={trackerAddSelection}
                onChange={(event) => setTrackerAddSelection(event.target.value)}
                disabled={availableTrackers.length === 0}
              >
                <option value="">Select tracker</option>
                {availableTrackers.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
              <Button
                type="button"
                disabled={!trackerAddSelection}
                onClick={() => {
                  const name = trackerAddSelection.trim();
                  if (!name) return;
                  if (!allEntrySet.has(name)) {
                    const schema = trackerSchemaFor(name);
                    addConfigKey(["Trackers", "Trackers"], name, buildTrackerDefaults(schema));
                  }
                  setManualTrackerEntries((prev) => ({ ...prev, [name]: true }));
                  setTrackerAddSelection("");
                  setSettingsTrackerPanels((prev) => ({ ...prev, [name]: true }));
                }}
              >
                Add entry
              </Button>
            </div>
          </div>

          <div className="settings-map__grid">
            {visibleEntries.length === 0 ? (
              <p className="muted">No configured entries yet.</p>
            ) : (
              visibleEntries.map(([key, value]) => {
                const schema = trackerSchemaFor(key).filter((meta): meta is FieldMeta =>
                  Boolean(meta),
                );
                return (
                  <details
                    className="settings-card settings-card--collapsible"
                    key={`Trackers-${key}`}
                    open={settingsTrackerPanels[key] ?? false}
                    onToggle={(event) => {
                      const target = event.currentTarget as HTMLDetailsElement;
                      setSettingsTrackerPanels((prev) => ({ ...prev, [key]: target.open }));
                    }}
                  >
                    <summary className="settings-card__summary">
                      <span className="settings-card__summary-name">{key}</span>
                      <Button
                        type="button"
                        onClick={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                          removeConfigKey(["Trackers", "Trackers"], key);
                          setManualTrackerEntries((prev) => {
                            const next = { ...prev };
                            delete next[key];
                            return next;
                          });
                          setSettingsTrackerPanels((prev) => {
                            const next = { ...prev };
                            delete next[key];
                            return next;
                          });
                        }}
                      >
                        Remove
                      </Button>
                    </summary>
                    <div className="settings-card__body">
                      <div className="settings-grid">
                        {schema
                          .filter((meta) => !(meta.advanced && !advancedOpen))
                          .map((meta) =>
                            renderField(
                              meta.key,
                              value[meta.key] ?? buildDefault(meta),
                              ["Trackers", "Trackers", key, meta.key],
                              meta,
                            ),
                          )}
                      </div>
                    </div>
                  </details>
                );
              })
            )}
          </div>
        </div>
      );
    } catch (err) {
      return <p className="error">Unable to render tracker settings: {String(err)}</p>;
    }
  };

  const renderImageHostingSection = () => {
    if (!configData || !configData.ImageHosting || typeof configData.ImageHosting !== "object") {
      return null;
    }

    const imageCfg = configData.ImageHosting as ConfigMap;
    const hostFields = ["Host1", "Host2", "Host3", "Host4", "Host5", "Host6"];
    const requiredKeys = new Set<string>();

    hostFields.forEach((field) => {
      const selected = String(imageCfg[field] ?? "").trim();
      if (!selected) return;
      const keys = imageHostKeyMap[selected];
      if (keys) {
        keys.forEach((key) => requiredKeys.add(key));
      }
    });

    return (
      <div className="settings-form">
        <div className="settings-subgroup">
          <div className="settings-subgroup__title">Host Priority</div>
          <div className="settings-grid">
            {hostFields.map((field, index) => (
              <label className="settings-field" key={field}>
                <span>{`Host ${index + 1}`}</span>
                <select
                  className={settingsSelectClass}
                  value={String(imageCfg[field] ?? "")}
                  onChange={(event) =>
                    updateConfigValue(["ImageHosting", field], event.target.value)
                  }
                >
                  {imageHostOptions.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </label>
            ))}
          </div>
        </div>

        <div className="settings-subgroup">
          <div className="settings-subgroup__title">API Keys</div>
          {requiredKeys.size === 0 ? (
            <p className="muted">Select an image host to edit its API keys.</p>
          ) : (
            <div className="settings-grid">
              {Array.from(requiredKeys).map((key) =>
                renderField(key, imageCfg[key] as ConfigValue, ["ImageHosting", key]),
              )}
            </div>
          )}
        </div>
      </div>
    );
  };

  return {
    configData,
    settingsLoading,
    settingsDirty,
    settingsSaved,
    settingsError,
    settingsSection,
    settingsSections,
    showAdvancedToggle,
    advancedOpen,
    setSettingsSection,
    setSettingsAdvanced,
    loadSettings,
    handleSaveSettings,
    renderImageHostingSection,
    renderTrackerSection,
    renderMapSection,
    renderField,
    sectionFieldMeta,
    updateConfigValue,
    updateScreenshotConfigValue,
    configuredImageHosts,
    screenshotConfig,
    buildSavePayload,
    clearSettingsStatus,
    markSettingsSaved,
    setSettingsSavedMessage,
    setSettingsErrorMessage,
    resolveImageHostLabel,
    knownTrackersLoading,
    trackerSelectionNames,
  };
};
