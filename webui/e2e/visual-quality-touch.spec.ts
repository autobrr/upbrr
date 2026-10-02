import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { expect, test, type BrowserContext, type Locator, type Page } from "@playwright/test";
import { createE2EWorkspace, fetchMetadata, startApp, type AppServer } from "./helpers/e2eHarness";

const output = path.resolve("../docs/plans/visual-quality-evidence/touch-reduced-motion");

async function toggleByTouch(control: Locator, selectedCount?: Locator) {
  const original = await control.getAttribute("aria-checked");
  expect(original === "true" || original === "false").toBe(true);
  const count = selectedCount
    ? Number.parseInt((await selectedCount.textContent()) ?? "", 10)
    : null;
  await control.tap();
  await expect(control).toHaveAttribute("aria-checked", original === "true" ? "false" : "true");
  if (count !== null)
    await expect(selectedCount!).toHaveText(
      new RegExp(`^${count + (original === "true" ? -1 : 1)}/`),
    );
  await control.tap();
  await expect(control).toHaveAttribute("aria-checked", original!);
  if (count !== null) await expect(selectedCount!).toHaveText(new RegExp(`^${count}/`));
}

async function record(page: Page, theme: string, mode: string, route: string) {
  const screenshot = `${theme}-${mode}-${route}-390.png`;
  const style = await page.addStyleTag({
    content:
      ".app-shell { overflow: visible !important; } .content { max-height: none !important; overflow-y: visible !important; }",
  });
  try {
    const image = await page.screenshot({
      path: path.join(output, screenshot),
      fullPage: true,
      animations: "disabled",
    });
    expect(image.readUInt32BE(16)).toBe(390);
    const requiredHeight = await page
      .locator("main.content")
      .evaluate((element) => element.getBoundingClientRect().top + scrollY + element.scrollHeight);
    expect(image.readUInt32BE(20)).toBeGreaterThanOrEqual(requiredHeight - 2);
  } finally {
    await style.evaluate((element) => element.remove());
  }
  const size = await page.evaluate(() => ({
    width: innerWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  expect(size.width).toBe(390);
  expect(size.scrollWidth).toBeLessThanOrEqual(size.width);
  return { theme, mode, route, screenshot, ...size };
}

test("touch controls work on dense mobile routes with reduced motion", async ({ browser }) => {
  test.setTimeout(120_000);
  await mkdir(output, { recursive: true });
  const records: Array<Record<string, unknown>> = [];
  for (const [theme, mode] of [
    ["minimal", "light"],
    ["swizzin", "dark"],
  ]) {
    const workspace = await createE2EWorkspace();
    let app: AppServer | undefined;
    let context: BrowserContext | undefined;
    try {
      app = await startApp(workspace);
      context = await browser.newContext({
        viewport: { width: 390, height: 844 },
        isMobile: true,
        hasTouch: true,
        reducedMotion: "reduce",
      });
      const page = await context.newPage();
      await fetchMetadata(page, app.url, workspace.sourcePath);
      await page.evaluate(
        ({ theme, mode }) =>
          localStorage.setItem(
            "upbrr:appearance:v1",
            JSON.stringify({ version: 1, theme, mode, accents: {} }),
          ),
        { theme, mode },
      );
      await page.reload();
      expect(
        await page.evaluate(() => matchMedia("(prefers-reduced-motion: reduce)").matches),
      ).toBe(true);

      const trackerDisclosure = page.locator("details.tracker-dropdown");
      if (!(await trackerDisclosure.evaluate((element) => (element as HTMLDetailsElement).open)))
        await trackerDisclosure.locator(":scope > summary").tap();
      const tracker = page.getByRole("checkbox", { name: "BTN", exact: true });
      await toggleByTouch(tracker, trackerDisclosure.locator(".tracker-summary-count"));
      records.push(await record(page, theme, mode, "input"));

      await page.getByRole("button", { name: "Settings", exact: true }).tap();
      await toggleByTouch(page.getByRole("switch", { name: "Use favicons" }));
      records.push(await record(page, theme, mode, "settings"));

      await page.getByRole("button", { name: "Logging", exact: true }).tap();
      await toggleByTouch(page.getByRole("switch", { name: "Auto-scroll logs" }));
      await toggleByTouch(page.getByRole("checkbox", { name: "TRACE", exact: true }));
      const longMute = `selection-token-${"A".repeat(180)}`;
      await page.getByPlaceholder("Message to mute").fill(longMute);
      await page.getByRole("button", { name: "Add", exact: true }).tap();
      const mutedRow = page.getByRole("button", { name: "Remove" }).locator("..");
      await expect(mutedRow.locator("span")).toHaveText(longMute);
      await expect(page.getByRole("button", { name: "Remove" })).toBeVisible();
      expect(
        await mutedRow.evaluate((element) => element.scrollWidth - element.clientWidth),
      ).toBeLessThanOrEqual(1);
      const logStream = page.locator("main [aria-live='polite']").first();
      expect(
        await logStream.evaluate((element) => element.scrollWidth - element.clientWidth),
      ).toBeLessThanOrEqual(1);
      records.push(await record(page, theme, mode, "logging"));
    } finally {
      try {
        await context?.close();
      } finally {
        try {
          await app?.stop();
        } finally {
          await workspace.cleanup();
        }
      }
    }
  }
  await writeFile(path.join(output, "cells.json"), JSON.stringify(records, null, 2));
});
