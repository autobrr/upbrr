// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { createRootRoute, createRoute, createRouter, redirect } from "@tanstack/react-router";
import type { ReactElement } from "react";
import { routeComponents } from "./routes/RouteViews";

/** Browser routes mirrored by the embedded host's UI fallback allowlist. */
export const screenPaths = {
  input: "/input",
  tracker: "/tracker-data",
  bluray: "/bluray-candidates",
  audio_analysis: "/audio-analysis",
  dupes: "/duplicates",
  screenshots: "/screenshots",
  menu_images: "/menu-images",
  upload_images: "/uploaded-images",
  description_builder: "/descriptions",
  upload: "/upload",
  history: "/history",
  settings: "/settings",
  logging: "/logging",
} as const;

export type ScreenId = keyof typeof screenPaths;

const screenByPath = Object.fromEntries(
  Object.entries(screenPaths).map(([screen, path]) => [path, screen]),
) as Record<string, ScreenId>;

export function screenFromPath(pathname: string): ScreenId | null {
  const base = window.__UPBRR_BASE_URL__?.replace(/\/$/, "") || "";
  const localPath =
    base && pathname.startsWith(`${base}/`) ? pathname.slice(base.length) : pathname;
  return screenByPath[localPath.replace(/\/$/, "") || "/"] ?? null;
}

export function createAppRouter(shell: () => ReactElement) {
  const rootRoute = createRootRoute({ component: shell });
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    beforeLoad: () => {
      throw redirect({ to: screenPaths.input });
    },
  });
  const routes = [
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.input,
      component: routeComponents.input,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.tracker,
      component: routeComponents.tracker,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.bluray,
      component: routeComponents.bluray,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.audio_analysis,
      component: routeComponents.audio_analysis,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.dupes,
      component: routeComponents.dupes,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.screenshots,
      component: routeComponents.screenshots,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.menu_images,
      component: routeComponents.menu_images,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.upload_images,
      component: routeComponents.upload_images,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.description_builder,
      component: routeComponents.description_builder,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.upload,
      component: routeComponents.upload,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.history,
      component: routeComponents.history,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.settings,
      component: routeComponents.settings,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: screenPaths.logging,
      component: routeComponents.logging,
    }),
  ] as const;
  const routeTree = rootRoute.addChildren([indexRoute, ...routes]);
  return createRouter({
    routeTree,
    basepath: window.__UPBRR_BASE_URL__ || "/",
    defaultErrorComponent: ({ reset }) => (
      <section className="panel" role="alert">
        <h2>View could not be loaded</h2>
        <p>Try opening this page again.</p>
        <button type="button" className="ghost" onClick={reset}>
          Retry view
        </button>
      </section>
    ),
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
