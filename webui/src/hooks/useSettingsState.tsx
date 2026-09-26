// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { configClient, trackerCatalogClient, type ConfigActivationFailureCode } from "../api/app";
import { trackerFieldPresentation } from "../settings/trackerFields";
import type { SettingsRenderContext } from "../settings/renderers";
import {
  REDACTED_VALUE,
  buildPathKey,
  isSensitiveKeyName,
  maskSensitiveConfig,
  restoreSensitiveConfig,
  normalizeTorrentClientsForSave,
  normalizeTrackersForSave,
} from "../settings/configTransforms";
import type {
  ConfigMap,
  ConfigValue,
  FieldMeta,
  ImageHostPolicyMetadata,
  TrackerCatalog,
  TrackerCatalogEntry,
} from "../types";
import { trackerCatalogKey, useTrackerCatalog } from "../trackerCatalog";
import { formatLabel } from "../utils/settings";

export {
  normalizeTorrentClientForSave,
  nextQbitDirectState,
  normalizeTorrentClientsForSave,
} from "../settings/configTransforms";

type SettingsSection = { key: string; jsonKey: string; label: string };
type ConfigActivation = Awaited<ReturnType<typeof configClient.getActivation>>;
type MaskedConfig = ReturnType<typeof maskSensitiveConfig>;
const activeConfigKey = ["config", "active"] as const;

const configActivationPollIntervalMS = 1000;
const configActivationFailureStage: Record<ConfigActivationFailureCode, string> = {
  normalize: "configuration normalization",
  validate_stored: "stored configuration validation",
  validate_runtime: "runtime configuration validation",
  build: "runtime construction",
  cookies: "cookie loading",
  persist: "activation persistence",
};

const configActivationFailureMessage = (failureCode?: ConfigActivationFailureCode) =>
  `Settings could not be applied during ${
    failureCode ? configActivationFailureStage[failureCode] : "configuration activation"
  }. Review the settings and save again.`;

type FieldOption = NonNullable<FieldMeta["options"]>[number];

type UseSettingsStateOptions = {
  activeTab: string;
};

type UseSettingsStateResult = {
  /** Last applied configuration, kept separate from the editable settings draft. */
  configData: ConfigMap | null;
  /** Editable settings draft, including a candidate waiting for activation. */
  settingsConfigData: ConfigMap | null;
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
  loadSettings: (invalidateCatalogWhenActive?: boolean) => void;
  handleSaveSettings: () => void;
  editorContext: SettingsRenderContext;
  sectionFieldMeta: Record<string, Record<string, FieldMeta>>;
  updateConfigValue: (path: string[], value: ConfigValue) => void;
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
  settingsTrackerSelectionNames: string[];
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
const imageHostOptionLabels = new Map<string, string>([
  ...imageHostOptions.map((option) => [option.value, option.label] as const),
  ["samaritano", "Samaritano"],
]);
const normalizeImageHostValue = (value: string) => value.trim().toLowerCase();
const imageHostOptionFor = (host: string) => {
  const value = normalizeImageHostValue(host);
  return { value, label: imageHostOptionLabels.get(value) ?? formatLabel(value) };
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

const conditionalImageHostEnabledKeys: Record<string, string> = {
  lostimg: "LostimgEnabled",
  reelflix: "ReelflixEnabled",
  samaritano: "SamaritanoEnabled",
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

const trackerConfigValue = (entries: ConfigMap, name: string): ConfigMap | null => {
  const exact = entries[name];
  if (exact && typeof exact === "object" && !Array.isArray(exact)) return exact as ConfigMap;
  const foldedName = Object.keys(entries).find(
    (candidate) => candidate.trim().toLowerCase() === name.trim().toLowerCase(),
  );
  if (!foldedName) return null;
  const value = entries[foldedName];
  return value && typeof value === "object" && !Array.isArray(value) ? (value as ConfigMap) : null;
};

const selectConfiguredTrackerNames = (
  configData: ConfigMap | null,
  catalog: TrackerCatalog | null,
  isConfigured: (entry: TrackerCatalogEntry, value: ConfigMap) => boolean,
  draftEntries: Readonly<Record<string, boolean>> = {},
) => {
  const trackerConfig = configData?.Trackers;
  if (!trackerConfig || typeof trackerConfig !== "object" || Array.isArray(trackerConfig)) {
    return [] as string[];
  }
  const rawEntries = trackerConfig.Trackers;
  const entries =
    rawEntries && typeof rawEntries === "object" && !Array.isArray(rawEntries)
      ? (rawEntries as ConfigMap)
      : {};

  return (catalog?.entries ?? [])
    .filter((entry) => {
      const value = trackerConfigValue(entries, entry.name);
      return draftEntries[entry.name] || (value ? isConfigured(entry, value) : entry.configured);
    })
    .map((entry) => entry.name);
};
const sectionFieldMeta: Record<string, Record<string, FieldMeta>> = {
  ImageHosting: {
    LostimgAPI: stringField("LostimgAPI", { label: "API key", sensitive: true }),
    ReelflixAPI: stringField("ReelflixAPI", {
      label: "ReelFliX API key",
      sensitive: true,
    }),
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
  TorrentCreation: {
    PreferMax16: {
      key: "PreferMax16",
      label: "Prefer reusable torrents up to 16 MiB",
    },
  },
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

/**
 * Owns settings-screen state, WebUI config loading, sensitive-value masking,
 * render helpers, and save payload construction for tabs that need config data.
 * Save payloads restore masked secrets before serialization. Deferred activation is polled
 * separately from saving; completion of an earlier save preserves newer unsaved edits.
 */
export const useSettingsState = (options: UseSettingsStateOptions): UseSettingsStateResult => {
  const { activeTab } = options;
  const queryClient = useQueryClient();
  const activeConfigQuery = useQuery({
    queryKey: activeConfigKey,
    queryFn: async ({ signal }): Promise<MaskedConfig> =>
      maskSensitiveConfig(JSON.parse(await configClient.get(signal)) as ConfigMap),
    enabled: false,
  });
  const configData = activeConfigQuery.data?.masked ?? null;
  const [settingsConfigData, setSettingsConfigData] = useState<ConfigMap | null>(null);
  const {
    catalog: trackerCatalog,
    loading: knownTrackersLoading,
    error: trackerCatalogError,
    removeUnsupported,
  } = useTrackerCatalog();
  const imageHostPolicyQuery = useQuery({
    queryKey: ["image-host-policy"],
    queryFn: ({ signal }) => trackerCatalogClient.getImageHostPolicyMetadata(signal),
    enabled: activeTab === "settings",
    staleTime: Infinity,
  });
  const imageHostPolicyMetadata: ImageHostPolicyMetadata | null = imageHostPolicyQuery.data ?? null;
  const [trackerAddSelection, setTrackerAddSelection] = useState("");
  const [draftTrackerEntries, setDraftTrackerEntries] = useState<Record<string, boolean>>({});
  const [settingsTrackerPanels, setSettingsTrackerPanels] = useState<Record<string, boolean>>({});
  const [defaultTrackersPanelOpen, setDefaultTrackersPanelOpen] = useState(false);
  const [settingsLoading, setSettingsLoading] = useState(false);
  const [settingsError, setSettingsError] = useState("");
  const [settingsSaved, setSettingsSaved] = useState("");
  const [settingsDirty, setSettingsDirty] = useState(false);
  const [settingsSection, setSettingsSection] = useState(settingsSections[0].key);
  const [settingsAdvanced, setSettingsAdvanced] = useState<Record<string, boolean>>({});
  const [sensitiveValues, setSensitiveValues] = useState<Record<string, string>>({});
  const settingsMutationVersion = useRef(0);
  const activationPollRevision = useRef(0);
  const configLoadRevision = useRef(0);
  const configReadController = useRef<AbortController | null>(null);

  useEffect(
    () => () => {
      activationPollRevision.current += 1;
      configLoadRevision.current += 1;
      configReadController.current?.abort();
    },
    [],
  );

  const configuredImageHosts = useMemo(() => {
    if (
      !settingsConfigData ||
      !settingsConfigData.ImageHosting ||
      typeof settingsConfigData.ImageHosting !== "object"
    ) {
      return [] as string[];
    }
    if (Array.isArray(settingsConfigData.ImageHosting)) {
      return [] as string[];
    }
    const imageCfg = settingsConfigData.ImageHosting as ConfigMap;
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
  }, [settingsConfigData]);

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
      !settingsConfigData ||
      !settingsConfigData.TorrentClients ||
      typeof settingsConfigData.TorrentClients !== "object" ||
      Array.isArray(settingsConfigData.TorrentClients)
    ) {
      return [];
    }

    return Object.entries(settingsConfigData.TorrentClients as ConfigMap)
      .filter(
        ([name, value]) =>
          name.trim() !== "" && value && typeof value === "object" && !Array.isArray(value),
      )
      .map(([name]) => ({ value: name, label: name }));
  }, [settingsConfigData]);

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

  const markSettingsChanged = () => {
    settingsMutationVersion.current += 1;
    setSettingsDirty(true);
    setSettingsSaved("");
  };

  const buildSavePayload = () => {
    if (!settingsConfigData) {
      return null;
    }
    const restored = normalizeTrackersForSave(
      normalizeTorrentClientsForSave(restoreSensitiveConfig(settingsConfigData, sensitiveValues)),
      trackerCatalog,
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
        return imageHostOptions;
      }

      const policyHosts = imageHostPolicyMetadata.TrackerUploadHosts?.[trackerKey];
      const fallbackHosts =
        imageHostPolicyMetadata.UploadHosts?.map((host) => normalizeImageHostValue(host)) ??
        imageHostOptions.filter((option) => option.value).map((option) => option.value);
      const ownerByHost = imageHostPolicyMetadata.OwnedHosts ?? {};
      const imageCfg =
        settingsConfigData?.ImageHosting &&
        typeof settingsConfigData.ImageHosting === "object" &&
        !Array.isArray(settingsConfigData.ImageHosting)
          ? (settingsConfigData.ImageHosting as ConfigMap)
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
        Array.from(policyHostSet).every((host) => conditionalImageHostEnabledKeys[host]);
      const hosts = globalHosts.filter(
        (host) => policyAllowsGlobalFallback || (policyHasGlobalHosts && policyHostSet.has(host)),
      );

      (policyHosts ?? []).forEach((host) => {
        const normalizedHost = normalizeImageHostValue(host);
        const owner = ownerByHost[normalizedHost];
        if (!owner || owner.trim().toUpperCase() !== trackerKey) {
          return;
        }
        const enabledKey = conditionalImageHostEnabledKeys[normalizedHost];
        if (enabledKey && !Boolean(imageCfg?.[enabledKey])) {
          return;
        }
        if (!hosts.includes(normalizedHost)) {
          hosts.push(normalizedHost);
        }
      });

      return buildImageHostOptions(hosts);
    },
    [buildImageHostOptions, configuredImageHosts, imageHostPolicyMetadata, settingsConfigData],
  );

  const updateConfigValue = (path: string[], value: ConfigValue) => {
    if (!settingsConfigData) return;
    markSettingsChanged();
    setSettingsConfigData((prev) => {
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

  const removeConfigKey = (path: string[], key: string) => {
    if (!settingsConfigData) return;
    markSettingsChanged();
    setSettingsConfigData((prev) => {
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

      if (path.length === 1 && path[0] === "TorrentClients") {
        const removedName = key.trim().toLowerCase();
        const remainingNames = Object.keys(cursor);
        const isRemovedReference = (value: ConfigValue) => {
          if (typeof value !== "string" || value.trim().toLowerCase() !== removedName) {
            return false;
          }
          const selected = value.trim();
          const exactMatches = remainingNames.filter((name) => name.trim() === selected).length;
          if (exactMatches > 0) {
            return exactMatches !== 1;
          }
          return (
            remainingNames.filter((name) => name.trim().toLowerCase() === selected.toLowerCase())
              .length !== 1
          );
        };

        const clientSetup = clone.ClientSetup;
        if (clientSetup && typeof clientSetup === "object" && !Array.isArray(clientSetup)) {
          if (isRemovedReference(clientSetup.DefaultClient)) {
            clientSetup.DefaultClient = "";
          }
          for (const field of ["InjectClients", "SearchClients"]) {
            const selected = clientSetup[field];
            if (Array.isArray(selected)) {
              clientSetup[field] = selected.filter((value) => !isRemovedReference(value));
            }
          }
        }

        const trackers = clone.Trackers;
        const trackerEntries =
          trackers && typeof trackers === "object" && !Array.isArray(trackers)
            ? trackers.Trackers
            : null;
        if (
          trackerEntries &&
          typeof trackerEntries === "object" &&
          !Array.isArray(trackerEntries)
        ) {
          Object.values(trackerEntries).forEach((tracker) => {
            if (
              tracker &&
              typeof tracker === "object" &&
              !Array.isArray(tracker) &&
              isRemovedReference(tracker.TorrentClient)
            ) {
              tracker.TorrentClient = "";
            }
          });
        }
      }
      return clone;
    });
  };

  const addConfigKey = (path: string[], key: string, value: ConfigValue) => {
    if (!settingsConfigData || !key.trim()) return;
    markSettingsChanged();
    setSettingsConfigData((prev) => {
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
      return clone;
    });
  };

  // Superseded reads, including their errors, cannot publish an older configuration.
  const loadLatestConfig = useCallback(async () => {
    const loadRevision = configLoadRevision.current + 1;
    configLoadRevision.current = loadRevision;
    configReadController.current?.abort();
    const controller = new AbortController();
    configReadController.current = controller;
    try {
      const result = await configClient.get(controller.signal);
      if (configLoadRevision.current !== loadRevision) return null;
      const masked = maskSensitiveConfig(JSON.parse(result) as ConfigMap);
      queryClient.setQueryData(activeConfigKey, masked);
      return masked;
    } catch (err) {
      if (configLoadRevision.current !== loadRevision) return null;
      throw err;
    }
  }, [queryClient]);

  const loadSettingsData = useCallback(async () => {
    clearSettingsStatus();
    const mutationVersion = settingsMutationVersion.current;
    setSettingsLoading(true);
    try {
      const masked = await loadLatestConfig();
      if (!masked) return;
      if (settingsMutationVersion.current === mutationVersion) {
        setSettingsConfigData(masked.masked);
        setSensitiveValues(masked.originals);
        setDraftTrackerEntries({});
        setSettingsDirty(false);
      }
    } catch (err) {
      setSettingsError(String(err));
    } finally {
      setSettingsLoading(false);
    }
  }, [clearSettingsStatus, loadLatestConfig]);

  const startConfigActivationMonitor = useCallback(
    (
      initialActivation: ConfigActivation | null,
      mutationVersion: number,
      restoreDraftOnFailure = false,
      invalidateCatalogWhenActive = false,
    ) => {
      const pollRevision = activationPollRevision.current + 1;
      activationPollRevision.current = pollRevision;
      void (async () => {
        let current = initialActivation;
        let sawPending = current?.status === "pending";
        const refreshOnActive = initialActivation === null;
        let pollError = "";
        const waitForNextPoll = () =>
          new Promise<void>((resolve) =>
            window.setTimeout(resolve, configActivationPollIntervalMS),
          );
        const clearPollError = () => {
          if (!pollError) return;
          const resolvedError = pollError;
          setSettingsError((existing) => (existing === resolvedError ? "" : existing));
          pollError = "";
        };

        while (activationPollRevision.current === pollRevision) {
          if (!current) {
            try {
              current = await configClient.getActivation();
              if (activationPollRevision.current !== pollRevision) return;
              clearPollError();
            } catch (err) {
              if (activationPollRevision.current !== pollRevision) return;
              pollError = String(err);
              setSettingsError(pollError);
              await waitForNextPoll();
              continue;
            }
          }

          if (current.status === "active") {
            if (sawPending || refreshOnActive) {
              try {
                const masked = await loadLatestConfig();
                if (activationPollRevision.current !== pollRevision || !masked) return;
                if (settingsMutationVersion.current === mutationVersion) {
                  setSettingsConfigData(masked.masked);
                  setSensitiveValues(masked.originals);
                  setDraftTrackerEntries({});
                }
                clearPollError();
              } catch (err) {
                if (activationPollRevision.current !== pollRevision) return;
                pollError = String(err);
                setSettingsError(pollError);
                await waitForNextPoll();
                continue;
              }
            }
            if (sawPending || invalidateCatalogWhenActive) {
              void queryClient.invalidateQueries({ queryKey: trackerCatalogKey });
            }
            if (sawPending) {
              setSettingsSaved(
                settingsMutationVersion.current === mutationVersion
                  ? "Settings saved and applied."
                  : "Earlier changes applied. Newer edits remain unsaved.",
              );
            }
            return;
          }

          if (current.status === "failed") {
            clearPollError();
            setSettingsSaved("");
            setSettingsError(configActivationFailureMessage(current.failureCode));
            if (restoreDraftOnFailure && settingsMutationVersion.current === mutationVersion) {
              setSettingsDirty(true);
            }
            return;
          }

          sawPending = true;
          setSettingsSaved(
            settingsMutationVersion.current === mutationVersion
              ? "Settings saved. Waiting for the active input to close before applying."
              : "Earlier changes saved. Newer edits remain unsaved.",
          );
          await waitForNextPoll();
          current = null;
        }
      })();
    },
    [loadLatestConfig, queryClient],
  );

  const loadSettings = useCallback(
    (invalidateCatalogWhenActive = false) => {
      startConfigActivationMonitor(
        null,
        settingsMutationVersion.current,
        false,
        invalidateCatalogWhenActive,
      );
      return loadSettingsData();
    },
    [loadSettingsData, startConfigActivationMonitor],
  );

  const handleSaveSettings = async () => {
    clearSettingsStatus();
    const saveConfig = configClient.save;
    const mutationVersion = settingsMutationVersion.current;
    const payload = buildSavePayload();
    if (!payload) {
      setSettingsError("Settings are not loaded.");
      return;
    }
    setSettingsLoading(true);
    try {
      const activation = await saveConfig(payload);
      const masked = maskSensitiveConfig(JSON.parse(payload) as ConfigMap);
      let activeRefreshRevision = 0;
      if (activation.status === "active") {
        activeRefreshRevision = activationPollRevision.current + 1;
        activationPollRevision.current = activeRefreshRevision;
        queryClient.setQueryData(activeConfigKey, masked);
        void queryClient.invalidateQueries({ queryKey: trackerCatalogKey });
      }
      if (settingsMutationVersion.current === mutationVersion) {
        setSettingsConfigData(masked.masked);
        setSensitiveValues(masked.originals);
        if (activation.status === "active") {
          markSettingsSaved("Settings saved and applied.");
        } else if (activation.status === "pending") {
          markSettingsSaved(
            "Settings saved. Waiting for the active input to close before applying.",
          );
        } else {
          setSettingsSaved("");
          setSettingsDirty(true);
          setSettingsError(configActivationFailureMessage(activation.failureCode));
        }
      } else if (activation.status === "failed") {
        setSettingsError(configActivationFailureMessage(activation.failureCode));
      } else {
        setSettingsSaved("Earlier changes saved. Newer edits remain unsaved.");
      }
      if (activation.status === "pending") {
        startConfigActivationMonitor(activation, mutationVersion, true);
      } else if (activation.status === "active") {
        try {
          const activeConfig = await loadLatestConfig();
          if (activationPollRevision.current !== activeRefreshRevision || !activeConfig) {
            return;
          }
          if (settingsMutationVersion.current === mutationVersion) {
            setSettingsConfigData(activeConfig.masked);
            setSensitiveValues(activeConfig.originals);
            setDraftTrackerEntries({});
          }
        } catch (err) {
          if (activationPollRevision.current !== activeRefreshRevision) return;
          setSettingsError(String(err));
        }
      } else {
        activationPollRevision.current += 1;
      }
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
      !settingsConfigData
    ) {
      loadSettings();
    }
  }, [activeTab, loadSettings, settingsConfigData]);

  useEffect(() => {
    try {
      trackerCatalog?.entries.forEach((entry) => {
        entry.fields.forEach((field) => trackerFieldPresentation(field.key));
      });
    } catch (error) {
      setSettingsError(String(error));
    }
  }, [trackerCatalog]);

  useEffect(() => {
    if (trackerCatalogError) setSettingsError(trackerCatalogError);
  }, [trackerCatalogError]);

  useEffect(() => {
    if (imageHostPolicyQuery.error) setSettingsError(String(imageHostPolicyQuery.error));
  }, [imageHostPolicyQuery.error]);

  const advancedOpen = settingsAdvanced[settingsSection] ?? false;
  const showAdvancedToggle = (() => {
    if (settingsSection === "trackers") {
      return (trackerCatalog?.entries ?? []).some((entry) =>
        entry.fields.some((field) => trackerFieldPresentation(field.key).advanced),
      );
    }
    const section = settingsSections.find((item) => item.key === settingsSection);
    if (!section) return false;
    const meta = effectiveSectionFieldMeta[section.jsonKey];
    if (!meta) return false;
    return Object.values(meta).some((field) => field.advanced);
  })();

  const isTrackerConfigured = useCallback(
    (entry: TrackerCatalogEntry, trackerValue: ConfigMap): boolean =>
      entry.fields.some(
        (field) =>
          field.activation && hasConfiguredTrackerValue(trackerValue[field.key], field.default),
      ),
    [],
  );

  const settingsTrackerSelectionNames = useMemo(() => {
    return selectConfiguredTrackerNames(
      settingsConfigData,
      trackerCatalog,
      isTrackerConfigured,
      draftTrackerEntries,
    );
  }, [draftTrackerEntries, isTrackerConfigured, settingsConfigData, trackerCatalog]);

  const trackerSelectionNames = useMemo(
    () => selectConfiguredTrackerNames(configData, trackerCatalog, isTrackerConfigured),
    [configData, isTrackerConfigured, trackerCatalog],
  );

  const editorContext: SettingsRenderContext = {
    settingsConfigData,
    updateConfigValue,
    markSettingsChanged,
    addConfigKey,
    removeConfigKey,
    effectiveSectionFieldMeta,
    sectionFieldMeta,
    imageHostOptions,
    imageHostKeyMap,
    conditionalImageHostEnabledKeys,
    trackerAddSelection,
    setTrackerAddSelection,
    setDraftTrackerEntries,
    settingsTrackerPanels,
    setSettingsTrackerPanels,
    defaultTrackersPanelOpen,
    setDefaultTrackersPanelOpen,
    settingsTrackerSelectionNames,
    trackerCatalog,
    imageHostPolicyMetadata,
    torrentClientOptions,
    trackerOptionsForImageHost,
    trackerConfigValue,
    normalizeImageHostValue,
    removeUnsupported,
  };

  return {
    configData,
    settingsConfigData,
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
    editorContext,
    sectionFieldMeta: effectiveSectionFieldMeta,
    updateConfigValue,
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
    settingsTrackerSelectionNames,
  };
};
