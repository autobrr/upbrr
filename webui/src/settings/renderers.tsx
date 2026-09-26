// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { Button } from "../components/ui/button";
import { PillCheckbox } from "../components/ui/checkbox";
import { Select } from "../components/ui/select";
import { Switch } from "../components/ui/switch";
import { trackerFieldPresentation } from "./trackerFields";
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
  "h-9 rounded-md border border-input bg-card px-3 py-1.5 text-sm text-card-foreground outline-none transition placeholder:text-muted-foreground focus:border-ring focus:ring-[3px] focus:ring-ring/50";

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
      <div className="settings-array">
        {value.map((entry, index) => (
          <div className="settings-array-row" key={`${path.join(".")}-${index}`}>
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
          <label className="settings-field" key={path.join(".")}>
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
        <label className="settings-field settings-field--switch" key={path.join(".")}>
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
                <div
                  className="settings-card"
                  key={`TorrentClients-${name}`}
                  role="group"
                  aria-labelledby={`torrent-client-${encodeURIComponent(name)}`}
                >
                  <div className="settings-card__header">
                    <p className="value" id={`torrent-client-${encodeURIComponent(name)}`}>
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
                      <label
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
                      </label>
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
                    <label className="settings-switch-row">
                      <span>qBit direct</span>
                      <Switch
                        aria-label="qBit direct"
                        checked={directEnabled}
                        onChange={(event) => setQbitDirect(name, event.target.checked)}
                      />
                    </label>
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
                <fieldset className="tracker-selection-container m-0 min-w-0 border-0 p-0">
                  <legend className="sr-only">Default trackers</legend>
                  <div className="tracker-pills">
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

          <details className="settings-subgroup settings-subgroup--collapsible">
            <summary className="settings-subgroup__title">Preferred tracker data source</summary>
            <div style={{ paddingTop: "0.5rem" }}>
              <div className="settings-map__controls">
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
              <p className="muted" style={{ marginTop: "0.5rem" }}>
                Moves the selected tracker to the top of tracker-data lookup and qBit tracker
                priority when present.
              </p>
            </div>
          </details>

          <div className="settings-map__header">
            <p className="label">Entries</p>
            <div className="settings-map__controls">
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

          <div className="settings-map__grid">
            {visibleEntries.length === 0 ? (
              <p className="muted">No configured entries yet.</p>
            ) : (
              visibleEntries.map(({ entry, value }) => {
                const key = entry.name;
                const schema = trackerSchemaFor(entry);
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
                    <div className="settings-card__body">
                      <div className="settings-grid">
                        {schema
                          .filter((meta) => !(meta.advanced && !advancedOpen))
                          .map((meta) =>
                            renderField(
                              meta.key,
                              value[meta.key] ??
                                entry.fields.find((field) => field.key === meta.key)?.default ??
                                "",
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

          {(trackerCatalog?.unsupported.length ?? 0) > 0 ? (
            <div className="settings-subgroup">
              <div className="settings-subgroup__title">Unsupported tracker entries</div>
              <p className="muted">
                Preserved config has no matching tracker implementation and cannot be used.
              </p>
              <div className="settings-map__grid">
                {trackerCatalog?.unsupported.map((name) => (
                  <div className="settings-card" key={`unsupported-${name}`}>
                    <div className="settings-card__summary">
                      <span className="settings-card__summary-name">{name}</span>
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
      return <p className="error">Unable to render tracker settings: {String(err)}</p>;
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
      <div className="settings-form">
        <div className="settings-subgroup">
          <div className="settings-subgroup__title">Host Priority</div>
          <div className="settings-grid">
            {hostFields.map((field, index) => {
              const selected = String(imageCfg[field] ?? "");
              return (
                <label className="settings-field" key={field}>
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
          <div className="settings-subgroup__title">Additional Hosts</div>
          <div className="settings-grid">
            <label className="settings-switch-row">
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
            <label className="settings-switch-row">
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
            <label className="settings-switch-row">
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
