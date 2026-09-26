// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test } from "@playwright/test";
import { createE2EWorkspace, startApp, type AppServer } from "./helpers/e2eHarness";
import type { ApplicationInfo } from "../src/types";

test("embedded web boots with dev auth, navigates core pages, and reports invalid paths", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    const applicationInfoResponse = page.waitForResponse((response) =>
      response.url().endsWith("/api/app/GetApplicationInfo"),
    );
    await page.goto(app.url);
    await expect(page.getByRole("heading", { name: "Build Release Name" })).toBeVisible();

    const applicationInfo = (await (await applicationInfoResponse).json()) as ApplicationInfo;
    const releaseVersion = applicationInfo.version.trim();
    const developmentBuild = ["", "dev", "(devel)"].includes(releaseVersion.toLowerCase());
    const expectedVersion = developmentBuild
      ? `${applicationInfo.buildIdentifier}${applicationInfo.buildTime ? ` (${applicationInfo.buildTime.slice(0, 10)})` : ""}`
      : releaseVersion;
    await expect(page.getByTitle(expectedVersion)).toBeVisible();
    await expect(page.getByRole("link", { name: "Open the autobrr Discord" })).toHaveAttribute(
      "href",
      "https://discord.autobrr.com",
    );
    await expect(page.getByRole("link", { name: "Open autobrr/upbrr on GitHub" })).toHaveAttribute(
      "href",
      "https://github.com/autobrr/upbrr",
    );

    await page.getByRole("button", { name: "Settings" }).click();
    await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
    await page.getByRole("button", { name: "Reload" }).click();
    await expect(page.getByText("Configuration", { exact: true })).toBeVisible();

    const dependency = applicationInfo.dependencies.find(
      ({ path }) => path === "github.com/autobrr/go-bdinfo",
    );
    expect(dependency).toBeDefined();
    await page.getByRole("button", { name: "Application Details" }).click();
    const dependencyName = page.getByTitle("github.com/autobrr/go-bdinfo");
    await expect(dependencyName).toHaveText("go-bdinfo");
    await expect(dependencyName.locator("..")).toContainText(dependency?.version ?? "");

    await page.getByRole("button", { name: "Logging" }).click();
    await expect(page.getByRole("heading", { name: "Logging" })).toBeVisible();

    await page.getByRole("button", { name: "History" }).click();
    await expect(page.getByRole("heading", { name: "History" })).toBeVisible();

    await page.getByRole("button", { name: "Input" }).click();
    await page.getByLabel("Source path").fill("Z:\\missing\\e2e.mkv");
    await page.getByRole("button", { name: "Fetch metadata" }).click();
    const error = page.locator(".error");
    await expect(error).toContainText("The source path is unavailable.");
    await expect(error).toContainText("Recovery: edit input.");
    await expect(error).not.toContainText("Z:\\missing\\e2e.mkv");
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("authenticated appearance is applied before login paint and stays in sync across tabs", async ({
  page,
  context,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace, { devNoAuth: false });
    await page.addInitScript(() => {
      if (!sessionStorage.getItem("appearance-e2e-seeded")) {
        localStorage.setItem(
          "upbrr:appearance:v1",
          JSON.stringify({ version: 1, theme: "kanagawa-wave", mode: "dark", accents: {} }),
        );
        sessionStorage.setItem("appearance-e2e-seeded", "1");
      }
      const observer = new MutationObserver(() => {
        if (!document.body) return;
        (window as Window & { __themeAtBody?: string }).__themeAtBody =
          document.documentElement.dataset.theme;
        observer.disconnect();
      });
      observer.observe(document, { childList: true, subtree: true });
    });

    await page.goto(app.url);
    await expect(page.getByRole("heading", { name: "Sign In" })).toBeVisible();
    expect(
      await page.evaluate(() => (window as Window & { __themeAtBody?: string }).__themeAtBody),
    ).toBe("kanagawa-wave");
    expect(await page.locator("html").getAttribute("class")).toContain("dark");
    await page.getByLabel("Username").fill("e2e-user");
    await page.getByLabel("Password").fill("synthetic-e2e-password");
    await page.getByRole("button", { name: "Sign In" }).click();
    await expect(page.getByRole("heading", { name: "Set Browse Access" })).toBeVisible();
    await page.getByLabel("Browse root").fill(workspace.root);
    await page.getByRole("button", { name: "Continue" }).click();
    await expect(page.getByRole("heading", { name: "Build Release Name" })).toBeVisible();
    expect(await page.locator("html").getAttribute("data-theme")).toBe("kanagawa-wave");

    await page.getByRole("button", { name: "Appearance" }).click();
    await expect(page.getByRole("heading", { name: "Appearance" })).toBeVisible();
    for (const mode of ["Dark", "Light"] as const) {
      await page.getByRole("radio", { name: mode }).click();
      for (const [name, id] of [
        ["Minimal", "minimal"],
        ["autobrr", "autobrr"],
        ["Kanagawa Dragon", "kanagawa-dragon"],
        ["Kanagawa Wave", "kanagawa-wave"],
        ["The Kyle", "the-kyle"],
        ["Napster", "napster"],
        ["Nightwalker", "nightwalker"],
        ["Swizzin", "swizzin"],
      ] as const) {
        await page.getByRole("radio", { name }).locator("..").click();
        await expect(page.locator("html")).toHaveAttribute("data-theme", id);
        const contrast = await page.evaluate(() => {
          const style = getComputedStyle(document.documentElement);
          const canvas = document.createElement("canvas");
          canvas.width = canvas.height = 1;
          const context = canvas.getContext("2d", { willReadFrequently: true });
          if (!context) throw new Error("Canvas color sampling is unavailable");
          const luminance = (token: string) => {
            context.clearRect(0, 0, 1, 1);
            context.fillStyle = style.getPropertyValue(token).trim();
            context.fillRect(0, 0, 1, 1);
            const channels = context.getImageData(0, 0, 1, 1).data;
            const linear = [channels[0], channels[1], channels[2]].map((channel) => {
              const value = channel / 255;
              return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
            });
            return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722;
          };
          const ratio = (foreground: string, background: string) => {
            const values = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
            return (values[0] + 0.05) / (values[1] + 0.05);
          };
          return {
            body: ratio("--foreground", "--background"),
            card: ratio("--card-foreground", "--card"),
            button: ratio("--primary-foreground", "--primary"),
            sidebar: ratio("--sidebar-foreground", "--sidebar"),
          };
        });
        for (const [surface, ratio] of Object.entries(contrast)) {
          expect.soft(ratio, `${name} ${mode} ${surface} contrast`).toBeGreaterThanOrEqual(4.5);
        }
      }
      for (const theme of ["Kanagawa Dragon", "Kanagawa Wave"] as const) {
        await page.getByRole("radio", { name: theme }).locator("..").click();
        for (const accent of ["blueish", "pink", "green", "purple", "grayish", "orange"]) {
          await page.getByRole("button", { name: accent, exact: true }).click();
          await expect(page.locator("html")).toHaveAttribute("data-accent", accent);
          const contrast = await page.evaluate(() => {
            const style = getComputedStyle(document.documentElement);
            const canvas = document.createElement("canvas");
            canvas.width = canvas.height = 1;
            const context = canvas.getContext("2d", { willReadFrequently: true });
            if (!context) throw new Error("Canvas color sampling is unavailable");
            const luminance = (token: string) => {
              context.clearRect(0, 0, 1, 1);
              context.fillStyle = style.getPropertyValue(token).trim();
              context.fillRect(0, 0, 1, 1);
              const channels = context.getImageData(0, 0, 1, 1).data;
              const linear = [channels[0], channels[1], channels[2]].map((channel) => {
                const value = channel / 255;
                return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
              });
              return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722;
            };
            const values = [luminance("--primary"), luminance("--primary-foreground")].sort(
              (a, b) => b - a,
            );
            return (values[0] + 0.05) / (values[1] + 0.05);
          });
          expect.soft(contrast, `${theme} ${mode} ${accent} contrast`).toBeGreaterThanOrEqual(4.5);
        }
      }
    }
    await page.getByRole("radio", { name: "Minimal" }).locator("..").click();
    await page.getByRole("radio", { name: "Light" }).click();
    await expect(page.locator("html")).toHaveClass(/light/);
    await page.getByRole("radio", { name: "Dark" }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.getByRole("radio", { name: "Napster" }).locator("..").click();
    await expect(page.locator("html")).toHaveClass(/light/);
    await page.getByRole("radio", { name: "Swizzin" }).locator("..").click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.getByRole("radio", { name: "Napster" }).focus();
    await page.keyboard.press("ArrowRight");
    await expect(page.getByRole("radio", { name: "Nightwalker" })).toBeChecked();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "nightwalker");
    await page.getByRole("radio", { name: "Swizzin" }).locator("..").click();
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "swizzin");

    const other = await context.newPage();
    await other.goto(app.url);
    await other.getByRole("button", { name: "Appearance" }).click();
    await other.getByRole("radio", { name: "Minimal" }).locator("..").click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "minimal");
    await other.close();
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("dirty settings survive navigation and require confirmation before logout or reload", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace, { devNoAuth: false });
    await page.goto(app.url);
    await page.getByLabel("Username").fill("e2e-user");
    await page.getByLabel("Password").fill("synthetic-e2e-password");
    await page.getByRole("button", { name: "Sign In" }).click();
    await page.getByRole("heading", { name: "Set Browse Access" }).waitFor();
    await page.getByLabel("Browse root").fill(workspace.root);
    await page.getByRole("button", { name: "Continue" }).click();
    await page.getByRole("heading", { name: "Build Release Name" }).waitFor();

    await page.getByRole("button", { name: "Logging" }).click();
    const level = page.getByRole("combobox", { name: "Level" });
    await expect(level).toBeVisible();
    const originalLevel = await level.inputValue();
    const changedLevel = originalLevel === "debug" ? "trace" : "debug";
    await level.selectOption(changedLevel);
    await expect(page.getByRole("button", { name: "Save", exact: true })).toBeEnabled();
    await page.getByRole("button", { name: "History" }).click();
    await page.goBack();
    await expect(level).toHaveValue(changedLevel);

    page.once("dialog", async (dialog) => {
      expect(dialog.type()).toBe("confirm");
      expect(dialog.message()).toContain("Discard unsaved settings and reload");
      await dialog.dismiss();
    });
    await page.getByRole("button", { name: "Reload" }).click();
    await expect(level).toHaveValue(changedLevel);

    page.once("dialog", async (dialog) => {
      expect(dialog.type()).toBe("confirm");
      expect(dialog.message()).toContain("Discard unsaved settings");
      await dialog.dismiss();
    });
    await page.getByRole("button", { name: "Logout" }).click();
    await expect(level).toHaveValue(changedLevel);

    page.once("dialog", async (dialog) => {
      expect(dialog.type()).toBe("beforeunload");
      await dialog.dismiss();
    });
    await page.evaluate(() => window.location.reload());
    await expect(level).toHaveValue(changedLevel);

    page.once("dialog", async (dialog) => {
      expect(dialog.type()).toBe("confirm");
      await dialog.accept();
    });
    await page.getByRole("button", { name: "Logout" }).click();
    await expect(page.getByRole("heading", { name: "Sign In" })).toBeVisible();
    await expect(page.getByLabel("Password")).toHaveValue("");
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("session loss in another tab discards private drafts and cached data", async ({
  page,
  context,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace, { devNoAuth: false });
    await page.goto(app.url);
    await page.getByLabel("Username").fill("e2e-user");
    await page.getByLabel("Password").fill("synthetic-e2e-password");
    await page.getByRole("button", { name: "Sign In" }).click();
    await page.getByRole("heading", { name: "Set Browse Access" }).waitFor();
    await page.getByLabel("Browse root").fill(workspace.root);
    await page.getByRole("button", { name: "Continue" }).click();
    await page.getByRole("heading", { name: "Build Release Name" }).waitFor();

    await page.getByRole("button", { name: "Logging" }).click();
    const level = page.getByRole("combobox", { name: "Level" });
    const originalLevel = await level.inputValue();
    await level.selectOption(originalLevel === "debug" ? "trace" : "debug");
    await expect(page.getByRole("button", { name: "Save", exact: true })).toBeEnabled();

    const other = await context.newPage();
    await other.goto(app.url);
    await other.getByRole("button", { name: "Logout" }).click();
    await expect(other.getByRole("heading", { name: "Sign In" })).toBeVisible();
    await other.close();

    await page.getByRole("button", { name: "History" }).click();
    await expect(page.getByRole("heading", { name: "Sign In" })).toBeVisible();
    await expect(
      page.getByText("Your session ended. Sign in again; unsaved settings were discarded."),
    ).toBeVisible();
    await expect(page.getByLabel("Password")).toHaveValue("");

    await page.getByLabel("Password").fill("synthetic-e2e-password");
    await page.getByRole("button", { name: "Sign In" }).click();
    await page.getByRole("heading", { name: "History" }).waitFor();
    await page.getByRole("button", { name: "Logging" }).click();
    await expect(page.getByRole("combobox", { name: "Level" })).toHaveValue(originalLevel);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded settings generates and revokes a persistent API token", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await page.goto(app.url);
    await page.getByRole("button", { name: "Settings" }).click();
    await page.getByRole("button", { name: "API Tokens" }).click();
    await expect(page.getByRole("heading", { name: "Generate API token" })).toBeVisible();

    await page.getByLabel("Name").fill("WebUI automation");
    await page.getByLabel("Owner").fill("webui-owner");
    await page.getByRole("button", { name: "Generate token" }).click();
    const generatedInput = page.getByLabel("Generated API token");
    await expect(generatedInput).toBeVisible();
    const apiToken = await generatedInput.inputValue();
    expect(apiToken.length > 24).toBe(true);

    const authorized = await page.request.post(
      new URL("api/v1/continuations", app.url).toString(),
      {
        headers: {
          Authorization: `Bearer ${apiToken}`,
          "Content-Type": "application/json",
          "Idempotency-Key": "webui-created-token",
        },
        data: {
          goal: "prepared",
          intent: { factInstructions: {} },
        },
      },
    );
    expect(authorized.status()).toBe(400);
    await expect(authorized.json()).resolves.toMatchObject({
      failure: { Code: "invalid_source" },
    });

    await page.getByRole("button", { name: /^Revoke WebUI automation \(.+\)$/ }).click();
    await page.getByRole("button", { name: "Revoke token" }).click();
    await expect(page.getByText("Revoked", { exact: true })).toBeVisible();

    const revoked = await page.request.post(new URL("api/v1/continuations", app.url).toString(), {
      headers: {
        Authorization: `Bearer ${apiToken}`,
        "Content-Type": "application/json",
        "Idempotency-Key": "webui-revoked-token",
      },
      data: {
        goal: "prepared",
        intent: { factInstructions: {} },
      },
    });
    expect(revoked.status()).toBe(401);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded tracker settings use the catalog for entries, reset, and unsupported config", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await page.goto(app.url);
    await page.getByRole("button", { name: "Settings" }).click();
    await page.getByRole("button", { name: "Trackers", exact: true }).click();

    await expect(
      page.getByText("BTN", { exact: true }).filter({ visible: true }).first(),
    ).toBeVisible();
    await expect(page.getByText("Unsupported tracker entries")).toBeVisible();
    await expect(page.getByText("OLD", { exact: true })).toBeVisible();

    const entryControls = page.locator(".settings-map__header .settings-map__controls");
    const trackerSelector = entryControls.locator("select");
    await expect(trackerSelector.locator('option[value="OLD"]')).toHaveCount(0);
    await trackerSelector.selectOption("BLU");
    await entryControls.getByRole("button", { name: "Add entry" }).click();

    let bluCard = page
      .locator("details.settings-card")
      .filter({ has: page.locator("summary", { hasText: "BLU" }) });
    await expect(bluCard).toBeVisible();
    await expect(
      bluCard.locator("label.settings-field > span, .settings-switch-row > span"),
    ).toHaveText([
      "API key",
      "Anonymous",
      "Image host",
      "Duplicate bypass groups",
      "Personal release groups",
      "Internal groups",
    ]);
    await bluCard.getByLabel("API key").fill("e2e-blu-activation");

    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Settings saved and applied.")).toBeVisible();
    await page.getByRole("button", { name: "Reload", exact: true }).click();

    bluCard = page
      .locator("details.settings-card")
      .filter({ has: page.locator("summary", { hasText: "BLU" }) });
    await expect(bluCard).toBeVisible();
    await bluCard.getByRole("button", { name: "Remove" }).click();
    await expect(bluCard).toHaveCount(0);
    await expect(trackerSelector.locator('option[value="BLU"]')).toHaveCount(1);

    await page.getByRole("button", { name: "Save", exact: true }).click();
    await page.getByRole("button", { name: "Reload", exact: true }).click();
    await expect(
      page.locator("details.settings-card").filter({
        has: page.locator("summary", { hasText: "BLU" }),
      }),
    ).toHaveCount(0);

    const unsupportedCard = page
      .locator(".settings-card")
      .filter({ has: page.locator(".settings-card__summary-name", { hasText: "OLD" }) });
    await unsupportedCard.getByRole("button", { name: "Delete" }).click();
    await expect(page.getByText("OLD", { exact: true })).toHaveCount(0);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});
