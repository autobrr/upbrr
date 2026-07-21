// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import * as AlertDialog from "@radix-ui/react-alert-dialog";
import { Button } from "../../components/ui/button";
import { Switch } from "../../components/ui/switch";
import { cn } from "../../utils/cn";
import { handleExternalLinkClick } from "../../utils/externalLinks";
import type {
  ApplicationInfo,
  ConfigMap,
  ConfigValue,
  FieldMeta,
  TrackerAuthCapability,
  TrackerAuthStatus,
  WebAuthStatus,
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

// Heroicons outline paths for the settings sidebar, mirroring autobrr's
// icon-per-section settings navigation.
const sectionGlyphPaths: Record<string, string[]> = {
  Main: [
    "M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z",
    "M15 12a3 3 0 11-6 0 3 3 0 016 0z",
  ],
  "Image Hosting": [
    "M2.25 15.75l5.159-5.159a2.25 2.25 0 013.182 0l5.159 5.159m-1.5-1.5l1.409-1.409a2.25 2.25 0 013.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 001.5-1.5V6a1.5 1.5 0 00-1.5-1.5H3.75A1.5 1.5 0 002.25 6v12a1.5 1.5 0 001.5 1.5zm10.5-11.25h.008v.008h-.008V8.25zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z",
  ],
  Metadata: [
    "M9.568 3H5.25A2.25 2.25 0 003 5.25v4.318c0 .597.237 1.17.659 1.591l9.581 9.581c.699.699 1.78.872 2.607.33a18.095 18.095 0 005.223-5.223c.542-.827.369-1.908-.33-2.607L11.16 3.66A2.25 2.25 0 009.568 3z",
    "M6 6h.008v.008H6V6z",
  ],
  Screens: [
    "M6.827 6.175A2.31 2.31 0 015.186 7.23c-.38.054-.757.112-1.134.175C2.999 7.58 2.25 8.507 2.25 9.574V18a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9.574c0-1.067-.75-1.994-1.802-2.169a47.865 47.865 0 00-1.134-.175 2.31 2.31 0 01-1.64-1.055l-.822-1.316a2.192 2.192 0 00-1.736-1.039 48.774 48.774 0 00-5.232 0 2.192 2.192 0 00-1.736 1.039l-.821 1.316z",
    "M16.5 12.75a4.5 4.5 0 11-9 0 4.5 4.5 0 019 0zM18.75 10.5h.008v.008h-.008V10.5z",
  ],
  Description: [
    "M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z",
  ],
  Arr: [
    "M12.75 19.5v-.75a7.5 7.5 0 00-7.5-7.5H4.5m0-6.75h.75c7.87 0 14.25 6.38 14.25 14.25v.75M6 18.75a.75.75 0 11-1.5 0 .75.75 0 011.5 0z",
  ],
  "Post Upload": [
    "M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5m-13.5-9L12 3m0 0l4.5 4.5M12 3v13.5",
  ],
  Trackers: [
    "M15.75 5.25a3 3 0 013 3m3 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8.25v2.25H6v2.25H2.25v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121.75 8.25z",
  ],
  "Torrent Clients": [
    "M9 13.5l3 3m0 0l3-3m-3 3v-6m1.06-4.19l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z",
  ],
  "Client Handling": [
    "M10.5 6h9.75M10.5 6a1.5 1.5 0 11-3 0m3 0a1.5 1.5 0 10-3 0M3.75 6H7.5m3 12h9.75m-9.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-3.75 0H7.5m9-6h3.75m-3.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-9.75 0h9.75",
  ],
  "Torrent Specific": [
    "M10.5 6h9.75M10.5 6a1.5 1.5 0 11-3 0m3 0a1.5 1.5 0 10-3 0M3.75 6H7.5m3 12h9.75m-9.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-3.75 0H7.5m9-6h3.75m-3.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-9.75 0h9.75",
  ],
  "Application Details": [
    "M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z",
  ],
  "Tracker Auth": [
    "M17.982 18.725A7.488 7.488 0 0012 15.75a7.488 7.488 0 00-5.982 2.975m11.963 0a9 9 0 10-11.963 0m11.963 0A8.966 8.966 0 0112 21a8.966 8.966 0 01-5.982-2.275M15 9.75a3 3 0 11-6 0 3 3 0 016 0z",
  ],
};

function SectionGlyph({ label, className }: { label: string; className: string }) {
  const paths = sectionGlyphPaths[label] ?? sectionGlyphPaths.Main;
  return (
    <svg
      className={className}
      fill="none"
      viewBox="0 0 24 24"
      strokeWidth={1.5}
      stroke="currentColor"
      aria-hidden="true"
    >
      {paths.map((d) => (
        <path key={d.slice(0, 24)} strokeLinecap="round" strokeLinejoin="round" d={d} />
      ))}
    </svg>
  );
}

const settingsInputClass =
  "h-8 rounded-md border border-[var(--border)] bg-[var(--input-bg)] px-2.5 text-sm text-[var(--text)] outline-none transition placeholder:text-[var(--muted)] focus:border-[var(--accent-2)] focus:ring-2 focus:ring-blue-500/25";
// Tracker-supplied auth kinds can be long adapter descriptors; keep chips
// wrapped inside the auth card on narrow screens.
const trackerAuthChipClass =
  "max-w-full whitespace-normal rounded-full border border-[var(--border)] bg-[var(--panel-light)] px-[0.45rem] py-[0.2rem] text-[0.74rem] leading-none text-[var(--muted)] [overflow-wrap:anywhere]";
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

type AppBridgeWithApplicationInfo = {
  GetApplicationInfo?: () => Promise<ApplicationInfo>;
};

type AppBridgeWithTrackerAuth = {
  ListTrackerAuthCapabilities?: () => Promise<TrackerAuthCapability[]>;
  GetTrackerAuthStatus?: (tracker: string) => Promise<TrackerAuthStatus>;
  ImportTrackerAuthCookies?: (tracker: string) => Promise<TrackerAuthStatus>;
  TestTrackerAuth?: (tracker: string) => Promise<TrackerAuthStatus>;
  SubmitTrackerAuth2FA?: (challengeID: string, code: string) => Promise<TrackerAuthStatus>;
  DeleteTrackerAuth?: (tracker: string) => Promise<TrackerAuthStatus>;
};

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
  webAuthAvailable: boolean;
  webAuthStatus: WebAuthStatus | null;
  webAuthLoading: boolean;
  webAuthCreating: boolean;
  webAuthUsername: string;
  webAuthPassword: string;
  webAuthConfirm: string;
  webAuthError: string;
  setWebAuthUsername: Dispatch<SetStateAction<string>>;
  setWebAuthPassword: Dispatch<SetStateAction<string>>;
  setWebAuthConfirm: Dispatch<SetStateAction<string>>;
  handleCreateWebAuth: () => void;
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
    webAuthAvailable,
    webAuthStatus,
    webAuthLoading,
    webAuthCreating,
    webAuthUsername,
    webAuthPassword,
    webAuthConfirm,
    webAuthError,
    setWebAuthUsername,
    setWebAuthPassword,
    setWebAuthConfirm,
    handleCreateWebAuth,
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
    const getter = (globalThis.go?.guiapp?.App as AppBridgeWithApplicationInfo | undefined)
      ?.GetApplicationInfo;
    if (!getter) {
      setApplicationInfoError("Application details are unavailable in this build.");
      return () => {
        cancelled = true;
      };
    }

    setApplicationInfoLoading(true);
    setApplicationInfoError("");
    void getter()
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
    const bridge = globalThis.go?.guiapp?.App as AppBridgeWithTrackerAuth | undefined;
    const list = bridge?.ListTrackerAuthCapabilities;
    const getStatus = bridge?.GetTrackerAuthStatus;
    if (!list || !getStatus) {
      setTrackerAuthError("Tracker auth is unavailable in this build.");
      return () => {
        cancelled = true;
      };
    }

    setTrackerAuthLoading(true);
    setTrackerAuthError("");
    setTrackerAuthActionErrors({});
    void list()
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
          void getStatus(capability.trackerID)
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
    const bridge = globalThis.go?.guiapp?.App as AppBridgeWithTrackerAuth | undefined;
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
                      disabled={
                        !bridge?.SubmitTrackerAuth2FA || !status.challengeID || !code.trim()
                      }
                      onClick={() =>
                        runTrackerAuthAction(capability.trackerID, "2fa", () =>
                          bridge!.SubmitTrackerAuth2FA!(status.challengeID, code),
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
                      disabled={!bridge?.ImportTrackerAuthCookies || Boolean(busy)}
                      onClick={() =>
                        runTrackerAuthAction(capability.trackerID, "import", () =>
                          bridge!.ImportTrackerAuthCookies!(capability.trackerID),
                        )
                      }
                    >
                      {busy === "import" ? "Importing..." : "Import Cookies"}
                    </Button>
                  ) : null}
                  {canTestAuth ? (
                    <Button
                      type="button"
                      disabled={!bridge?.TestTrackerAuth || Boolean(busy)}
                      onClick={() =>
                        runTrackerAuthAction(capability.trackerID, "test", () =>
                          bridge!.TestTrackerAuth!(capability.trackerID),
                        )
                      }
                    >
                      {busy === "test" ? "Checking..." : "Check Auth"}
                    </Button>
                  ) : null}
                  <Button
                    type="button"
                    disabled={!bridge?.DeleteTrackerAuth || Boolean(busy)}
                    onClick={() =>
                      runTrackerAuthAction(capability.trackerID, "delete", () =>
                        bridge!.DeleteTrackerAuth!(capability.trackerID),
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

  const activeSectionLabel =
    [...settingsSections, applicationDetailsSection, trackerAuthSection].find(
      (item) => item.key === settingsSection,
    )?.label ?? "Settings";

  return (
    <div className="content-stack">
      <header className="hero">
        <h1>Settings</h1>
        <p className="subtitle">
          Edit settings by section. Changes apply immediately and are saved to SQLite.
        </p>
      </header>

      <section className="overflow-hidden rounded-lg border border-gray-250 bg-white shadow-table dark:border-gray-775 dark:bg-gray-800">
        <div className="lg:grid lg:grid-cols-12">
          <aside className="border-b border-gray-150 py-2 dark:border-gray-725 lg:col-span-3 lg:border-b-0 lg:border-r">
            <nav className="space-y-1">
              {[...settingsSections, applicationDetailsSection, trackerAuthSection].map(
                (section) => (
                  <button
                    key={section.key}
                    type="button"
                    className={cn(
                      "group flex w-full items-center rounded-none border-0 border-l-4 bg-transparent px-3 py-2 text-left text-sm font-medium shadow-none transition",
                      settingsSection === section.key
                        ? "border-sky-500 bg-blue-100 font-bold text-sky-700 hover:bg-blue-200 hover:text-sky-900 dark:border-blue-500 dark:bg-gray-700 dark:text-gray-200 dark:hover:bg-gray-600 dark:hover:text-white"
                        : "border-transparent text-gray-900 hover:bg-gray-100 hover:text-gray-900 dark:text-gray-300 dark:hover:bg-gray-600 dark:hover:text-gray-300",
                    )}
                    onClick={() => setSettingsSection(section.key)}
                  >
                    <SectionGlyph
                      label={section.label}
                      className="-ml-1 mr-3 h-6 w-6 shrink-0 text-gray-500 group-hover:text-gray-600 dark:text-gray-400 dark:group-hover:text-gray-300"
                    />
                    <span className="truncate">{section.label}</span>
                  </button>
                ),
              )}
            </nav>
          </aside>

          <div className="px-4 py-6 sm:p-6 lg:col-span-9 lg:pb-8">
            <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
              <div className="sm:px-2">
                <h2 className="text-lg font-bold leading-4 text-gray-900 dark:text-white">
                  {activeSectionLabel}
                </h2>
                <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  Invalid changes will be rejected with a validation error.
                </p>
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
                  disabled={
                    settingsLoading || settingsExporting || settingsImporting || !settingsDirty
                  }
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
                      <path
                        d="M10 7v4"
                        stroke="currentColor"
                        strokeWidth="1.5"
                        strokeLinecap="round"
                      />
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
                        {warningsExpanded ? "Hide" : "Show"} {configOpStatus.warnings.length}{" "}
                        warning
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

            <div className="settings-body">
              {settingsSection === applicationDetailsSection.key ? applicationDetailsPanel : null}
              {settingsSection === trackerAuthSection.key ? trackerAuthPanel : null}
              {settingsSection !== applicationDetailsSection.key &&
              settingsSection !== trackerAuthSection.key &&
              webAuthAvailable ? (
                <details className="settings-subgroup settings-subgroup--collapsible settings-subgroup--auth">
                  <summary>Secret Encryption</summary>
                  <div>
                    <p className="helper">
                      Desktop installs can keep using plaintext secrets, or you can create
                      <code> web-auth.json </code>
                      to enable encrypted secret storage for future saves and exports.
                    </p>
                    <div className="settings-auth-status">
                      <span
                        className={`settings-auth-badge ${webAuthStatus?.usable ? "is-ready" : webAuthStatus?.exists ? "is-warning" : "is-idle"}`}
                      >
                        {webAuthLoading
                          ? "Checking..."
                          : webAuthStatus?.usable
                            ? "Encryption enabled"
                            : webAuthStatus?.exists
                              ? "Auth file invalid"
                              : "Plaintext fallback active"}
                      </span>
                      {webAuthStatus?.path ? (
                        <p className="muted">Path: {webAuthStatus.path}</p>
                      ) : null}
                      {webAuthStatus?.message ? (
                        <p className="muted">{webAuthStatus.message}</p>
                      ) : null}
                      {webAuthStatus?.usable && webAuthStatus.username ? (
                        <p className="muted">Configured user: {webAuthStatus.username}</p>
                      ) : null}
                      {webAuthStatus?.browseRoot ? (
                        <p className="muted">Web browse root: {webAuthStatus.browseRoot}</p>
                      ) : null}
                      {webAuthStatus?.allowUnrestrictedBrowse ? (
                        <p className="muted">Web browse access: Unrestricted</p>
                      ) : null}
                    </div>
                    {webAuthStatus?.canCreate ? (
                      <div className="settings-grid">
                        <label className="settings-field">
                          <span>Username</span>
                          <input
                            className={settingsInputClass}
                            value={webAuthUsername}
                            onChange={(event) => setWebAuthUsername(event.target.value)}
                            autoComplete="username"
                          />
                        </label>
                        <label className="settings-field">
                          <span>Password</span>
                          <input
                            className={settingsInputClass}
                            type="password"
                            value={webAuthPassword}
                            onChange={(event) => setWebAuthPassword(event.target.value)}
                            autoComplete="new-password"
                          />
                        </label>
                        <label className="settings-field">
                          <span>Confirm password</span>
                          <input
                            className={settingsInputClass}
                            type="password"
                            value={webAuthConfirm}
                            onChange={(event) => setWebAuthConfirm(event.target.value)}
                            autoComplete="new-password"
                          />
                        </label>
                      </div>
                    ) : null}
                    <div className="settings-auth-actions">
                      <Button
                        variant="primary"
                        type="button"
                        onClick={handleCreateWebAuth}
                        disabled={
                          webAuthLoading ||
                          webAuthCreating ||
                          !webAuthStatus?.canCreate ||
                          !webAuthUsername.trim() ||
                          !webAuthPassword.trim() ||
                          !webAuthConfirm.trim()
                        }
                      >
                        {webAuthCreating ? "Creating..." : "Create web-auth.json"}
                      </Button>
                    </div>
                    {webAuthError ? <p className="error">{webAuthError}</p> : null}
                  </div>
                </details>
              ) : null}
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
                        const section = settingsSections.find(
                          (item) => item.key === settingsSection,
                        );
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

            {settingsSaved ? <p className="settings-saved">{settingsSaved}</p> : null}
            {settingsError ? <p className="error">{settingsError}</p> : null}
          </div>
        </div>
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
