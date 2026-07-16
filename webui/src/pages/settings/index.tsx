// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import * as AlertDialog from "@radix-ui/react-alert-dialog";
import { Button } from "../../components/ui/button";
import { Switch } from "../../components/ui/switch";
import { applicationClient, trackerAuthClient } from "../../api/app";
import { cn } from "../../utils/cn";
import { handleExternalLinkClick } from "../../utils/externalLinks";
import type {
  ApplicationInfo,
  ConfigMap,
  ConfigValue,
  FieldMeta,
  TrackerAuthCapability,
  TrackerAuthStatus,
} from "../../types";

type SettingsSection = { key: string; jsonKey: string; label: string };

const applicationDetailsSection = {
  key: "application_details",
  label: "Application Details",
};

const trackerAuthSection = {
  key: "tracker_auth",
  label: "Tracker Auth",
};

const settingsInputClass =
  "h-8 rounded-md border border-white/10 bg-slate-950/45 px-2.5 text-sm text-[var(--text)] outline-none transition placeholder:text-[var(--muted)] focus:border-[var(--accent-2)] focus:ring-2 focus:ring-[rgba(53,194,193,0.18)]";
// Tracker-supplied auth kinds can be long adapter descriptors; keep chips
// wrapped inside the auth card on narrow screens.
const trackerAuthChipClass =
  "max-w-full whitespace-normal rounded-full border border-slate-400/20 bg-slate-950/35 px-[0.45rem] py-[0.2rem] text-[0.74rem] leading-none text-[var(--muted)] [overflow-wrap:anywhere]";
const trackerAuthMetaClass = "m-0 text-[0.8rem] text-[var(--muted)]";

/** Trackers with backend adapters that can perform a remote auth check. */
const remoteAuthValidationTrackers = new Set([
  "AR",
  "BTN",
  "FF",
  "FL",
  "HDB",
  "MTV",
  "PTP",
  "RTF",
  "THR",
]);

/** Builds the case-insensitive key shared by main tracker config and tracker auth rows. */
const trackerNameKey = (name: string) => name.trim().toLowerCase();

/**
 * Returns true for tracker auth capabilities that perform stored-cookie,
 * relogin, refresh, or 2FA handling beyond static API-key/passkey config.
 */
const isManagedTrackerAuthCapability = (capability: TrackerAuthCapability) => {
  const authKind = capability.authKind.toLowerCase();
  return (
    capability.supportsCookieFile ||
    capability.supportsLogin ||
    capability.supportsAutoLogin ||
    capability.supportsTOTP ||
    capability.supportsManual2FA ||
    authKind.includes("refresh") ||
    authKind.includes("2fa")
  );
};

/** Returns tracker auth summary/detail text using the shared API display contract. */
const trackerAuthStatusDisplay = (status?: TrackerAuthStatus) => {
  const message = status?.message.trim() ?? "";
  const lastError = status?.lastError.trim() ?? "";
  return {
    message,
    lastError: lastError && lastError !== message ? lastError : "",
  };
};

type ConfigOpStatus = {
  type: "success" | "error" | "warning";
  title: string;
  message: string;
  warnings?: string[];
} | null;

type Props = {
  configData: ConfigMap | null;
  settingsLoading: boolean;
  settingsExporting: boolean;
  settingsImporting: boolean;
  settingsDirty: boolean;
  settingsSaved: string;
  settingsError: string;
  configOpStatus: ConfigOpStatus;
  dismissConfigOpStatus: () => void;
  settingsSection: string;
  settingsSections: SettingsSection[];
  /** Tracker names already enabled by the main tracker settings panel. */
  trackerSelectionNames: string[];
  showAdvancedToggle: boolean;
  advancedOpen: boolean;
  setSettingsSection: Dispatch<SetStateAction<string>>;
  setSettingsAdvanced: Dispatch<SetStateAction<Record<string, boolean>>>;
  loadSettings: () => void;
  handleExportSettings: () => void;
  handleImportConfig: () => void;
  importConfirmOpen: boolean;
  handleImportConfigConfirm: () => void | Promise<void>;
  handleImportConfigCancel: () => void;
  handleSaveSettings: () => void | Promise<void>;
  renderImageHostingSection: () => JSX.Element | null;
  renderTrackerSection: (advancedOpen: boolean) => JSX.Element | null;
  renderTorrentClientsSection: (advancedOpen: boolean) => JSX.Element | null;
  renderField: (label: string, value: ConfigValue, path: string[], meta?: FieldMeta) => JSX.Element;
  sectionFieldMeta: Record<string, Record<string, FieldMeta>>;
};

/**
 * Renders settings plus tracker auth controls with generation-gated async state
 * so config saves/imports, section changes, and per-tracker actions ignore
 * stale tracker auth responses.
 */
export default function SettingsPage(props: Props) {
  const {
    configData,
    settingsLoading,
    settingsExporting,
    settingsImporting,
    settingsDirty,
    settingsSaved,
    settingsError,
    configOpStatus,
    dismissConfigOpStatus,
    settingsSection,
    settingsSections,
    trackerSelectionNames,
    showAdvancedToggle,
    advancedOpen,
    setSettingsSection,
    setSettingsAdvanced,
    loadSettings,
    handleExportSettings,
    handleImportConfig,
    importConfirmOpen,
    handleImportConfigConfirm,
    handleImportConfigCancel,
    handleSaveSettings,
    renderImageHostingSection,
    renderTrackerSection,
    renderTorrentClientsSection,
    renderField,
    sectionFieldMeta,
  } = props;

  const [warningsExpanded, setWarningsExpanded] = useState(false);
  const [applicationInfo, setApplicationInfo] = useState<ApplicationInfo | null>(null);
  const [applicationInfoError, setApplicationInfoError] = useState("");
  const [applicationInfoLoading, setApplicationInfoLoading] = useState(false);
  const [applicationInfoFetchedAt, setApplicationInfoFetchedAt] = useState<number | null>(null);
  const [uptimeTick, setUptimeTick] = useState(() => Date.now());
  const [trackerAuthCapabilities, setTrackerAuthCapabilities] = useState<TrackerAuthCapability[]>(
    [],
  );
  const [trackerAuthStatuses, setTrackerAuthStatuses] = useState<Record<string, TrackerAuthStatus>>(
    {},
  );
  const [trackerAuthLoading, setTrackerAuthLoading] = useState(false);
  const [trackerAuthError, setTrackerAuthError] = useState("");
  const [trackerAuthActionErrors, setTrackerAuthActionErrors] = useState<Record<string, string>>(
    {},
  );
  const [trackerAuthFilter, setTrackerAuthFilter] = useState("");
  const [trackerAuthActions, setTrackerAuthActions] = useState<Record<string, string>>({});
  const [trackerAuthCodes, setTrackerAuthCodes] = useState<Record<string, string>>({});
  const [trackerAuthReloadRevision, setTrackerAuthReloadRevision] = useState(0);
  const trackerAuthStatusVersions = useRef<Record<string, number>>({});
  const trackerAuthActionSequences = useRef<Record<string, number>>({});
  const trackerAuthLoadGeneration = useRef(0);
  const trackerAuthSectionActiveRef = useRef(settingsSection === trackerAuthSection.key);

  const invalidateTrackerAuthStatusVersions = useCallback(() => {
    Object.keys(trackerAuthStatusVersions.current).forEach((trackerID) => {
      trackerAuthStatusVersions.current[trackerID] =
        (trackerAuthStatusVersions.current[trackerID] ?? 0) + 1;
    });
  }, []);

  useEffect(() => {
    const active = settingsSection === trackerAuthSection.key;
    trackerAuthSectionActiveRef.current = active;
    if (!active) {
      invalidateTrackerAuthStatusVersions();
      trackerAuthLoadGeneration.current += 1;
      setTrackerAuthLoading(false);
      setTrackerAuthActions({});
      setTrackerAuthActionErrors({});
    }
  }, [invalidateTrackerAuthStatusVersions, settingsSection]);

  useEffect(() => {
    let cancelled = false;
    setApplicationInfoLoading(true);
    setApplicationInfoError("");
    void applicationClient
      .getInfo()
      .then((info) => {
        if (cancelled) {
          return;
        }
        setApplicationInfo(info);
        setApplicationInfoFetchedAt(Date.now());
      })
      .catch((error) => {
        if (cancelled) {
          return;
        }
        setApplicationInfoError(String(error));
      })
      .finally(() => {
        if (!cancelled) {
          setApplicationInfoLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!applicationInfo) {
      return undefined;
    }
    const timer = window.setInterval(() => {
      setUptimeTick(Date.now());
    }, 1000);
    return () => window.clearInterval(timer);
  }, [applicationInfo]);

  useEffect(() => {
    if (settingsSection !== trackerAuthSection.key) {
      return undefined;
    }
    let cancelled = false;
    const loadGeneration = trackerAuthLoadGeneration.current + 1;
    trackerAuthLoadGeneration.current = loadGeneration;
    setTrackerAuthLoading(true);
    setTrackerAuthError("");
    setTrackerAuthActionErrors({});
    void trackerAuthClient
      .listCapabilities()
      .then((capabilities) => {
        if (cancelled) {
          return;
        }
        const configuredTrackerNames = new Set(trackerSelectionNames.map(trackerNameKey));
        const managedCapabilities = capabilities.filter(
          (capability) =>
            configuredTrackerNames.has(trackerNameKey(capability.trackerID)) &&
            isManagedTrackerAuthCapability(capability),
        );
        setTrackerAuthCapabilities(managedCapabilities);
        setTrackerAuthStatuses({});
        if (managedCapabilities.length === 0) {
          setTrackerAuthLoading(false);
          return;
        }
        let pendingStatuses = managedCapabilities.length;
        const markStatusComplete = () => {
          pendingStatuses -= 1;
          if (
            pendingStatuses === 0 &&
            !cancelled &&
            trackerAuthLoadGeneration.current === loadGeneration
          ) {
            setTrackerAuthLoading(false);
          }
        };
        managedCapabilities.forEach((capability) => {
          const statusVersion = trackerAuthStatusVersions.current[capability.trackerID] ?? 0;
          void trackerAuthClient
            .getStatus(capability.trackerID)
            .then((status) => {
              if (
                !cancelled &&
                (trackerAuthStatusVersions.current[capability.trackerID] ?? 0) === statusVersion
              ) {
                setTrackerAuthStatuses((prev) => ({
                  ...prev,
                  [capability.trackerID]: status,
                }));
              }
            })
            .catch((error) => {
              if (
                !cancelled &&
                (trackerAuthStatusVersions.current[capability.trackerID] ?? 0) === statusVersion
              ) {
                setTrackerAuthStatuses((prev) => ({
                  ...prev,
                  [capability.trackerID]: {
                    trackerID: capability.trackerID,
                    displayName: capability.displayName,
                    state: "error",
                    cookieCount: 0,
                    lastCheckedAt: "",
                    lastError: String(error),
                    encryptedStorage: false,
                    needs2FA: false,
                    challengeID: "",
                    message: "",
                  },
                }));
              }
            })
            .finally(() => {
              if (!cancelled) {
                markStatusComplete();
              }
            });
        });
      })
      .catch((error) => {
        if (!cancelled) {
          setTrackerAuthCapabilities([]);
          setTrackerAuthStatuses({});
          setTrackerAuthError(String(error));
          setTrackerAuthLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [settingsSection, trackerAuthReloadRevision, trackerSelectionNames]);

  const reloadTrackerAuthAfterConfigChange = useCallback(
    async (handler: () => void | Promise<void>) => {
      await handler();
      invalidateTrackerAuthStatusVersions();
      setTrackerAuthActions({});
      setTrackerAuthActionErrors({});
      setTrackerAuthReloadRevision((revision) => revision + 1);
    },
    [invalidateTrackerAuthStatusVersions],
  );

  const runTrackerAuthAction = async (
    trackerID: string,
    action: string,
    fn: () => Promise<TrackerAuthStatus>,
  ) => {
    const actionVersion = (trackerAuthStatusVersions.current[trackerID] ?? 0) + 1;
    trackerAuthStatusVersions.current[trackerID] = actionVersion;
    const actionSequence = (trackerAuthActionSequences.current[trackerID] ?? 0) + 1;
    trackerAuthActionSequences.current[trackerID] = actionSequence;
    setTrackerAuthActions((prev) => ({ ...prev, [trackerID]: action }));
    setTrackerAuthActionErrors((prev) => {
      const next = { ...prev };
      delete next[trackerID];
      return next;
    });
    try {
      const status = await fn();
      if (
        trackerAuthSectionActiveRef.current &&
        trackerAuthStatusVersions.current[trackerID] === actionVersion
      ) {
        setTrackerAuthStatuses((prev) => ({ ...prev, [trackerID]: status }));
      }
    } catch (error) {
      if (
        trackerAuthSectionActiveRef.current &&
        trackerAuthActionSequences.current[trackerID] === actionSequence &&
        trackerAuthStatusVersions.current[trackerID] === actionVersion
      ) {
        setTrackerAuthActionErrors((prev) => ({ ...prev, [trackerID]: String(error) }));
      }
    } finally {
      if (
        trackerAuthSectionActiveRef.current &&
        trackerAuthActionSequences.current[trackerID] === actionSequence
      ) {
        setTrackerAuthActions((prev) => ({ ...prev, [trackerID]: "" }));
      }
    }
  };

  const trackerAuthPanel = (() => {
    const filter = trackerAuthFilter.trim().toLowerCase();
    const capabilities = trackerAuthCapabilities.filter((capability) => {
      if (!filter) return true;
      return (
        capability.trackerID.toLowerCase().includes(filter) ||
        capability.authKind.toLowerCase().includes(filter)
      );
    });
    const storageReady = Object.values(trackerAuthStatuses).some(
      (status) => status.encryptedStorage,
    );
    return (
      <div className="settings-form gap-4">
        <div className="settings-subgroup">
          <div className="settings-subgroup__title">Tracker Auth</div>
          <div className="settings-auth-status">
            <span className={`settings-auth-badge ${storageReady ? "is-ready" : "is-warning"}`}>
              {storageReady
                ? "Encrypted cookie storage ready"
                : "Encrypted cookie storage unavailable"}
            </span>
            <p className="helper">
              Import Netscape or JSON cookies, check local auth state, and confirm which trackers
              can relogin automatically during unattended uploads.
            </p>
          </div>
          <label className="settings-field max-w-[360px]">
            <span>Filter trackers</span>
            <input
              className={settingsInputClass}
              value={trackerAuthFilter}
              onChange={(event) => setTrackerAuthFilter(event.target.value)}
              placeholder="MTV, cookies, api"
            />
          </label>
        </div>
        {trackerAuthLoading ? <p className="muted">Loading tracker auth...</p> : null}
        {trackerAuthError ? <p className="error">{trackerAuthError}</p> : null}
        <div className="grid gap-[0.85rem]">
          {capabilities.map((capability) => {
            const status = trackerAuthStatuses[capability.trackerID];
            const busy = trackerAuthActions[capability.trackerID] || "";
            const actionError = trackerAuthActionErrors[capability.trackerID] || "";
            const code = trackerAuthCodes[capability.trackerID] || "";
            const statusDisplay = trackerAuthStatusDisplay(status);
            const canTestAuth = remoteAuthValidationTrackers.has(
              capability.trackerID.trim().toUpperCase(),
            );
            return (
              <div
                className="settings-card tracker-auth-card grid min-w-0 gap-3"
                key={capability.trackerID}
              >
                <div className="flex min-w-0 flex-wrap items-center justify-between gap-[0.6rem]">
                  <div className="min-w-0 flex-1">
                    <p className="settings-detail-card__label">Tracker</p>
                    <h2 className="m-0 mt-[0.1rem] text-[1.05rem] leading-tight [overflow-wrap:anywhere]">
                      {capability.displayName || capability.trackerID}
                    </h2>
                  </div>
                  <span className={`settings-auth-badge ${statusBadgeClass(status?.state)}`}>
                    {formatTrackerAuthState(status?.state)}
                  </span>
                </div>
                <div className="flex min-w-0 flex-wrap items-center gap-[0.6rem]">
                  <span className={trackerAuthChipClass}>{capability.authKind}</span>
                  {capability.supportsCookieFile ? (
                    <span className={trackerAuthChipClass}>cookie import</span>
                  ) : null}
                  {capability.supportsLogin ? (
                    <span className={trackerAuthChipClass}>login</span>
                  ) : null}
                  {capability.supportsAutoLogin ? (
                    <span className={trackerAuthChipClass}>auto relogin</span>
                  ) : null}
                  {capability.supportsTOTP ? (
                    <span className={trackerAuthChipClass}>TOTP</span>
                  ) : null}
                  {capability.supportsManual2FA ? (
                    <span className={trackerAuthChipClass}>manual 2FA</span>
                  ) : null}
                  {capability.requiresAPIKey ? (
                    <span className={trackerAuthChipClass}>API key</span>
                  ) : null}
                  {capability.requiresPasskey ? (
                    <span className={trackerAuthChipClass}>passkey</span>
                  ) : null}
                </div>
                <div className="flex flex-wrap items-center gap-[0.6rem]">
                  <p className={trackerAuthMetaClass}>Cookies: {status?.cookieCount ?? 0}</p>
                  <p className={trackerAuthMetaClass}>
                    Checked: {formatTrackerAuthDate(status?.lastCheckedAt)}
                  </p>
                  <p className={trackerAuthMetaClass}>
                    Storage: {status?.encryptedStorage ? "encrypted" : "unavailable"}
                  </p>
                </div>
                {statusDisplay.message ? (
                  <p className="helper [overflow-wrap:anywhere]">{statusDisplay.message}</p>
                ) : null}
                {statusDisplay.lastError ? (
                  <p className="error [overflow-wrap:anywhere]">{statusDisplay.lastError}</p>
                ) : null}
                {actionError ? <p className="error">{actionError}</p> : null}
                {(capability.notes ?? []).map((note) => (
                  <p className="muted [overflow-wrap:anywhere]" key={note}>
                    {note}
                  </p>
                ))}
                {status?.needs2FA ? (
                  <div className="flex flex-wrap items-center gap-[0.6rem]">
                    <input
                      className={`${settingsInputClass} w-36`}
                      value={code}
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      onChange={(event) =>
                        setTrackerAuthCodes((prev) => ({
                          ...prev,
                          [capability.trackerID]: event.target.value,
                        }))
                      }
                      placeholder="2FA code"
                    />
                    <Button
                      type="button"
                      disabled={!status.challengeID || !code.trim()}
                      onClick={() =>
                        runTrackerAuthAction(capability.trackerID, "2fa", () =>
                          trackerAuthClient.submit2FA(status.challengeID, code),
                        )
                      }
                    >
                      Submit 2FA
                    </Button>
                  </div>
                ) : null}
                <div className="settings-auth-actions">
                  {capability.supportsCookieFile ? (
                    <Button
                      type="button"
                      disabled={Boolean(busy)}
                      onClick={() =>
                        runTrackerAuthAction(capability.trackerID, "import", () =>
                          trackerAuthClient.importCookies(capability.trackerID),
                        )
                      }
                    >
                      {busy === "import" ? "Importing..." : "Import Cookies"}
                    </Button>
                  ) : null}
                  {canTestAuth ? (
                    <Button
                      type="button"
                      disabled={Boolean(busy)}
                      onClick={() =>
                        runTrackerAuthAction(capability.trackerID, "test", () =>
                          trackerAuthClient.test(capability.trackerID),
                        )
                      }
                    >
                      {busy === "test" ? "Checking..." : "Check Auth"}
                    </Button>
                  ) : null}
                  <Button
                    type="button"
                    disabled={Boolean(busy)}
                    onClick={() =>
                      runTrackerAuthAction(capability.trackerID, "delete", () =>
                        trackerAuthClient.remove(capability.trackerID),
                      )
                    }
                  >
                    {busy === "delete" ? "Deleting..." : "Delete Auth"}
                  </Button>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    );
  })();

  const uptimeSeconds =
    applicationInfo && applicationInfoFetchedAt !== null
      ? applicationInfo.uptimeSeconds +
        Math.max(0, Math.floor((uptimeTick - applicationInfoFetchedAt) / 1000))
      : 0;
  const uptimeValue = applicationInfo ? formatApplicationUptime(uptimeSeconds) : "";
  const applicationDetailsPanel = (
    <div className="settings-subgroup settings-subgroup--application">
      <p className="helper">
        Read-only build and runtime details for this install. Auth, bind, and storage paths are
        intentionally excluded.
      </p>
      <div className="settings-details-grid">
        <div className="settings-detail-card">
          <p className="settings-detail-card__label">Project</p>
          <p className="settings-detail-card__value">
            <a
              href="https://github.com/autobrr/upbrr"
              target="_blank"
              rel="noreferrer"
              onAuxClick={handleExternalLinkClick}
              onClick={handleExternalLinkClick}
            >
              autobrr/upbrr
            </a>
          </p>
        </div>
        {applicationInfo ? (
          <>
            <div className="settings-detail-card">
              <p className="settings-detail-card__label">Version</p>
              <p className="settings-detail-card__value mono">
                {applicationInfo.version || "Unavailable"}
              </p>
            </div>
            <div className="settings-detail-card">
              <p className="settings-detail-card__label">Build</p>
              <p className="settings-detail-card__value mono">
                {applicationInfo.buildIdentifier || "Unavailable"}
              </p>
            </div>
            <div className="settings-detail-card">
              <p className="settings-detail-card__label">Go Runtime</p>
              <p className="settings-detail-card__value mono">{applicationInfo.goVersion}</p>
            </div>
            <div className="settings-detail-card">
              <p className="settings-detail-card__label">DVD Menu Engine</p>
              <p className="settings-detail-card__value mono">
                {applicationInfo.dvdMenuEngine.EngineVersion || "Unavailable"}
              </p>
            </div>
            <div className="settings-detail-card">
              <p className="settings-detail-card__label">FFmpeg DVD Menus</p>
              <p className="settings-detail-card__value mono">
                {applicationInfo.dvdMenuCapabilityStatus === "available"
                  ? "Available"
                  : applicationInfo.dvdMenuCapabilityStatus === "incompatible"
                    ? "Incompatible"
                    : "Unavailable"}
              </p>
              <p className="helper">{applicationInfo.dvdMenuCapabilityMessage}</p>
            </div>
            <div className="settings-detail-card">
              <p className="settings-detail-card__label">FFmpeg Version</p>
              <p className="settings-detail-card__value mono">
                {applicationInfo.dvdMenuEngine.FFmpegVersion || "Unavailable"}
              </p>
            </div>
            <div className="settings-detail-card">
              <p className="settings-detail-card__label">Platform</p>
              <p className="settings-detail-card__value mono">
                {applicationInfo.goos}/{applicationInfo.goarch}
              </p>
            </div>
            <div className="settings-detail-card">
              <p className="settings-detail-card__label">Uptime</p>
              <p className="settings-detail-card__value mono">
                {uptimeValue || applicationInfo.uptime}
              </p>
            </div>
          </>
        ) : null}
      </div>
      {applicationInfoLoading ? <p className="muted">Loading application details...</p> : null}
      {applicationInfoError ? <p className="error">{applicationInfoError}</p> : null}
    </div>
  );

  return (
    <div className="content-stack">
      <header className="hero">
        <p className="eyebrow">upbrr</p>
        <h1>Settings</h1>
        <p className="subtitle">
          Edit settings by section. Changes apply immediately and are saved to SQLite.
        </p>
      </header>

      <section className="panel">
        <div className="settings-header">
          <div className="settings-meta">
            <p className="label">Configuration</p>
            <p className="helper">Invalid changes will be rejected with a validation error.</p>
          </div>
          <div className="settings-actions">
            <Button type="button" onClick={loadSettings} disabled={settingsLoading}>
              Reload
            </Button>
            <Button
              type="button"
              onClick={handleExportSettings}
              disabled={settingsLoading || settingsExporting || settingsImporting}
            >
              {settingsExporting ? "Exporting..." : "Export"}
            </Button>
            <Button
              type="button"
              onClick={handleImportConfig}
              disabled={settingsLoading || settingsExporting || settingsImporting}
            >
              {settingsImporting ? "Importing..." : "Import"}
            </Button>
            <Button
              variant="primary"
              type="button"
              onClick={() => {
                void reloadTrackerAuthAfterConfigChange(handleSaveSettings);
              }}
              disabled={settingsLoading || settingsExporting || settingsImporting || !settingsDirty}
            >
              Save
            </Button>
          </div>
        </div>

        {configOpStatus ? (
          <div className={`config-status-banner config-status-banner--${configOpStatus.type}`}>
            <div className="config-status-banner__icon">
              {configOpStatus.type === "success" ? (
                <svg width="20" height="20" viewBox="0 0 20 20" fill="none">
                  <path
                    d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Z"
                    fill="currentColor"
                    opacity=".15"
                  />
                  <path
                    d="M6.5 10.5 8.5 12.5 13.5 7.5"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                  <circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.5" />
                </svg>
              ) : configOpStatus.type === "warning" ? (
                <svg width="20" height="20" viewBox="0 0 20 20" fill="none">
                  <path
                    d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Z"
                    fill="currentColor"
                    opacity=".15"
                  />
                  <path d="M10 7v4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                  <circle cx="10" cy="13.5" r=".75" fill="currentColor" />
                  <circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.5" />
                </svg>
              ) : (
                <svg width="20" height="20" viewBox="0 0 20 20" fill="none">
                  <path
                    d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Z"
                    fill="currentColor"
                    opacity=".15"
                  />
                  <path
                    d="M12.5 7.5 7.5 12.5M7.5 7.5l5 5"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                  />
                  <circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.5" />
                </svg>
              )}
            </div>
            <div className="config-status-banner__body">
              <p className="config-status-banner__title">{configOpStatus.title}</p>
              <p className="config-status-banner__message">{configOpStatus.message}</p>
              {configOpStatus.warnings && configOpStatus.warnings.length > 0 ? (
                <div className="config-status-banner__warnings">
                  <button
                    type="button"
                    className="config-status-banner__toggle"
                    onClick={() => setWarningsExpanded((prev) => !prev)}
                  >
                    {warningsExpanded ? "Hide" : "Show"} {configOpStatus.warnings.length} warning
                    {configOpStatus.warnings.length !== 1 ? "s" : ""}
                  </button>
                  {warningsExpanded ? (
                    <ul className="config-status-banner__warning-list">
                      {configOpStatus.warnings.map((w, i) => (
                        <li key={i}>{w}</li>
                      ))}
                    </ul>
                  ) : null}
                </div>
              ) : null}
            </div>
            <button
              type="button"
              className="config-status-banner__dismiss"
              onClick={dismissConfigOpStatus}
              aria-label="Dismiss"
            >
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
                <path
                  d="M10.5 3.5 3.5 10.5M3.5 3.5l7 7"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                />
              </svg>
            </button>
          </div>
        ) : null}

        <div className="settings-shell">
          <div className="settings-tags">
            {settingsSections.map((section) => (
              <button
                key={section.key}
                type="button"
                className={cn(
                  "flex h-8 w-full items-center rounded-md px-3 text-left text-sm font-medium transition",
                  settingsSection === section.key
                    ? "bg-[var(--accent)] text-slate-950 shadow-[0_8px_24px_rgba(245,185,66,0.16)]"
                    : "text-[var(--muted)] hover:bg-white/10 hover:text-[var(--text)]",
                )}
                onClick={() => setSettingsSection(section.key)}
              >
                {section.label}
              </button>
            ))}
            <button
              key={applicationDetailsSection.key}
              type="button"
              className={cn(
                "flex h-8 w-full items-center rounded-md px-3 text-left text-sm font-medium transition",
                settingsSection === applicationDetailsSection.key
                  ? "bg-[var(--accent)] text-slate-950 shadow-[0_8px_24px_rgba(245,185,66,0.16)]"
                  : "text-[var(--muted)] hover:bg-white/10 hover:text-[var(--text)]",
              )}
              onClick={() => setSettingsSection(applicationDetailsSection.key)}
            >
              {applicationDetailsSection.label}
            </button>
            <button
              key={trackerAuthSection.key}
              type="button"
              className={cn(
                "flex h-8 w-full items-center rounded-md px-3 text-left text-sm font-medium transition",
                settingsSection === trackerAuthSection.key
                  ? "bg-[var(--accent)] text-slate-950 shadow-[0_8px_24px_rgba(245,185,66,0.16)]"
                  : "text-[var(--muted)] hover:bg-white/10 hover:text-[var(--text)]",
              )}
              onClick={() => setSettingsSection(trackerAuthSection.key)}
            >
              {trackerAuthSection.label}
            </button>
          </div>

          <div className="settings-body">
            {settingsSection === applicationDetailsSection.key ? applicationDetailsPanel : null}
            {settingsSection === trackerAuthSection.key ? trackerAuthPanel : null}
            {settingsSection === applicationDetailsSection.key ||
            settingsSection === trackerAuthSection.key ? null : configData ? (
              <div className="settings-form">
                {showAdvancedToggle ? (
                  <div className="settings-switch-row">
                    <span>Show advanced</span>
                    <Switch
                      aria-label="Show advanced"
                      checked={advancedOpen}
                      onChange={(event) =>
                        setSettingsAdvanced((prev) => ({
                          ...prev,
                          [settingsSection]: event.target.checked,
                        }))
                      }
                    />
                  </div>
                ) : null}
                {settingsSection === "image_hosting" ? (
                  renderImageHostingSection()
                ) : settingsSection === "trackers" &&
                  configData.Trackers &&
                  typeof configData.Trackers === "object" &&
                  !Array.isArray(configData.Trackers) ? (
                  renderTrackerSection(advancedOpen)
                ) : settingsSection === "torrent_clients" &&
                  configData.TorrentClients &&
                  typeof configData.TorrentClients === "object" ? (
                  renderTorrentClientsSection(advancedOpen)
                ) : (
                  <div className="settings-grid">
                    {(() => {
                      const section = settingsSections.find((item) => item.key === settingsSection);
                      if (!section) return null;
                      const sectionData = configData[section.jsonKey];
                      if (
                        !sectionData ||
                        typeof sectionData !== "object" ||
                        Array.isArray(sectionData)
                      ) {
                        return null;
                      }
                      const meta = sectionFieldMeta[section.jsonKey] || {};
                      return Object.entries(sectionData as ConfigMap)
                        .filter(([key]) => {
                          const fieldMeta = meta[key];
                          if (fieldMeta?.advanced && !advancedOpen) return false;
                          return true;
                        })
                        .map(([key, value]) =>
                          renderField(key, value, [section.jsonKey, key], meta[key]),
                        );
                    })()}
                  </div>
                )}
              </div>
            ) : (
              <p className="muted">Loading configuration...</p>
            )}
          </div>
        </div>

        {settingsSaved ? <p className="settings-saved">{settingsSaved}</p> : null}
        {settingsError ? <p className="error">{settingsError}</p> : null}
      </section>

      <AlertDialog.Root
        open={importConfirmOpen}
        onOpenChange={(open) => {
          if (!open) handleImportConfigCancel();
        }}
      >
        <AlertDialog.Portal>
          <AlertDialog.Overlay className="import-confirm-overlay" />
          <AlertDialog.Content className="import-confirm-dialog">
            <div className="import-confirm-dialog__icon">
              <svg width="28" height="28" viewBox="0 0 24 24" fill="none">
                <path d="M12 3 1.5 21h21L12 3Z" fill="currentColor" opacity=".12" />
                <path
                  d="M12 3 1.5 21h21L12 3Z"
                  stroke="currentColor"
                  strokeWidth="1.6"
                  strokeLinejoin="round"
                />
                <path d="M12 10v5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
                <circle cx="12" cy="18" r="1" fill="currentColor" />
              </svg>
            </div>
            <div className="import-confirm-dialog__body">
              <AlertDialog.Title asChild>
                <h2 className="import-confirm-dialog__title">Replace current configuration?</h2>
              </AlertDialog.Title>
              <AlertDialog.Description asChild>
                <p className="import-confirm-dialog__message">
                  Importing a configuration file will overwrite your current settings in the
                  database. This action cannot be undone.
                </p>
              </AlertDialog.Description>
              <p className="import-confirm-dialog__hint">
                We strongly recommend exporting your current configuration first so you can restore
                it if the imported file isn&apos;t what you expected.
              </p>
            </div>
            <div className="import-confirm-dialog__actions">
              <AlertDialog.Cancel asChild>
                <Button type="button" disabled={settingsImporting}>
                  Cancel
                </Button>
              </AlertDialog.Cancel>
              <Button
                type="button"
                onClick={handleExportSettings}
                disabled={settingsExporting || settingsImporting}
              >
                {settingsExporting ? "Exporting..." : "Export current config"}
              </Button>
              <AlertDialog.Action asChild>
                <Button
                  type="button"
                  variant="primary"
                  className="import-confirm-dialog__confirm"
                  onClick={(event) => {
                    event.preventDefault();
                    void reloadTrackerAuthAfterConfigChange(handleImportConfigConfirm);
                  }}
                  disabled={settingsImporting}
                >
                  {settingsImporting ? "Importing..." : "Choose file & import"}
                </Button>
              </AlertDialog.Action>
            </div>
          </AlertDialog.Content>
        </AlertDialog.Portal>
      </AlertDialog.Root>
    </div>
  );
}

function formatApplicationUptime(totalSeconds: number) {
  const days = Math.floor(totalSeconds / 86400);
  const hours = Math.floor((totalSeconds % 86400) / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;

  const parts: string[] = [];
  if (days > 0) {
    parts.push(`${days}d`);
  }
  if (hours > 0 || parts.length > 0) {
    parts.push(`${hours}h`);
  }
  if (minutes > 0 || parts.length > 0) {
    parts.push(`${minutes}m`);
  }
  parts.push(`${seconds}s`);

  return parts.join(" ");
}

function formatTrackerAuthState(state?: string) {
  switch (state) {
    case "configured":
      return "Configured";
    case "has_cookies":
      return "Has cookies";
    case "login_required":
      return "Login required";
    case "encrypted_storage_unavailable":
      return "Storage unavailable";
    case "error":
      return "Error";
    default:
      return "Not configured";
  }
}

function statusBadgeClass(state?: string) {
  switch (state) {
    case "configured":
    case "has_cookies":
      return "is-ready";
    case "login_required":
    case "encrypted_storage_unavailable":
    case "error":
      return "is-warning";
    default:
      return "is-idle";
  }
}

function formatTrackerAuthDate(value?: string) {
  if (!value) {
    return "Never";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}
