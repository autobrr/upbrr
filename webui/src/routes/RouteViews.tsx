// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { createContext, lazy, Suspense, useContext } from "react";
import type { Dispatch, ReactNode, SetStateAction } from "react";
import InputPage from "../pages/input";
import type { useReleaseSession } from "../releaseSession";
import type { ReleaseRoute } from "../releaseSession/types";
import type { useSettingsState } from "../hooks/useSettingsState";
import type { SourcePathHistoryEntry, SourcePathMode } from "../utils/inputHistory";
import type { ScreenId } from "../router";
import type { ApplicationInfo } from "../types";

const AudioAnalysisPage = lazy(() => import("../pages/audio_analysis"));
const BlurayCandidatesPage = lazy(() => import("../pages/bluray_candidates"));
const DescriptionBuilderPage = lazy(() => import("../pages/description_builder"));
const DupeCheckPage = lazy(() => import("../pages/dupe_check"));
const HistoryPage = lazy(() => import("../pages/history"));
const MenuImagesPage = lazy(() => import("../pages/menu_images"));
const ScreenshotsPage = lazy(() => import("../pages/screenshots"));
const TrackerDataPage = lazy(() => import("../pages/tracker_data"));
const TrackerUploadPage = lazy(() => import("../pages/tracker_upload"));
const UploadImagesPage = lazy(() => import("../pages/upload_images"));

export type RouteViewContextValue = Readonly<{
  session: ReturnType<typeof useReleaseSession>;
  settings: ReturnType<typeof useSettingsState>;
  applicationInfo: ApplicationInfo | null;
  applicationInfoFetchedAt: number | null;
  applicationInfoLoading: boolean;
  applicationInfoError: string;
  sourcePathHistory: SourcePathHistoryEntry[];
  trackerUploadItems: Parameters<typeof InputPage>[0]["trackerUploadItems"];
  trackerIconSrcByName: Parameters<typeof InputPage>[0]["trackerIconSrcByName"];
  useFavicons: boolean;
  faviconOnly: boolean;
  currentDiscType: string;
  maxMenuItems: number;
  setLightboxImage: Dispatch<SetStateAction<string>>;
  setLightboxAlt: Dispatch<SetStateAction<string>>;
  navigateTo: (screen: ScreenId) => void;
  openReleaseTab: (screen: ScreenId, route: ReleaseRoute) => void;
  openHostBrowser: (mode: SourcePathMode) => void;
}>;

export const RouteViewsContext = createContext<RouteViewContextValue | null>(null);

export function useRouteViews() {
  const value = useContext(RouteViewsContext);
  if (!value) throw new Error("Route views provider is missing");
  return value;
}

function GuardedReleaseView({
  route,
  children,
}: Readonly<{ route: ReleaseRoute; children: ReactNode }>) {
  const { session, navigateTo } = useRouteViews();
  const access = session.navigation.view.access[route];
  const uploadView = session.upload.view;
  const hasUploadProgress =
    route === "upload" &&
    (uploadView.dryRunStatus !== "idle" ||
      uploadView.uploadStatus !== "idle" ||
      uploadView.dryRunResult !== null ||
      uploadView.result !== null);
  const workflowComplete = session.workflow.view.current?.workflow.status === "completed";
  if (access.available || hasUploadProgress || workflowComplete) return children;
  return (
    <section className="panel" role="status">
      <h2 className="text-lg font-semibold">View unavailable</h2>
      <p>{access.reason || "This view is not available for the active release."}</p>
      <button className="ghost" type="button" onClick={() => navigateTo("input")}>
        Open Input
      </button>
    </section>
  );
}

function InputRoute() {
  const {
    session,
    sourcePathHistory,
    openHostBrowser,
    trackerUploadItems,
    setLightboxImage,
    setLightboxAlt,
    useFavicons,
    faviconOnly,
    trackerIconSrcByName,
  } = useRouteViews();
  return (
    <InputPage
      facet={session.input}
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
  );
}

function TrackerRoute() {
  const {
    session,
    setLightboxImage,
    setLightboxAlt,
    useFavicons,
    faviconOnly,
    trackerIconSrcByName,
  } = useRouteViews();
  return (
    <GuardedReleaseView route="trackerData">
      <TrackerDataPage
        facet={session.input}
        setLightboxImage={setLightboxImage}
        setLightboxAlt={setLightboxAlt}
        useFavicons={useFavicons}
        faviconOnly={faviconOnly}
        trackerIconSrcByName={trackerIconSrcByName}
      />
    </GuardedReleaseView>
  );
}

function BlurayRoute() {
  const { session, setLightboxImage, setLightboxAlt, navigateTo } = useRouteViews();
  if (!session.identity.view.preview?.Bluray)
    return (
      <section className="panel" role="status">
        <h2 className="text-lg font-semibold">View unavailable</h2>
        <p>Blu-ray candidates are available after preparing a Blu-ray source.</p>
        <button className="ghost" type="button" onClick={() => navigateTo("input")}>
          Open Input
        </button>
      </section>
    );
  return (
    <BlurayCandidatesPage
      facet={session.input}
      setLightboxImage={setLightboxImage}
      setLightboxAlt={setLightboxAlt}
    />
  );
}

function AudioRoute() {
  const { session, setLightboxImage, setLightboxAlt } = useRouteViews();
  return (
    <GuardedReleaseView route="audioAnalysis">
      <AudioAnalysisPage
        key={session.audioAnalysis.view.releaseGeneration}
        facet={session.audioAnalysis}
        setLightboxImage={setLightboxImage}
        setLightboxAlt={setLightboxAlt}
      />
    </GuardedReleaseView>
  );
}

function DuplicatesRoute() {
  const { session, trackerUploadItems, useFavicons, faviconOnly, trackerIconSrcByName } =
    useRouteViews();
  return (
    <GuardedReleaseView route="duplicates">
      <DupeCheckPage
        facet={session.duplicates}
        sourcePath={session.identity.view.sourcePath}
        trackerUploadItems={trackerUploadItems}
        useFavicons={useFavicons}
        faviconOnly={faviconOnly}
        trackerIconSrcByName={trackerIconSrcByName}
        submissionExclusions={session.upload.view.submissionExclusions}
        workflowComplete={session.workflow.view.current?.workflow.status === "completed"}
      />
    </GuardedReleaseView>
  );
}

function ScreenshotsRoute() {
  const { session, setLightboxImage, setLightboxAlt } = useRouteViews();
  return (
    <GuardedReleaseView route="screenshots">
      <ScreenshotsPage
        facet={session.screenshots}
        setLightboxImage={setLightboxImage}
        setLightboxAlt={setLightboxAlt}
      />
    </GuardedReleaseView>
  );
}

function MenuImagesRoute() {
  const {
    session,
    currentDiscType,
    maxMenuItems,
    openReleaseTab,
    setLightboxImage,
    setLightboxAlt,
  } = useRouteViews();
  return (
    <GuardedReleaseView route="menuImages">
      <MenuImagesPage
        facet={session.menuImages}
        currentDiscType={currentDiscType}
        maxMenuItems={maxMenuItems}
        onContinue={() => openReleaseTab("upload_images", "uploadedImages")}
        setLightboxImage={setLightboxImage}
        setLightboxAlt={setLightboxAlt}
      />
    </GuardedReleaseView>
  );
}

function UploadedImagesRoute() {
  const { session, settings, setLightboxImage, setLightboxAlt } = useRouteViews();
  return (
    <GuardedReleaseView route="uploadedImages">
      <UploadImagesPage
        facet={session.uploadedImages}
        resolveImageHostLabel={settings.resolveImageHostLabel}
        setLightboxImage={setLightboxImage}
        setLightboxAlt={setLightboxAlt}
      />
    </GuardedReleaseView>
  );
}

function DescriptionsRoute() {
  const { session, useFavicons, faviconOnly, trackerIconSrcByName } = useRouteViews();
  return (
    <GuardedReleaseView route="descriptions">
      <DescriptionBuilderPage
        facet={session.descriptions}
        sourcePath={session.identity.view.sourcePath}
        useFavicons={useFavicons}
        faviconOnly={faviconOnly}
        trackerIconSrcByName={trackerIconSrcByName}
      />
    </GuardedReleaseView>
  );
}

function UploadRoute() {
  const { session } = useRouteViews();
  return (
    <GuardedReleaseView route="upload">
      <TrackerUploadPage facet={session.upload} />
    </GuardedReleaseView>
  );
}

function HistoryRoute() {
  const { session, navigateTo } = useRouteViews();
  return (
    <HistoryPage
      onOpenInput={async (path) => {
        const opened = await session.input.openSource(path);
        if (opened) navigateTo("input");
        return opened;
      }}
      onReleaseDeleted={(path) => {
        if (path === session.identity.view.sourcePath) session.input.selectSource("");
      }}
    />
  );
}

const SettingsRoute = lazy(() =>
  import("./SettingsRoutes").then(({ SettingsRoute }) => ({ default: SettingsRoute })),
);
const LoggingRoute = lazy(() =>
  import("./SettingsRoutes").then(({ LoggingRoute }) => ({ default: LoggingRoute })),
);

function withLoading(Component: () => ReactNode) {
  return function LoadingRoute() {
    return (
      <Suspense
        fallback={
          <p className="muted" role="status">
            Loading view…
          </p>
        }
      >
        <Component />
      </Suspense>
    );
  };
}

/** Each URL mounts only its own page while the parent session and drafts stay mounted. */
export const routeComponents: Record<ScreenId, () => ReactNode> = {
  input: InputRoute,
  tracker: withLoading(TrackerRoute),
  bluray: withLoading(BlurayRoute),
  audio_analysis: withLoading(AudioRoute),
  dupes: withLoading(DuplicatesRoute),
  screenshots: withLoading(ScreenshotsRoute),
  menu_images: withLoading(MenuImagesRoute),
  upload_images: withLoading(UploadedImagesRoute),
  description_builder: withLoading(DescriptionsRoute),
  upload: withLoading(UploadRoute),
  history: withLoading(HistoryRoute),
  settings: withLoading(() => <SettingsRoute />),
  logging: withLoading(() => <LoggingRoute />),
};
