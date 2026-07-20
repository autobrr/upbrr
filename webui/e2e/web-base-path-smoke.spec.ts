// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test } from "@playwright/test";
import { createE2EWorkspace, startApp, type AppServer } from "./helpers/e2eHarness";

test("embedded web serves UI, API, assets, manifest, and events under a base path", async ({
  page,
  request,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;

  try {
    app = await startApp(workspace, { baseURL: "/upbrr/" });
    const origin = new URL(app.url).origin;
    const requestPaths: string[] = [];
    page.on("request", (req) => requestPaths.push(new URL(req.url()).pathname));

    const authStatus = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return url.pathname === "/upbrr/api/auth/status" && response.ok();
    });
    const eventRequest = page.waitForRequest((req) => {
      const url = new URL(req.url());
      return url.pathname === "/upbrr/api/events";
    });

    await page.goto(app.url);
    await authStatus;
    await expect(page.getByRole("heading", { name: "Build Release Name" })).toBeVisible();
    await eventRequest;

    const apiAuthStatus = await request.get(`${origin}/upbrr/api/auth/status`);
    expect(apiAuthStatus.ok()).toBe(true);
    const { csrfToken } = (await apiAuthStatus.json()) as { csrfToken: string };
    const defaultConfig = await request.post(`${origin}/upbrr/api/app/GetDefaultConfig`, {
      data: {},
      headers: { Origin: origin, "X-CSRF-Token": csrfToken },
    });
    expect(defaultConfig.ok()).toBe(true);
    expect(await defaultConfig.json()).toBeTruthy();

    const manifest = await request.get(`${origin}/upbrr/site.webmanifest`);
    expect(manifest.ok()).toBe(true);
    const manifestBody = await manifest.text();
    expect(manifestBody).toContain("/upbrr/");

    const rootAuth = await request.get(`${origin}/api/auth/status`);
    expect(rootAuth.status()).toBe(404);

    expect(requestPaths).toContain("/upbrr/api/auth/status");
    expect(requestPaths).toContain("/upbrr/api/events");
    expect(requestPaths.some((path) => path.startsWith("/upbrr/assets/"))).toBe(true);
    expect(
      requestPaths.filter(
        (path) =>
          (path.startsWith("/api/") ||
            path.startsWith("/assets/") ||
            path === "/site.webmanifest") &&
          !path.startsWith("/upbrr/"),
      ),
    ).toEqual([]);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});
