// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { pageStyle } from "../components/ui/pageStyle";
import { useEffect, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { Button } from "../components/ui/button";
import { PillCheckbox } from "../components/ui/checkbox";
import { Select } from "../components/ui/select";
import { Switch } from "../components/ui/switch";
import { trackerFieldPresentation } from "./trackerFields";
import { settingsStyle } from "./style";
import { SettingsFieldGroups } from "./FieldGroups";
import { formatLabel, normalizeDefaultTrackerList } from "../utils/settings";
import {
  nextQbitDirectState,
  normalizeStringArray,
  normalizeTorrentClientType,
  qbitDefaultClient,
} from "./configTransforms";
import type {
  ConfigMap,
  ConfigValue,
  FieldMeta,
  ImageHostPolicyMetadata,
  TrackerCatalog,
  TrackerCatalogEntry,
} from "../types";

const settingsInputClass =
  "min-h-9 rounded-md border border-input bg-card px-3 py-1.5 text-sm text-card-foreground outline-none transition placeholder:text-muted-foreground focus:border-ring focus:ring-[3px] focus:ring-ring/50";

type FieldOption = NonNullable<FieldMeta["options"]>[number];

const normalizeCommaSeparatedGroups = (value: string): string[] => {
  const seen = new Set<string>();
  const groups: string[] = [];
  value.split(",").forEach((item) => {
    const group = item.trim().replace(/^-/, "").trim();
    const key = group.toLowerCase();
    if (group === "" || seen.has(key)) return;
    seen.add(key);
    groups.push(group);
  });
  return groups;
};

type CommaSeparatedInputProps = {
  label: string;
  value: ConfigValue[];
  onChange: (value: string[]) => void;
  onInput: () => void;
};

const CommaSeparatedInput = ({ label, value, onChange, onInput }: CommaSeparatedInputProps) => {
  const serialized = value.map((item) => String(item ?? "")).join(", ");
  const [draft, setDraft] = useState(serialized);
  useEffect(() => setDraft(serialized), [serialized]);

  return (
    <input
      aria-label={label}
      className={settingsInputClass}
      type="text"
      value={draft}
      onChange={(event) => {
        setDraft(event.target.value);
        onInput();
      }}
      onBlur={() => {
        const normalized = normalizeCommaSeparatedGroups(draft);
        setDraft(normalized.join(", "));
        if (
          value.length !== normalized.length ||
          value.some((item, index) => String(item ?? "") !== normalized[index])
        ) {
          onChange(normalized);
        }
      }}
    />
  );
};

export type SettingsRenderContext = {
  settingsConfigData: ConfigMap | null;
  updateConfigValue: (path: string[], value: ConfigValue) => void;
  markSettingsChanged: () => void;
  addConfigKey: (path: string[], key: string, value: ConfigValue) => void;
  removeConfigKey: (path: string[], key: string) => void;
  effectiveSectionFieldMeta: Record<string, Record<string, FieldMeta>>;
  sectionFieldMeta: Record<string, Record<string, FieldMeta>>;
  imageHostOptions: FieldOption[];
  imageHostKeyMap: Record<string, string[]>;
  conditionalImageHostEnabledKeys: Record<string, string>;
  trackerAddSelection: string;
  setTrackerAddSelection: Dispatch<SetStateAction<string>>;
  setDraftTrackerEntries: Dispatch<SetStateAction<Record<string, boolean>>>;
  settingsTrackerPanels: Record<string, boolean>;
  setSettingsTrackerPanels: Dispatch<SetStateAction<Record<string, boolean>>>;
  defaultTrackersPanelOpen: boolean;
  setDefaultTrackersPanelOpen: Dispatch<SetStateAction<boolean>>;
  settingsTrackerSelectionNames: string[];
  trackerCatalog: TrackerCatalog | null;
  imageHostPolicyMetadata: ImageHostPolicyMetadata | null;
  torrentClientOptions: FieldOption[];
  trackerOptionsForImageHost: (trackerName: string) => FieldOption[];
  trackerConfigValue: (entries: ConfigMap, name: string) => ConfigMap | null;
  normalizeImageHostValue: (value: string) => string;
  removeUnsupported: (name: string) => void;
};

/** Presentation helpers for schema-driven settings fields and sections. */
export const createSettingsRenderers = (context: SettingsRenderContext) => {
  const {
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
  } = context;

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
      <div className={settingsStyle.map}>
        {value.map((entry, index) => (
          <div className={settingsStyle.header} key={`${path.join(".")}-${index}`}>
            {options.length > 0 ? (
              <Select
                aria-label={displayLabel ? `${displayLabel} ${index + 1}` : undefined}
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
              </Select>
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
              aria-label={
                displayLabel ? `Remove ${displayLabel} ${index + 1}` : `Remove item ${index + 1}`
              }
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
      if (meta?.commaSeparated) {
        return (
          <label className={settingsStyle.field} key={path.join(".")}>
            <span>{displayLabel}</span>
            <CommaSeparatedInput
              label={displayLabel}
              value={value}
              onChange={(next) => updateConfigValue(path, next)}
              onInput={markSettingsChanged}
            />
          </label>
        );
      }
      return (
        <div className={settingsStyle.field} key={path.join(".")}>
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
        <label className={settingsStyle.field} key={path.join(".")}>
          <span>{displayLabel}</span>
          <Select
            value={selectedValue}
            onChange={(event) => updateConfigValue(path, event.target.value)}
          >
            {options.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </Select>
        </label>
      );
    }
    if (typeHint === "boolean" || typeof value === "boolean") {
      return (
        <label className={settingsStyle.switchRow} key={path.join(".")}>
          <span>{displayLabel}</span>
          <Switch
            aria-label={displayLabel}
            checked={Boolean(value)}
            onChange={(event) => updateConfigValue(path, event.target.checked)}
          />
        </label>
      );
    }
    if (typeHint === "number" || typeof value === "number") {
      const numericValue = typeof value === "number" && Number.isFinite(value) ? value : 0;
      return (
        <label className={settingsStyle.field} key={path.join(".")}>
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
        <div className={settingsStyle.subgroup} key={path.join(".")}>
          <div className={settingsStyle.title}>{displayLabel}</div>
          <div className={settingsStyle.grid}>
            {Object.entries(value).map(([childKey, childValue]) =>
              renderField(childKey, childValue, [...path, childKey]),
            )}
          </div>
        </div>
      );
    }

    const descriptionHeader =
      path[0] === "Description" &&
      [
        "TonemappedHeader",
        "CustomDescriptionHeader",
        "ScreenshotHeader",
        "DiscMenuHeader",
        "CustomSignature",
      ].includes(label);
    return (
      <label
        className={`${settingsStyle.field}${descriptionHeader ? " col-span-full" : ""}`}
        key={path.join(".")}
      >
        <span>{displayLabel}</span>
        {descriptionHeader ? (
          <textarea
            className={`${settingsInputClass} w-full resize-y`}
            rows={3}
            value={value === null ? "" : String(value ?? "")}
            onChange={(event) => updateConfigValue(path, event.target.value)}
          />
        ) : (
          <input
            className={settingsInputClass}
            value={value === null ? "" : String(value ?? "")}
            onChange={(event) => updateConfigValue(path, event.target.value)}
          />
        )}
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
      <div className={settingsStyle.map}>
        {defaultKey ? (
          <div className={settingsStyle.subgroup}>
            <div className={settingsStyle.title}>{formatLabel(defaultKey)}</div>
            {renderField(defaultKey, sectionValue[defaultKey] as ConfigValue, [
              sectionKey,
              defaultKey,
            ])}
          </div>
        ) : null}

        <div className={settingsStyle.header}>
          <p className={pageStyle.label}>Entries</p>
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

        <div className={settingsStyle.map}>
          {entries.length === 0 ? (
            <p className="text-muted-foreground">No entries yet.</p>
          ) : (
            entries.map(([key, value]) => (
              <div className={settingsStyle.card} key={`${sectionKey}-${key}`}>
                <div className={settingsStyle.header}>
                  <p className={pageStyle.value}>{key}</p>
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
                <div className={settingsStyle.grid}>
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
      !settingsConfigData ||
      !settingsConfigData.TorrentClients ||
      typeof settingsConfigData.TorrentClients !== "object" ||
      Array.isArray(settingsConfigData.TorrentClients)
    ) {
      return null;
    }

    const clients = Object.entries(settingsConfigData.TorrentClients as ConfigMap).filter(
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
      <div className={settingsStyle.map}>
        <div className={settingsStyle.header}>
          <p className={pageStyle.label}>Entries</p>
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

        <div className={settingsStyle.map}>
          {clients.length === 0 ? (
            <p className="text-muted-foreground">No entries yet.</p>
          ) : (
            clients.map(([name, client]) => {
              const clientType = normalizeTorrentClientType(client);
              const watchClient = clientType === "watch";
              const directEnabled = !watchClient && hasDirectConfig(client);
              return (
                <div
                  className={settingsStyle.card}
                  key={`TorrentClients-${name}`}
                  role="group"
                  aria-labelledby={`torrent-client-${encodeURIComponent(name)}`}
                >
                  <div className={settingsStyle.header}>
                    <p
                      className={pageStyle.value}
                      id={`torrent-client-${encodeURIComponent(name)}`}
                    >
                      {name}
                    </p>
                    <Button
                      type="button"
                      aria-label={`Remove ${name}`}
                      onClick={() => removeConfigKey(["TorrentClients"], name)}
                    >
                      Remove
                    </Button>
                  </div>
                  <SettingsFieldGroups
                    section="TorrentClients"
                    fields={[
                      [
                        "Type",
                        renderField(
                          "Type",
                          valueFor(client, "Type", "TorrentClient") ?? "qbit",
                          ["TorrentClients", name, "Type"],
                          meta.Type,
                        ),
                      ],
                      [
                        "WatchFolder",
                        watchClient
                          ? renderField(
                              "WatchFolder",
                              client.WatchFolder ?? "",
                              ["TorrentClients", name, "WatchFolder"],
                              meta.WatchFolder,
                            )
                          : null,
                      ],
                      [
                        "StorageDir",
                        watchClient
                          ? renderField(
                              "StorageDir",
                              client.StorageDir ?? "",
                              ["TorrentClients", name, "StorageDir"],
                              meta.StorageDir,
                            )
                          : null,
                      ],
                      [
                        "QuiProxyURL",
                        !watchClient
                          ? renderField(
                              "QuiProxyURL",
                              client.QuiProxyURL ?? "",
                              ["TorrentClients", name, "QuiProxyURL"],
                              meta.QuiProxyURL,
                            )
                          : null,
                      ],
                      [
                        "QbitCategoryValue",
                        !watchClient
                          ? renderField(
                              "QbitCategoryValue",
                              valueFor(client, "QbitCategoryValue", "Category") ?? "",
                              ["TorrentClients", name, "QbitCategoryValue"],
                              meta.QbitCategoryValue,
                            )
                          : null,
                      ],
                      [
                        "QbitTag",
                        !watchClient
                          ? renderField(
                              "QbitTag",
                              qbitTagFor(client),
                              ["TorrentClients", name, "QbitTag"],
                              meta.QbitTag,
                            )
                          : null,
                      ],
                      [
                        "QbitCrossCategory",
                        !watchClient
                          ? renderField(
                              "QbitCrossCategory",
                              client.QbitCrossCategory ?? "",
                              ["TorrentClients", name, "QbitCrossCategory"],
                              meta.QbitCrossCategory,
                            )
                          : null,
                      ],
                      [
                        "QbitCrossTag",
                        !watchClient
                          ? renderField(
                              "QbitCrossTag",
                              client.QbitCrossTag ?? "",
                              ["TorrentClients", name, "QbitCrossTag"],
                              meta.QbitCrossTag,
                            )
                          : null,
                      ],
                      [
                        "UseTrackerAsTag",
                        !watchClient
                          ? renderField(
                              "UseTrackerAsTag",
                              client.UseTrackerAsTag ?? false,
                              ["TorrentClients", name, "UseTrackerAsTag"],
                              meta.UseTrackerAsTag,
                            )
                          : null,
                      ],
                      [
                        "Linking",
                        !watchClient
                          ? renderField(
                              "Linking",
                              client.Linking ?? "",
                              ["TorrentClients", name, "Linking"],
                              meta.Linking,
                            )
                          : null,
                      ],
                      [
                        "AllowFallback",
                        !watchClient ? (
                          <label
                            className={settingsStyle.switchRow}
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
                          </label>
                        ) : null,
                      ],
                      [
                        "LinkedFolder",
                        !watchClient
                          ? renderField(
                              "LinkedFolder",
                              arrayFor(client, "LinkedFolder"),
                              ["TorrentClients", name, "LinkedFolder"],
                              meta.LinkedFolder,
                            )
                          : null,
                      ],
                      [
                        "LocalPath",
                        !watchClient
                          ? renderField(
                              "LocalPath",
                              arrayFor(client, "LocalPath"),
                              ["TorrentClients", name, "LocalPath"],
                              meta.LocalPath,
                            )
                          : null,
                      ],
                      [
                        "RemotePath",
                        !watchClient
                          ? renderField(
                              "RemotePath",
                              arrayFor(client, "RemotePath"),
                              ["TorrentClients", name, "RemotePath"],
                              meta.RemotePath,
                            )
                          : null,
                      ],
                      [
                        "AutomaticManagementPaths",
                        !watchClient
                          ? renderField(
                              "AutomaticManagementPaths",
                              arrayFor(client, "AutomaticManagementPaths"),
                              ["TorrentClients", name, "AutomaticManagementPaths"],
                              meta.AutomaticManagementPaths,
                            )
                          : null,
                      ],
                      [
                        "VerifyWebUICertificate",
                        !watchClient && advancedOpen
                          ? renderField(
                              "VerifyWebUICertificate",
                              client.VerifyWebUICertificate ?? true,
                              ["TorrentClients", name, "VerifyWebUICertificate"],
                              meta.VerifyWebUICertificate,
                            )
                          : null,
                      ],
                      [
                        "QbitDirect",
                        !watchClient ? (
                          <label
                            className={`${settingsStyle.switchRow} col-span-full border-t border-foreground/10 pt-4`}
                            key={`TorrentClients-${name}-QbitDirect`}
                          >
                            <span>qBit direct</span>
                            <Switch
                              aria-label="qBit direct"
                              checked={directEnabled}
                              onChange={(event) => setQbitDirect(name, event.target.checked)}
                            />
                          </label>
                        ) : null,
                      ],
                      [
                        "QbitURL",
                        !watchClient && directEnabled
                          ? renderField(
                              "QbitURL",
                              valueFor(client, "QbitURL", "URL") ?? "",
                              ["TorrentClients", name, "QbitURL"],
                              meta.QbitURL,
                            )
                          : null,
                      ],
                      [
                        "QbitPort",
                        !watchClient && directEnabled
                          ? renderField(
                              "QbitPort",
                              client.QbitPort ?? 0,
                              ["TorrentClients", name, "QbitPort"],
                              meta.QbitPort,
                            )
                          : null,
                      ],
                      [
                        "QbitUser",
                        !watchClient && directEnabled
                          ? renderField(
                              "QbitUser",
                              valueFor(client, "QbitUser", "Username") ?? "",
                              ["TorrentClients", name, "QbitUser"],
                              meta.QbitUser,
                            )
                          : null,
                      ],
                      [
                        "QbitPass",
                        !watchClient && directEnabled
                          ? renderField(
                              "QbitPass",
                              valueFor(client, "QbitPass", "Password") ?? "",
                              ["TorrentClients", name, "QbitPass"],
                              meta.QbitPass,
                            )
                          : null,
                      ],
                    ]}
                  />
                </div>
              );
            })
          )}
        </div>
      </div>
    );
  };

  /** Reports configured state from backend-owned activation markers. */
  const renderTrackerSection = (advancedOpen: boolean) => {
    try {
      if (
        !settingsConfigData ||
        !settingsConfigData.Trackers ||
        typeof settingsConfigData.Trackers !== "object" ||
        Array.isArray(settingsConfigData.Trackers)
      ) {
        return null;
      }

      const trackerRoot = settingsConfigData.Trackers as ConfigMap;
      const defaultTrackers = (trackerRoot.DefaultTrackers as ConfigValue) ?? [];
      const rawEntries = trackerRoot.Trackers;
      const entriesRoot =
        rawEntries && typeof rawEntries === "object" && !Array.isArray(rawEntries)
          ? (rawEntries as ConfigMap)
          : {};

      const visibleTrackerSet = new Set(settingsTrackerSelectionNames);
      const catalogEntries = trackerCatalog?.entries ?? [];
      const visibleEntries = catalogEntries
        .filter((entry) => visibleTrackerSet.has(entry.name))
        .map((entry) => ({ entry, value: trackerConfigValue(entriesRoot, entry.name) ?? {} }));

      const normalizedDefaultTrackers = normalizeDefaultTrackerList(defaultTrackers);
      const selectedDefaultTrackerNames = new Set(
        normalizedDefaultTrackers.map((name) => name.toLowerCase()),
      );
      const preferredTrackerRaw = trackerRoot.PreferredTracker;
      const preferredTracker =
        typeof preferredTrackerRaw === "string" ? preferredTrackerRaw.trim() : "";
      const trackerNames = settingsTrackerSelectionNames;
      const selectedDefaultTrackerCount = trackerNames.filter((name) =>
        selectedDefaultTrackerNames.has(name.toLowerCase()),
      ).length;
      const trackerOptions = catalogEntries.map((entry) => entry.name);
      const availableTrackers = trackerOptions.filter((name) => !visibleTrackerSet.has(name));
      const trackerClientOptions = [{ value: "", label: "" }, ...torrentClientOptions];
      const imageCfg =
        settingsConfigData.ImageHosting &&
        typeof settingsConfigData.ImageHosting === "object" &&
        !Array.isArray(settingsConfigData.ImageHosting)
          ? (settingsConfigData.ImageHosting as ConfigMap)
          : null;

      const trackerHasEnabledOwnedImageHost = (trackerName: string) => {
        const trackerKey = trackerName.trim().toUpperCase();
        const ownerByHost = imageHostPolicyMetadata?.OwnedHosts ?? {};
        return (imageHostPolicyMetadata?.TrackerUploadHosts?.[trackerKey] ?? []).some((host) => {
          const normalizedHost = normalizeImageHostValue(host);
          const enabledKey = conditionalImageHostEnabledKeys[normalizedHost];
          return (
            ownerByHost[normalizedHost]?.trim().toUpperCase() === trackerKey &&
            Boolean(enabledKey && imageCfg?.[enabledKey])
          );
        });
      };

      const trackerSchemaFor = (entry: TrackerCatalogEntry) => {
        const fields = trackerHasEnabledOwnedImageHost(entry.name)
          ? entry.fields.filter((field) => field.key !== "ImageHost")
          : entry.fields;
        return fields.map((field) => {
          const base = trackerFieldPresentation(field.key);
          if (field.key === "ImageHost") {
            return { ...base, options: trackerOptionsForImageHost(entry.name) };
          }
          if (field.key === "TorrentClient") {
            return { ...base, options: trackerClientOptions };
          }
          return base;
        });
      };

      const buildTrackerDefaults = (entry: TrackerCatalogEntry) => {
        const defaults: ConfigMap = {};
        entry.fields.forEach((field) => {
          defaults[field.key] = structuredClone(field.default);
        });
        return defaults;
      };

      const toggleDefaultTracker = (name: string, enabled: boolean) => {
        const current = normalizeDefaultTrackerList(defaultTrackers);
        const next = current.filter((entry) => entry.toLowerCase() !== name.toLowerCase());
        if (enabled) {
          next.push(name);
        }
        updateConfigValue(["Trackers", "DefaultTrackers"], next);
      };

      const updatePreferredTracker = (value: string) => {
        updateConfigValue(["Trackers", "PreferredTracker"], value.trim());
      };

      return (
        <div className={settingsStyle.map}>
          <details
            className={settingsStyle.collapsible}
            open={defaultTrackersPanelOpen}
            onToggle={(event) => {
              const target = event.currentTarget as HTMLDetailsElement;
              setDefaultTrackersPanelOpen(target.open);
            }}
          >
            <summary
              className={`tracker-summary-heading ${settingsStyle.summary} px-2.5 py-2 text-sm`}
            >
              <span>Default trackers</span>
              <span className="tracker-summary-count inline-flex min-w-12 items-center justify-center rounded-full border border-current px-2 py-0.5 text-[0.76rem] font-bold tracking-[0.03em] text-inherit">
                {selectedDefaultTrackerCount}/{trackerNames.length}
              </span>
            </summary>
            <div className="pt-0">
              {trackerNames.length === 0 ? (
                <p className="text-muted-foreground">Add tracker entries to select defaults.</p>
              ) : (
                <fieldset className="m-0 grid min-w-0 gap-2 border-0 p-0">
                  <legend className="sr-only">Default trackers</legend>
                  <div className="grid w-full grid-cols-[repeat(auto-fill,minmax(min(100%,180px),1fr))] gap-2">
                    {trackerNames.map((tracker) => (
                      <PillCheckbox
                        key={tracker}
                        checked={selectedDefaultTrackerNames.has(tracker.toLowerCase())}
                        onCheckedChange={(checked) => toggleDefaultTracker(tracker, checked)}
                      >
                        {tracker}
                      </PillCheckbox>
                    ))}
                  </div>
                </fieldset>
              )}
            </div>
          </details>

          <details className={settingsStyle.collapsible}>
            <summary className={settingsStyle.title}>Preferred tracker data source</summary>
            <div style={{ paddingTop: "0.5rem" }}>
              <div className={settingsStyle.mapControls}>
                <Select
                  aria-label="Preferred tracker data source"
                  value={preferredTracker}
                  onChange={(event) => updatePreferredTracker(event.target.value)}
                >
                  <option value="">None</option>
                  {preferredTracker && !trackerOptions.includes(preferredTracker) ? (
                    <option value={preferredTracker}>{preferredTracker} (saved)</option>
                  ) : null}
                  {trackerOptions.map((name) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                </Select>
                <Button
                  type="button"
                  disabled={preferredTracker === ""}
                  onClick={() => updatePreferredTracker("")}
                >
                  Clear
                </Button>
              </div>
              <p className="text-muted-foreground" style={{ marginTop: "0.5rem" }}>
                Moves the selected tracker to the top of tracker-data lookup and qBit tracker
                priority when present.
              </p>
            </div>
          </details>

          <div className={`settings-map__header ${settingsStyle.header}`}>
            <p className={pageStyle.label}>Entries</p>
            <div className={settingsStyle.mapControls}>
              <Select
                aria-label="Add tracker entry"
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
              </Select>
              <Button
                type="button"
                disabled={!trackerAddSelection}
                onClick={() => {
                  const name = trackerAddSelection.trim();
                  if (!name) return;
                  const entry = catalogEntries.find((candidate) => candidate.name === name);
                  if (!entry) return;
                  if (!trackerConfigValue(entriesRoot, name)) {
                    addConfigKey(["Trackers", "Trackers"], name, buildTrackerDefaults(entry));
                  }
                  setDraftTrackerEntries((prev) => ({ ...prev, [name]: true }));
                  setTrackerAddSelection("");
                  setSettingsTrackerPanels((prev) => ({ ...prev, [name]: true }));
                }}
              >
                Add entry
              </Button>
            </div>
          </div>

          <div className={settingsStyle.map}>
            {visibleEntries.length === 0 ? (
              <p className="text-muted-foreground">No configured entries yet.</p>
            ) : (
              visibleEntries.map(({ entry, value }) => {
                const key = entry.name;
                const schema = trackerSchemaFor(entry);
                return (
                  <details
                    className={settingsStyle.collapsibleCard}
                    key={`Trackers-${key}`}
                    open={settingsTrackerPanels[key] ?? false}
                    onToggle={(event) => {
                      const target = event.currentTarget as HTMLDetailsElement;
                      setSettingsTrackerPanels((prev) => ({ ...prev, [key]: target.open }));
                    }}
                  >
                    <summary className={settingsStyle.summary}>
                      <span className="settings-card__summary-name min-w-0 truncate">{key}</span>
                      <Button
                        type="button"
                        aria-label={`Remove ${key}`}
                        onClick={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                          updateConfigValue(
                            ["Trackers", "Trackers", key],
                            buildTrackerDefaults(entry),
                          );
                          toggleDefaultTracker(key, false);
                          if (preferredTracker.toLowerCase() === key.toLowerCase()) {
                            updatePreferredTracker("");
                          }
                          setDraftTrackerEntries((prev) => {
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
                    <div className={settingsStyle.body}>
                      <SettingsFieldGroups
                        section="Trackers"
                        fields={schema
                          .filter((meta) => !(meta.advanced && !advancedOpen))
                          .map((meta) => [
                            meta.key,
                            renderField(
                              meta.key,
                              value[meta.key] ??
                                entry.fields.find((field) => field.key === meta.key)?.default ??
                                "",
                              ["Trackers", "Trackers", key, meta.key],
                              meta,
                            ),
                          ])}
                      />
                    </div>
                  </details>
                );
              })
            )}
          </div>

          {(trackerCatalog?.unsupported.length ?? 0) > 0 ? (
            <div className={settingsStyle.subgroup}>
              <div className={settingsStyle.title}>Unsupported tracker entries</div>
              <p className="text-muted-foreground">
                Preserved config has no matching tracker implementation and cannot be used.
              </p>
              <div className={settingsStyle.map}>
                {trackerCatalog?.unsupported.map((name) => (
                  <div className={settingsStyle.card} key={`unsupported-${name}`}>
                    <div className={settingsStyle.summary}>
                      <span className="settings-card__summary-name min-w-0 truncate">{name}</span>
                      <Button
                        type="button"
                        aria-label={`Delete ${name}`}
                        onClick={() => {
                          removeConfigKey(["Trackers", "Trackers"], name);
                          removeUnsupported(name);
                        }}
                      >
                        Delete
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          ) : null}
        </div>
      );
    } catch (err) {
      return <p className={pageStyle.error}>Unable to render tracker settings: {String(err)}</p>;
    }
  };

  const renderImageHostingSection = () => {
    if (
      !settingsConfigData ||
      !settingsConfigData.ImageHosting ||
      typeof settingsConfigData.ImageHosting !== "object"
    ) {
      return null;
    }

    const imageCfg = settingsConfigData.ImageHosting as ConfigMap;
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
      <div className={settingsStyle.form}>
        <div className={settingsStyle.subgroup}>
          <div className={settingsStyle.title}>Host Priority</div>
          <div className={settingsStyle.grid}>
            {hostFields.map((field, index) => {
              const selected = String(imageCfg[field] ?? "");
              return (
                <label className={settingsStyle.field} key={field}>
                  <span>{`Host ${index + 1}`}</span>
                  <Select
                    value={selected}
                    onChange={(event) =>
                      updateConfigValue(["ImageHosting", field], event.target.value)
                    }
                  >
                    {selected && !imageHostOptions.some((option) => option.value === selected) ? (
                      <option value={selected}>{selected} (saved)</option>
                    ) : null}
                    {imageHostOptions.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </Select>
                </label>
              );
            })}
          </div>
        </div>

        <div className={settingsStyle.subgroup}>
          <div className={settingsStyle.title}>API Keys</div>
          {requiredKeys.size === 0 ? (
            <p className="text-muted-foreground">Select an image host to edit its API keys.</p>
          ) : (
            <div className={settingsStyle.grid}>
              {Array.from(requiredKeys).map((key) =>
                renderField(key, imageCfg[key] as ConfigValue, ["ImageHosting", key]),
              )}
            </div>
          )}
        </div>

        <div className={settingsStyle.subgroup}>
          <div className={settingsStyle.title}>Additional Hosts</div>
          <div className={settingsStyle.grid}>
            <section className="grid min-w-0 content-start gap-3" aria-label="Lostimg">
              <h3 className="m-0 text-sm font-semibold">Lostimg</h3>
              <label className={settingsStyle.switchRow}>
                <span>Lostimg enabled</span>
                <Switch
                  aria-label="Lostimg enabled"
                  checked={Boolean(imageCfg.LostimgEnabled)}
                  onChange={(event) =>
                    updateConfigValue(["ImageHosting", "LostimgEnabled"], event.target.checked)
                  }
                />
              </label>
              {renderField(
                "LostimgAPI",
                (imageCfg.LostimgAPI as ConfigValue) ?? "",
                ["ImageHosting", "LostimgAPI"],
                sectionFieldMeta.ImageHosting.LostimgAPI,
              )}
            </section>
            <section className="grid min-w-0 content-start gap-3" aria-label="ReelFliX">
              <h3 className="m-0 text-sm font-semibold">ReelFliX</h3>
              <label className={settingsStyle.switchRow}>
                <span>ReelFliX enabled</span>
                <Switch
                  aria-label="ReelFliX enabled"
                  checked={Boolean(imageCfg.ReelflixEnabled)}
                  onChange={(event) =>
                    updateConfigValue(["ImageHosting", "ReelflixEnabled"], event.target.checked)
                  }
                />
              </label>
              {renderField(
                "ReelflixAPI",
                (imageCfg.ReelflixAPI as ConfigValue) ?? "",
                ["ImageHosting", "ReelflixAPI"],
                sectionFieldMeta.ImageHosting.ReelflixAPI,
              )}
            </section>
            <section className="grid min-w-0 content-start gap-3" aria-label="Samaritano">
              <h3 className="m-0 text-sm font-semibold">Samaritano</h3>
              <label className={settingsStyle.switchRow}>
                <span>Samaritano enabled</span>
                <Switch
                  aria-label="Samaritano enabled"
                  checked={Boolean(imageCfg.SamaritanoEnabled)}
                  onChange={(event) =>
                    updateConfigValue(["ImageHosting", "SamaritanoEnabled"], event.target.checked)
                  }
                />
              </label>
              {renderField(
                "SamaritanoAPI",
                (imageCfg.SamaritanoAPI as ConfigValue) ?? "",
                ["ImageHosting", "SamaritanoAPI"],
                sectionFieldMeta.ImageHosting.SamaritanoAPI,
              )}
            </section>
          </div>
        </div>
      </div>
    );
  };

  return {
    renderField,
    renderMapSection,
    renderTorrentClientsSection,
    renderTrackerSection,
    renderImageHostingSection,
  };
};
