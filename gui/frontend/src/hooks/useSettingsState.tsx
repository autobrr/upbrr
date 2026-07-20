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
type FieldOption = NonNullable<FieldMeta["options"]>[number];

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
  renderTorrentClientsSection: (advancedOpen: boolean) => JSX.Element | null;
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
  { key: "arr_integration", jsonKey: "ArrIntegration", label: "Arr" },
  { key: "post_upload", jsonKey: "PostUpload", label: "Post Upload" },
  { key: "trackers", jsonKey: "Trackers", label: "Trackers" },
  { key: "torrent_clients", jsonKey: "TorrentClients", label: "Torrent Clients" },
  { key: "client_setup", jsonKey: "ClientSetup", label: "Client Handling" },
  { key: "torrent_creation", jsonKey: "TorrentCreation", label: "Torrent Specific" },
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

const trackerImageHostOptions = [
  ...imageHostOptions,
  { value: "hdb", label: "HDB" },
  { value: "lostimg", label: "Lostimg" },
  { value: "reelflix", label: "Reelflix" },
];
const torrentClientTypeOptions = [
  { value: "qbit", label: "qBit" },
  { value: "watch", label: "Watch" },
];
const torrentClientLinkingOptions = [
  { value: "", label: "None" },
  { value: "hardlink", label: "Hardlink" },
  { value: "reflink", label: "Reflink" },
  { value: "symlink", label: "Symlink" },
];
const imageHostOptionLabels = new Map(
  trackerImageHostOptions.map((option) => [option.value, option.label]),
);
const defaultOwnedImageHosts: Record<string, string> = {
  hdb: "HDB",
  lostimg: "LST",
  reelflix: "RF",
};
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
  }),
  MyAnnounceURL: stringField("MyAnnounceURL", {
    label: "My announce URL",
    sensitive: true,
  }),
  URL: stringField("URL", { label: "URL" }),
  FaviconURL: stringField("FaviconURL", { label: "Favicon URL", advanced: true }),
  UploaderName: stringField("UploaderName", { label: "Uploader name" }),
  UploaderStatus: boolField("UploaderStatus", { label: "Uploader status" }),
  CustomLayout: stringField("CustomLayout", { label: "Custom layout" }),
  TagForCustomRelease: stringField("TagForCustomRelease", { label: "Tag for custom release" }),
  CheckForRules: boolField("CheckForRules", { label: "Check for rules" }),
  ModQ: boolField("ModQ", { label: "Mod queue" }),
  Draft: boolField("Draft", { label: "Draft" }),
  DraftDefault: boolField("DraftDefault", { label: "Draft default" }),
  Anon: boolField("Anon", { label: "Anonymous" }),
  ShowGroupIfAnon: boolField("ShowGroupIfAnon", { label: "Show group if anon" }),
  BhdRSSKey: stringField("BhdRSSKey", { label: "BHD RSS key", sensitive: true }),
  CheckRequests: boolField("CheckRequests", { label: "Check requests" }),
  FullMediainfo: boolField("FullMediainfo", { label: "Full mediainfo" }),
  ImgRehost: boolField("ImgRehost", { label: "Image rehost" }),
  ImageHost: stringField("ImageHost", {
    label: "Image host",
    options: imageHostOptions,
  }),
  TorrentClient: stringField("TorrentClient", { label: "Torrent client", advanced: true }),
  UseSpanishTitle: boolField("UseSpanishTitle", { label: "Use Spanish title" }),
  UseItalianTitle: boolField("UseItalianTitle", { label: "Use Italian title" }),
  OTPURI: stringField("OTPURI", { label: "OTP URI", sensitive: true }),
  SkipIfRehash: boolField("SkipIfRehash", { label: "Skip if rehash", advanced: true }),
  PreferMTV: boolField("PreferMTV", { label: "Prefer MTV torrent", advanced: true }),
  PTGenAPI: stringField("PTGenAPI", { label: "PTGen API", sensitive: true }),
  AddWebSourceToDesc: boolField("AddWebSourceToDesc", {
    label: "Add web source to desc",
  }),
  ImageCount: numberField("ImageCount", { label: "Image count" }),
  Channel: stringField("Channel", { label: "Channel" }),
  ImgAPI: stringField("ImgAPI", { label: "Image API", sensitive: true }),
  PronfoAPIKey: stringField("PronfoAPIKey", {
    label: "Pronfo API key",
    sensitive: true,
  }),
  PronfoTheme: stringField("PronfoTheme", { label: "Pronfo theme" }),
  PronfoRAPIID: stringField("PronfoRAPIID", { label: "Pronfo RAPI ID" }),
  APIUpload: boolField("APIUpload", { label: "API upload" }),
  Exclusive: boolField("Exclusive", { label: "Exclusive" }),
  LoginQuestion: stringField("LoginQuestion", {
    label: "Login question",
    sensitive: true,
  }),
  LoginAnswer: stringField("LoginAnswer", {
    label: "Login answer",
    sensitive: true,
  }),
  UserID: stringField("UserID", { label: "User ID", sensitive: true }),
  Filebrowser: stringField("Filebrowser", { label: "Filebrowser" }),
};

const trackerSchemas: Record<string, FieldMeta[]> = {
  A4K: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  ACM: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  AITHER: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  ANT: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  AR: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.AnnounceURL,
  ],
  ASC: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.UploaderStatus,
    trackerFieldMeta.CustomLayout,
    trackerFieldMeta.AnnounceURL,
  ],
  AZ: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.CheckForRules,
  ],
  BHD: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.BhdRSSKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.DraftDefault,
    trackerFieldMeta.Anon,
  ],
  BHDTV: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.MyAnnounceURL,
    trackerFieldMeta.Anon,
  ],
  BJS: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ShowGroupIfAnon,
  ],
  BLU: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  BT: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  BTN: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.URL,
    trackerFieldMeta.OTPURI,
  ],
  CBR: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
    trackerFieldMeta.TagForCustomRelease,
  ],
  CZ: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.CheckForRules,
  ],
  CZT: [trackerFieldMeta.FaviconURL, trackerFieldMeta.LinkDirName, trackerFieldMeta.Passkey],
  DC: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  DP: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  EMUW: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.UseSpanishTitle,
  ],
  FF: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.CheckRequests,
    trackerFieldMeta.FullMediainfo,
  ],
  FL: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Passkey,
    trackerFieldMeta.UploaderName,
    trackerFieldMeta.Anon,
  ],
  FLD: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  FRIKI: [trackerFieldMeta.FaviconURL, trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey],
  GPW: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
  ],
  HDB: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Passkey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.ImgRehost,
  ],
  HDS: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.FullMediainfo,
  ],
  HDT: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.URL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.FullMediainfo,
  ],
  HHD: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  IHD: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  IS: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  ITT: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  LCD: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.TagForCustomRelease,
  ],
  LDU: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  LST: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
    trackerFieldMeta.Draft,
  ],
  LT: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  LUME: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  MNS: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  MTV: [
    trackerFieldMeta.FaviconURL,
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
  NBL: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
  ],
  OE: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  OTW: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.ModQ,
    trackerFieldMeta.Anon,
  ],
  PHD: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.CheckForRules,
  ],
  PT: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  PTP: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.AddWebSourceToDesc,
    trackerFieldMeta.ApiUser,
    trackerFieldMeta.ApiKey,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.OTPURI,
  ],
  PTS: [trackerFieldMeta.FaviconURL, trackerFieldMeta.LinkDirName, trackerFieldMeta.AnnounceURL],
  PTT: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  R4E: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  RAS: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  RF: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.ImgAPI,
    trackerFieldMeta.Anon,
  ],
  RTF: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.Username,
    trackerFieldMeta.Password,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  SAM: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.TagForCustomRelease,
  ],
  SHRI: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.UseItalianTitle,
  ],
  SP: [trackerFieldMeta.FaviconURL, trackerFieldMeta.LinkDirName, trackerFieldMeta.APIKey],
  SPD: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Channel,
  ],
  STC: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  THR: [
    trackerFieldMeta.FaviconURL,
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
  TIK: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  TL: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIUpload,
    trackerFieldMeta.Passkey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ImgRehost,
    trackerFieldMeta.FullMediainfo,
  ],
  TLZ: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  TOS: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
    trackerFieldMeta.Exclusive,
  ],
  TTR: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  TVC: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.ImageCount,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.AnnounceURL,
    trackerFieldMeta.Anon,
  ],
  ULCX: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  UTP: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  YUS: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
  ],
  ZNTH: [
    trackerFieldMeta.FaviconURL,
    trackerFieldMeta.LinkDirName,
    trackerFieldMeta.APIKey,
    trackerFieldMeta.Anon,
    trackerFieldMeta.ModQ,
  ],
  MANUAL: [trackerFieldMeta.FaviconURL, trackerFieldMeta.Filebrowser],
};

const trackerHasAdvancedFields = Object.values(trackerSchemas).some((fields) =>
  fields.some((field) => field.advanced),
);

const REDACTED_VALUE = "[REDACTED]";
const ENCRYPTED_SECRET_PREFIX = "upbrr-enc:v1:";
const trackerActivationKeys = new Set([
  "APIKey",
  "ApiUser",
  "ApiKey",
  "Username",
  "Password",
  "Passkey",
  "AnnounceURL",
  "MyAnnounceURL",
  "BhdRSSKey",
  "OTPURI",
  "PTGenAPI",
  "ImageHost",
  "ImgAPI",
  "TorrentClient",
  "PronfoAPIKey",
  "LoginQuestion",
  "LoginAnswer",
  "UserID",
  "Filebrowser",
]);

function hasConfiguredTrackerValue(
  value: ConfigValue | undefined,
  baseline: ConfigValue | undefined,
): boolean {
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!trimmed) {
      return false;
    }
    return baseline !== value;
  }
  if (typeof value === "number") {
    return value !== 0 && baseline !== value;
  }
  if (typeof value === "boolean") {
    return value && baseline !== value;
  }
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return false;
    }
    if (Array.isArray(baseline) && JSON.stringify(value) === JSON.stringify(baseline)) {
      return false;
    }
    return true;
  }
  if (value && typeof value === "object") {
    if (Object.keys(value).length === 0) {
      return false;
    }
    if (
      baseline &&
      typeof baseline === "object" &&
      JSON.stringify(value) === JSON.stringify(baseline)
    ) {
      return false;
    }
    return true;
  }
  return false;
}
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
  ImageHosting: {
    LostimgAPI: stringField("LostimgAPI", { label: "API key", sensitive: true }),
  },
  MainSettings: {
    InputHistoryLimit: { key: "InputHistoryLimit", label: "Input history limit", type: "number" },
    UseFavicons: { key: "UseFavicons", label: "Use favicons" },
    FaviconOnly: { key: "FaviconOnly", label: "Favicon only" },
    SceneDetection: { key: "SceneDetection", label: "Scene detection (srrdb)" },
  },
  Metadata: {
    BTNAPI: { key: "BTNAPI", advanced: true, sensitive: true },
    SkipAutoTorrent: { key: "SkipAutoTorrent", advanced: true },
    SkipTrackerFilenameLookup: { key: "SkipTrackerFilenameLookup", advanced: true },
    UserOverrides: { key: "UserOverrides", advanced: true },
    BlurayScore: { key: "BlurayScore", advanced: true },
    BluraySingleScore: { key: "BluraySingleScore", advanced: true },
    CheckPredb: { key: "CheckPredb", advanced: true },
  },
  ScreenshotHandling: {
    MaxMenuItems: { key: "MaxMenuItems", label: "Maximum DVD menu images", type: "number" },
    ProcessLimit: { key: "ProcessLimit", advanced: true },
    MaxConcurrentUploads: { key: "MaxConcurrentUploads", advanced: true },
    FFmpegLimit: { key: "FFmpegLimit", advanced: true },
    FFmpegCompression: { key: "FFmpegCompression", advanced: true },
    TonemapAlgorithm: { key: "TonemapAlgorithm", advanced: true },
    Desat: { key: "Desat", advanced: true },
  },
  Description: {
    LogoSize: { key: "LogoSize", advanced: true },
    LogoLanguage: { key: "LogoLanguage", advanced: true },
    CharLimit: { key: "CharLimit", advanced: true },
    FileLimit: { key: "FileLimit", advanced: true },
    ProcessLimit: { key: "ProcessLimit", advanced: true },
    CustomSignature: { key: "CustomSignature", advanced: true },
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
  TorrentCreation: {},
  PostUpload: {
    InjectDelay: { key: "InjectDelay", advanced: true },
    MaxConcurrentTrackers: { key: "MaxConcurrentTrackers", advanced: true },
  },
  Logging: {},
  TorrentClients: {
    Type: stringField("Type", { label: "Type", options: torrentClientTypeOptions }),
    WatchFolder: stringField("WatchFolder", { label: "Watch folder" }),
    StorageDir: stringField("StorageDir", { label: "Storage directory" }),
    QuiProxyURL: stringField("QuiProxyURL", { label: "Qui proxy URL", sensitive: true }),
    QbitURL: stringField("QbitURL", { label: "qBit URL" }),
    QbitPort: numberField("QbitPort", { label: "qBit port" }),
    QbitUser: stringField("QbitUser", { label: "qBit user", sensitive: true }),
    QbitPass: stringField("QbitPass", { label: "qBit pass", sensitive: true }),
    QbitCategoryValue: stringField("QbitCategoryValue", { label: "qBit category" }),
    QbitTag: stringField("QbitTag", { label: "qBit tag" }),
    QbitCrossCategory: stringField("QbitCrossCategory", { label: "qBit cross category" }),
    QbitCrossTag: stringField("QbitCrossTag", { label: "qBit cross tag" }),
    UseTrackerAsTag: boolField("UseTrackerAsTag", { label: "Use tracker as tag" }),
    Linking: stringField("Linking", {
      label: "Linking",
      options: torrentClientLinkingOptions,
    }),
    AllowFallback: boolField("AllowFallback", { label: "Allow link fallback" }),
    LinkedFolder: stringField("LinkedFolder", { label: "Linked folder" }),
    LocalPath: stringField("LocalPath", { label: "Local path" }),
    RemotePath: stringField("RemotePath", { label: "Remote path" }),
    AutomaticManagementPaths: stringField("AutomaticManagementPaths", {
      label: "Automatic management paths",
    }),
    VerifyWebUICertificate: boolField("VerifyWebUICertificate", {
      label: "Verify WebUI certificate",
      advanced: true,
    }),
  },
};

const isSensitiveKeyName = (key: string) => {
  const lower = key.toLowerCase();
  return sensitiveKeyHints.some((hint) => lower.includes(hint));
};

const buildPathKey = (path: string[]) => path.join(".");

const isEncryptedSecretEnvelope = (value: string) => value.startsWith(ENCRYPTED_SECRET_PREFIX);

/** Masks secret-bearing config strings while retaining originals by config path for save payloads. */
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
      if (value && (isSensitiveKeyName(key) || isEncryptedSecretEnvelope(value))) {
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

const legacyTorrentClientKeys = [
  "Type",
  "TorrentClient",
  "URL",
  "WatchFolder",
  "StorageDir",
  "Username",
  "Password",
  "Category",
  "Tags",
  "TLSSkipVerify",
  "QbitTagsValue",
];

const qbitDefaultClient = (): ConfigMap => ({
  Type: "qbit",
  QuiProxyURL: "",
  QbitCategoryValue: "",
  QbitTag: "",
  QbitCrossCategory: "",
  QbitCrossTag: "",
  UseTrackerAsTag: false,
  Linking: "",
  AllowFallback: true,
  LinkedFolder: [],
  LocalPath: [],
  RemotePath: [],
  AutomaticManagementPaths: [],
  VerifyWebUICertificate: true,
});

const qbitDirectDisabledValues: Readonly<Record<string, ConfigValue>> = {
  QbitURL: "",
  QbitPort: 0,
  QbitUser: "",
  QbitPass: "",
  URL: "",
  Username: "",
  Password: "",
  QuiProxyURL: "",
};

const normalizeStringArray = (value: ConfigValue) => {
  if (Array.isArray(value)) {
    return value.map((entry) => String(entry ?? "").trim()).filter(Boolean);
  }
  if (typeof value === "string") {
    return value
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean);
  }
  return [];
};

const normalizeTorrentClientType = (client: ConfigMap) => {
  const directType = typeof client.Type === "string" ? client.Type.trim() : "";
  if (directType) {
    return directType.toLowerCase();
  }
  const legacyType = typeof client.TorrentClient === "string" ? client.TorrentClient.trim() : "";
  if (legacyType) {
    return legacyType.toLowerCase();
  }
  return "qbit";
};

/**
 * Normalizes a torrent client before save, migrating legacy qBit fields to the
 * canonical qBit keys while preserving non-qBit client configs.
 */
export const normalizeTorrentClientForSave = (client: ConfigMap) => {
  const next = { ...client };
  if (normalizeTorrentClientType(next) !== "qbit") {
    return next;
  }

  if (!next.QbitURL && typeof next.URL === "string") next.QbitURL = next.URL;
  if (!next.QbitUser && typeof next.Username === "string") next.QbitUser = next.Username;
  if (!next.QbitPass && typeof next.Password === "string") next.QbitPass = next.Password;
  if (!next.QbitCategoryValue && typeof next.Category === "string") {
    next.QbitCategoryValue = next.Category;
  }
  if (!next.QbitTag) {
    if (Array.isArray(next.Tags)) {
      next.QbitTag = normalizeStringArray(next.Tags).join(",");
    } else if (Array.isArray(next.QbitTagsValue)) {
      next.QbitTag = normalizeStringArray(next.QbitTagsValue).join(",");
    }
  }
  if (next.VerifyWebUICertificate === undefined && typeof next.TLSSkipVerify === "boolean") {
    next.VerifyWebUICertificate = !next.TLSSkipVerify;
  }

  legacyTorrentClientKeys.forEach((key) => {
    delete next[key];
  });

  return next;
};

/**
 * Builds the next qBit direct-connection state for the settings toggle.
 * Enabling seeds host defaults; disabling clears direct, proxy, and legacy
 * credential fields so the client no longer attempts a direct qBit connection.
 */
export const nextQbitDirectState = (client: ConfigMap, enabled: boolean): ConfigMap => {
  if (enabled) {
    return {
      ...client,
      QbitURL:
        typeof client.QbitURL === "string" && client.QbitURL.trim() !== ""
          ? client.QbitURL
          : "http://127.0.0.1",
      QbitPort: typeof client.QbitPort === "number" && client.QbitPort > 0 ? client.QbitPort : 8080,
    };
  }
  return { ...client, ...qbitDirectDisabledValues };
};

/**
 * Normalizes all configured torrent client entries in a settings payload before
 * serializing it for the backend.
 */
export const normalizeTorrentClientsForSave = (input: ConfigMap) => {
  const clients = input.TorrentClients;
  if (!clients || typeof clients !== "object" || Array.isArray(clients)) {
    return input;
  }

  const nextClients: ConfigMap = {};
  Object.entries(clients as ConfigMap).forEach(([name, value]) => {
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      return;
    }
    nextClients[name] = normalizeTorrentClientForSave(value as ConfigMap);
  });

  return { ...input, TorrentClients: nextClients };
};

const normalizeCZTTrackerForSave = (input: ConfigMap) => {
  const trackerRoot = input.Trackers;
  if (!trackerRoot || typeof trackerRoot !== "object" || Array.isArray(trackerRoot)) {
    return input;
  }
  const trackerEntries = (trackerRoot as ConfigMap).Trackers;
  if (!trackerEntries || typeof trackerEntries !== "object" || Array.isArray(trackerEntries)) {
    return input;
  }

  let changed = false;
  const nextEntries: ConfigMap = {};
  Object.entries(trackerEntries as ConfigMap).forEach(([name, value]) => {
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      nextEntries[name] = value;
      return;
    }
    if (name.trim().toUpperCase() !== "CZT") {
      nextEntries[name] = value;
      return;
    }
    const next = { ...(value as ConfigMap) };
    if ("APIKey" in next) {
      delete next.APIKey;
      changed = true;
    }
    if ("URL" in next) {
      delete next.URL;
      changed = true;
    }
    if ("AnnounceURL" in next) {
      delete next.AnnounceURL;
      changed = true;
    }
    nextEntries[name] = next;
  });

  if (!changed) {
    return input;
  }
  return {
    ...input,
    Trackers: {
      ...(trackerRoot as ConfigMap),
      Trackers: nextEntries,
    },
  };
};

/**
 * Owns settings-screen state, Wails config loading, sensitive-value masking,
 * render helpers, and save payload construction for tabs that need config data.
 * Save payloads restore masked secrets before serialization.
 */
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

  const torrentClientOptions = useMemo<FieldOption[]>(() => {
    if (
      !configData ||
      !configData.TorrentClients ||
      typeof configData.TorrentClients !== "object" ||
      Array.isArray(configData.TorrentClients)
    ) {
      return [];
    }

    return Object.entries(configData.TorrentClients as ConfigMap)
      .filter(
        ([name, value]) =>
          name.trim() !== "" && value && typeof value === "object" && !Array.isArray(value),
      )
      .map(([name]) => ({ value: name, label: name }));
  }, [configData]);

  const effectiveSectionFieldMeta = useMemo<Record<string, Record<string, FieldMeta>>>(() => {
    const clientOptions = [{ value: "", label: "" }, ...torrentClientOptions];
    return {
      ...sectionFieldMeta,
      ClientSetup: {
        ...(sectionFieldMeta.ClientSetup ?? {}),
        DefaultClient: stringField("DefaultClient", {
          label: "Default client",
          options: clientOptions,
        }),
        InjectClients: stringField("InjectClients", {
          label: "Injected clients",
          options: clientOptions,
        }),
        SearchClients: stringField("SearchClients", {
          label: "Searching clients",
          options: clientOptions,
        }),
      },
    };
  }, [torrentClientOptions]);

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
    const restored = normalizeCZTTrackerForSave(
      normalizeTorrentClientsForSave(restoreSensitiveConfig(configData, sensitiveValues)),
    );
    return JSON.stringify(restored, null, 2);
  };

  const resolveImageHostLabel = (value: string) => {
    return imageHostOptionLabels.get(value) ?? value;
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
      const imageCfg =
        configData?.ImageHosting &&
        typeof configData.ImageHosting === "object" &&
        !Array.isArray(configData.ImageHosting)
          ? (configData.ImageHosting as ConfigMap)
          : null;
      const trackerRoot =
        configData?.Trackers &&
        typeof configData.Trackers === "object" &&
        !Array.isArray(configData.Trackers)
          ? (configData.Trackers as ConfigMap)
          : null;
      const trackerEntries =
        trackerRoot?.Trackers &&
        typeof trackerRoot.Trackers === "object" &&
        !Array.isArray(trackerRoot.Trackers)
          ? (trackerRoot.Trackers as ConfigMap)
          : null;
      const trackerCfg =
        trackerEntries?.[trackerKey] &&
        typeof trackerEntries[trackerKey] === "object" &&
        !Array.isArray(trackerEntries[trackerKey])
          ? (trackerEntries[trackerKey] as ConfigMap)
          : null;
      const globalFallbackHosts = fallbackHosts.filter((host) => !ownerByHost[host]);
      const globalHosts = (configuredImageHosts.length ? configuredImageHosts : globalFallbackHosts)
        .map((host) => normalizeImageHostValue(host))
        .filter((host) => host.length > 0 && !ownerByHost[host]);
      const policyHostSet = new Set(
        (policyHosts ?? []).map((host) => normalizeImageHostValue(host)).filter(Boolean),
      );
      const policyHasGlobalHosts = Array.from(policyHostSet).some((host) => !ownerByHost[host]);
      const policyAllowsGlobalFallback =
        policyHostSet.size === 0 ||
        Array.from(policyHostSet).every((host) => host === "lostimg" || host === "reelflix");
      const hosts = globalHosts.filter(
        (host) => policyAllowsGlobalFallback || (policyHasGlobalHosts && policyHostSet.has(host)),
      );

      (policyHosts ?? []).forEach((host) => {
        const normalizedHost = normalizeImageHostValue(host);
        const owner = ownerByHost[normalizedHost];
        if (!owner || owner.trim().toUpperCase() !== trackerKey) {
          return;
        }
        if (normalizedHost === "lostimg" && !Boolean(imageCfg?.LostimgEnabled)) {
          return;
        }
        if (
          normalizedHost === "reelflix" &&
          normalizeImageHostValue(String(trackerCfg?.ImageHost ?? "")) !== "reelflix"
        ) {
          return;
        }
        if (!hosts.includes(normalizedHost)) {
          hosts.push(normalizedHost);
        }
      });

      return buildImageHostOptions(hosts);
    },
    [buildImageHostOptions, configData, configuredImageHosts, imageHostPolicyMetadata],
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
    if (
      typeof value === "string" &&
      (isSensitiveKeyName(key) || sensitiveValues[buildPathKey(path)] !== undefined)
    ) {
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
        activeTab === "dupes" ||
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
    const meta = effectiveSectionFieldMeta[section.jsonKey];
    if (!meta) return false;
    return Object.values(meta).some((field) => field.advanced);
  })();

  const renderArrayEditor = (
    value: ConfigValue[],
    path: string[],
    meta?: FieldMeta,
    displayLabel?: string,
  ) => {
    const options = meta?.options ?? [];
    const optionValueFor = (entry: ConfigValue) => (entry === null ? "" : String(entry ?? ""));
    const optionsFor = (entry: ConfigValue) => {
      const selected = optionValueFor(entry);
      if (!selected || options.some((option) => option.value === selected)) {
        return options;
      }
      return [...options, { value: selected, label: selected }];
    };
    const newItemValue = options.find((option) => option.value !== "")?.value ?? "";

    return (
      <div className="settings-array">
        {value.map((entry, index) => (
          <div className="settings-array-row" key={`${path.join(".")}-${index}`}>
            {options.length > 0 ? (
              <select
                aria-label={displayLabel ? `${displayLabel} ${index + 1}` : undefined}
                className={settingsSelectClass}
                value={optionValueFor(entry)}
                onChange={(event) => {
                  const updated = [...value];
                  updated[index] = event.target.value;
                  updateConfigValue(path, updated);
                }}
              >
                {optionsFor(entry).map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            ) : (
              <input
                aria-label={displayLabel ? `${displayLabel} ${index + 1}` : undefined}
                className={settingsInputClass}
                value={optionValueFor(entry)}
                onChange={(event) => {
                  const updated = [...value];
                  updated[index] = event.target.value;
                  updateConfigValue(path, updated);
                }}
              />
            )}
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
        <Button
          type="button"
          aria-label={displayLabel ? `Add ${displayLabel} item` : "Add item"}
          onClick={() => updateConfigValue(path, [...value, newItemValue])}
        >
          Add item
        </Button>
      </div>
    );
  };

  const renderField = (label: string, value: ConfigValue, path: string[], meta?: FieldMeta) => {
    const displayLabel = meta?.label ?? formatLabel(label);
    const typeHint = meta?.type;
    if (Array.isArray(value)) {
      return (
        <div className="settings-field" key={path.join(".")}>
          <span>{displayLabel}</span>
          {renderArrayEditor(value, path, meta, displayLabel)}
        </div>
      );
    }
    if (meta?.options && meta.options.length > 0) {
      const selectedValue = value === null ? "" : String(value ?? "");
      const options = meta.options.some((option) => option.value === selectedValue)
        ? meta.options
        : [...meta.options, { value: selectedValue, label: selectedValue }];
      return (
        <label className="settings-field" key={path.join(".")}>
          <span>{displayLabel}</span>
          <select
            className={settingsSelectClass}
            value={selectedValue}
            onChange={(event) => updateConfigValue(path, event.target.value)}
          >
            {options.map((option) => (
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

  const renderTorrentClientsSection = (advancedOpen: boolean) => {
    if (
      !configData ||
      !configData.TorrentClients ||
      typeof configData.TorrentClients !== "object" ||
      Array.isArray(configData.TorrentClients)
    ) {
      return null;
    }

    const clients = Object.entries(configData.TorrentClients as ConfigMap).filter(
      ([, value]) => value && typeof value === "object" && !Array.isArray(value),
    ) as Array<[string, ConfigMap]>;
    const meta = effectiveSectionFieldMeta.TorrentClients || {};
    const valueFor = (client: ConfigMap, primary: string, fallback?: string) => {
      const primaryValue = client[primary];
      if (primaryValue !== undefined && primaryValue !== null && primaryValue !== "") {
        return primaryValue;
      }
      return fallback ? client[fallback] : primaryValue;
    };
    const arrayFor = (client: ConfigMap, key: string) => {
      const value = client[key];
      return Array.isArray(value) ? value : [];
    };
    const qbitTagFor = (client: ConfigMap) => {
      const direct = client.QbitTag;
      if (typeof direct === "string" && direct.trim() !== "") return direct;
      const qbitTags = normalizeStringArray(client.QbitTagsValue);
      if (qbitTags.length > 0) return qbitTags.join(",");
      return normalizeStringArray(client.Tags).join(",");
    };
    const hasDirectConfig = (client: ConfigMap) =>
      ["QbitURL", "QbitPort", "QbitUser", "QbitPass", "URL", "Username", "Password"].some((key) => {
        const value = client[key];
        if (typeof value === "number") return value > 0;
        return typeof value === "string" && value.trim() !== "";
      });
    const setQbitDirect = (name: string, enabled: boolean) => {
      const client = clients.find(([clientName]) => clientName === name)?.[1];
      if (!client) {
        return;
      }
      const nextClient = nextQbitDirectState(client, enabled);
      for (const key of [
        "QbitURL",
        "QbitPort",
        "QbitUser",
        "QbitPass",
        "URL",
        "Username",
        "Password",
        "QuiProxyURL",
      ]) {
        if (client[key] === nextClient[key]) {
          continue;
        }
        updateConfigValue(["TorrentClients", name, key], nextClient[key]);
      }
    };

    return (
      <div className="settings-map">
        <div className="settings-map__header">
          <p className="label">Entries</p>
          <Button
            type="button"
            onClick={() => {
              const name = globalThis.prompt("New entry name");
              if (!name) return;
              addConfigKey(["TorrentClients"], name, qbitDefaultClient());
            }}
          >
            Add entry
          </Button>
        </div>

        <div className="settings-map__grid">
          {clients.length === 0 ? (
            <p className="muted">No entries yet.</p>
          ) : (
            clients.map(([name, client]) => {
              const clientType = normalizeTorrentClientType(client);
              const watchClient = clientType === "watch";
              const directEnabled = !watchClient && hasDirectConfig(client);
              return (
                <div className="settings-card" key={`TorrentClients-${name}`}>
                  <div className="settings-card__header">
                    <p className="value">{name}</p>
                    <Button type="button" onClick={() => removeConfigKey(["TorrentClients"], name)}>
                      Remove
                    </Button>
                  </div>
                  <div className="settings-grid">
                    {renderField(
                      "Type",
                      valueFor(client, "Type", "TorrentClient") ?? "qbit",
                      ["TorrentClients", name, "Type"],
                      meta.Type,
                    )}
                    {watchClient
                      ? renderField(
                          "WatchFolder",
                          client.WatchFolder ?? "",
                          ["TorrentClients", name, "WatchFolder"],
                          meta.WatchFolder,
                        )
                      : null}
                    {watchClient
                      ? renderField(
                          "StorageDir",
                          client.StorageDir ?? "",
                          ["TorrentClients", name, "StorageDir"],
                          meta.StorageDir,
                        )
                      : null}
                    {!watchClient
                      ? renderField(
                          "QuiProxyURL",
                          client.QuiProxyURL ?? "",
                          ["TorrentClients", name, "QuiProxyURL"],
                          meta.QuiProxyURL,
                        )
                      : null}
                    {!watchClient
                      ? renderField(
                          "QbitCategoryValue",
                          valueFor(client, "QbitCategoryValue", "Category") ?? "",
                          ["TorrentClients", name, "QbitCategoryValue"],
                          meta.QbitCategoryValue,
                        )
                      : null}
                    {!watchClient
                      ? renderField(
                          "QbitTag",
                          qbitTagFor(client),
                          ["TorrentClients", name, "QbitTag"],
                          meta.QbitTag,
                        )
                      : null}
                    {!watchClient
                      ? renderField(
                          "QbitCrossCategory",
                          client.QbitCrossCategory ?? "",
                          ["TorrentClients", name, "QbitCrossCategory"],
                          meta.QbitCrossCategory,
                        )
                      : null}
                    {!watchClient
                      ? renderField(
                          "QbitCrossTag",
                          client.QbitCrossTag ?? "",
                          ["TorrentClients", name, "QbitCrossTag"],
                          meta.QbitCrossTag,
                        )
                      : null}
                    {!watchClient
                      ? renderField(
                          "UseTrackerAsTag",
                          client.UseTrackerAsTag ?? false,
                          ["TorrentClients", name, "UseTrackerAsTag"],
                          meta.UseTrackerAsTag,
                        )
                      : null}
                    {!watchClient
                      ? renderField(
                          "Linking",
                          client.Linking ?? "",
                          ["TorrentClients", name, "Linking"],
                          meta.Linking,
                        )
                      : null}
                    {!watchClient ? (
                      <div
                        className="settings-switch-row"
                        key={`TorrentClients-${name}-AllowFallback`}
                      >
                        <span>Allow link fallback</span>
                        <Switch
                          aria-label="Allow link fallback"
                          checked={Boolean(client.AllowFallback ?? true)}
                          onChange={(event) =>
                            updateConfigValue(
                              ["TorrentClients", name, "AllowFallback"],
                              event.target.checked,
                            )
                          }
                        />
                      </div>
                    ) : null}
                    {!watchClient
                      ? renderField(
                          "LinkedFolder",
                          arrayFor(client, "LinkedFolder"),
                          ["TorrentClients", name, "LinkedFolder"],
                          meta.LinkedFolder,
                        )
                      : null}
                    {!watchClient
                      ? renderField(
                          "LocalPath",
                          arrayFor(client, "LocalPath"),
                          ["TorrentClients", name, "LocalPath"],
                          meta.LocalPath,
                        )
                      : null}
                    {!watchClient
                      ? renderField(
                          "RemotePath",
                          arrayFor(client, "RemotePath"),
                          ["TorrentClients", name, "RemotePath"],
                          meta.RemotePath,
                        )
                      : null}
                    {!watchClient
                      ? renderField(
                          "AutomaticManagementPaths",
                          arrayFor(client, "AutomaticManagementPaths"),
                          ["TorrentClients", name, "AutomaticManagementPaths"],
                          meta.AutomaticManagementPaths,
                        )
                      : null}
                    {!watchClient && advancedOpen
                      ? renderField(
                          "VerifyWebUICertificate",
                          client.VerifyWebUICertificate ?? true,
                          ["TorrentClients", name, "VerifyWebUICertificate"],
                          meta.VerifyWebUICertificate,
                        )
                      : null}
                  </div>

                  {!watchClient ? (
                    <div className="settings-switch-row">
                      <span>qBit direct</span>
                      <Switch
                        aria-label="qBit direct"
                        checked={directEnabled}
                        onChange={(event) => setQbitDirect(name, event.target.checked)}
                      />
                    </div>
                  ) : null}

                  {!watchClient && directEnabled ? (
                    <div className="settings-grid">
                      {renderField(
                        "QbitURL",
                        valueFor(client, "QbitURL", "URL") ?? "",
                        ["TorrentClients", name, "QbitURL"],
                        meta.QbitURL,
                      )}
                      {renderField(
                        "QbitPort",
                        client.QbitPort ?? 0,
                        ["TorrentClients", name, "QbitPort"],
                        meta.QbitPort,
                      )}
                      {renderField(
                        "QbitUser",
                        valueFor(client, "QbitUser", "Username") ?? "",
                        ["TorrentClients", name, "QbitUser"],
                        meta.QbitUser,
                      )}
                      {renderField(
                        "QbitPass",
                        valueFor(client, "QbitPass", "Password") ?? "",
                        ["TorrentClients", name, "QbitPass"],
                        meta.QbitPass,
                      )}
                    </div>
                  ) : null}
                </div>
              );
            })
          )}
        </div>
      </div>
    );
  };

  /**
   * Check if a tracker has user-supplied upload/auth config. Catalog defaults
   * include every tracker and URL, so entry presence or default_tracker membership
   * cannot mean the tracker is enabled.
   */
  const isTrackerConfigured = useCallback(
    (trackerName: string, trackerValue: ConfigMap): boolean => {
      if (!defaultConfig || typeof defaultConfig !== "object" || Array.isArray(defaultConfig)) {
        return Object.entries(trackerValue).some(([key, value]) =>
          trackerActivationKeys.has(key) ? hasConfiguredTrackerValue(value, undefined) : false,
        );
      }

      const defaultTrackersCfg = defaultConfig.Trackers;
      if (
        !defaultTrackersCfg ||
        typeof defaultTrackersCfg !== "object" ||
        Array.isArray(defaultTrackersCfg)
      ) {
        return Object.entries(trackerValue).some(([key, value]) =>
          trackerActivationKeys.has(key) ? hasConfiguredTrackerValue(value, undefined) : false,
        );
      }

      const defaultTrackersMap = defaultTrackersCfg.Trackers;
      if (
        !defaultTrackersMap ||
        typeof defaultTrackersMap !== "object" ||
        Array.isArray(defaultTrackersMap)
      ) {
        return Object.entries(trackerValue).some(([key, value]) =>
          trackerActivationKeys.has(key) ? hasConfiguredTrackerValue(value, undefined) : false,
        );
      }

      const baselineTracker = (defaultTrackersMap as ConfigMap)[trackerName];
      const baseline =
        baselineTracker && typeof baselineTracker === "object" && !Array.isArray(baselineTracker)
          ? (baselineTracker as ConfigMap)
          : {};

      for (const [key, currentValue] of Object.entries(trackerValue)) {
        if (!trackerActivationKeys.has(key)) {
          continue;
        }
        if (hasConfiguredTrackerValue(currentValue, baseline[key])) {
          return true;
        }
      }

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
      const trackerClientOptions = [{ value: "", label: "" }, ...torrentClientOptions];

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
        const torrentClientField = {
          ...trackerFieldMeta.TorrentClient,
          options: trackerClientOptions,
        };
        const base = trackerSchemas[name] || trackerFallbackSchema;
        return [
          imageHostField,
          torrentClientField,
          ...base.filter((meta) => meta.key !== "ImageHost" && meta.key !== "TorrentClient"),
        ];
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
    const trackersRoot =
      configData.Trackers &&
      typeof configData.Trackers === "object" &&
      !Array.isArray(configData.Trackers)
        ? (configData.Trackers as ConfigMap)
        : null;
    const trackerEntries =
      trackersRoot?.Trackers &&
      typeof trackersRoot.Trackers === "object" &&
      !Array.isArray(trackersRoot.Trackers)
        ? (trackersRoot.Trackers as ConfigMap)
        : null;
    const rfTrackerCfg =
      trackerEntries?.RF &&
      typeof trackerEntries.RF === "object" &&
      !Array.isArray(trackerEntries.RF)
        ? (trackerEntries.RF as ConfigMap)
        : null;
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

        <div className="settings-subgroup">
          <div className="settings-subgroup__title">Tracker Image Hosts</div>
          <div className="settings-grid">
            <div className="settings-switch-row">
              <span>LST Lostimg</span>
              <Switch
                aria-label="LST Lostimg"
                checked={Boolean(imageCfg.LostimgEnabled)}
                onChange={(event) =>
                  updateConfigValue(["ImageHosting", "LostimgEnabled"], event.target.checked)
                }
              />
            </div>
            {renderField(
              "LostimgAPI",
              (imageCfg.LostimgAPI as ConfigValue) ?? "",
              ["ImageHosting", "LostimgAPI"],
              sectionFieldMeta.ImageHosting.LostimgAPI,
            )}
            <div className="settings-switch-row">
              <span>RF Reelflix</span>
              <Switch
                aria-label="RF Reelflix"
                checked={
                  normalizeImageHostValue(String(rfTrackerCfg?.ImageHost ?? "")) === "reelflix"
                }
                onChange={(event) =>
                  updateConfigValue(
                    ["Trackers", "Trackers", "RF", "ImageHost"],
                    event.target.checked ? "reelflix" : "",
                  )
                }
              />
            </div>
            {renderField(
              "ImgAPI",
              (rfTrackerCfg?.ImgAPI as ConfigValue) ?? "",
              ["Trackers", "Trackers", "RF", "ImgAPI"],
              trackerFieldMeta.ImgAPI,
            )}
          </div>
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
    renderTorrentClientsSection,
    renderMapSection,
    renderField,
    sectionFieldMeta: effectiveSectionFieldMeta,
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
