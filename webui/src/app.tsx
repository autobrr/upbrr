// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { createContext, useContext, useEffect, useMemo, useState } from "react";
import { Outlet, RouterProvider, useNavigate, useRouterState } from "@tanstack/react-router";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { applicationClient } from "./api/app";
import { WorkflowOperationProgress } from "./components/WorkflowOperationProgress";
import { WorkflowRequiredActions } from "./components/WorkflowRequiredActions";
import { HostBrowserDialog } from "./features/hostBrowser/HostBrowserDialog";
import { ImageLightbox, useImageLightbox } from "./features/lightbox/ImageLightbox";
import { useSourcePathHistory } from "./features/input/useSourcePathHistory";
import { AppLayout, type NavigationItem } from "./layouts/AppLayout";
import { RouteViewsContext } from "./routes/RouteViews";
import { useSettingsState } from "./hooks/useSettingsState";
import { useTrackerIcons } from "./hooks/useTrackerIcons";
import { ReleaseSessionProvider, useReleaseSession } from "./releaseSession";
import { TrackerCatalogProvider, useTrackerCatalog } from "./trackerCatalog";
import type { ReleaseRoute } from "./releaseSession/types";
import type { ApplicationInfo, ConfigMap } from "./types";
import { createAppRouter, screenFromPath, screenPaths, type ScreenId } from "./router";
import { resolveInputHistoryLimit, type SourcePathMode } from "./utils/inputHistory";

type ActiveTab = ScreenId;

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
const AppRuntimeContext = createContext<{
  applicationInfo: ApplicationInfo | null;
  applicationInfoFetchedAt: number | null;
  applicationInfoLoading: boolean;
  applicationInfoError: string;
  runtimeInfoError: boolean;
  onSettingsDirtyChange?: (dirty: boolean) => void;
} | null>(null);

/** Stable shell around the sole active release-session interface. */
function AppShell() {
  const runtime = useContext(AppRuntimeContext);
  if (!runtime) throw new Error("App runtime provider is missing");
  const {
    applicationInfo,
    applicationInfoFetchedAt,
    applicationInfoLoading,
    applicationInfoError,
    runtimeInfoError,
    onSettingsDirtyChange,
  } = runtime;
  const releaseSession = useReleaseSession();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const activeTab = screenFromPath(pathname) ?? "input";
  const navigate = useNavigate();
  const setActiveTab = (tab: ActiveTab) => {
    void navigate({ to: screenPaths[tab] });
  };
  const [navigationNotice, setNavigationNotice] = useState("");
  const {
    image: lightboxImage,
    alt: lightboxAlt,
    setImage: setLightboxImage,
    setAlt: setLightboxAlt,
    close: closeLightbox,
  } = useImageLightbox();
  const [hostBrowserMode, setHostBrowserMode] = useState<SourcePathMode | null>(null);
  const settings = useSettingsState({ activeTab });
  const {
    configData,
    settingsDirty,
    settingsSection,
    setSettingsSection,
    screenshotConfig,
    trackerSelectionNames,
  } = settings;

  useEffect(() => {
    onSettingsDirtyChange?.(settingsDirty);
  }, [onSettingsDirtyChange, settingsDirty]);
  useEffect(() => () => onSettingsDirtyChange?.(false), [onSettingsDirtyChange]);

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
  const { entries: sourcePathHistory, remember: rememberSource } = useSourcePathHistory(
    inputHistoryLimit,
    releaseSession.identity.view.release?.SourcePath || "",
  );
  const access = releaseSession.navigation.view.access;
  const hasTrackerData = releaseSession.input.view.trackerData.length > 0;
  const hasBlurayData = Boolean(preview?.Bluray);
  const hasAudioData = releaseSession.audioAnalysis.view.available;
  const currentDiscType =
    releaseSession.workflow.view.current?.release?.release.Source.Classification.DiscType || "";
  const openReleaseTab = (tab: ActiveTab, route: ReleaseRoute) => {
    const routeAccess = access[route];
    if (!routeAccess.available) {
      setNavigationNotice(routeAccess.reason);
      return;
    }
    setNavigationNotice("");
    setActiveTab(tab);
  };

  const openHostBrowser = (mode: SourcePathMode) => {
    setHostBrowserMode(mode);
  };
  const selectHostPath = (path: string, _isDir: boolean, mode: SourcePathMode) => {
    releaseSession.input.updateSourceDraft(path);
    releaseSession.input.selectSource(path);
    rememberSource(path, mode);
    setActiveTab("input");
  };

  const releaseNavItem = (
    label: string,
    tab: ActiveTab,
    route: ReleaseRoute,
    nested = false,
  ): NavigationItem => ({
    label,
    active: activeTab === tab,
    disabled: !access[route].available,
    reason: access[route].reason,
    nested,
    onSelect: () => openReleaseTab(tab, route),
  });
  const releaseNavigation: NavigationItem[] = [
    { label: "Input", active: activeTab === "input", onSelect: () => setActiveTab("input") },
    ...(hasTrackerData ? [releaseNavItem("Tracker Data", "tracker", "trackerData", true)] : []),
    ...(hasBlurayData
      ? [
          {
            label: "Blu-ray Candidates",
            active: activeTab === "bluray",
            nested: true,
            onSelect: () => setActiveTab("bluray"),
          },
        ]
      : []),
    ...(hasAudioData
      ? [releaseNavItem("Audio Analysis", "audio_analysis", "audioAnalysis", true)]
      : []),
    releaseNavItem("Dupe Check", "dupes", "duplicates"),
    releaseNavItem("Screenshots", "screenshots", "screenshots"),
    releaseNavItem("Menu Images", "menu_images", "menuImages", true),
    releaseNavItem("Upload Images", "upload_images", "uploadedImages", true),
    releaseNavItem("Descriptions", "description_builder", "descriptions"),
    releaseNavItem("Upload", "upload", "upload"),
  ];
  const utilityNavigation: NavigationItem[] = [
    { label: "History", active: activeTab === "history", onSelect: () => setActiveTab("history") },
    {
      label: "Settings",
      active: activeTab === "settings" && settingsSection !== "appearance",
      onSelect: () => setActiveTab("settings"),
    },
    { label: "Logging", active: activeTab === "logging", onSelect: () => setActiveTab("logging") },
    {
      label: "Appearance",
      active: activeTab === "settings" && settingsSection === "appearance",
      onSelect: () => {
        setSettingsSection("appearance");
        setActiveTab("settings");
      },
    },
  ];

  return (
    <RouteViewsContext.Provider
      value={{
        session: releaseSession,
        settings,
        applicationInfo,
        applicationInfoFetchedAt,
        applicationInfoLoading,
        applicationInfoError,
        sourcePathHistory,
        trackerUploadItems,
        trackerIconSrcByName,
        useFavicons,
        faviconOnly,
        currentDiscType,
        maxMenuItems,
        setLightboxImage,
        setLightboxAlt,
        navigateTo: setActiveTab,
        openReleaseTab,
        openHostBrowser,
      }}
    >
      <AppLayout
        applicationInfo={applicationInfo}
        releaseNavigation={releaseNavigation}
        utilityNavigation={utilityNavigation}
      >
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
        <Outlet />

        <ImageLightbox image={lightboxImage} alt={lightboxAlt} onClose={closeLightbox} />

        <HostBrowserDialog
          mode={hostBrowserMode}
          initialPath={releaseSession.input.view.sourceDraft}
          onClose={() => setHostBrowserMode(null)}
          onSelect={selectHostPath}
        />
      </AppLayout>
    </RouteViewsContext.Provider>
  );
}

/** Composes the active release session around the shell. */
export default function App({
  onSettingsDirtyChange,
}: Readonly<{ onSettingsDirtyChange?: (dirty: boolean) => void }>) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
      }),
  );
  return (
    <QueryClientProvider client={queryClient}>
      <TrackerCatalogProvider>
        <AppReleaseSession onSettingsDirtyChange={onSettingsDirtyChange} />
      </TrackerCatalogProvider>
    </QueryClientProvider>
  );
}

function AppReleaseSession({
  onSettingsDirtyChange,
}: Readonly<{ onSettingsDirtyChange?: (dirty: boolean) => void }>) {
  const { catalog, loaded: catalogLoaded } = useTrackerCatalog();
  const [appRouter] = useState(() => createAppRouter(AppShell));
  const infoQuery = useQuery({
    queryKey: ["application-info"],
    queryFn: ({ signal }) => applicationClient.getInfo(signal),
  });
  const applicationInfo = infoQuery.data ?? null;
  const runtimeInfoError = infoQuery.isError;
  const defaultTrackers = useMemo(
    () =>
      (catalog?.entries || [])
        .filter((entry) => entry.configured && entry.default === true)
        .map((entry) => entry.name),
    [catalog],
  );
  if (!catalogLoaded) return <p role="status">Loading tracker catalog…</p>;
  return (
    <ReleaseSessionProvider
      defaultTrackers={defaultTrackers}
      testRuntime={applicationInfo?.testRuntime}
      runtimeInfoReady={applicationInfo !== null}
    >
      <AppRuntimeContext.Provider
        value={{
          applicationInfo,
          applicationInfoFetchedAt: infoQuery.dataUpdatedAt || null,
          applicationInfoLoading: infoQuery.isPending && !infoQuery.isError,
          applicationInfoError: infoQuery.error ? String(infoQuery.error) : "",
          runtimeInfoError,
          onSettingsDirtyChange,
        }}
      >
        <RouterProvider router={appRouter} />
      </AppRuntimeContext.Provider>
    </ReleaseSessionProvider>
  );
}
