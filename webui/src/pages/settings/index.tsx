// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { pageStyle } from "../../components/ui/pageStyle";
import { useCallback, useEffect, useRef, useState } from "react";
import type { Dispatch, ReactElement, SetStateAction } from "react";
import * as AlertDialog from "@radix-ui/react-alert-dialog";
import { Button } from "../../components/ui/button";
import { Switch } from "../../components/ui/switch";
import { trackerAuthClient } from "../../api/app";
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
import { formatApplicationVersion } from "../../utils/applicationInfo";
import APITokensSettings from "./api_tokens";
import { AppearanceSettings } from "../../themes/AppearanceSettings";
import { SettingsFieldGroups } from "../../settings/FieldGroups";
import { confirmationDialogStyle, settingsStyle } from "../../settings/style";

type SettingsSection = { key: string; jsonKey: string; label: string };

const applicationDetailsSection = {
  key: "application_details",
  label: "Application Details",
};

const appearanceSection = {
  key: "appearance",
  label: "Appearance",
};

const trackerAuthSection = {
  key: "tracker_auth",
  label: "Tracker Auth",
};

const apiTokensSection = {
  key: "api_tokens",
  label: "API Tokens",
};

const settingsInputClass =
  "h-9 rounded-md border border-input bg-card px-3 text-sm text-card-foreground outline-none transition placeholder:text-muted-foreground focus:border-ring focus:ring-2 focus:ring-ring/30";
// Tracker-supplied auth kinds can be long adapter descriptors; keep chips
// wrapped inside the auth card on narrow screens.
const trackerAuthChipClass =
  "max-w-full whitespace-normal rounded-full border border-border bg-muted px-[0.45rem] py-[0.2rem] text-[0.74rem] leading-none text-muted-foreground [overflow-wrap:anywhere]";
const trackerAuthMetaClass = "m-0 text-[0.8rem] text-muted-foreground";

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
  applicationInfo: ApplicationInfo | null;
  applicationInfoFetchedAt: number | null;
  applicationInfoLoading: boolean;
  applicationInfoError: string;
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
  renderImageHostingSection: () => ReactElement | null;
  renderTrackerSection: (advancedOpen: boolean) => ReactElement | null;
  renderTorrentClientsSection: (advancedOpen: boolean) => ReactElement | null;
  renderField: (
    label: string,
    value: ConfigValue,
    path: string[],
    meta?: FieldMeta,
  ) => ReactElement;
  sectionFieldMeta: Record<string, Record<string, FieldMeta>>;
};

/**
 * Renders settings plus tracker auth controls with generation-gated async state
 * so config saves/imports, section changes, and per-tracker actions ignore
 * stale tracker auth responses.
 */
export default function SettingsPage(props: Props) {
  const {
    applicationInfo,
    applicationInfoFetchedAt,
    applicationInfoLoading,
    applicationInfoError,
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
    const storageStatuses = trackerAuthCapabilities
      .map((capability) => trackerAuthStatuses[capability.trackerID])
      .filter((status): status is TrackerAuthStatus => status !== undefined);
    const storageReady =
      !trackerAuthLoading &&
      trackerAuthCapabilities.length > 0 &&
      storageStatuses.length === trackerAuthCapabilities.length &&
      storageStatuses.every((status) => status.encryptedStorage);
    const storagePartiallyReady = storageStatuses.some((status) => status.encryptedStorage);
    const storageStatusLabel = trackerAuthLoading
      ? "Checking encrypted cookie storage"
      : storageReady
        ? "Encrypted cookie storage ready"
        : storagePartiallyReady
          ? "Encrypted cookie storage partially ready"
          : "Encrypted cookie storage unavailable";
    return (
      <div className="flex flex-col gap-4">
        <div className={settingsStyle.subgroup}>
          <div className={settingsStyle.title}>Tracker Auth</div>
          <div className="flex flex-col gap-2.5">
            <span
              className={`${settingsStyle.authBadge} ${storageReady ? settingsStyle.authReady : settingsStyle.authWarning}`}
            >
              {storageStatusLabel}
            </span>
            <p className="helper">
              Import Netscape or JSON cookies, check local auth state, and confirm which trackers
              can relogin automatically during unattended uploads.
            </p>
          </div>
          <label className={`${settingsStyle.field} max-w-[360px]`}>
            <span>Filter trackers</span>
            <input
              className={settingsInputClass}
              value={trackerAuthFilter}
              onChange={(event) => setTrackerAuthFilter(event.target.value)}
              placeholder="BTN, cookies, api"
            />
          </label>
        </div>
        {trackerAuthLoading ? (
          <p className="text-muted-foreground">Loading tracker auth...</p>
        ) : null}
        {trackerAuthError ? <p className={pageStyle.error}>{trackerAuthError}</p> : null}
        <div className="grid gap-[0.85rem]">
          {capabilities.map((capability) => {
            const status = trackerAuthStatuses[capability.trackerID];
            const busy = trackerAuthActions[capability.trackerID] || "";
            const actionError = trackerAuthActionErrors[capability.trackerID] || "";
            const code = trackerAuthCodes[capability.trackerID] || "";
            const statusDisplay = trackerAuthStatusDisplay(status);
            const canTestAuth = capability.supportsRemoteValidation === true;
            return (
              <div
                className={`${settingsStyle.card} tracker-auth-card min-w-0 gap-3`}
                key={capability.trackerID}
              >
                <div className="flex min-w-0 flex-wrap items-center justify-between gap-[0.6rem]">
                  <div className="min-w-0 flex-1">
                    <p className={settingsStyle.detailLabel}>Tracker</p>
                    <h2 className="m-0 mt-[0.1rem] text-[1.05rem] leading-tight [overflow-wrap:anywhere]">
                      {capability.displayName || capability.trackerID}
                    </h2>
                  </div>
                  <span className={`${settingsStyle.authBadge} ${statusBadgeClass(status?.state)}`}>
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
                  <p className={`${pageStyle.error} [overflow-wrap:anywhere]`}>
                    {statusDisplay.lastError}
                  </p>
                ) : null}
                {actionError ? <p className={pageStyle.error}>{actionError}</p> : null}
                {(capability.notes ?? []).map((note) => (
                  <p className="text-muted-foreground [overflow-wrap:anywhere]" key={note}>
                    {note}
                  </p>
                ))}
                {status?.needs2FA ? (
                  <div className="flex flex-wrap items-center gap-[0.6rem]">
                    <input
                      aria-label={`${capability.displayName || capability.trackerID} 2FA code`}
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
                      aria-label={`Submit 2FA — ${capability.displayName || capability.trackerID}`}
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
                <div className="flex flex-wrap items-center gap-2">
                  {capability.supportsCookieFile ? (
                    <Button
                      type="button"
                      aria-label={`Import Cookies — ${capability.displayName || capability.trackerID}`}
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
                      aria-label={`Check Auth — ${capability.displayName || capability.trackerID}`}
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
                    aria-label={`Delete Auth — ${capability.displayName || capability.trackerID}`}
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
  const applicationVersion = applicationInfo ? formatApplicationVersion(applicationInfo) : "";
  const applicationDetailsPanel = (
    <div className={settingsStyle.subgroup}>
      <p className="helper">
        Read-only build and runtime details for this install. Auth, bind, and storage paths are
        intentionally excluded.
      </p>
      <div className={settingsStyle.detailGrid}>
        <div className={settingsStyle.detailCard}>
          <p className={settingsStyle.detailLabel}>Project</p>
          <p className={settingsStyle.detailValue}>
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
            <div className={settingsStyle.detailCard}>
              <p className={settingsStyle.detailLabel}>Version</p>
              <p className={`${settingsStyle.detailValue} font-mono text-[0.95rem]`}>
                {applicationVersion}
              </p>
            </div>
            <div className={settingsStyle.detailCard}>
              <p className={settingsStyle.detailLabel}>Build</p>
              <p className={`${settingsStyle.detailValue} font-mono text-[0.95rem]`}>
                {applicationInfo.buildIdentifier || "Unavailable"}
              </p>
            </div>
            <div className={settingsStyle.detailCard}>
              <p className={settingsStyle.detailLabel}>Go Runtime</p>
              <p className={`${settingsStyle.detailValue} font-mono text-[0.95rem]`}>
                {applicationInfo.goVersion}
              </p>
            </div>
            <div className={settingsStyle.detailCard}>
              <p className={settingsStyle.detailLabel}>DVD Menu Engine</p>
              <p className={`${settingsStyle.detailValue} font-mono text-[0.95rem]`}>
                {applicationInfo.dvdMenuEngine.EngineVersion || "Unavailable"}
              </p>
            </div>
            <div className={settingsStyle.detailCard}>
              <p className={settingsStyle.detailLabel}>FFmpeg DVD Menus</p>
              <p className={`${settingsStyle.detailValue} font-mono text-[0.95rem]`}>
                {applicationInfo.dvdMenuCapabilityStatus === "available"
                  ? "Available"
                  : applicationInfo.dvdMenuCapabilityStatus === "incompatible"
                    ? "Incompatible"
                    : "Unavailable"}
              </p>
              <p className="helper">{applicationInfo.dvdMenuCapabilityMessage}</p>
            </div>
            <div className={settingsStyle.detailCard}>
              <p className={settingsStyle.detailLabel}>FFmpeg Version</p>
              <p className={`${settingsStyle.detailValue} font-mono text-[0.95rem]`}>
                {applicationInfo.dvdMenuEngine.FFmpegVersion || "Unavailable"}
              </p>
            </div>
            <div className={settingsStyle.detailCard}>
              <p className={settingsStyle.detailLabel}>Platform</p>
              <p className={`${settingsStyle.detailValue} font-mono text-[0.95rem]`}>
                {applicationInfo.goos}/{applicationInfo.goarch}
              </p>
            </div>
            <div className={settingsStyle.detailCard}>
              <p className={settingsStyle.detailLabel}>Uptime</p>
              <p className={`${settingsStyle.detailValue} font-mono text-[0.95rem]`}>
                {uptimeValue || applicationInfo.uptime}
              </p>
            </div>
            {applicationInfo.dependencies.length > 0 ? (
              <div className={`${settingsStyle.detailCard} col-span-full`}>
                <p className={settingsStyle.detailLabel}>Autobrr dependencies</p>
                <div className="mt-2 grid">
                  {applicationInfo.dependencies.map((dependency) => (
                    <div
                      className="grid gap-1 border-t border-border py-2 first:border-t-0 first:pt-0 last:pb-0 min-[720px]:grid-cols-[minmax(0,1fr)_minmax(0,2fr)] min-[720px]:items-baseline"
                      key={dependency.path}
                    >
                      <p
                        className={`${settingsStyle.detailValue} font-mono text-[0.95rem]`}
                        title={dependency.path}
                      >
                        {dependency.path.replace(/^github\.com\/autobrr\//, "")}
                      </p>
                      <p className="font-mono text-[0.95rem] break-words min-[720px]:text-right">
                        {dependency.version}
                      </p>
                    </div>
                  ))}
                </div>
              </div>
            ) : null}
          </>
        ) : null}
      </div>
      {applicationInfoLoading ? (
        <p className="text-muted-foreground">Loading application details...</p>
      ) : null}
      {applicationInfoError ? <p className={pageStyle.error}>{applicationInfoError}</p> : null}
    </div>
  );

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-4" data-testid="settings-page">
      <header className="relative z-[1] max-w-[720px]">
        <p className={pageStyle.eyebrow}>upbrr</p>
        <h1>Settings</h1>
        <p className={pageStyle.subtitle}>
          Edit settings by section. Changes apply immediately and are saved to SQLite.
        </p>
      </header>

      <section className={pageStyle.panel}>
        <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
          <div className="flex flex-col gap-2.5">
            <p className={pageStyle.label}>Configuration</p>
            <p className="helper">Invalid changes will be rejected with a validation error.</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              onClick={() => {
                if (!settingsDirty || window.confirm("Discard unsaved settings and reload?")) {
                  loadSettings();
                }
              }}
              disabled={settingsLoading}
            >
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
          <div
            className={cn(
              "my-2.5 flex items-start gap-2.5 rounded-[14px] border p-3",
              configOpStatus.type === "success" &&
                "border-status-success/30 bg-status-success/10 text-status-success",
              configOpStatus.type === "warning" &&
                "border-status-warning/30 bg-status-warning/10 text-status-warning",
              configOpStatus.type === "error" &&
                "border-destructive/30 bg-destructive/10 text-destructive-text",
            )}
          >
            <div>
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
            <div className="flex min-w-0 flex-1 flex-col gap-[3px]">
              <p className="m-0 font-semibold">{configOpStatus.title}</p>
              <p className="m-0 text-foreground [overflow-wrap:anywhere]">
                {configOpStatus.message}
              </p>
              {configOpStatus.warnings && configOpStatus.warnings.length > 0 ? (
                <div>
                  <button
                    type="button"
                    className="border-0 bg-transparent text-inherit"
                    onClick={() => setWarningsExpanded((prev) => !prev)}
                  >
                    {warningsExpanded ? "Hide" : "Show"} {configOpStatus.warnings.length} warning
                    {configOpStatus.warnings.length !== 1 ? "s" : ""}
                  </button>
                  {warningsExpanded ? (
                    <ul className="mt-1.5 mb-0 pl-[18px] text-[0.84rem] text-foreground [overflow-wrap:anywhere]">
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
              className="border-0 bg-transparent text-inherit"
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

        <div className="grid grid-cols-[180px_minmax(0,1fr)] gap-6 max-[960px]:grid-cols-1">
          <nav
            className="settings-tags sticky top-4 flex flex-col gap-1 self-start max-[960px]:static max-[960px]:flex-row max-[960px]:flex-wrap"
            aria-label="Settings sections"
          >
            {[
              ...settingsSections,
              appearanceSection,
              applicationDetailsSection,
              apiTokensSection,
              trackerAuthSection,
            ].map((section) => (
              <button
                key={section.key}
                type="button"
                aria-pressed={settingsSection === section.key}
                className={cn(
                  "flex min-h-9 w-auto shrink-0 items-center rounded-md px-3 text-left text-sm font-medium transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring min-[961px]:w-full",
                  settingsSection === section.key
                    ? "bg-accent text-accent-foreground"
                    : "bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground",
                )}
                onClick={() => setSettingsSection(section.key)}
              >
                {section.label}
              </button>
            ))}
          </nav>
          <div className="settings-body min-w-0">
            {settingsSection === appearanceSection.key ? <AppearanceSettings /> : null}
            {settingsSection === applicationDetailsSection.key ? applicationDetailsPanel : null}
            {settingsSection === apiTokensSection.key ? <APITokensSettings /> : null}
            {settingsSection === trackerAuthSection.key ? trackerAuthPanel : null}
            {settingsSection === appearanceSection.key ||
            settingsSection === applicationDetailsSection.key ||
            settingsSection === apiTokensSection.key ||
            settingsSection === trackerAuthSection.key ? null : configData ? (
              <div className={settingsStyle.form}>
                <h2 className="m-0 text-lg font-semibold">
                  {settingsSections.find((section) => section.key === settingsSection)?.label}
                </h2>
                {showAdvancedToggle ? (
                  <label className={`${settingsStyle.switchRow} self-start gap-6`}>
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
                  </label>
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
                  <div className="grid min-w-0 gap-5">
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
                      return (
                        <SettingsFieldGroups
                          section={section.jsonKey}
                          fields={Object.entries(sectionData as ConfigMap)
                            .filter(([key]) => !meta[key]?.advanced || advancedOpen)
                            .map(([key, value]) => [
                              key,
                              renderField(key, value, [section.jsonKey, key], meta[key]),
                            ])}
                        />
                      );
                    })()}
                  </div>
                )}
              </div>
            ) : (
              <p className="text-muted-foreground">Loading configuration...</p>
            )}
          </div>
        </div>

        {settingsSaved ? (
          <p className="mt-[9px] text-[var(--status-success)]">{settingsSaved}</p>
        ) : null}
        {settingsError ? <p className={pageStyle.error}>{settingsError}</p> : null}
      </section>

      <AlertDialog.Root
        open={importConfirmOpen}
        onOpenChange={(open) => {
          if (!open) handleImportConfigCancel();
        }}
      >
        <AlertDialog.Portal>
          <AlertDialog.Overlay className={confirmationDialogStyle.overlay} />
          <AlertDialog.Content className={confirmationDialogStyle.content}>
            <div>
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
            <div className="flex min-w-0 flex-col gap-2">
              <AlertDialog.Title asChild>
                <h2 className="m-0">Replace current configuration?</h2>
              </AlertDialog.Title>
              <AlertDialog.Description asChild>
                <p className="m-0">
                  Importing a configuration file will overwrite your current settings in the
                  database. This action cannot be undone.
                </p>
              </AlertDialog.Description>
              <p className="m-0">
                We strongly recommend exporting your current configuration first so you can restore
                it if the imported file isn&apos;t what you expected.
              </p>
            </div>
            <div className={confirmationDialogStyle.actions}>
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
                  className="bg-destructive text-destructive-foreground"
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
      return settingsStyle.authReady;
    case "login_required":
    case "encrypted_storage_unavailable":
    case "error":
      return settingsStyle.authWarning;
    default:
      return settingsStyle.authIdle;
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
