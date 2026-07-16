// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { configClient, hostBrowser as hostBrowserClient } from "./api/app";
import { isHostPathCaseInsensitive } from "./api/client";
import BlurayCandidatesPage from "./pages/bluray_candidates";
import DescriptionBuilderPage from "./pages/description_builder";
import DupeCheckPage from "./pages/dupe_check";
import HistoryPage from "./pages/history";
import InputPage from "./pages/input";
import LoggingPage from "./pages/logging";
import MenuImagesPage from "./pages/menu_images";
import ScreenshotsPage from "./pages/screenshots";
import SettingsPage from "./pages/settings";
import TrackerDataPage from "./pages/tracker_data";
import TrackerUploadPage from "./pages/tracker_upload";
import UploadImagesPage from "./pages/upload_images";
import { useSettingsState } from "./hooks/useSettingsState";
import { useTrackerIcons } from "./hooks/useTrackerIcons";
import { JobRegistryProvider, useJobNotifications } from "./jobRegistry";
import { ReleaseSessionProvider, useReleaseSession } from "./releaseSession";
import type { ReleaseRoute } from "./releaseSession/types";
import type { BrowseDirectoryResponse, ConfigMap } from "./types";
import { cn } from "./utils/cn";
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
function AppShell() {
  const releaseSession = useReleaseSession();
  const jobNotifications = useJobNotifications();
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
  const sourceDraftRestored = useRef(false);
  const [hostBrowserMode, setHostBrowserMode] = useState<SourcePathMode | null>(null);
  const [hostBrowser, setHostBrowser] = useState<BrowseDirectoryResponse | null>(null);
  const [hostBrowserLoading, setHostBrowserLoading] = useState(false);
  const [hostBrowserError, setHostBrowserError] = useState("");
  const [hostBrowserSearch, setHostBrowserSearch] = useState("");
  const [settingsExporting, setSettingsExporting] = useState(false);
  const [settingsImporting, setSettingsImporting] = useState(false);
  const [importConfirmOpen, setImportConfirmOpen] = useState(false);
  const [configOpStatus, setConfigOpStatus] = useState<ConfigOpStatus>(null);

  const settings = useSettingsState({ activeTab });
  const {
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
    renderField,
    sectionFieldMeta,
    updateConfigValue,
    updateScreenshotConfigValue,
    configuredImageHosts,
    screenshotConfig,
    clearSettingsStatus,
    resolveImageHostLabel,
    trackerSelectionNames,
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
  const currentDiscType = /(^|[\\/])VIDEO_TS([\\/]|$)/i.test(sourcePath)
    ? "DVD"
    : /(^|[\\/])BDMV([\\/]|$)/i.test(sourcePath)
      ? "BDMV"
      : "";

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
    if (sourceDraftRestored.current) return;
    sourceDraftRestored.current = true;
    const recentPath = sourcePathHistory[0]?.path.trim() || "";
    if (!releaseSession.input.view.sourceDraft.trim() && recentPath) {
      releaseSession.input.updateSourceDraft(recentPath);
    }
  }, [releaseSession.input, sourcePathHistory]);
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
      if (isDir) void loadHostDirectory(path, hostBrowserMode);
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
            {jobNotifications.pending.length ? (
              <span className="px-2 text-xs text-[var(--muted)]">
                Pending jobs: {jobNotifications.pending.length}
              </span>
            ) : null}
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
        </aside>

        <main className="content">
          {navigationNotice ? (
            <p className="muted" role="status">
              {navigationNotice}
            </p>
          ) : null}
          {activeTab === "settings" ? (
            <SettingsPage
              configData={configData}
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
              trackerSelectionNames={trackerSelectionNames}
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
          ) : activeTab === "logging" ? (
            <LoggingPage
              configData={configData}
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
            />
          ) : activeTab === "screenshots" ? (
            <ScreenshotsPage
              facet={releaseSession.screenshots}
              screenshotConfig={screenshotConfig}
              updateScreenshotConfigValue={updateScreenshotConfigValue}
              loadSettings={loadSettings}
              settingsLoading={settingsLoading}
              settingsDirty={settingsDirty}
              settingsSaved={settingsSaved}
              settingsError={settingsError}
              applyScreenshotSettings={() => void handleSaveSettings()}
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
              configuredImageHosts={configuredImageHosts}
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
          ) : activeTab === "description_builder" ? (
            <DescriptionBuilderPage
              facet={releaseSession.descriptions}
              sourcePath={sourcePath}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
            />
          ) : activeTab === "upload" ? (
            <TrackerUploadPage
              facet={releaseSession.upload}
              trackerUploadItems={trackerUploadItems}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
            />
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
            <Dialog.Overlay className="dialog-overlay" />
            <Dialog.Content className="host-browser-dialog">
              <Dialog.Title>Select {hostBrowserMode === "file" ? "file" : "folder"}</Dialog.Title>
              <Dialog.Description className="sr-only">
                Browse host paths allowed by WebUI policy.
              </Dialog.Description>
              <div className="flex flex-wrap gap-2">
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
                <input
                  aria-label="Filter host paths"
                  value={hostBrowserSearch}
                  onChange={(event) => setHostBrowserSearch(event.target.value)}
                  placeholder="Filter paths"
                />
                {hostBrowserMode === "folder" && hostBrowser?.currentPath ? (
                  <button
                    className="primary"
                    type="button"
                    onClick={() => selectHostPath(hostBrowser.currentPath, true)}
                  >
                    Select current folder
                  </button>
                ) : null}
              </div>
              {hostBrowserError ? (
                <p className="error" role="alert">
                  {hostBrowserError}
                </p>
              ) : null}
              <div className="host-browser-list">
                {hostBrowserLoading ? (
                  <p className="muted">Loading...</p>
                ) : (
                  visibleHostEntries.map((entry) => (
                    <button
                      className="host-browser-entry"
                      type="button"
                      key={entry.path}
                      onClick={() => selectHostPath(entry.path, entry.isDir)}
                    >
                      <span>{entry.isDir ? "Folder" : "File"}</span>
                      <span className="mono">{entry.path}</span>
                    </button>
                  ))
                )}
              </div>
              <Dialog.Close className="ghost">Cancel</Dialog.Close>
            </Dialog.Content>
          </Dialog.Portal>
        </Dialog.Root>
      </div>
    </div>
  );
}

/** Composes owner Job registry and active release session around the shell. */
export default function App({ jobOwnerKey = "standalone" }: Readonly<{ jobOwnerKey?: string }>) {
  return (
    <JobRegistryProvider ownerKey={jobOwnerKey}>
      <ReleaseSessionProvider>
        <AppShell />
      </ReleaseSessionProvider>
    </JobRegistryProvider>
  );
}
