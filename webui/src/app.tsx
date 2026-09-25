// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { applicationClient, configClient, hostBrowser as hostBrowserClient } from "./api/app";
import { isHostPathCaseInsensitive } from "./api/client";
import { WorkflowOperationProgress } from "./components/WorkflowOperationProgress";
import { WorkflowRequiredActions } from "./components/WorkflowRequiredActions";
import BlurayCandidatesPage from "./pages/bluray_candidates";
import DescriptionBuilderPage from "./pages/description_builder";
import DupeCheckPage from "./pages/dupe_check";
import HistoryPage from "./pages/history";
import InputPage from "./pages/input";
import LoggingPage from "./pages/logging";
import MenuImagesPage from "./pages/menu_images";
import ScreenshotsPage from "./pages/screenshots";
import TrackerDataPage from "./pages/tracker_data";
import TrackerUploadPage from "./pages/tracker_upload";
import UploadImagesPage from "./pages/upload_images";
import { useSettingsState } from "./hooks/useSettingsState";
import { useTrackerIcons } from "./hooks/useTrackerIcons";
import { ReleaseSessionProvider, useReleaseSession } from "./releaseSession";
import { TrackerCatalogProvider, useTrackerCatalog } from "./trackerCatalog";
import type { ReleaseRoute } from "./releaseSession/types";
import type { ApplicationInfo, BrowseDirectoryResponse, ConfigMap } from "./types";
import { formatApplicationVersion, getApplicationVersionDisplay } from "./utils/applicationInfo";
import { cn } from "./utils/cn";
import { handleExternalLinkClick } from "./utils/externalLinks";
import {
  addSourcePathHistoryEntry,
  defaultInputHistoryLimit,
  filterBrowseEntries,
  inferSourcePathMode,
  normalizeSourcePathHistory,
  resolveInputHistoryLimit,
  sourcePathHistoryStorageKey,
  type SourcePathHistoryEntry,
  type SourcePathMode,
} from "./utils/inputHistory";

const AudioAnalysisPage = lazy(() => import("./pages/audio_analysis"));
const SettingsPage = lazy(() => import("./pages/settings"));

const appLayoutClass =
  "relative z-[1] block min-h-screen ml-[204px] max-[960px]:ml-0 max-[960px]:pb-[78px]";
const sidebarClass =
  "fixed left-0 top-0 z-[1000] flex h-screen w-[204px] flex-col gap-2.5 border-r border-white/10 bg-[var(--panel)]/95 p-2.5 backdrop-blur max-[960px]:bottom-0 max-[960px]:top-auto max-[960px]:h-auto max-[960px]:w-full max-[960px]:flex-row max-[960px]:items-center max-[960px]:gap-2 max-[960px]:border-r-0 max-[960px]:border-t max-[960px]:p-2";
const sidebarGroupClass =
  "grid gap-1 rounded-lg border border-[rgba(148,163,184,0.18)] bg-[rgba(148,163,184,0.08)] p-1.5 max-[960px]:flex max-[960px]:flex-wrap max-[960px]:gap-1 max-[960px]:p-1";
const navButtonClass = (active: boolean, nested = false) =>
  cn(
    "w-full rounded-md border border-transparent bg-transparent px-2 py-1.5 text-left text-[0.84rem] font-semibold leading-tight text-[var(--muted)] transition hover:bg-white/10 hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-45 max-[960px]:w-auto",
    nested && "pl-4 text-[0.8rem] font-medium max-[960px]:pl-2",
    active &&
      "border-[var(--sidebar-active-border)] bg-[var(--sidebar-active-bg)] text-[var(--sidebar-active-text)]",
  );

type ActiveTab =
  | "input"
  | "tracker"
  | "bluray"
  | "audio_analysis"
  | "dupes"
  | "screenshots"
  | "menu_images"
  | "upload_images"
  | "description_builder"
  | "upload"
  | "history"
  | "settings"
  | "logging";
type ThemeMode = "light" | "dark" | "auto";

const releaseRouteTabs: Readonly<Record<ReleaseRoute, ActiveTab>> = {
  input: "input",
  trackerData: "tracker",
  audioAnalysis: "audio_analysis",
  duplicates: "dupes",
  screenshots: "screenshots",
  menuImages: "menu_images",
  uploadedImages: "upload_images",
  descriptions: "description_builder",
  upload: "upload",
};
type ConfigOpStatus = {
  type: "success" | "error" | "warning";
  title: string;
  message: string;
  warnings?: string[];
} | null;

const browserStorage = () => {
  try {
    return document.defaultView?.localStorage || null;
  } catch {
    return null;
  }
};

/** Shell/router around the sole active release-session interface. */
function AppShell({
  applicationInfo,
  runtimeInfoError,
}: Readonly<{ applicationInfo: ApplicationInfo | null; runtimeInfoError: boolean }>) {
  const releaseSession = useReleaseSession();
  const [activeTab, setActiveTab] = useState<ActiveTab>("input");
  const [navigationNotice, setNavigationNotice] = useState("");
  const [lightboxImage, setLightboxImage] = useState("");
  const [lightboxAlt, setLightboxAlt] = useState("");
  const [theme, setTheme] = useState<ThemeMode>(
    () => (browserStorage()?.getItem("theme") as ThemeMode | null) || "auto",
  );
  const [sourcePathHistory, setSourcePathHistory] = useState<SourcePathHistoryEntry[]>(() => {
    try {
      return normalizeSourcePathHistory(
        JSON.parse(browserStorage()?.getItem(sourcePathHistoryStorageKey) || "[]"),
        defaultInputHistoryLimit,
        isHostPathCaseInsensitive(),
      );
    } catch {
      return [];
    }
  });
  const [hostBrowserMode, setHostBrowserMode] = useState<SourcePathMode | null>(null);
  const [hostBrowser, setHostBrowser] = useState<BrowseDirectoryResponse | null>(null);
  const [hostBrowserLoading, setHostBrowserLoading] = useState(false);
  const [hostBrowserError, setHostBrowserError] = useState("");
  const [hostBrowserSearch, setHostBrowserSearch] = useState("");
  const [settingsExporting, setSettingsExporting] = useState(false);
  const [settingsImporting, setSettingsImporting] = useState(false);
  const [importConfirmOpen, setImportConfirmOpen] = useState(false);
  const [configOpStatus, setConfigOpStatus] = useState<ConfigOpStatus>(null);
  const applicationVersion = applicationInfo ? getApplicationVersionDisplay(applicationInfo) : null;
  const applicationVersionLabel = applicationInfo ? formatApplicationVersion(applicationInfo) : "";

  const settings = useSettingsState({ activeTab });
  const {
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
    renderImageHostingSection,
    renderTrackerSection,
    renderTorrentClientsSection,
    renderField,
    sectionFieldMeta,
    updateConfigValue,
    screenshotConfig,
    clearSettingsStatus,
    resolveImageHostLabel,
    trackerSelectionNames,
    settingsTrackerSelectionNames,
  } = settings;

  const inputHistoryLimit = useMemo(() => {
    const main = (configData?.MainSettings || null) as ConfigMap | null;
    return resolveInputHistoryLimit(main?.InputHistoryLimit);
  }, [configData]);
  const useFavicons = useMemo(() => {
    const main = (configData?.MainSettings || null) as ConfigMap | null;
    return typeof main?.UseFavicons === "boolean" ? main.UseFavicons : true;
  }, [configData]);
  const faviconOnly = useMemo(() => {
    const main = (configData?.MainSettings || null) as ConfigMap | null;
    return typeof main?.FaviconOnly === "boolean" ? main.FaviconOnly : false;
  }, [configData]);
  const trackerUploadItems = useMemo(() => {
    const root = configData?.Trackers as ConfigMap | undefined;
    const entriesRoot = root?.Trackers;
    if (!entriesRoot || typeof entriesRoot !== "object" || Array.isArray(entriesRoot)) return [];
    const visible = new Set(trackerSelectionNames);
    return Object.entries(entriesRoot as ConfigMap)
      .filter(
        ([name, value]) =>
          visible.has(name) && value && typeof value === "object" && !Array.isArray(value),
      )
      .map(([name, config]) => ({ name, config: config as ConfigMap }))
      .sort((left, right) => left.name.localeCompare(right.name));
  }, [configData, trackerSelectionNames]);
  const trackerIconSrcByName = useTrackerIcons(trackerUploadItems, useFavicons);
  const maxMenuItems = useMemo(() => {
    const value = screenshotConfig?.MaxMenuItems;
    return typeof value === "number" && Number.isFinite(value) && value > 0 ? Math.trunc(value) : 6;
  }, [screenshotConfig]);

  const preview = releaseSession.identity.view.preview;
  const sourcePath = releaseSession.identity.view.sourcePath;
  const access = releaseSession.navigation.view.access;
  const hasTrackerData = releaseSession.input.view.trackerData.length > 0;
  const hasBlurayData = Boolean(preview?.Bluray);
  const hasAudioData = releaseSession.audioAnalysis.view.available;
  const currentDiscType =
    releaseSession.workflow.view.current?.release?.release.Source.Classification.DiscType || "";

  const applyTheme = useCallback((value: ThemeMode) => {
    const resolved =
      value === "auto"
        ? document.defaultView?.matchMedia?.("(prefers-color-scheme: dark)").matches
          ? "dark"
          : "light"
        : value;
    document.documentElement.classList.remove("light", "dark");
    document.documentElement.classList.add(resolved);
  }, []);
  useEffect(() => applyTheme(theme), [applyTheme, theme]);

  const persistHistory = useCallback((entries: SourcePathHistoryEntry[]) => {
    setSourcePathHistory(entries);
    try {
      if (entries.length)
        browserStorage()?.setItem(sourcePathHistoryStorageKey, JSON.stringify(entries));
      else browserStorage()?.removeItem(sourcePathHistoryStorageKey);
    } catch {
      // Local storage can be disabled by browser policy.
    }
  }, []);
  const rememberSource = useCallback(
    (path: string, mode: SourcePathMode) => {
      setSourcePathHistory((previous) => {
        const next = addSourcePathHistoryEntry(
          previous,
          path,
          mode,
          inputHistoryLimit,
          isHostPathCaseInsensitive(),
        );
        try {
          if (next.length)
            browserStorage()?.setItem(sourcePathHistoryStorageKey, JSON.stringify(next));
          else browserStorage()?.removeItem(sourcePathHistoryStorageKey);
        } catch {
          // Local storage can be disabled by browser policy.
        }
        return next;
      });
    },
    [inputHistoryLimit],
  );
  useEffect(() => {
    const release = releaseSession.identity.view.release;
    if (release?.SourcePath)
      rememberSource(release.SourcePath, inferSourcePathMode(release.SourcePath));
  }, [releaseSession.identity.view.release, rememberSource]);
  useEffect(() => {
    persistHistory(
      normalizeSourcePathHistory(sourcePathHistory, inputHistoryLimit, isHostPathCaseInsensitive()),
    );
    // Re-normalize only when the configured limit changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inputHistoryLimit]);

  const openReleaseTab = (tab: ActiveTab, route: ReleaseRoute) => {
    const routeAccess = access[route];
    if (!routeAccess.available) {
      setNavigationNotice(routeAccess.reason);
      return;
    }
    setNavigationNotice("");
    setActiveTab(tab);
  };

  const loadHostDirectory = async (path: string, mode: SourcePathMode) => {
    setHostBrowserLoading(true);
    setHostBrowserError("");
    try {
      setHostBrowser(await hostBrowserClient.list(path, mode));
    } catch (error) {
      setHostBrowserError(error instanceof Error ? error.message : String(error));
    } finally {
      setHostBrowserLoading(false);
    }
  };
  const openHostBrowser = (mode: SourcePathMode) => {
    setHostBrowserMode(mode);
    setHostBrowserSearch("");
    void loadHostDirectory(releaseSession.input.view.sourceDraft, mode);
  };
  const selectHostPath = (path: string, isDir: boolean) => {
    if (!hostBrowserMode) return;
    if ((hostBrowserMode === "folder" && !isDir) || (hostBrowserMode === "file" && isDir)) {
      return;
    }
    releaseSession.input.updateSourceDraft(path);
    releaseSession.input.selectSource(path);
    rememberSource(path, hostBrowserMode);
    setHostBrowserMode(null);
    setActiveTab("input");
  };
  const visibleHostEntries = useMemo(
    () => filterBrowseEntries(hostBrowser?.entries || [], hostBrowserSearch),
    [hostBrowser?.entries, hostBrowserSearch],
  );

  const handleExportSettings = async () => {
    clearSettingsStatus();
    setConfigOpStatus(null);
    setSettingsExporting(true);
    try {
      const file = await configClient.exportDownload();
      setConfigOpStatus({ type: "success", title: "Configuration exported", message: file });
    } catch (error) {
      setConfigOpStatus({ type: "error", title: "Export failed", message: String(error) });
    } finally {
      setSettingsExporting(false);
    }
  };
  const handleImportConfigConfirm = async () => {
    setSettingsImporting(true);
    try {
      const result = await configClient.importFile();
      if (result.message) {
        setConfigOpStatus({
          type: result.warnings.length ? "warning" : "success",
          title: result.warnings.length ? "Imported with warnings" : "Configuration imported",
          message: result.message,
          warnings: result.warnings,
        });
        loadSettings();
      }
    } catch (error) {
      setConfigOpStatus({ type: "error", title: "Import failed", message: String(error) });
    } finally {
      setSettingsImporting(false);
      setImportConfirmOpen(false);
    }
  };

  return (
    <div className="app-shell">
      <div className="gradient-orb orb-a" />
      <div className="gradient-orb orb-b" />
      <div className={appLayoutClass}>
        <aside className={sidebarClass}>
          <div className={sidebarGroupClass}>
            <button
              className={navButtonClass(activeTab === "input")}
              type="button"
              onClick={() => setActiveTab("input")}
            >
              Input
            </button>
            {hasTrackerData ? (
              <button
                className={navButtonClass(activeTab === "tracker", true)}
                type="button"
                disabled={!access.trackerData.available}
                title={access.trackerData.reason}
                onClick={() => openReleaseTab("tracker", "trackerData")}
              >
                Tracker Data
              </button>
            ) : null}
            {hasBlurayData ? (
              <button
                className={navButtonClass(activeTab === "bluray", true)}
                type="button"
                onClick={() => setActiveTab("bluray")}
              >
                Blu-ray Candidates
              </button>
            ) : null}
            {hasAudioData ? (
              <button
                className={navButtonClass(activeTab === "audio_analysis", true)}
                type="button"
                disabled={!access.audioAnalysis.available}
                title={access.audioAnalysis.reason}
                onClick={() => openReleaseTab("audio_analysis", "audioAnalysis")}
              >
                Audio Analysis
              </button>
            ) : null}
            <button
              className={navButtonClass(activeTab === "dupes")}
              type="button"
              disabled={!access.duplicates.available}
              title={access.duplicates.reason}
              onClick={() => openReleaseTab("dupes", "duplicates")}
            >
              Dupe Check
            </button>
            <button
              className={navButtonClass(activeTab === "screenshots")}
              type="button"
              disabled={!access.screenshots.available}
              title={access.screenshots.reason}
              onClick={() => openReleaseTab("screenshots", "screenshots")}
            >
              Screenshots
            </button>
            <button
              className={navButtonClass(activeTab === "menu_images", true)}
              type="button"
              disabled={!access.menuImages.available}
              title={access.menuImages.reason}
              onClick={() => openReleaseTab("menu_images", "menuImages")}
            >
              Menu Images
            </button>
            <button
              className={navButtonClass(activeTab === "upload_images", true)}
              type="button"
              disabled={!access.uploadedImages.available}
              title={access.uploadedImages.reason}
              onClick={() => openReleaseTab("upload_images", "uploadedImages")}
            >
              Upload Images
            </button>
            <button
              className={navButtonClass(activeTab === "description_builder")}
              type="button"
              disabled={!access.descriptions.available}
              title={access.descriptions.reason}
              onClick={() => openReleaseTab("description_builder", "descriptions")}
            >
              Descriptions
            </button>
            <button
              className={navButtonClass(activeTab === "upload")}
              type="button"
              disabled={!access.upload.available}
              title={access.upload.reason}
              onClick={() => openReleaseTab("upload", "upload")}
            >
              Upload
            </button>
          </div>
          <div className={`${sidebarGroupClass} mt-auto max-[960px]:mt-0`}>
            <button
              className={navButtonClass(activeTab === "history")}
              type="button"
              onClick={() => setActiveTab("history")}
            >
              History
            </button>
            <button
              className={navButtonClass(activeTab === "settings")}
              type="button"
              onClick={() => setActiveTab("settings")}
            >
              Settings
            </button>
            <button
              className={navButtonClass(activeTab === "logging")}
              type="button"
              onClick={() => setActiveTab("logging")}
            >
              Logging
            </button>
            <button
              className={navButtonClass(false)}
              type="button"
              onClick={() => {
                const modes: ThemeMode[] = ["auto", "light", "dark"];
                const next = modes[(modes.indexOf(theme) + 1) % modes.length];
                setTheme(next);
                browserStorage()?.setItem("theme", next);
              }}
            >
              Theme: {theme}
            </button>
          </div>
          <div className="mt-1 grid gap-1 rounded-lg border border-[rgba(148,163,184,0.18)] bg-[rgba(148,163,184,0.08)] px-2 py-1.5 text-[0.72rem] leading-tight text-[var(--muted)] max-[960px]:hidden">
            {applicationVersion ? (
              <div
                className="grid min-w-0 font-semibold text-[var(--text)]"
                title={applicationVersionLabel}
              >
                <span>{applicationVersion.version}</span>
                {applicationVersion.buildDate ? (
                  <span>({applicationVersion.buildDate})</span>
                ) : null}
              </div>
            ) : null}
            <div className="flex min-w-0 items-center justify-between gap-1">
              <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">
                © 2026 autobrr
              </span>
              <div className="flex items-center gap-1">
                <a
                  className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-white/10 text-[var(--muted)] transition hover:border-[var(--accent)] hover:text-[var(--accent)]"
                  href="https://discord.autobrr.com"
                  target="_blank"
                  rel="noreferrer"
                  onAuxClick={handleExternalLinkClick}
                  onClick={handleExternalLinkClick}
                  aria-label="Open the autobrr Discord"
                  title="autobrr Discord"
                >
                  <svg
                    aria-hidden="true"
                    viewBox="0 0 24 24"
                    className="h-4 w-4"
                    fill="currentColor"
                  >
                    <path d="M19.5 5.34A17.3 17.3 0 0 0 15.44 4l-.5 1.02a15.8 15.8 0 0 0-5.86 0L8.55 4A17.5 17.5 0 0 0 4.5 5.35C1.93 9.2 1.23 12.96 1.58 16.67a17.7 17.7 0 0 0 4.98 2.51l1.2-1.64a11.2 11.2 0 0 1-1.88-.9l.46-.36c3.63 1.68 7.57 1.68 11.16 0l.47.36c-.6.36-1.23.66-1.89.9l1.2 1.64a17.6 17.6 0 0 0 4.98-2.51c.42-4.3-.72-8.03-2.76-11.33ZM8.68 14.4c-1.09 0-1.98-1-1.98-2.22 0-1.23.87-2.23 1.98-2.23 1.12 0 2 1 1.98 2.23 0 1.22-.87 2.22-1.98 2.22Zm6.64 0c-1.1 0-1.98-1-1.98-2.22 0-1.23.87-2.23 1.98-2.23 1.12 0 2 1 1.98 2.23 0 1.22-.86 2.22-1.98 2.22Z" />
                  </svg>
                </a>
                <a
                  className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-white/10 text-[var(--muted)] transition hover:border-[var(--accent)] hover:text-[var(--accent)]"
                  href="https://github.com/autobrr/upbrr"
                  target="_blank"
                  rel="noreferrer"
                  onAuxClick={handleExternalLinkClick}
                  onClick={handleExternalLinkClick}
                  aria-label="Open autobrr/upbrr on GitHub"
                  title="autobrr/upbrr"
                >
                  <svg
                    aria-hidden="true"
                    viewBox="0 0 16 16"
                    className="h-4 w-4"
                    fill="currentColor"
                  >
                    <path d="M8 0C3.58 0 0 3.67 0 8.2c0 3.62 2.29 6.69 5.47 7.78.4.08.55-.18.55-.4l-.01-1.4c-2.22.5-2.69-1.1-2.69-1.1-.36-.95-.89-1.2-.89-1.2-.73-.51.05-.5.05-.5.81.06 1.24.85 1.24.85.72 1.27 1.89.9 2.35.69.07-.53.28-.9.51-1.1-1.78-.21-3.64-.91-3.64-4.04 0-.89.31-1.62.82-2.19-.08-.21-.36-1.04.08-2.16 0 0 .68-.22 2.2.84A7.37 7.37 0 0 1 8 3.99c.68 0 1.36.09 2 .28 1.52-1.06 2.19-.84 2.19-.84.44 1.12.16 1.95.08 2.16.52.57.82 1.3.82 2.19 0 3.14-1.87 3.83-3.65 4.04.29.25.54.76.54 1.54l-.01 2.22c0 .22.14.48.55.4A8.13 8.13 0 0 0 16 8.2C16 3.67 12.42 0 8 0Z" />
                  </svg>
                </a>
              </div>
            </div>
          </div>
        </aside>

        <main className="content">
          {applicationInfo?.testRuntime?.mode === "live_test" ? (
            <div className="panel mb-4 border-amber-500 p-4" role="status">
              <strong>Live testing active</strong>
              <p>
                Tracker submission and torrent-client writes are disabled. Run a dry run to test
                preparation with normal rules. Image uploads require a journal.
              </p>
              <p className="break-all">Run: {applicationInfo.testRuntime.runId}</p>
            </div>
          ) : runtimeInfoError ? (
            <p className="error" role="alert">
              Runtime capabilities could not be loaded. Uploads are disabled; reload to try again.
            </p>
          ) : !applicationInfo ? (
            <p className="muted" role="status">
              Checking runtime capabilities…
            </p>
          ) : null}
          {navigationNotice ? (
            <p className="muted" role="status">
              {navigationNotice}
            </p>
          ) : null}
          <WorkflowOperationProgress operation={releaseSession.workflow.view.current?.operation} />
          <WorkflowRequiredActions
            continuation={releaseSession.workflow.view.current?.continuation}
            onConfirm={(action, confirmed) =>
              void releaseSession.workflow.confirmAction(action, confirmed)
            }
            onNavigate={(route) => openReleaseTab(releaseRouteTabs[route], route)}
          />
          {activeTab === "settings" ? (
            <Suspense fallback={<p className="muted">Loading settings…</p>}>
              <SettingsPage
                configData={settingsConfigData}
                settingsLoading={settingsLoading}
                settingsExporting={settingsExporting}
                settingsImporting={settingsImporting}
                settingsDirty={settingsDirty}
                settingsSaved={settingsSaved}
                settingsError={settingsError}
                configOpStatus={configOpStatus}
                dismissConfigOpStatus={() => setConfigOpStatus(null)}
                settingsSection={settingsSection}
                settingsSections={settingsSections}
                trackerSelectionNames={settingsTrackerSelectionNames}
                showAdvancedToggle={showAdvancedToggle}
                advancedOpen={advancedOpen}
                setSettingsSection={setSettingsSection}
                setSettingsAdvanced={setSettingsAdvanced}
                loadSettings={loadSettings}
                handleExportSettings={() => void handleExportSettings()}
                handleImportConfig={() => setImportConfirmOpen(true)}
                importConfirmOpen={importConfirmOpen}
                handleImportConfigConfirm={handleImportConfigConfirm}
                handleImportConfigCancel={() => !settingsImporting && setImportConfirmOpen(false)}
                handleSaveSettings={handleSaveSettings}
                renderImageHostingSection={renderImageHostingSection}
                renderTrackerSection={renderTrackerSection}
                renderTorrentClientsSection={renderTorrentClientsSection}
                renderField={renderField}
                sectionFieldMeta={sectionFieldMeta}
              />
            </Suspense>
          ) : activeTab === "logging" ? (
            <LoggingPage
              configData={settingsConfigData}
              settingsLoading={settingsLoading}
              settingsDirty={settingsDirty}
              settingsSaved={settingsSaved}
              settingsError={settingsError}
              loadSettings={loadSettings}
              handleSaveSettings={handleSaveSettings}
              renderField={renderField}
              updateConfigValue={updateConfigValue}
              sectionFieldMeta={sectionFieldMeta}
            />
          ) : activeTab === "history" ? (
            <HistoryPage
              onOpenInput={async (path) => {
                const opened = await releaseSession.input.openSource(path);
                if (opened) setActiveTab("input");
                return opened;
              }}
              onReleaseDeleted={(deletedPath) => {
                if (deletedPath === sourcePath) releaseSession.input.selectSource("");
              }}
            />
          ) : activeTab === "dupes" ? (
            <DupeCheckPage
              facet={releaseSession.duplicates}
              sourcePath={sourcePath}
              trackerUploadItems={trackerUploadItems}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
              submissionExclusions={releaseSession.upload.view.submissionExclusions}
              workflowComplete={
                releaseSession.workflow.view.current?.workflow.status === "completed"
              }
            />
          ) : activeTab === "screenshots" ? (
            <ScreenshotsPage
              facet={releaseSession.screenshots}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
            />
          ) : activeTab === "menu_images" ? (
            <MenuImagesPage
              facet={releaseSession.menuImages}
              currentDiscType={currentDiscType}
              maxMenuItems={maxMenuItems}
              onContinue={() => openReleaseTab("upload_images", "uploadedImages")}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
            />
          ) : activeTab === "upload_images" ? (
            <UploadImagesPage
              facet={releaseSession.uploadedImages}
              resolveImageHostLabel={resolveImageHostLabel}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
            />
          ) : activeTab === "bluray" ? (
            <BlurayCandidatesPage
              facet={releaseSession.input}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
            />
          ) : activeTab === "audio_analysis" ? (
            <Suspense fallback={<p className="muted">Loading audio analysis…</p>}>
              <AudioAnalysisPage
                key={releaseSession.audioAnalysis.view.releaseGeneration}
                facet={releaseSession.audioAnalysis}
                setLightboxImage={setLightboxImage}
                setLightboxAlt={setLightboxAlt}
              />
            </Suspense>
          ) : activeTab === "description_builder" ? (
            <DescriptionBuilderPage
              facet={releaseSession.descriptions}
              sourcePath={sourcePath}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
            />
          ) : activeTab === "upload" ? (
            <TrackerUploadPage facet={releaseSession.upload} />
          ) : activeTab === "tracker" ? (
            <TrackerDataPage
              facet={releaseSession.input}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
            />
          ) : (
            <InputPage
              facet={releaseSession.input}
              sourcePathHistory={sourcePathHistory}
              handleBrowseFile={() => openHostBrowser("file")}
              handleBrowseFolder={() => openHostBrowser("folder")}
              trackerUploadItems={trackerUploadItems}
              showExternalIDInputUI
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
            />
          )}
        </main>

        <Dialog.Root
          open={Boolean(lightboxImage)}
          onOpenChange={(open) => {
            if (!open) {
              setLightboxImage("");
              setLightboxAlt("");
            }
          }}
        >
          <Dialog.Portal>
            <Dialog.Overlay className="dialog-overlay" />
            <Dialog.Content className="lightbox-content">
              <Dialog.Title className="sr-only">{lightboxAlt || "Image preview"}</Dialog.Title>
              <Dialog.Description className="sr-only">
                Expanded release image preview.
              </Dialog.Description>
              <img src={lightboxImage} alt={lightboxAlt} />
              <Dialog.Close className="ghost" aria-label="Close image preview">
                Close
              </Dialog.Close>
            </Dialog.Content>
          </Dialog.Portal>
        </Dialog.Root>

        <Dialog.Root
          open={Boolean(hostBrowserMode)}
          onOpenChange={(open) => {
            if (!open) setHostBrowserMode(null);
          }}
        >
          <Dialog.Portal>
            <Dialog.Overlay className="host-browser-overlay" />
            <Dialog.Content className="host-browser-dialog">
              <div className="host-browser-header">
                <div>
                  <Dialog.Title asChild>
                    <h2 className="label">Host browser</h2>
                  </Dialog.Title>
                  <Dialog.Description asChild>
                    <p className="mono host-browser-path">
                      {hostBrowser?.currentPath || "Computer"}
                    </p>
                  </Dialog.Description>
                </div>
                <Dialog.Close asChild>
                  <button className="ghost" type="button">
                    Close
                  </button>
                </Dialog.Close>
              </div>
              <div className="host-browser-toolbar">
                <button
                  className="ghost"
                  type="button"
                  disabled={!hostBrowser?.parentPath || hostBrowserLoading}
                  onClick={() => {
                    if (hostBrowserMode && hostBrowser?.parentPath)
                      void loadHostDirectory(hostBrowser.parentPath, hostBrowserMode);
                  }}
                >
                  Up
                </button>
                <button
                  className="ghost"
                  type="button"
                  disabled={hostBrowserLoading}
                  onClick={() => {
                    if (hostBrowserMode) void loadHostDirectory("", hostBrowserMode);
                  }}
                >
                  Roots
                </button>
                {hostBrowserMode === "folder" && hostBrowser?.currentPath ? (
                  <button
                    className="primary"
                    type="button"
                    disabled={hostBrowserLoading}
                    onClick={() => selectHostPath(hostBrowser.currentPath, true)}
                  >
                    Select folder
                  </button>
                ) : null}
                <label className="host-browser-search" htmlFor="host-browser-search">
                  <span>Search</span>
                  <input
                    id="host-browser-search"
                    className="host-browser-search__input"
                    value={hostBrowserSearch}
                    onChange={(event) => setHostBrowserSearch(event.target.value)}
                    placeholder="Filter current path"
                    disabled={hostBrowserLoading || !hostBrowser}
                  />
                </label>
              </div>
              {hostBrowserError ? (
                <p className="error" role="alert">
                  {hostBrowserError}
                </p>
              ) : null}
              {hostBrowserLoading ? <p className="muted">Loading host paths...</p> : null}
              {!hostBrowserLoading && hostBrowser ? (
                <div className="host-browser-list">
                  {visibleHostEntries.length === 0 ? (
                    <p className="muted host-browser-empty">No matching paths.</p>
                  ) : (
                    visibleHostEntries.map((entry) => (
                      <div className="host-browser-entry" key={entry.path}>
                        <span className="host-browser-entry__name">
                          {entry.isDir ? "[DIR] " : ""}
                          {entry.name}
                        </span>
                        <span className="host-browser-entry__meta">
                          {entry.isDir
                            ? "Folder"
                            : `${Math.round(entry.size / 1024).toLocaleString()} KiB`}
                        </span>
                        <span className="host-browser-entry__actions">
                          {entry.isDir ? (
                            <button
                              className="ghost"
                              type="button"
                              aria-label={`Open ${entry.name}`}
                              onClick={() => {
                                if (hostBrowserMode)
                                  void loadHostDirectory(entry.path, hostBrowserMode);
                              }}
                            >
                              Open
                            </button>
                          ) : null}
                          {(hostBrowserMode === "folder" && entry.isDir) ||
                          (hostBrowserMode === "file" && !entry.isDir) ? (
                            <button
                              className="primary"
                              type="button"
                              aria-label={`Select ${entry.name}`}
                              onClick={() => selectHostPath(entry.path, entry.isDir)}
                            >
                              Select
                            </button>
                          ) : null}
                        </span>
                      </div>
                    ))
                  )}
                </div>
              ) : null}
            </Dialog.Content>
          </Dialog.Portal>
        </Dialog.Root>
      </div>
    </div>
  );
}

/** Composes the active release session around the shell. */
export default function App() {
  return (
    <TrackerCatalogProvider>
      <AppReleaseSession />
    </TrackerCatalogProvider>
  );
}

function AppReleaseSession() {
  const { catalog } = useTrackerCatalog();
  const [applicationInfo, setApplicationInfo] = useState<ApplicationInfo | null>(null);
  const [runtimeInfoError, setRuntimeInfoError] = useState(false);
  useEffect(() => {
    let active = true;
    void applicationClient.getInfo().then(
      (info) => {
        if (active) setApplicationInfo(info);
      },
      () => {
        if (active) setRuntimeInfoError(true);
      },
    );
    return () => {
      active = false;
    };
  }, []);
  const defaultTrackers = useMemo(
    () =>
      (catalog?.entries || [])
        .filter((entry) => entry.configured && entry.default === true)
        .map((entry) => entry.name),
    [catalog],
  );
  return (
    <ReleaseSessionProvider
      defaultTrackers={defaultTrackers}
      testRuntime={applicationInfo?.testRuntime}
      runtimeInfoReady={applicationInfo !== null}
    >
      <AppShell applicationInfo={applicationInfo} runtimeInfoError={runtimeInfoError} />
    </ReleaseSessionProvider>
  );
}
