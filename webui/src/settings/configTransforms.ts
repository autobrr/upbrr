// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ConfigMap, ConfigValue, TrackerCatalog } from "../types";

export const REDACTED_VALUE = "[REDACTED]";
const ENCRYPTED_SECRET_PREFIX = "upbrr-enc:v1:";

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

export const isSensitiveKeyName = (key: string) => {
  const lower = key.toLowerCase();
  return sensitiveKeyHints.some((hint) => lower.includes(hint));
};

export const buildPathKey = (path: string[]) => path.join(".");

const isEncryptedSecretEnvelope = (value: string) => value.startsWith(ENCRYPTED_SECRET_PREFIX);

/** Masks secret-bearing config strings while retaining originals by config path for save payloads. */
export const maskSensitiveConfig = (input: ConfigMap) => {
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

export const restoreSensitiveConfig = (input: ConfigMap, originals: Record<string, string>) => {
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

export const qbitDefaultClient = (): ConfigMap => ({
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

export const normalizeStringArray = (value: ConfigValue) => {
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

export const normalizeTorrentClientType = (client: ConfigMap) => {
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

/** Filters supported trackers to catalog fields and removes all legacy endpoint overrides. */
export const normalizeTrackersForSave = (input: ConfigMap, catalog: TrackerCatalog | null) => {
  const trackerRoot = input.Trackers;
  if (!trackerRoot || typeof trackerRoot !== "object" || Array.isArray(trackerRoot)) {
    return input;
  }
  const trackerEntries = (trackerRoot as ConfigMap).Trackers;
  if (!trackerEntries || typeof trackerEntries !== "object" || Array.isArray(trackerEntries)) {
    return input;
  }

  let changed = false;
  const catalogByName = new Map(
    (catalog?.entries ?? []).map((entry) => [entry.name.trim().toUpperCase(), entry]),
  );
  const nextEntries: ConfigMap = {};
  Object.entries(trackerEntries as ConfigMap).forEach(([name, value]) => {
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      nextEntries[name] = value;
      return;
    }
    const supported = catalogByName.get(name.trim().toUpperCase());
    const allowed = supported ? new Set(supported.fields.map((field) => field.key)) : null;
    const next: ConfigMap = {};
    Object.entries(value as ConfigMap).forEach(([key, fieldValue]) => {
      if (key.trim().toLowerCase() === "url") {
        changed = true;
        return;
      }
      if (allowed && !allowed.has(key) && key !== "Internal") {
        changed = true;
        return;
      }
      next[key] = fieldValue;
    });
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
