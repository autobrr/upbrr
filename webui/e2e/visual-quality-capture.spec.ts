import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { expect, test, type Locator, type Page } from "@playwright/test";
import {
  createE2EWorkspace,
  createMultiDVDSourceFixture,
  fetchMetadata,
  startApp,
  type AppServer,
} from "./helpers/e2eHarness";
import { sourcePathHistoryStorageKey } from "../src/utils/inputHistory";

// Capture the full scrollable app pane while leaving its normal layout intact for interaction.
const expandedContentStyle =
  ".app-shell { overflow: visible !important; } .content { max-height: none !important; overflow-y: visible !important; }";

async function captureFullContent(page: Page, target: string) {
  const content = page.locator("main.content");
  let requiredHeight = 0;
  let screenshotHeight = 0;
  for (let attempt = 0; attempt < 3; attempt++) {
    const style = await page.addStyleTag({ content: expandedContentStyle });
    try {
      const screenshot = await page.screenshot({
        path: target,
        fullPage: true,
        animations: "disabled",
      });
      expect(
        screenshot.readUInt32BE(16),
        `${target}: full-content screenshot must fit the viewport width`,
      ).toBeLessThanOrEqual(page.viewportSize()?.width ?? Number.POSITIVE_INFINITY);
      screenshotHeight = screenshot.readUInt32BE(20);
    } finally {
      await style.evaluate((element) => element.remove());
    }
    if (!(await content.count())) return;
    requiredHeight = await content.evaluate(
      (element) => element.getBoundingClientRect().top + scrollY + element.scrollHeight,
    );
    if (screenshotHeight >= requiredHeight - 2) return;
    await page.waitForTimeout(350);
  }
  expect(
    screenshotHeight,
    `${target}: screenshot must cover settled content`,
  ).toBeGreaterThanOrEqual(requiredHeight - 2);
}

function captureControlRecords(elements: Element[]) {
  const labelText = (label: Element | null) => {
    if (!label) return "";
    const copy = label.cloneNode(true) as Element;
    copy
      .querySelectorAll("select, option, input, textarea, button, [aria-hidden='true']")
      .forEach((node) => node.remove());
    return copy.textContent?.trim() ?? "";
  };
  return elements
    .filter(
      (element) => !element.closest('[aria-hidden="true"]') && element.getClientRects().length,
    )
    .map((element) => {
      const id = element.id;
      const label = id ? document.querySelector(`label[for='${CSS.escape(id)}']`) : null;
      const labelledBy = element
        .getAttribute("aria-labelledby")
        ?.split(/\s+/)
        .map((labelID) => document.getElementById(labelID)?.textContent?.trim() || "")
        .filter(Boolean)
        .join(" ");
      const group = element.closest('[role="group"], fieldset, details');
      const groupLabelID = group?.getAttribute("aria-labelledby");
      return {
        tag: element.tagName.toLowerCase(),
        role: element.getAttribute("role"),
        type: element.getAttribute("type"),
        name:
          element.getAttribute("aria-label") ||
          labelledBy ||
          labelText(label) ||
          labelText(element.closest("label")) ||
          (element instanceof HTMLDetailsElement
            ? labelText(element.querySelector(":scope > summary"))
            : "") ||
          (element instanceof HTMLSelectElement ? "" : element.textContent?.trim()) ||
          element.getAttribute("title") ||
          "",
        groupName: groupLabelID
          ? document.getElementById(groupLabelID)?.textContent?.trim() || ""
          : labelText(group?.querySelector("legend, summary") || null),
        id,
        value:
          element instanceof HTMLSelectElement
            ? element.value
            : element instanceof HTMLInputElement
              ? element.type === "checkbox" || element.type === "radio"
                ? element.checked
                : element.value
              : element instanceof HTMLDetailsElement
                ? element.open
                : (element.getAttribute("aria-pressed") ?? element.getAttribute("aria-checked")),
        disabled: element.matches(":disabled") || element.hasAttribute("disabled"),
      };
    });
}

const selectorSurface = (scope: string) =>
  `${scope} select:visible, ${scope} [role='switch']:visible, ${scope} [role='checkbox']:visible, ${scope} input[type='checkbox']:visible:not([aria-hidden='true']), ${scope} input[type='radio']:visible:not([aria-hidden='true'])`;
const inventorySurface = (scope: string) =>
  `${selectorSurface(scope)}, ${scope} details:visible, ${scope} button[aria-pressed]:visible`;

async function exerciseVisibleControls(page: Page, output: string, prefix: string, scope = "main") {
  const locator = page.locator(selectorSurface(scope));
  const controls = await locator.evaluateAll(captureControlRecords);
  const results: Array<Record<string, unknown>> = [];
  for (const [index, info] of controls.entries()) {
    const current = await locator.evaluateAll(captureControlRecords);
    const candidates = current
      .map((item, position) => ({ item, position }))
      .filter(
        ({ item }) =>
          item.tag === info.tag &&
          item.role === info.role &&
          item.type === info.type &&
          item.name === info.name,
      );
    const matches =
      candidates.length === 1
        ? candidates
        : candidates.filter(({ item }) => item.groupName === info.groupName);
    expect(matches, `${prefix}: locate ${info.name} in ${info.groupName}`).toHaveLength(1);
    await locator
      .nth(matches[0].position)
      .evaluate(
        (element, marker) => element.setAttribute("data-vq-control", marker),
        `${prefix}-${index}`,
      );
    const control = page.locator(`${scope} [data-vq-control='${prefix}-${index}']`);
    expect(await control.count(), `${prefix}: ${info.name}`).toBe(1);
    expect(info.name, `${prefix}: unnamed ${info.tag} ${index}`).not.toBe("");
    await control.scrollIntoViewIfNeeded();
    const result: Record<string, unknown> = { index, ...info, keyboard: false, pointer: false };
    if (await control.isDisabled()) {
      result.skipped = "disabled";
      results.push(result);
      continue;
    }
    if (info.tag === "select") {
      const original = await control.inputValue();
      const choices = await control
        .locator("option:not([disabled])")
        .evaluateAll((options) => options.map((option) => (option as HTMLOptionElement).value));
      await control.click();
      const openScreenshot = `${prefix}-${index}-open.png`;
      await page.screenshot({ path: path.join(output, openScreenshot), animations: "disabled" });
      await page.keyboard.press("Escape");
      result.openScreenshot = openScreenshot;
      result.pointerOpen = true;
      if (choices.some((choice) => choice !== original)) {
        await control.focus();
        await page.keyboard.press(choices.at(-1) === original ? "Home" : "End");
        await expect.poll(() => control.inputValue()).not.toBe(original);
        result.keyboard = true;
        result.changedValue = await control.inputValue();
        await control.selectOption(original);
        await expect(control).toHaveValue(original);
      } else {
        result.skipped = "single option";
      }
    } else if (info.type === "radio") {
      const groupName = await control.getAttribute("name");
      const groupSelector = `${scope} input[type='radio'][name='${groupName}']`;
      const original = page.locator(`${groupSelector}:checked`).first();
      const originalValue = (await original.count()) ? await original.inputValue() : "";
      const controlValue = await control.inputValue();
      const alternative = page.locator(`${groupSelector}:not([value='${controlValue}'])`).first();
      if ((await control.isChecked()) && (await alternative.count()))
        await alternative.locator("..").click();
      await control.locator("..").click();
      await expect(control).toBeChecked();
      result.pointer = true;
      if (originalValue)
        await page.locator(`${groupSelector}[value='${originalValue}']`).locator("..").click();
      if ((await control.isChecked()) && (await alternative.count()))
        await alternative.locator("..").click();
      await control.focus();
      await page.keyboard.press("Space");
      await expect(control).toBeChecked();
      result.keyboard = true;
      if (originalValue)
        await page.locator(`${groupSelector}[value='${originalValue}']`).locator("..").click();
      result.restoredControlState = originalValue
        ? await page.locator(`${groupSelector}[value='${originalValue}']`).isChecked()
        : false;
    } else {
      const checked = () =>
        control.evaluate((element) =>
          element instanceof HTMLInputElement
            ? element.checked
            : element.getAttribute("aria-checked") === "true",
        );
      const original = await checked();
      const trackerSummary = (await control.evaluate((element) =>
        Boolean(element.closest("details.tracker-dropdown")),
      ))
        ? page.locator("details.tracker-dropdown .tracker-summary-count")
        : null;
      const originalTrackerCount = trackerSummary
        ? Number.parseInt((await trackerSummary.textContent()) ?? "", 10)
        : 0;
      const expectChecked = async (expected: boolean) => {
        if (!trackerSummary) {
          await expect.poll(checked).toBe(expected);
          return;
        }
        const expectedCount = originalTrackerCount + Number(expected) - Number(original);
        await expect
          .poll(async () => ({
            checked: await checked(),
            selectedCount: Number.parseInt((await trackerSummary.textContent()) ?? "", 10),
          }))
          .toEqual({ checked: expected, selectedCount: expectedCount });
      };
      await control.focus();
      await page.keyboard.press("Space");
      await expectChecked(!original);
      result.keyboard = true;
      await page.keyboard.press("Space");
      await expectChecked(original);
      await control.click();
      await expectChecked(!original);
      result.pointer = true;
      await control.click();
      await expectChecked(original);
    }
    if (info.type !== "radio")
      result.restoredControlState =
        info.tag === "select"
          ? (await control.inputValue()) === info.value
          : await control.evaluate(
              (element, expected) =>
                (element instanceof HTMLInputElement
                  ? element.checked
                  : element.getAttribute("aria-checked") === "true") === expected,
              info.value === true || info.value === "true",
            );
    results.push(result);
  }
  return results;
}

async function exerciseDisclosuresAndPressed(page: Page, scope = "main") {
  const results: Array<Record<string, unknown>> = [];
  const details = page.locator(`${scope} details:visible`);
  for (let index = (await details.count()) - 1; index >= 0; index--) {
    const detail = details.nth(index);
    const summary = detail.locator(":scope > summary");
    const name = (await detail.evaluateAll(captureControlRecords))[0]?.name ?? "";
    const open = () => detail.evaluate((element) => (element as HTMLDetailsElement).open);
    const original = await open();
    await summary.scrollIntoViewIfNeeded();
    await summary.click();
    await expect.poll(open).toBe(!original);
    await summary.click();
    await expect.poll(open).toBe(original);
    await summary.focus();
    await page.keyboard.press("Space");
    await expect.poll(open).toBe(!original);
    await page.keyboard.press("Space");
    await expect.poll(open).toBe(original);
    results.push({
      kind: "details",
      name,
      pointer: true,
      keyboard: true,
      restoredControlState: true,
    });
  }
  const pressed = page.locator(`${scope} button[aria-pressed]:visible`);
  for (let index = 0, count = await pressed.count(); index < count; index++) {
    const button = pressed.nth(index);
    const name = (await button.textContent())?.trim() ?? "";
    if (
      await button.evaluate((element) =>
        Boolean(element.closest('nav[aria-label="Settings sections"]')),
      )
    ) {
      results.push({ kind: "pressed", name, skipped: "settings section navigation" });
      continue;
    }
    const parent = button.locator("..");
    const group = parent.locator("button[aria-pressed]");
    const originalIndex = await parent.evaluate((element) =>
      Array.from(element.querySelectorAll("button[aria-pressed]")).findIndex(
        (candidate) => candidate.getAttribute("aria-pressed") === "true",
      ),
    );
    const currentIndex = await button.evaluate((element) =>
      Array.from(element.parentElement?.querySelectorAll("button[aria-pressed]") ?? []).indexOf(
        element,
      ),
    );
    await button.scrollIntoViewIfNeeded();
    await button.click();
    await expect(button).toHaveAttribute("aria-pressed", "true");
    if (originalIndex >= 0 && originalIndex !== currentIndex)
      await group.nth(originalIndex).click();
    await button.focus();
    await page.keyboard.press("Enter");
    await expect(button).toHaveAttribute("aria-pressed", "true");
    if (originalIndex >= 0 && originalIndex !== currentIndex)
      await group.nth(originalIndex).click();
    results.push({
      kind: "pressed",
      name,
      pointer: true,
      keyboard: true,
      restoredControlState: true,
    });
  }
  return results;
}

async function captureExpandedSelectionState(page: Page, output: string, prefix: string) {
  await page.locator("main details").evaluateAll((elements) =>
    elements.forEach((element) => {
      (element as HTMLDetailsElement).open = true;
    }),
  );
  await expect(page.getByRole("combobox", { name: "HDS No English subtitles" })).toBeVisible();
  const screenshot = `${prefix}-all-details.png`;
  await captureFullContent(page, path.join(output, screenshot));
  return {
    screenshot,
    controls: await page.locator(inventorySurface("main")).evaluateAll(captureControlRecords),
    interactions: [
      ...(await exerciseVisibleControls(page, output, `${prefix}-all-details`)),
      ...(await exerciseDisclosuresAndPressed(page)),
    ],
  };
}

async function captureAudioSelectedTracks(page: Page, output: string, prefix: string) {
  await page.getByRole("radio", { name: "Selected" }).locator("..").click();
  const screenshot = `${prefix}-selected-tracks.png`;
  const checkedScreenshot = `${prefix}-selected-track-checked.png`;
  await captureFullContent(page, path.join(output, screenshot));
  const tracks = page.getByRole("group", { name: "Tracks" }).getByRole("checkbox");
  const controls = await tracks.evaluateAll(captureControlRecords);
  const interactions: Array<Record<string, unknown>> = [];
  for (let index = 0, count = await tracks.count(); index < count; index++) {
    const track = tracks.nth(index);
    const name = (await track.locator("..").textContent())?.trim() ?? "";
    const original = await track.isChecked();
    if (index === 0 && original) {
      await captureFullContent(page, path.join(output, checkedScreenshot));
    }
    await track.focus();
    await page.keyboard.press("Space");
    await expect(track).toBeChecked({ checked: !original });
    await page.keyboard.press("Space");
    await expect(track).toBeChecked({ checked: original });
    await track.locator("..").click();
    await expect(track).toBeChecked({ checked: !original });
    if (index === 0 && !original) {
      await captureFullContent(page, path.join(output, checkedScreenshot));
    }
    await track.locator("..").click();
    await expect(track).toBeChecked({ checked: original });
    interactions.push({
      kind: "checkbox",
      name,
      pointer: true,
      keyboard: true,
      restoredControlState: true,
    });
  }
  await page.getByRole("radio", { name: "Primary" }).locator("..").click();
  return { screenshot, checkedScreenshot, controls, interactions };
}

function auditComposedText(selector?: string) {
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 1;
  const context = canvas.getContext("2d", { willReadFrequently: true })!;
  const parse = (value: string): number[] => {
    context.clearRect(0, 0, 1, 1);
    context.fillStyle = value;
    context.fillRect(0, 0, 1, 1);
    return [...context.getImageData(0, 0, 1, 1).data].map((v, i) => (i === 3 ? v / 255 : v));
  };
  const over = (top: number[], bottom: number[]) => {
    const alpha = top[3] + bottom[3] * (1 - top[3]);
    return [0, 1, 2]
      .map((i) => (top[i] * top[3] + bottom[i] * bottom[3] * (1 - top[3])) / alpha)
      .concat(alpha);
  };
  const luminance = (rgb: number[]) =>
    [0, 1, 2].reduce((sum, i) => {
      const s = rgb[i] / 255;
      const linear = s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      return sum + linear * [0.2126, 0.7152, 0.0722][i];
    }, 0);
  const ratio = (a: number[], b: number[]) => {
    const values = [luminance(a), luminance(b)].sort((x, y) => y - x);
    return (values[0] + 0.05) / (values[1] + 0.05);
  };
  const failures: {
    text: string;
    ratio: number;
    threshold: number;
    color: string;
    background: string;
    tag: string;
    className: string;
  }[] = [];
  let checked = 0;
  for (const element of document.querySelectorAll<HTMLElement>(
    selector ?? (document.querySelector("main") ? "main *" : "body *"),
  )) {
    const content = [...element.childNodes]
      .filter((node) => node.nodeType === Node.TEXT_NODE)
      .map((node) => node.textContent?.trim())
      .filter(Boolean)
      .join(" ");
    if (!content || !element.getClientRects().length || element.closest('[aria-hidden="true"]'))
      continue;
    const style = getComputedStyle(element);
    if (style.visibility !== "visible" || Number(style.opacity) === 0) continue;
    const ancestry: Element[] = [];
    for (let parent: Element | null = element; parent; parent = parent.parentElement)
      ancestry.unshift(parent);
    let background = [255, 255, 255, 1];
    for (const ancestor of ancestry)
      background = over(parse(getComputedStyle(ancestor).backgroundColor), background);
    const foreground = over(parse(style.color), background);
    const value = ratio(foreground, background);
    const fontSize = Number.parseFloat(style.fontSize);
    const weight = Number.parseInt(style.fontWeight, 10);
    const threshold = fontSize >= 24 || (fontSize >= 18.66 && weight >= 700) ? 3 : 4.5;
    checked++;
    if (value < threshold)
      failures.push({
        text: content.slice(0, 100),
        ratio: Math.round(value * 100) / 100,
        threshold,
        color: style.color,
        background: background.slice(0, 3).map(Math.round).join(","),
        tag: element.tagName.toLowerCase(),
        className: typeof element.className === "string" ? element.className.slice(0, 120) : "",
      });
  }
  failures.sort((a, b) => a.ratio - b.ratio);
  return { checked, failureCount: failures.length, failures: failures.slice(0, 15) };
}

const populatedRouteHeadings: Record<string, string> = {
  input: "Build Release Name",
  duplicates: "Check Trackers",
  screenshots: "Plan & Capture",
  "menu-images": "Menu Images",
  "uploaded-images": "Upload Images",
  descriptions: "Customize Description",
  upload: "Review & Upload",
  history: "History",
  settings: "Settings",
  logging: "Logging",
};

async function waitForPopulatedRoute(page: Page, route: string) {
  await page
    .getByRole("main")
    .getByRole("heading", { name: populatedRouteHeadings[route], exact: true })
    .waitFor({ timeout: 15_000 });
  if (route === "input") {
    await expect(page.locator("details.tracker-dropdown > summary")).toContainText("1/6");
    await expect(
      page.locator("details.tracker-dropdown [role='checkbox'][aria-label='HDS']"),
    ).toHaveAttribute("aria-checked", "true");
  }
  if (route === "screenshots") await expect(page.getByAltText("Screenshot 1")).toBeVisible();
  if (route === "descriptions")
    await expect(page.getByRole("button", { name: "Expand" }).first()).toBeVisible();
  if (route === "history")
    await expect(page.getByRole("button", { name: /E2E Movie/ }).first()).toBeVisible();
  if (route === "duplicates")
    await expect(page.getByRole("checkbox", { name: "HDS" })).toBeChecked();
  if (route === "settings") {
    await expect(page.locator("main .settings-form")).toBeVisible();
    await expect(page.locator("main .settings-form .settings-field").first()).toBeVisible();
  }
  if (route === "logging") {
    await expect(page.getByRole("combobox", { name: "Level" })).toBeVisible();
    await expect(page.getByRole("group", { name: "Visible levels" })).toBeVisible();
    await expect(page.getByText("Connected", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Mute DEBUG message:/ }).first()).toBeVisible();
    const logStream = page.locator("main [aria-live='polite']").first();
    await expect
      .poll(() => logStream.evaluate((element) => element.scrollWidth - element.clientWidth))
      .toBeLessThanOrEqual(1);
  }
  await expect(page.getByRole("main")).not.toContainText("Loading configuration...");
}

test("capture baseline reported surfaces", async ({ page }) => {
  test.setTimeout(300_000);
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  const output = path.resolve(
    "../docs/plans/visual-quality-evidence",
    process.env.VQ_STAGE ?? "baseline",
  );
  await mkdir(output, { recursive: true });
  try {
    const records = [];
    let prepared = false;
    for (const theme of ["minimal", "swizzin"]) {
      for (const mode of ["light", "dark"]) {
        app = await startApp(workspace, { seed: !prepared });
        await fetchMetadata(page, app.url, workspace.sourcePath);
        prepared = true;
        await page.evaluate(
          ({ theme, mode }) => {
            localStorage.setItem(
              "upbrr:appearance:v1",
              JSON.stringify({ version: 1, theme, mode, accents: {} }),
            );
          },
          { theme, mode },
        );
        for (const route of ["input", "settings", "logging", "history"]) {
          for (const width of [1280, 390]) {
            await page.setViewportSize({ width, height: 900 });
            await page.goto(new URL(route, app.url).toString());
            await page.getByRole("heading", { level: 1 }).first().waitFor();
            const name = `${theme}-${mode}-${route}-${width}`;
            await page.screenshot({ path: path.join(output, `${name}.png`), fullPage: true });
            if (route === "input") {
              await page.getByText("Select Trackers", { exact: true }).click({ timeout: 10_000 });
              await page.screenshot({
                path: path.join(output, `${name}-trackers-open.png`),
                fullPage: true,
              });
            }
            if (route === "settings") {
              await page
                .getByRole("navigation", { name: "Settings sections" })
                .getByRole("button", { name: "Image Hosting" })
                .click();
              await page.screenshot({
                path: path.join(output, `${name}-image-hosting.png`),
                fullPage: true,
              });
            }
            if (route === "logging") {
              await page.getByRole("combobox", { name: "Level" }).click();
              await page.screenshot({
                path: path.join(output, `${name}-level-open.png`),
                fullPage: true,
              });
              await page.keyboard.press("Escape");
            }
            records.push({
              name,
              route,
              theme,
              mode,
              width,
              title: await page.title(),
              root: await page.locator("html").evaluate((el) => ({
                theme: el.getAttribute("data-theme"),
                className: el.className,
                colorScheme: getComputedStyle(el).colorScheme,
              })),
              overflow: await page.evaluate(
                () => document.documentElement.scrollWidth - innerWidth,
              ),
              controls: await page
                .locator("select, [role='switch'], [role='checkbox']")
                .evaluateAll((els) =>
                  els.map((el) => ({
                    role: el.getAttribute("role") ?? el.tagName.toLowerCase(),
                    name:
                      el.getAttribute("aria-label") ??
                      el.closest("label")?.textContent?.trim() ??
                      "",
                    rect: (() => {
                      const r = el.getBoundingClientRect();
                      return [r.x, r.y, r.width, r.height];
                    })(),
                    color: getComputedStyle(el).color,
                    background: getComputedStyle(el).backgroundColor,
                  })),
                ),
            });
          }
        }
        await app.stop();
        app = undefined;
      }
    }
    await writeFile(path.join(output, "dom.json"), JSON.stringify(records, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("inventory rendered controls across routes and settings sections", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  const output = path.resolve(
    "../docs/plans/visual-quality-evidence",
    process.env.VQ_STAGE ?? "baseline",
  );
  await mkdir(output, { recursive: true });
  const records: unknown[] = [];
  const capture = async (route: string, subsection = "") => {
    const controls = await page
      .locator(
        "select, [role='switch'], [role='checkbox'], input[type='checkbox'], input[type='radio'], details, [aria-pressed]",
      )
      .evaluateAll((els) =>
        els.map((el) => {
          const id = el.getAttribute("id");
          const explicitLabel = id
            ? document.querySelector(`label[for='${CSS.escape(id)}']`)?.textContent?.trim()
            : "";
          return {
            tag: el.tagName.toLowerCase(),
            role: el.getAttribute("role"),
            type: el.getAttribute("type"),
            name:
              el.getAttribute("aria-label") ??
              explicitLabel ??
              el.closest("label")?.textContent?.trim() ??
              el.querySelector("summary")?.textContent?.trim() ??
              "",
            value:
              el instanceof HTMLSelectElement
                ? el.value
                : (el.getAttribute("aria-checked") ?? el.getAttribute("aria-pressed") ?? ""),
            visible: !!(el.getBoundingClientRect().width && el.getBoundingClientRect().height),
            disabled: el.hasAttribute("disabled"),
          };
        }),
      );
    records.push({ route, subsection, controls });
  };
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    const routes = [
      "input",
      "tracker-data",
      "bluray-candidates",
      "audio-analysis",
      "duplicates",
      "screenshots",
      "menu-images",
      "uploaded-images",
      "descriptions",
      "upload",
      "history",
      "logging",
    ];
    for (const route of routes) {
      await page.goto(new URL(route, app.url).toString());
      await page.getByRole("main").getByRole("heading").first().waitFor({ timeout: 10_000 });
      await page.locator("details").evaluateAll((els) =>
        els.forEach((el) => {
          el.open = true;
        }),
      );
      await capture(route);
    }
    await page.goto(new URL("settings", app.url).toString());
    await page.getByRole("heading", { level: 1 }).first().waitFor();
    const sections = [
      "Main",
      "Image Hosting",
      "Metadata",
      "Screens",
      "Description",
      "Arr",
      "Post Upload",
      "Trackers",
      "Torrent Clients",
      "Client Handling",
      "Torrent Specific",
      "Appearance",
      "Application Details",
      "API Tokens",
      "Tracker Auth",
    ];
    for (const section of sections) {
      await page
        .getByRole("navigation", { name: "Settings sections" })
        .getByRole("button", { name: section, exact: true })
        .click();
      const advanced = page.getByRole("switch", { name: "Show advanced" });
      if (await advanced.count()) await advanced.click();
      await page.locator("details").evaluateAll((els) =>
        els.forEach((el) => {
          el.open = true;
        }),
      );
      await capture("settings", section);
    }
    await writeFile(path.join(output, "dom-inventory.json"), JSON.stringify(records, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("audit resolved palette color roles", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  const output = path.resolve(
    "../docs/plans/visual-quality-evidence",
    process.env.VQ_STAGE ?? "baseline",
  );
  await mkdir(output, { recursive: true });
  try {
    app = await startApp(workspace);
    await page.goto(app.url);
    await page.getByRole("heading", { level: 1 }).first().waitFor();
    const report = await page.evaluate(() => {
      const themes = [
        "minimal",
        "autobrr",
        "the-kyle",
        "nightwalker",
        "swizzin",
        "kanagawa-dragon",
        "kanagawa-wave",
        "napster",
      ];
      const accents = ["blueish", "pink", "green", "purple", "grayish", "orange"];
      const pairs = [
        ["--background", "--foreground"],
        ["--card", "--card-foreground"],
        ["--popover", "--popover-foreground"],
        ["--primary", "--primary-foreground"],
        ["--secondary", "--secondary-foreground"],
        ["--semantic-accent", "--accent-foreground"],
        ["--semantic-muted", "--muted-foreground"],
        ["--card", "--muted-foreground"],
        ["--semantic-accent", "--foreground"],
        ["--secondary", "--foreground"],
        ["--secondary", "--muted-foreground"],
        ["--semantic-muted", "--foreground"],
        ["--card", "--input"],
        ["--background", "--input"],
        ["--card", "--ring"],
        ["--card", "--primary"],
        ["--card", "--control-border"],
        ["--card", "--control-ring"],
      ];
      const canvas = document.createElement("canvas");
      canvas.width = canvas.height = 1;
      const context = canvas.getContext("2d", { willReadFrequently: true })!;
      const rgb = (css: string) => {
        context.clearRect(0, 0, 1, 1);
        context.fillStyle = css;
        context.fillRect(0, 0, 1, 1);
        return [...context.getImageData(0, 0, 1, 1).data].slice(0, 3);
      };
      const luminance = (rgbValue: number[]) =>
        rgbValue
          .map((v) => {
            const s = v / 255;
            return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
          })
          .reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
      const ratio = (a: string, b: string) => {
        const values = [luminance(rgb(a)), luminance(rgb(b))].sort((x, y) => y - x);
        return Math.round(((values[0] + 0.05) / (values[1] + 0.05)) * 100) / 100;
      };
      const results = [];
      for (const theme of themes)
        for (const mode of theme === "napster" ? ["light"] : ["light", "dark"]) {
          for (const accent of theme.startsWith("kanagawa") ? accents : [""]) {
            const root = document.documentElement;
            root.dataset.theme = theme;
            root.dataset.accent = accent;
            root.classList.toggle("dark", mode === "dark");
            root.classList.toggle("light", mode === "light");
            root.style.colorScheme = mode;
            const css = getComputedStyle(root);
            results.push({
              theme,
              mode,
              accent,
              pairs: pairs.map(([background, foreground]) => {
                const bg = css.getPropertyValue(background).trim();
                const fg = css.getPropertyValue(foreground).trim();
                return { background, foreground, ratio: ratio(bg, fg), bg, fg };
              }),
            });
          }
        }
      return results;
    });
    await writeFile(path.join(output, "palette-roles.json"), JSON.stringify(report, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("switch thumbs retain state contrast across resolved palettes", async ({ page }) => {
  const output = path.resolve("../docs/plans/visual-quality-evidence/control-indicators-switch");
  await mkdir(output, { recursive: true });
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await page.goto(new URL("settings", app.url).toString());
    await expect(page.getByRole("switch", { name: "Verbose Notification" })).toBeVisible();
    const results = await page.evaluate(() => {
      const off = document.querySelector<HTMLElement>(
        '[role="switch"][aria-label="Verbose Notification"]',
      )!;
      const on = document.querySelector<HTMLElement>(
        '[role="switch"][aria-label="Update Notification"]',
      )!;
      off.style.transition = "none";
      on.style.transition = "none";
      const canvas = document.createElement("canvas");
      canvas.width = canvas.height = 1;
      const context = canvas.getContext("2d", { willReadFrequently: true })!;
      const luminance = (color: string) => {
        context.clearRect(0, 0, 1, 1);
        context.fillStyle = color;
        context.fillRect(0, 0, 1, 1);
        const channels = context.getImageData(0, 0, 1, 1).data;
        return [0, 1, 2].reduce((sum, index) => {
          const value = channels[index] / 255;
          const linear = value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
          return sum + linear * [0.2126, 0.7152, 0.0722][index];
        }, 0);
      };
      const ratio = (a: string, b: string) => {
        const values = [luminance(a), luminance(b)].sort((x, y) => y - x);
        return Math.round(((values[0] + 0.05) / (values[1] + 0.05)) * 100) / 100;
      };
      const results = [];
      for (const theme of [
        "minimal",
        "autobrr",
        "the-kyle",
        "nightwalker",
        "swizzin",
        "kanagawa-dragon",
        "kanagawa-wave",
        "napster",
      ])
        for (const mode of theme === "napster" ? ["light"] : ["light", "dark"])
          for (const accent of theme.startsWith("kanagawa")
            ? ["blueish", "pink", "green", "purple", "grayish", "orange"]
            : [""]) {
            const root = document.documentElement;
            root.dataset.theme = theme;
            root.dataset.accent = accent;
            root.classList.toggle("dark", mode === "dark");
            root.classList.toggle("light", mode === "light");
            const offStyle = getComputedStyle(off);
            const onStyle = getComputedStyle(on);
            const offThumb = getComputedStyle(off.firstElementChild!);
            const onThumb = getComputedStyle(on.firstElementChild!);
            results.push({
              theme,
              mode,
              accent,
              off: ratio(offStyle.backgroundColor, offThumb.backgroundColor),
              on: ratio(onStyle.backgroundColor, onThumb.backgroundColor),
              offTrack: offStyle.backgroundColor,
              offThumb: offThumb.backgroundColor,
              onTrack: onStyle.backgroundColor,
              onThumb: onThumb.backgroundColor,
            });
          }
      return results;
    });
    expect(results).toHaveLength(35);
    await writeFile(path.join(output, "switch-states.json"), JSON.stringify(results, null, 2));
    for (const result of results) {
      expect(
        result.off,
        `${result.theme} ${result.mode} ${result.accent} off`,
      ).toBeGreaterThanOrEqual(3);
      expect(
        result.on,
        `${result.theme} ${result.mode} ${result.accent} on`,
      ).toBeGreaterThanOrEqual(3);
    }
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("tracker checkbox indicators retain contrast across resolved palettes", async ({ page }) => {
  const output = path.resolve("../docs/plans/visual-quality-evidence/control-indicators-checkbox");
  await mkdir(output, { recursive: true });
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    const disclosure = page.locator("details.tracker-dropdown");
    if (!(await disclosure.evaluate((element) => (element as HTMLDetailsElement).open)))
      await disclosure.locator("summary").click();
    await expect(page.getByRole("checkbox", { name: "BTN" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "AITHER" })).not.toBeChecked();
    const results = await page.evaluate(() => {
      const selected = document.querySelector<HTMLElement>('[role="checkbox"][aria-label="BTN"]')!;
      const unselected = document.querySelector<HTMLElement>(
        '[role="checkbox"][aria-label="AITHER"]',
      )!;
      selected.style.transition = "none";
      unselected.style.transition = "none";
      const canvas = document.createElement("canvas");
      canvas.width = canvas.height = 1;
      const context = canvas.getContext("2d", { willReadFrequently: true })!;
      const luminance = (color: string) => {
        context.clearRect(0, 0, 1, 1);
        context.fillStyle = color;
        context.fillRect(0, 0, 1, 1);
        const channels = context.getImageData(0, 0, 1, 1).data;
        return [0, 1, 2].reduce((sum, index) => {
          const value = channels[index] / 255;
          const linear = value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
          return sum + linear * [0.2126, 0.7152, 0.0722][index];
        }, 0);
      };
      const ratio = (a: string, b: string) => {
        const values = [luminance(a), luminance(b)].sort((x, y) => y - x);
        return Math.round(((values[0] + 0.05) / (values[1] + 0.05)) * 100) / 100;
      };
      const rows = [];
      for (const theme of [
        "minimal",
        "autobrr",
        "the-kyle",
        "nightwalker",
        "swizzin",
        "kanagawa-dragon",
        "kanagawa-wave",
        "napster",
      ])
        for (const mode of theme === "napster" ? ["light"] : ["light", "dark"])
          for (const accent of theme.startsWith("kanagawa")
            ? ["blueish", "pink", "green", "purple", "grayish", "orange"]
            : [""]) {
            const root = document.documentElement;
            root.dataset.theme = theme;
            root.dataset.accent = accent;
            root.classList.toggle("dark", mode === "dark");
            root.classList.toggle("light", mode === "light");
            root.style.colorScheme = mode;
            const checkedStyle = getComputedStyle(selected);
            const uncheckedStyle = getComputedStyle(unselected);
            const rootStyle = getComputedStyle(root);
            rows.push({
              theme,
              mode,
              accent,
              checked: ratio(checkedStyle.color, checkedStyle.backgroundColor),
              unchecked: ratio(uncheckedStyle.color, uncheckedStyle.backgroundColor),
              checkedForeground: checkedStyle.color,
              checkedBackground: checkedStyle.backgroundColor,
              uncheckedForeground: uncheckedStyle.color,
              uncheckedBackground: uncheckedStyle.backgroundColor,
              ringCard: ratio(
                rootStyle.getPropertyValue("--control-ring"),
                rootStyle.getPropertyValue("--card"),
              ),
            });
          }
      return rows;
    });
    expect(results).toHaveLength(35);
    await writeFile(path.join(output, "checkbox-states.json"), JSON.stringify(results, null, 2));
    for (const result of results) {
      expect(
        result.checked,
        `${result.theme} ${result.mode} ${result.accent} checked`,
      ).toBeGreaterThanOrEqual(3);
      expect(
        result.unchecked,
        `${result.theme} ${result.mode} ${result.accent} unchecked`,
      ).toBeGreaterThanOrEqual(3);
      expect(
        result.ringCard,
        `${result.theme} ${result.mode} ${result.accent} focus ring on card`,
      ).toBeGreaterThanOrEqual(3);
    }
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("reported selectors keep keyboard and label behavior", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    const disclosure = page.locator("details.tracker-dropdown");
    if (!(await disclosure.evaluate((element) => (element as HTMLDetailsElement).open))) {
      await disclosure.locator("summary").click();
    }
    const tracker = page.getByRole("checkbox", { name: "BTN" });
    await expect(tracker).toBeChecked();
    await tracker.focus();
    await page.keyboard.press("Space");
    await expect(tracker).not.toBeChecked();
    await expect(page.locator(".tracker-summary-count").first()).toContainText("0/6");
    await tracker.click();
    await expect(tracker).toBeChecked();

    await page.goto(new URL("logging", app.url).toString());
    await page.getByRole("heading", { name: "Logging", exact: true }).waitFor();
    const level = page.getByRole("combobox", { name: "Level" });
    await level.focus();
    await page.keyboard.press("End");
    await expect(level).toHaveValue("error");
    await level.selectOption("debug");
    const infoFilter = page.getByRole("checkbox", { name: "INFO" });
    await expect(infoFilter).toBeChecked();
    await page.locator("label[for='log-level-info']").click();
    await expect(infoFilter).not.toBeChecked();
    await page.locator("label[for='log-level-info']").click();
    await expect(infoFilter).toBeChecked();
    const autoScroll = page.getByRole("switch", { name: "Auto-scroll logs" });
    const autoScrollBefore = await autoScroll.getAttribute("aria-checked");
    await page.getByText("Auto-scroll", { exact: true }).click();
    expect(await autoScroll.getAttribute("aria-checked")).not.toBe(autoScrollBefore);

    await page.goto(new URL("settings", app.url).toString());
    await page.getByRole("heading", { name: "Settings", exact: true }).waitFor();
    const booleanField = page.locator(".settings-field--switch").first();
    const booleanSwitch = booleanField.getByRole("switch");
    const booleanBefore = await booleanSwitch.getAttribute("aria-checked");
    await booleanField.locator("span").first().click();
    expect(await booleanSwitch.getAttribute("aria-checked")).not.toBe(booleanBefore);

    await page
      .getByRole("navigation", { name: "Settings sections" })
      .getByRole("button", { name: "Appearance", exact: true })
      .click();
    await expect(page.getByRole("heading", { name: "Appearance" })).toBeVisible();
    await page.getByRole("radio", { name: "Kanagawa Dragon" }).locator("..").click();
    const purpleAccent = page.getByRole("button", { name: "purple" });
    await purpleAccent.click();
    await expect(purpleAccent).toHaveAttribute("aria-pressed", "true");
    await expect(purpleAccent).toContainText("✓");
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("capture populated main workflow routes", async ({ page }) => {
  test.setTimeout(240_000);
  const workspace = await createE2EWorkspace({ screenshotCount: 2, preparedMediaInfo: true });
  let app: AppServer | undefined;
  const output = path.resolve("../docs/plans/visual-quality-evidence/workflow-main");
  await mkdir(output, { recursive: true });
  const records: unknown[] = [];
  const capture = async (name: string) => {
    await page.screenshot({ path: path.join(output, `${name}.png`), fullPage: true });
    records.push({
      name,
      url: page.url(),
      title: await page.locator("main").innerText(),
      controls: await page
        .locator(
          "main select, main [role='switch'], main [role='checkbox'], main input[type='checkbox'], main input[type='radio']",
        )
        .count(),
    });
  };
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await capture("input-prepared");
    await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
    await expect(page.getByRole("checkbox", { name: "BTN" })).toBeChecked();
    await page.getByRole("checkbox", { name: "BTN" }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await capture("duplicates-selected");
    await page.getByRole("button", { name: "Run dupe check" }).click();
    await expect(page.getByRole("button", { name: "Run dupe check" })).toBeEnabled({
      timeout: 30_000,
    });
    await expect(page.getByRole("button", { name: "Screenshots", exact: true })).toBeEnabled({
      timeout: 30_000,
    });
    await capture("duplicates-complete");
    const expectedHeadings: Record<string, string> = {
      "tracker-data": "View unavailable",
      screenshots: "Plan & Capture",
      "menu-images": "Menu Images",
      "uploaded-images": "Upload Images",
      descriptions: "Customize Description",
      upload: "Review & Upload",
      history: "History",
    };
    for (const route of Object.keys(expectedHeadings)) {
      await page.goto(new URL(route, app.url).toString());
      await page
        .getByRole("main")
        .getByRole("heading", { name: expectedHeadings[route], exact: true })
        .waitFor();
      await capture(`${route}-entry`);
      if (route === "screenshots") {
        const generate = page.getByRole("button", { name: "Generate screenshots" });
        if (await generate.isEnabled()) {
          await generate.click();
          await expect(page.getByAltText("Screenshot 1")).toBeVisible();
          await capture("screenshots-populated");
        }
      }
      if (route === "descriptions") {
        const refresh = page.getByRole("button", { name: "Refresh descriptions" });
        if (await refresh.isEnabled()) {
          await refresh.click();
          await page.getByRole("button", { name: "Expand" }).first().waitFor();
          await capture("descriptions-populated");
        }
      }
    }
    await writeFile(path.join(output, "route-states.json"), JSON.stringify(records, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("capture Blu-ray candidate, audio analysis, and sign-in states", async ({ page }) => {
  test.setTimeout(240_000);
  const output = path.resolve("../docs/plans/visual-quality-evidence/workflow-special");
  await mkdir(output, { recursive: true });
  const records: unknown[] = [];
  const capture = async (name: string) => {
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await page.screenshot({ path: path.join(output, `${name}-${width}.png`), fullPage: true });
      records.push({
        name,
        width,
        url: page.url(),
        overflow: await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
      });
    }
  };
  for (const kind of ["bluray", "audio", "auth"] as const) {
    const workspace = await createE2EWorkspace({ audioAnalysis: kind === "audio" });
    if (kind === "bluray") workspace.env.UPBRR_E2E_BLURAY_CANDIDATES = "1";
    let app: AppServer | undefined;
    try {
      app = await startApp(workspace, { devNoAuth: kind !== "auth" });
      if (kind === "auth") {
        await page.goto(app.url);
        await expect(page.getByRole("heading", { name: "Sign In" })).toBeVisible();
        await capture("sign-in");
        continue;
      }
      await fetchMetadata(page, app.url, workspace.sourcePath);
      if (kind === "bluray") {
        await page.route("https://example.com/images/example-bluray-primary.jpg", (route) =>
          route.fulfill({
            contentType: "image/svg+xml",
            body: '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="220"><rect width="160" height="220" fill="#40536d"/><text x="80" y="115" fill="white" font-size="18" text-anchor="middle">Front</text></svg>',
          }),
        );
        await page.getByRole("button", { name: "Blu-ray Candidates" }).click();
        await expect(page.getByText("Example Release 2026 Collector Edition")).toBeVisible();
        await capture("bluray-candidates");
        await page.getByRole("button", { name: /^Select candidate \d+:/ }).click();
        await expect(
          page.getByRole("button", {
            name: "Selected candidate 2: Example Release 2026 Standard Edition",
          }),
        ).toBeVisible();
        await expect(page.getByText(/Operation running/)).toHaveCount(0);
        await capture("bluray-selected");
      } else {
        await page.getByRole("button", { name: "Audio Analysis", exact: true }).click();
        await expect(
          page.getByRole("heading", { name: "Waveforms, Spectrograms & Statistics" }),
        ).toBeVisible();
        await capture("audio-analysis");
        await page.getByRole("button", { name: "Generate", exact: true }).click();
        await expect(page.getByRole("heading", { name: "Results" })).toBeVisible({
          timeout: 20_000,
        });
        await expect(page.locator("pre").filter({ hasText: "DC offset" })).toBeVisible();
        await capture("audio-results");
      }
    } finally {
      await app?.stop();
      await workspace.cleanup();
    }
  }
  await writeFile(path.join(output, "route-states.json"), JSON.stringify(records, null, 2));
});

test("capture populated tracker metadata from a synthetic active-input fixture", async ({
  page,
}) => {
  test.setTimeout(240_000);
  const output = path.resolve("../docs/plans/visual-quality-evidence/workflow-tracker-data");
  await mkdir(output, { recursive: true });
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.route("**/api/app/GetActiveInput", async (route) => {
      const response = await route.fetch();
      const snapshot = await response.json();
      const display = snapshot.current?.release?.display;
      if (display)
        display.TrackerData = [
          {
            Tracker: "HDS",
            TrackerID: "e2e-2048",
            InfoHash: "ABCDEF0123456789",
            TMDBID: 1001,
            IMDBID: 1234567,
            TVDBID: 0,
            MALID: 0,
            Category: "MOVIE",
            Description:
              "Synthetic tracker description for visual review.\n\nSecond paragraph with a longer line that must wrap cleanly across mobile widths.",
            DescriptionHTML: "<p>Synthetic tracker description for visual review.</p>",
            ImageURLs: [],
            Filename: "E2E.Movie.2026.1080p.WEB-DL.DD5.1.H264-UPBRR.mkv",
            Matched: true,
            UpdatedAt: "2026-09-25T00:00:00Z",
          },
          {
            Tracker: "BTN",
            TrackerID: "e2e-4096",
            InfoHash: "",
            TMDBID: 0,
            IMDBID: 0,
            TVDBID: 0,
            MALID: 0,
            Category: "MOVIE",
            Description: "",
            DescriptionHTML: "",
            ImageURLs: [],
            Filename: "",
            Matched: false,
            UpdatedAt: "2026-09-25T00:00:00Z",
          },
        ];
      await route.fulfill({ response, body: JSON.stringify(snapshot) });
    });
    await page.reload();
    await expect(page.getByRole("button", { name: "Tracker Data" })).toBeVisible({
      timeout: 5_000,
    });
    await page.getByRole("button", { name: "Tracker Data" }).click();
    await expect(page.getByRole("heading", { name: "Input Metadata" })).toBeVisible();
    await expect(
      page.getByText("Synthetic tracker description for visual review.", { exact: false }),
    ).toBeVisible();
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await page.screenshot({
        path: path.join(output, `tracker-data-${width}.png`),
        fullPage: true,
      });
    }
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("sweep populated routes across resolved palettes and widths", async ({ page }) => {
  test.setTimeout(1_200_000);
  const output = path.resolve(
    "../docs/plans/visual-quality-evidence/matrix-v2",
    [
      process.env.VQ_THEME || "all",
      process.env.VQ_MODE || "all",
      process.env.VQ_THEME?.startsWith("kanagawa") ? process.env.VQ_ACCENT || "all" : "all",
    ].join("-"),
  );
  await mkdir(output, { recursive: true });
  const workspace = await createE2EWorkspace({ screenshotCount: 2, preparedMediaInfo: true });
  let app: AppServer | undefined;
  const records: unknown[] = [];
  const themes = [
    "minimal",
    "autobrr",
    "the-kyle",
    "nightwalker",
    "swizzin",
    "kanagawa-dragon",
    "kanagawa-wave",
    "napster",
  ].filter((theme) => !process.env.VQ_THEME || theme === process.env.VQ_THEME);
  const accents = ["blueish", "pink", "green", "purple", "grayish", "orange"];
  const routes = [
    "input",
    "duplicates",
    "screenshots",
    "menu-images",
    "uploaded-images",
    "descriptions",
    "upload",
    "history",
    "settings",
    "logging",
  ];
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
    await page.getByRole("checkbox", { name: "BTN" }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await page.getByRole("button", { name: "Run dupe check" }).click();
    await expect(page.getByRole("button", { name: "Screenshots", exact: true })).toBeEnabled({
      timeout: 30_000,
    });
    await page.getByRole("button", { name: "Screenshots", exact: true }).click();
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByAltText("Screenshot 1")).toBeVisible();
    await page.getByRole("button", { name: "Descriptions", exact: true }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).first().waitFor();
    for (const theme of themes) {
      for (const mode of (theme === "napster" ? ["light"] : ["light", "dark"]).filter(
        (mode) => !process.env.VQ_MODE || mode === process.env.VQ_MODE,
      )) {
        for (const accent of theme.startsWith("kanagawa")
          ? accents.filter((accent) => !process.env.VQ_ACCENT || accent === process.env.VQ_ACCENT)
          : [""]) {
          await page.evaluate(
            ({ theme, mode, accent }) => {
              localStorage.setItem(
                "upbrr:appearance:v1",
                JSON.stringify({
                  version: 1,
                  theme,
                  mode,
                  accents: theme.startsWith("kanagawa") ? { [theme]: accent } : {},
                }),
              );
            },
            { theme, mode, accent },
          );
          for (const width of [1280, 390]) {
            await page.setViewportSize({ width, height: 900 });
            for (const route of routes) {
              if (route === "input") {
                await page.route("**/api/app/GetActiveInput", async (intercept) => {
                  const response = await intercept.fetch();
                  const snapshot = await response.json();
                  if (snapshot.current)
                    snapshot.current.inputReadiness = {
                      ...(snapshot.current.inputReadiness || {}),
                      status: "completed",
                      fields: [],
                      schemas: [
                        {
                          Tracker: "HDS",
                          Fields: [
                            {
                              Key: "no_english_subtitles",
                              Label: "No English subtitles",
                              Kind: "select",
                              Options: ["auto", "yes", "no"],
                              Value: "",
                              Placeholder: "Select an answer",
                              Help: "Synthetic tracker input for visual review.",
                              Required: true,
                            },
                          ],
                        },
                      ],
                    };
                  await intercept.fulfill({ response, body: JSON.stringify(snapshot) });
                });
              }
              if (route === "upload") {
                await page.route("**/api/app/GetActiveInput", async (intercept) => {
                  const response = await intercept.fetch();
                  const snapshot = await response.json();
                  const projection = snapshot.current?.projections?.projections?.find(
                    (item: { trackerId: string }) => item.trackerId === "HDS",
                  );
                  if (projection)
                    projection.questionnaire = [
                      {
                        key: "edition",
                        label: "Edition",
                        options: ["Standard", "Extended"],
                        required: true,
                      },
                      { key: "note", label: "Note", required: false },
                    ];
                  await intercept.fulfill({ response, body: JSON.stringify(snapshot) });
                });
              }
              await page.goto(new URL(route, app.url).toString());
              await waitForPopulatedRoute(page, route);
              if (route === "input") {
                await expect(page.getByTestId("input-tracker-fields")).toHaveCount(1);
              }
              if (route === "upload") {
                await expect(page.getByRole("combobox", { name: "Edition *" })).toBeVisible();
              }
              const mainText = await page.getByRole("main").innerText();
              expect(mainText).not.toContain("rate limit exceeded");
              expect(mainText).not.toContain("Runtime capabilities could not be loaded");
              expect(mainText).not.toContain("View unavailable");
              const name = [theme, accent || "default", mode, route, width].join("-");
              await captureFullContent(page, path.join(output, `${name}.png`));
              if (route === "input") {
                const disclosure = page.locator("details.tracker-dropdown");
                if (!(await disclosure.evaluate((element) => (element as HTMLDetailsElement).open)))
                  await disclosure.locator("summary").click();
                await captureFullContent(page, path.join(output, `${name}-trackers-open.png`));
              }
              if (route === "logging") {
                const level = page.getByRole("combobox", { name: "Level" });
                await level.evaluate((element) => element.scrollIntoView({ block: "center" }));
                await level.click();
                await page.screenshot({
                  path: path.join(output, `${name}-level-open.png`),
                  animations: "disabled",
                });
                await page.keyboard.press("Escape");
              }
              records.push({
                name,
                theme,
                accent,
                mode,
                route,
                width,
                root: await page.locator("html").evaluate((element) => ({
                  theme: element.getAttribute("data-theme"),
                  accent: element.getAttribute("data-accent"),
                  mode: element.classList.contains("dark") ? "dark" : "light",
                  colorScheme: getComputedStyle(element).colorScheme,
                })),
                overflow: await page.evaluate(
                  () => document.documentElement.scrollWidth - innerWidth,
                ),
                heading: await page.getByRole("main").getByRole("heading").first().textContent(),
                controls: await page
                  .locator(inventorySurface("main"))
                  .evaluateAll(captureControlRecords),
                ...(await page.evaluate(auditComposedText)),
                interactions: [
                  ...(await exerciseVisibleControls(page, output, name)),
                  ...(await exerciseDisclosuresAndPressed(page)),
                ],
                conditional:
                  route === "input"
                    ? await captureExpandedSelectionState(page, output, name)
                    : null,
              });
              if (route === "input" || route === "upload")
                await page.unroute("**/api/app/GetActiveInput");
            }
          }
          await writeFile(path.join(output, "progress.json"), JSON.stringify(records, null, 2));
        }
      }
    }
    if (process.env.VQ_THEME) expect(records).toHaveLength(routes.length * 2);
    await writeFile(path.join(output, "cells.json"), JSON.stringify(records, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("audit composed text contrast on populated routes", async ({ page }) => {
  test.setTimeout(1_200_000);
  const output = path.resolve(
    "../docs/plans/visual-quality-evidence/composed-contrast-v2",
    [
      process.env.VQ_THEME || "all",
      process.env.VQ_MODE || "all",
      process.env.VQ_ACCENT || "all",
    ].join("-"),
  );
  await mkdir(output, { recursive: true });
  const workspace = await createE2EWorkspace({ screenshotCount: 2, preparedMediaInfo: true });
  let app: AppServer | undefined;
  const records: unknown[] = [];
  const themes = [
    "minimal",
    "autobrr",
    "the-kyle",
    "nightwalker",
    "swizzin",
    "kanagawa-dragon",
    "kanagawa-wave",
    "napster",
  ].filter((theme) => !process.env.VQ_THEME || theme === process.env.VQ_THEME);
  const accents = ["blueish", "pink", "green", "purple", "grayish", "orange"];
  const routes = [
    "input",
    "duplicates",
    "screenshots",
    "menu-images",
    "uploaded-images",
    "descriptions",
    "upload",
    "history",
    "settings",
    "logging",
  ].filter((route) => !process.env.VQ_ROUTE || route === process.env.VQ_ROUTE);
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
    await page.getByRole("checkbox", { name: "BTN" }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await page.getByRole("button", { name: "Run dupe check" }).click();
    await expect(page.getByRole("button", { name: "Screenshots", exact: true })).toBeEnabled({
      timeout: 30_000,
    });
    await page.getByRole("button", { name: "Screenshots", exact: true }).click();
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByAltText("Screenshot 1")).toBeVisible();
    await page.getByRole("button", { name: "Descriptions", exact: true }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).first().waitFor();
    for (const theme of themes) {
      for (const mode of (theme === "napster" ? ["light"] : ["light", "dark"]).filter(
        (mode) => !process.env.VQ_MODE || mode === process.env.VQ_MODE,
      )) {
        for (const accent of (theme.startsWith("kanagawa") ? accents : [""]).filter(
          (accent) => !process.env.VQ_ACCENT || accent === process.env.VQ_ACCENT,
        )) {
          await page.evaluate(
            ({ theme, mode, accent }) => {
              localStorage.setItem(
                "upbrr:appearance:v1",
                JSON.stringify({
                  version: 1,
                  theme,
                  mode,
                  accents: theme.startsWith("kanagawa") ? { [theme]: accent } : {},
                }),
              );
            },
            { theme, mode, accent },
          );
          for (const width of [1280, 390]) {
            await page.setViewportSize({ width, height: 900 });
            for (const route of routes) {
              await page.goto(new URL(route, app.url).toString());
              await waitForPopulatedRoute(page, route);
              const mainText = await page.getByRole("main").innerText();
              expect(mainText).not.toContain("rate limit exceeded");
              expect(mainText).not.toContain("Runtime capabilities could not be loaded");
              const audit = await page.evaluate(auditComposedText);
              records.push({ theme, accent, mode, width, route, ...audit });
            }
          }
          await writeFile(path.join(output, "progress.json"), JSON.stringify(records, null, 2));
        }
      }
    }
    await writeFile(path.join(output, "cells.json"), JSON.stringify(records, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

async function seedVisualClients(workspace: Awaited<ReturnType<typeof createE2EWorkspace>>) {
  const config = await readFile(workspace.configPath, "utf8");
  await writeFile(
    workspace.configPath,
    config.replace(
      "torrent_clients: {}",
      `torrent_clients:
  visual-qbit:
    type: qbit
    url: "http://127.0.0.1:8765"
    username: "synthetic-user"
    password: "synthetic-password"
    allow_fallback: true
    linked_folder: ["C:/synthetic/linked"]
    local_path: ["C:/synthetic/source"]
    remote_path: ["/synthetic/source"]
  visual-watch:
    type: watch
    watch_folder: "C:/synthetic/watch"
    torrent_storage_dir: "C:/synthetic/storage"`,
    ),
  );
}

test("capture all settings subsections and their visible controls", async ({ page }) => {
  test.setTimeout(600_000);
  const theme = process.env.VQ_THEME ?? "minimal";
  const mode = process.env.VQ_MODE ?? "light";
  const accent = theme.startsWith("kanagawa") ? (process.env.VQ_ACCENT ?? "blueish") : "";
  const output = path.resolve(
    "../docs/plans/visual-quality-evidence/settings-matrix",
    [theme, mode, accent].join("-"),
  );
  await mkdir(output, { recursive: true });
  let workspace = await createE2EWorkspace();
  await seedVisualClients(workspace);
  let app: AppServer | undefined;
  const records: unknown[] = [];
  const sections = [
    "Main",
    "Image Hosting",
    "Metadata",
    "Screens",
    "Description",
    "Arr",
    "Post Upload",
    "Trackers",
    "Torrent Clients",
    "Client Handling",
    "Torrent Specific",
    "Appearance",
    "Application Details",
    "API Tokens",
    "Tracker Auth",
  ];
  const expectedControlCounts: Record<string, number> = {
    Main: 20,
    "Image Hosting": 24,
    Metadata: 25,
    Screens: 20,
    Description: 20,
    Arr: 18,
    "Post Upload": 22,
    Trackers: 52,
    "Torrent Clients": 23,
    "Client Handling": 16,
    "Torrent Specific": 16,
    Appearance: theme.startsWith("kanagawa") ? 32 : 26,
    "Application Details": 15,
    "API Tokens": 18,
    "Tracker Auth": 15,
  };
  try {
    app = await startApp(workspace);
    await page.goto(app.url);
    await page.evaluate(
      ({ theme, mode, accent }) =>
        localStorage.setItem(
          "upbrr:appearance:v1",
          JSON.stringify({
            version: 1,
            theme,
            mode,
            accents: theme.startsWith("kanagawa") ? { [theme]: accent } : {},
          }),
        ),
      { theme, mode, accent },
    );
    for (const width of [1280, 390]) {
      if (width === 390) {
        await app?.stop();
        await workspace.cleanup();
        workspace = await createE2EWorkspace();
        await seedVisualClients(workspace);
        app = await startApp(workspace);
        await page.goto(app.url);
        await page.evaluate(
          ({ theme, mode, accent }) =>
            localStorage.setItem(
              "upbrr:appearance:v1",
              JSON.stringify({
                version: 1,
                theme,
                mode,
                accents: theme.startsWith("kanagawa") ? { [theme]: accent } : {},
              }),
            ),
          { theme, mode, accent },
        );
      }
      await page.setViewportSize({ width, height: 900 });
      await page.goto(new URL("settings", app.url).toString());
      await page.getByRole("heading", { name: "Settings", exact: true }).waitFor();
      for (const section of sections) {
        if (section === "API Tokens")
          await page.route("**/api/app/ListAPITokens", (route) =>
            route.fulfill({
              contentType: "application/json",
              body: JSON.stringify([
                {
                  id: "synthetic-token-1",
                  name: "Visual token one",
                  ownerId: "visual-owner",
                  scopes: ["workflow:read"],
                  createdAt: "2026-09-25T00:00:00Z",
                },
                {
                  id: "synthetic-token-2",
                  name: "Visual token two",
                  ownerId: "visual-owner",
                  scopes: ["workflow:read", "workflow:write"],
                  createdAt: "2026-09-25T00:00:00Z",
                },
              ]),
            }),
          );
        const button = page
          .getByRole("navigation", { name: "Settings sections" })
          .getByRole("button", { name: section, exact: true });
        await button.click();
        await expect(button).toHaveAttribute("aria-pressed", "true");
        await button.focus();
        await page.keyboard.press("Enter");
        await expect(button).toHaveAttribute("aria-pressed", "true");
        if (
          !["Appearance", "Application Details", "API Tokens", "Tracker Auth"].includes(section)
        ) {
          await expect(page.locator(".settings-body > .settings-form")).toBeVisible();
          await expect(page.getByText("Loading configuration...")).toHaveCount(0);
        }
        if (section === "Tracker Auth") {
          await expect(page.locator(".tracker-auth-card")).toHaveCount(4);
          await expect(page.getByText("Loading tracker auth...")).toBeHidden();
        }
        if (section === "API Tokens") {
          await expect(
            page.getByRole("button", { name: "Revoke Visual token one (synthetic-token-1)" }),
          ).toBeVisible();
          await expect(
            page.getByRole("button", { name: "Revoke Visual token two (synthetic-token-2)" }),
          ).toBeVisible();
        }
        if (section === "Application Details") {
          await expect(
            page.locator(".settings-body").getByText("Version", { exact: true }),
          ).toBeVisible();
        }
        await expect(page.locator(".settings-body").getByText(/^Loading /)).toHaveCount(0);
        const advanced = page.getByRole("switch", { name: "Show advanced" });
        if (section === "Trackers") await expect(advanced).toHaveCount(1);
        if ((await advanced.count()) && (await advanced.getAttribute("aria-checked")) === "false")
          await advanced.click();
        if (await advanced.count()) await expect(advanced).toHaveAttribute("aria-checked", "true");
        await page.locator("main details").evaluateAll((elements) =>
          elements.forEach((element) => {
            element.open = true;
          }),
        );
        await expect
          .poll(
            async () =>
              (await page.locator(inventorySurface("main")).evaluateAll(captureControlRecords))
                .length,
            { timeout: 20_000 },
          )
          .toBe(expectedControlCounts[section]);
        const name = section.toLowerCase().replaceAll(" ", "-");
        await captureFullContent(page, path.join(output, `${name}-${width}.png`));
        const controls = await page
          .locator(inventorySurface("main"))
          .evaluateAll(captureControlRecords);
        records.push({
          section,
          width,
          controls,
          overflow: await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
          pane: await page.locator("main.content").evaluate((element) => ({
            clientHeight: element.clientHeight,
            scrollHeight: element.scrollHeight,
          })),
          screenshot: `${name}-${width}.png`,
          ...(await page.evaluate(auditComposedText)),
          interactions: [
            ...(await exerciseVisibleControls(page, output, `${name}-${width}`)),
            ...(await exerciseDisclosuresAndPressed(page)),
          ],
        });
        if (section === "API Tokens") {
          await page
            .getByRole("button", { name: "Revoke Visual token two (synthetic-token-2)" })
            .click();
          await expect(page.getByRole("alertdialog")).toContainText("Revoke Visual token two?");
          await captureFullContent(page, path.join(output, `api-tokens-confirm-${width}.png`));
          await page.getByRole("button", { name: "Cancel" }).click();
          await page.unroute("**/api/app/ListAPITokens");
        }
        await page.reload();
      }
    }
    await writeFile(path.join(output, "controls.json"), JSON.stringify(records, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("sweep conditional routes and sign-in across the selected palette", async ({ page }) => {
  test.setTimeout(600_000);
  const theme = process.env.VQ_THEME ?? "minimal";
  const mode = process.env.VQ_MODE ?? "light";
  const accent = theme.startsWith("kanagawa") ? (process.env.VQ_ACCENT ?? "blueish") : "";
  const output = path.resolve(
    "../docs/plans/visual-quality-evidence/matrix-special",
    [theme, mode, accent].join("-"),
  );
  await mkdir(output, { recursive: true });
  const records: unknown[] = [];
  for (const kind of [
    "tracker-data",
    "bluray-candidates",
    "audio-analysis",
    "multi-disc-screenshots",
    "sign-in",
  ] as const) {
    const workspace = await createE2EWorkspace({ audioAnalysis: kind === "audio-analysis" });
    if (kind === "bluray-candidates") workspace.env.UPBRR_E2E_BLURAY_CANDIDATES = "1";
    const sourcePath =
      kind === "multi-disc-screenshots"
        ? await createMultiDVDSourceFixture(workspace)
        : workspace.sourcePath;
    let app: AppServer | undefined;
    try {
      app = await startApp(workspace, { devNoAuth: kind !== "sign-in" });
      await page.goto(app.url);
      await page.evaluate(() => {
        localStorage.clear();
        sessionStorage.clear();
      });
      await page.evaluate(
        ({ theme, mode, accent }) =>
          localStorage.setItem(
            "upbrr:appearance:v1",
            JSON.stringify({
              version: 1,
              theme,
              mode,
              accents: theme.startsWith("kanagawa") ? { [theme]: accent } : {},
            }),
          ),
        { theme, mode, accent },
      );
      if (kind === "sign-in") {
        await page.reload();
        await expect(page.getByRole("heading", { name: "Sign In" })).toBeVisible();
      } else {
        await fetchMetadata(page, app.url, sourcePath);
        if (kind === "tracker-data") {
          await page.route("**/api/app/GetActiveInput", async (route) => {
            const response = await route.fetch();
            const snapshot = await response.json();
            const display = snapshot.current?.release?.display;
            if (display)
              display.TrackerData = [
                {
                  Tracker: "HDS",
                  TrackerID: "e2e-2048",
                  InfoHash: "ABCDEF0123456789",
                  TMDBID: 1001,
                  IMDBID: 1234567,
                  TVDBID: 0,
                  MALID: 0,
                  Category: "MOVIE",
                  Description:
                    "Synthetic tracker description for visual review.\n\nSecond paragraph with a longer line that must wrap cleanly across mobile widths.",
                  DescriptionHTML: "<p>Synthetic tracker description for visual review.</p>",
                  ImageURLs: [],
                  Filename: "E2E.Movie.2026.1080p.WEB-DL.DD5.1.H264-UPBRR.mkv",
                  Matched: true,
                  UpdatedAt: "2026-09-25T00:00:00Z",
                },
              ];
            await route.fulfill({ response, body: JSON.stringify(snapshot) });
          });
          await page.reload();
          await page.getByRole("button", { name: "Tracker Data" }).click();
          await expect(page.getByRole("heading", { name: "Input Metadata" })).toBeVisible();
        } else if (kind === "bluray-candidates") {
          await page.route("https://example.com/images/example-bluray-primary.jpg", (route) =>
            route.fulfill({
              contentType: "image/svg+xml",
              body: '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="220"><rect width="160" height="220" fill="#40536d"/><text x="80" y="115" fill="white" font-size="18" text-anchor="middle">Front</text></svg>',
            }),
          );
          await page.getByRole("button", { name: "Blu-ray Candidates" }).click();
          await expect(page.getByRole("heading", { name: "Release Candidates" })).toBeVisible();
        } else if (kind === "multi-disc-screenshots") {
          await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
          await page.getByRole("checkbox", { name: "BTN" }).uncheck();
          await page.getByRole("checkbox", { name: "HDS" }).check();
          await page.getByRole("button", { name: "Run dupe check" }).click();
          await expect(page.getByRole("button", { name: "Screenshots", exact: true })).toBeEnabled({
            timeout: 30_000,
          });
          await page.getByRole("button", { name: "Screenshots", exact: true }).click();
          await page.getByRole("button", { name: "Generate screenshots" }).click();
          await expect(page.getByAltText("Disc 1 screenshot 1")).toBeVisible();
          await expect(page.getByRole("combobox", { name: "Preview disc" })).toBeVisible();
        } else {
          await page.getByRole("button", { name: "Audio Analysis", exact: true }).click();
          await expect(
            page.getByRole("heading", { name: "Waveforms, Spectrograms & Statistics" }),
          ).toBeVisible();
        }
      }
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 900 });
        await captureFullContent(page, path.join(output, `${kind}-${width}.png`));
        records.push({
          kind,
          width,
          heading: await page.getByRole("heading", { level: 1 }).first().textContent(),
          overflow: await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
          root: await page.locator("html").evaluate((element) => ({
            theme: element.getAttribute("data-theme"),
            accent: element.getAttribute("data-accent"),
            mode: element.classList.contains("dark") ? "dark" : "light",
            colorScheme: getComputedStyle(element).colorScheme,
          })),
          controls: await page
            .locator(inventorySurface(kind === "sign-in" ? "body" : "main"))
            .evaluateAll(captureControlRecords),
          ...(await page.evaluate(auditComposedText)),
          interactions: [
            ...(await exerciseVisibleControls(
              page,
              output,
              `${kind}-${width}`,
              kind === "sign-in" ? "body" : "main",
            )),
            ...(await exerciseDisclosuresAndPressed(page, kind === "sign-in" ? "body" : "main")),
          ],
          conditional:
            kind === "audio-analysis"
              ? await captureAudioSelectedTracks(page, output, `${kind}-${width}`)
              : null,
        });
      }
      if (kind === "audio-analysis") {
        await page.getByRole("button", { name: "Generate", exact: true }).click();
        await expect(page.locator("pre").filter({ hasText: "DC offset" })).toBeVisible({
          timeout: 20_000,
        });
        for (const width of [1280, 390]) {
          await page.setViewportSize({ width, height: 900 });
          await captureFullContent(page, path.join(output, `audio-results-${width}.png`));
        }
      }
      if (kind === "bluray-candidates") {
        await page.getByRole("button", { name: /^Select candidate \d+:/ }).click();
        await expect(
          page.getByRole("button", {
            name: "Selected candidate 2: Example Release 2026 Standard Edition",
          }),
        ).toBeVisible();
        await expect(page.getByText(/Operation running/)).toHaveCount(0);
        for (const width of [1280, 390]) {
          await page.setViewportSize({ width, height: 900 });
          await captureFullContent(page, path.join(output, `bluray-selected-${width}.png`));
        }
      }
    } finally {
      await page.unroute("**/api/app/GetActiveInput");
      await page.unroute("https://example.com/images/example-bluray-primary.jpg");
      await app?.stop();
      await workspace.cleanup();
    }
  }
  await writeFile(path.join(output, "cells.json"), JSON.stringify(records, null, 2));
});

test("reload every deep link at root and base path in representative palettes", async ({
  page,
}) => {
  test.setTimeout(600_000);
  const output = path.resolve("../docs/plans/visual-quality-evidence/deep-links");
  await mkdir(output, { recursive: true });
  const routes = [
    "input",
    "tracker-data",
    "bluray-candidates",
    "audio-analysis",
    "duplicates",
    "screenshots",
    "menu-images",
    "uploaded-images",
    "descriptions",
    "upload",
    "history",
    "settings",
    "logging",
  ];
  const records: unknown[] = [];
  for (const baseURL of ["/", "/upbrr/"]) {
    const workspace = await createE2EWorkspace();
    let app: AppServer | undefined;
    try {
      app = await startApp(workspace, { baseURL });
      await page.setViewportSize({ width: 768, height: 900 });
      await page.goto(app.url);
      for (const theme of ["minimal", "swizzin"]) {
        for (const mode of ["light", "dark"]) {
          await page.evaluate(
            ({ theme, mode }) =>
              localStorage.setItem(
                "upbrr:appearance:v1",
                JSON.stringify({ version: 1, theme, mode, accents: {} }),
              ),
            { theme, mode },
          );
          for (const route of routes) {
            const url = new URL(route, app.url).toString();
            const response = await page.goto(url);
            expect(response?.status(), `${baseURL}${route} initial response`).toBe(200);
            await expect(page.getByRole("main")).toBeVisible();
            await page.reload();
            await expect(page.getByRole("main")).toBeVisible();
            await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
            const state = await page.evaluate(() => ({
              mode: document.documentElement.classList.contains("dark") ? "dark" : "light",
              colorScheme: getComputedStyle(document.documentElement).colorScheme,
              overflow: document.documentElement.scrollWidth - innerWidth,
              heading: document.querySelector("main h1, main h2")?.textContent?.trim(),
            }));
            expect(state.mode, `${baseURL}${route} ${theme} ${mode} mode`).toBe(mode);
            expect(state.colorScheme, `${baseURL}${route} ${theme} ${mode} scheme`).toBe(mode);
            expect(
              state.overflow,
              `${baseURL}${route} ${theme} ${mode} overflow`,
            ).toBeLessThanOrEqual(0);
            const name = `${baseURL === "/" ? "root" : "base"}-${theme}-${mode}-${route}`;
            await page.screenshot({
              path: path.join(output, `${name}-768.png`),
              animations: "disabled",
            });
            records.push({ baseURL, theme, mode, route, width: 768, ...state });
          }
        }
      }
    } finally {
      await app?.stop();
      await workspace.cleanup();
    }
  }
  await writeFile(path.join(output, "cells.json"), JSON.stringify(records, null, 2));
});

test("capture a populated DVD menu set in representative palettes", async ({ page }) => {
  test.setTimeout(180_000);
  const output = path.resolve("../docs/plans/visual-quality-evidence/menu-populated");
  await mkdir(output, { recursive: true });
  const workspace = await createE2EWorkspace({ screenshotCount: 4 });
  let app: AppServer | undefined;
  const records: unknown[] = [];
  try {
    const sourcePath = await createMultiDVDSourceFixture(workspace);
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, sourcePath);
    await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
    await page.getByRole("checkbox", { name: "BTN" }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await page.getByRole("button", { name: "Run dupe check" }).click();
    await expect(page.getByRole("button", { name: "Screenshots", exact: true })).toBeEnabled({
      timeout: 30_000,
    });
    await page.getByRole("button", { name: "Screenshots", exact: true }).click();
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByAltText("Disc 1 screenshot 1")).toBeVisible();
    await page.getByRole("button", { name: "Upload Images", exact: true }).click();
    await page.getByRole("button", { name: "Prepare required hosts (4)" }).click();
    await expect(page.getByText("4 saved")).toBeVisible();
    await page.getByRole("button", { name: "Menu Images", exact: true }).click();
    await page.getByRole("button", { name: "Capture DVD menus" }).click();
    await expect(page.getByRole("heading", { name: "Authoritative DVD menu set" })).toBeVisible();
    await expect(page.getByText("2 captured menu image(s)")).toBeVisible();
    for (const theme of ["minimal", "swizzin"]) {
      for (const mode of ["light", "dark"]) {
        await page.evaluate(
          ({ theme, mode }) =>
            localStorage.setItem(
              "upbrr:appearance:v1",
              JSON.stringify({ version: 1, theme, mode, accents: {} }),
            ),
          { theme, mode },
        );
        await page.reload();
        await expect(
          page.getByRole("heading", { name: "Authoritative DVD menu set" }),
        ).toBeVisible();
        for (const width of [1280, 390]) {
          await page.setViewportSize({ width, height: 900 });
          const screenshot = `${theme}-${mode}-${width}.png`;
          await page.screenshot({
            path: path.join(output, screenshot),
            fullPage: true,
            animations: "disabled",
          });
          const overflow = await page.evaluate(
            () => document.documentElement.scrollWidth - innerWidth,
          );
          expect(overflow).toBeLessThanOrEqual(0);
          records.push({ theme, mode, width, overflow, screenshot });
        }
      }
    }
    await writeFile(path.join(output, "cells.json"), JSON.stringify(records, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("capture expanded frames, published images, menus, descriptions, and icon-only choices", async ({
  page,
}) => {
  test.setTimeout(600_000);
  const theme = process.env.VQ_THEME ?? "minimal";
  const mode = process.env.VQ_MODE ?? "light";
  const accent = theme.startsWith("kanagawa") ? (process.env.VQ_ACCENT ?? "blueish") : "";
  const output = path.resolve(
    "../docs/plans/visual-quality-evidence/matrix-populated-conditional",
    [theme, mode, accent].join("-"),
  );
  await mkdir(output, { recursive: true });
  const workspace = await createE2EWorkspace({ screenshotCount: 4 });
  let app: AppServer | undefined;
  const records: unknown[] = [];
  const frameEdits: unknown[] = [];
  const capture = async (route: string, state: string, width: number) => {
    await page.setViewportSize({ width, height: 900 });
    const name = `${route}-${state}-${width}`;
    await captureFullContent(page, path.join(output, `${name}.png`));
    const audit = await page.evaluate(auditComposedText);
    const record = {
      route,
      state,
      width,
      screenshot: `${name}.png`,
      heading: await page.getByRole("main").getByRole("heading").first().textContent(),
      root: await page.locator("html").evaluate((element) => ({
        theme: element.getAttribute("data-theme"),
        accent: element.getAttribute("data-accent"),
        mode: element.classList.contains("dark") ? "dark" : "light",
      })),
      overflow: await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
      controls: await page.locator(inventorySurface("main")).evaluateAll(captureControlRecords),
      ...audit,
      interactions: [
        ...(await exerciseVisibleControls(page, output, name)),
        ...(await exerciseDisclosuresAndPressed(page)),
      ],
    };
    expect(record.overflow).toBeLessThanOrEqual(0);
    expect(record.failureCount).toBe(0);
    records.push(record);
  };
  try {
    const sourcePath = await createMultiDVDSourceFixture(workspace);
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, sourcePath);
    await page.evaluate(
      ({ theme, mode, accent }) =>
        localStorage.setItem(
          "upbrr:appearance:v1",
          JSON.stringify({
            version: 1,
            theme,
            mode,
            accents: theme.startsWith("kanagawa") ? { [theme]: accent } : {},
          }),
        ),
      { theme, mode, accent },
    );
    await page.reload();
    await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
    await page.getByRole("checkbox", { name: "BTN" }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await page.getByRole("button", { name: "Run dupe check" }).click();
    await expect(page.getByRole("button", { name: "Screenshots", exact: true })).toBeEnabled({
      timeout: 30_000,
    });
    await page.getByRole("button", { name: "Screenshots", exact: true }).click();
    const frameSummary = page.getByText(/^Frame Selection · [1-9]/);
    await frameSummary.click();
    const firstSeconds = page.getByRole("spinbutton", { name: /shot \d+ seconds$/ }).first();
    const firstFrame = page.getByRole("spinbutton", { name: /shot \d+ frame$/ }).first();
    await expect(firstSeconds).toBeVisible();
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 });
      for (const input of [firstSeconds, firstFrame]) {
        const original = await input.inputValue();
        const changed = String(Number(original) + 1);
        await input.click();
        await expect(input).toBeFocused();
        await input.press("ControlOrMeta+A");
        await page.keyboard.type(changed);
        await expect(input).toHaveValue(changed);
        await input.press("ControlOrMeta+A");
        await page.keyboard.type(original);
        await expect(input).toHaveValue(original);
        frameEdits.push({
          width,
          name: await input.getAttribute("aria-label"),
          original,
          changed,
          pointerFocus: true,
          keyboardChangedAndRestored: true,
        });
      }
      await frameSummary.focus();
      await page.keyboard.press("Tab");
      await expect(firstSeconds).toBeFocused();
      const outline = await firstSeconds.evaluate((element) => ({
        width: getComputedStyle(element).outlineWidth,
        style: getComputedStyle(element).outlineStyle,
      }));
      expect(Number.parseFloat(outline.width)).toBeGreaterThanOrEqual(2);
      expect(outline.style).toBe("solid");
      await capture("screenshots", "frames-expanded", width);
    }
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByAltText("Disc 1 screenshot 1")).toBeVisible();
    await page.getByRole("button", { name: "Upload Images", exact: true }).click();
    await page.getByRole("button", { name: "Prepare required hosts (4)" }).click();
    await expect(page.getByText("4 saved")).toBeVisible();
    for (const width of [1280, 390]) await capture("uploaded-images", "published", width);
    await page.getByRole("button", { name: "Menu Images", exact: true }).click();
    await page.getByRole("button", { name: "Capture DVD menus" }).click();
    await expect(page.getByText("2 captured menu image(s)")).toBeVisible();
    for (const width of [1280, 390]) await capture("menu-images", "captured", width);
    await page.getByRole("button", { name: "Descriptions", exact: true }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    const expand = page.getByRole("button", { name: /^Expand / }).first();
    await expect(expand).toBeVisible();
    await expand.click();
    await expect(
      page.getByRole("textbox", { name: /^Raw description for / }).first(),
    ).toBeVisible();
    for (const width of [1280, 390]) await capture("descriptions", "expanded", width);
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    const faviconOnly = page.getByRole("switch", { name: "Favicon only" });
    await faviconOnly.click();
    await expect(faviconOnly).toHaveAttribute("aria-checked", "true");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await page.getByRole("button", { name: "Input", exact: true }).click();
    await expect(page.locator("details.tracker-dropdown > summary")).toContainText("1/6");
    await page.locator("details.tracker-dropdown > summary").click();
    const inputHDS = page.locator("details.tracker-dropdown [role='checkbox'][aria-label='HDS']");
    await expect(inputHDS).toHaveAttribute("aria-checked", "true");
    for (const width of [1280, 390]) await capture("input", "favicon-only", width);
    await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
    await expect(page.getByRole("checkbox", { name: "HDS" })).toBeChecked();
    for (const width of [1280, 390]) await capture("duplicates", "favicon-only", width);
    await page.route("**/api/app/GetActiveInput", async (route) => {
      const response = await route.fetch();
      const snapshot = await response.json();
      const display = snapshot.current?.release?.display;
      if (display)
        display.TrackerData = [
          {
            Tracker: "HDS",
            TrackerID:
              "synthetic-2048-very-long-tracker-identifier-that-must-remain-readable-on-mobile",
            InfoHash: "ABCDEF0123456789",
            TMDBID: 1001,
            IMDBID: 1234567,
            TVDBID: 0,
            MALID: 0,
            Category: "MOVIE",
            Description: "Synthetic tracker description for icon-only review.",
            DescriptionHTML: "",
            ImageURLs: [],
            Filename: "E2E.Movie.2026.1080p.WEB-DL.DD5.1.H264-UPBRR.mkv",
            Matched: true,
            UpdatedAt: "2026-09-25T00:00:00Z",
          },
        ];
      await route.fulfill({ response, body: JSON.stringify(snapshot) });
    });
    await page.reload();
    await page.getByRole("button", { name: "Tracker Data" }).click();
    await expect(page.getByRole("heading", { name: "Input Metadata" })).toBeVisible();
    await expect(page.locator("main details > summary").first()).toHaveAccessibleName(
      /HDS.*Torrent ID/,
    );
    await page.setViewportSize({ width: 390, height: 900 });
    const trackerIDLine = page.locator("main details > summary > span").last();
    expect(
      await trackerIDLine.evaluate((element) => element.scrollWidth - element.clientWidth),
      "long tracker ID must wrap within its summary at mobile width",
    ).toBeLessThanOrEqual(1);
    for (const width of [1280, 390]) await capture("tracker-data", "favicon-only", width);
    await page.unroute("**/api/app/GetActiveInput");
    await writeFile(path.join(output, "cells.json"), JSON.stringify(records, null, 2));
    await writeFile(path.join(output, "frame-edits.json"), JSON.stringify(frameEdits, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("capture source history and host browser controls", async ({ page }) => {
  test.setTimeout(180_000);
  const theme = process.env.VQ_THEME ?? "minimal";
  const mode = process.env.VQ_MODE ?? "light";
  const accent = theme.startsWith("kanagawa") ? (process.env.VQ_ACCENT ?? "blueish") : "";
  const output = path.resolve(
    "../docs/plans/visual-quality-evidence/matrix-source-browser",
    [theme, mode, accent].join("-"),
  );
  await mkdir(output, { recursive: true });
  const workspace = await createE2EWorkspace();
  const nestedName = "Nested synthetic folder";
  const nestedPath = path.join(path.dirname(workspace.sourcePath), nestedName);
  await mkdir(nestedPath);
  let app: AppServer | undefined;
  const records: unknown[] = [];
  const capture = async (
    state: string,
    width: number,
    selector: string,
    controlLocators: Locator[],
  ) => {
    const screenshot = `input-${state}-${width}.png`;
    await captureFullContent(page, path.join(output, screenshot));
    const audit = await page.evaluate(auditComposedText, selector);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
    expect(overflow, `${state}-${width}: page overflow`).toBeLessThanOrEqual(0);
    expect(audit.failureCount, `${state}-${width}: composed text contrast`).toBe(0);
    const controls = [];
    for (const control of controlLocators)
      controls.push(...(await control.evaluateAll(captureControlRecords)));
    const record = {
      route: "input",
      state,
      width,
      screenshot,
      root: await page.locator("html").evaluate((element) => ({
        theme: element.getAttribute("data-theme"),
        accent: element.getAttribute("data-accent"),
        mode: element.classList.contains("dark") ? "dark" : "light",
      })),
      overflow,
      controls,
      interactions: [] as Array<Record<string, unknown>>,
      ...audit,
    };
    records.push(record);
    return record;
  };
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.evaluate(
      ({ theme, mode, accent }) =>
        localStorage.setItem(
          "upbrr:appearance:v1",
          JSON.stringify({
            version: 1,
            theme,
            mode,
            accents: theme.startsWith("kanagawa") ? { [theme]: accent } : {},
          }),
        ),
      { theme, mode, accent },
    );
    await page.evaluate(
      ({ historyKey, sourcePath, folderPath }) =>
        localStorage.setItem(
          historyKey,
          JSON.stringify([
            { path: sourcePath, mode: "file" },
            { path: folderPath, mode: "folder" },
          ]),
        ),
      {
        historyKey: sourcePathHistoryStorageKey,
        sourcePath: workspace.sourcePath,
        folderPath: path.dirname(workspace.sourcePath),
      },
    );
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(app.url);
      const source = page.getByRole("textbox", { name: "Source path" });
      await expect(source).toHaveValue(workspace.sourcePath);
      await source.click();
      const history = page.getByRole("listbox", { name: "Source path history" });
      await expect(history).toBeVisible();
      const folderPath = path.dirname(workspace.sourcePath);
      const folderOption = history.getByRole("option", { name: folderPath, exact: true });
      const fileOption = history.getByRole("option", { name: workspace.sourcePath, exact: true });
      await expect(folderOption).toBeVisible();
      await expect(fileOption).toBeVisible();
      await folderOption.focus();
      const historyRecord = await capture("source-history", width, "main *", [
        folderOption,
        fileOption,
      ]);
      await page.keyboard.press("Enter");
      await expect(history).toBeHidden();
      await expect(source).toHaveValue(folderPath);
      await source.click();
      await fileOption.click();
      await expect(history).toBeHidden();
      await expect(source).toHaveValue(workspace.sourcePath);
      await source.click();
      await folderOption.click();
      await expect(history).toBeHidden();
      await expect(source).toHaveValue(folderPath);
      await source.click();
      await fileOption.focus();
      await page.keyboard.press("Enter");
      await expect(history).toBeHidden();
      await expect(source).toHaveValue(workspace.sourcePath);
      historyRecord.interactions.push(
        {
          kind: "option",
          name: folderPath,
          keyboard: true,
          pointer: true,
          restoredControlState: true,
        },
        {
          kind: "option",
          name: workspace.sourcePath,
          keyboard: true,
          pointer: true,
          restoredControlState: true,
        },
      );

      await page.getByRole("button", { name: "Browse file" }).click();
      const dialog = page.getByRole("dialog", { name: "Host browser" });
      await expect(dialog).toBeVisible();
      const search = dialog.getByRole("textbox", { name: "Search" });
      await expect(search).toBeEnabled();
      await search.click();
      await expect(search).toBeFocused();
      const outline = await search.evaluate((element) => getComputedStyle(element).outlineWidth);
      expect(
        Number.parseFloat(outline),
        `${theme}-${mode}: browser search focus ring`,
      ).toBeGreaterThanOrEqual(2);
      const fileName = path.basename(workspace.sourcePath);
      const selectFile = dialog.getByRole("button", { name: `Select ${fileName}` });
      await expect(selectFile).toBeVisible();
      const fileRecord = await capture("browser-file", width, "[role='dialog'] *", [
        search,
        selectFile,
      ]);
      await page.keyboard.type("E2E.Movie");
      await expect(search).toHaveValue("E2E.Movie");
      await expect(selectFile).toBeVisible();
      const filteredRecord = await capture("browser-file-filtered", width, "[role='dialog'] *", [
        search,
      ]);
      await selectFile.focus();
      await page.keyboard.press("Enter");
      await expect(dialog).toBeHidden();
      await expect(source).toHaveValue(workspace.sourcePath);
      await page.getByRole("button", { name: "Browse folder" }).click();
      await expect(dialog).toBeVisible();
      const selectFolder = dialog.getByRole("button", { name: "Select folder" });
      await expect(selectFolder).toBeVisible();
      await selectFolder.focus();
      const folderRecord = await capture("browser-folder", width, "[role='dialog'] *", [
        selectFolder,
      ]);
      await page.keyboard.press("Enter");
      await expect(dialog).toBeHidden();
      await expect(source).toHaveValue(folderPath);
      await page.getByRole("button", { name: "Browse file" }).click();
      await expect(search).toHaveValue("");
      await expect(selectFile).toBeVisible();
      await selectFile.click();
      await expect(dialog).toBeHidden();
      await expect(source).toHaveValue(workspace.sourcePath);
      await page.getByRole("button", { name: "Browse folder" }).click();
      await expect(selectFolder).toBeVisible();
      await selectFolder.click();
      await expect(dialog).toBeHidden();
      await expect(source).toHaveValue(folderPath);
      await page.getByRole("button", { name: "Browse file" }).click();
      await expect(selectFile).toBeVisible();
      await selectFile.focus();
      await page.keyboard.press("Enter");
      await expect(dialog).toBeHidden();
      await expect(source).toHaveValue(workspace.sourcePath);
      fileRecord.interactions.push(
        {
          kind: "input",
          name: "Search",
          keyboard: true,
          pointer: true,
          restoredControlState: true,
        },
        {
          kind: "button",
          name: `Select ${fileName}`,
          keyboard: true,
          pointer: true,
          restoredControlState: true,
        },
      );
      filteredRecord.interactions.push({
        kind: "input",
        name: "Search",
        keyboard: true,
        pointer: true,
        restoredControlState: true,
      });
      folderRecord.interactions.push({
        kind: "button",
        name: "Select folder",
        keyboard: true,
        pointer: true,
        restoredControlState: true,
      });
      await page.getByRole("button", { name: "Browse folder" }).click();
      await expect(dialog).toBeVisible();
      const openNested = dialog.getByRole("button", { name: `Open ${nestedName}` });
      const location = dialog.locator(".host-browser-path");
      await expect(openNested).toBeVisible();
      const openRecord = await capture("browser-directory-choice", width, "[role='dialog'] *", [
        openNested,
      ]);
      await openNested.click();
      await expect(location).toHaveText(nestedPath);
      const up = dialog.getByRole("button", { name: "Up" });
      const roots = dialog.getByRole("button", { name: "Roots" });
      const close = dialog.getByRole("button", { name: "Close" });
      const nestedRecord = await capture("browser-directory-open", width, "[role='dialog'] *", [
        up,
        roots,
      ]);
      await up.focus();
      await page.keyboard.press("Enter");
      await expect(location).toHaveText(folderPath);
      await openNested.focus();
      await page.keyboard.press("Enter");
      await expect(location).toHaveText(nestedPath);
      await up.click();
      await expect(location).toHaveText(folderPath);
      openRecord.interactions.push({
        kind: "button",
        name: `Open ${nestedName}`,
        keyboard: true,
        pointer: true,
        restoredControlState: true,
      });
      nestedRecord.interactions.push({
        kind: "button",
        name: "Up",
        keyboard: true,
        pointer: true,
        restoredControlState: true,
      });
      await roots.click();
      await expect(location).toHaveText("Computer");
      const rootsRecord = await capture("browser-roots", width, "[role='dialog'] *", [close]);
      await page.keyboard.press("Escape");
      await expect(dialog).toBeHidden();
      await page.getByRole("button", { name: "Browse folder" }).click();
      await expect(dialog).toBeVisible();
      await expect(roots).toBeEnabled();
      await roots.press("Enter");
      await expect(location).toHaveText("Computer");
      await close.click();
      await expect(dialog).toBeHidden();
      nestedRecord.interactions.push({
        kind: "button",
        name: "Roots",
        keyboard: true,
        pointer: true,
        restoredControlState: true,
      });
      await page.getByRole("button", { name: "Browse folder" }).click();
      await close.focus();
      await page.keyboard.press("Enter");
      await expect(dialog).toBeHidden();
      await page.getByRole("button", { name: "Browse folder" }).click();
      await page.locator(".host-browser-overlay").click({ position: { x: 2, y: 2 } });
      await expect(dialog).toBeHidden();
      rootsRecord.interactions.push(
        {
          kind: "button",
          name: "Close",
          keyboard: true,
          pointer: true,
          restoredControlState: true,
        },
        {
          kind: "dialog-dismissal",
          name: "Escape and outside click",
          keyboard: true,
          pointer: true,
          restoredControlState: true,
        },
      );
      await expect
        .poll(() =>
          page.evaluate(
            (key) => JSON.parse(localStorage.getItem(key) || "[]"),
            sourcePathHistoryStorageKey,
          ),
        )
        .toEqual(
          expect.arrayContaining([
            { path: workspace.sourcePath, mode: "file" },
            { path: folderPath, mode: "folder" },
          ]),
        );
      await page.reload();
      await expect(source).toHaveValue(workspace.sourcePath);
      await source.click();
      await expect(folderOption).toBeVisible();
      await expect(fileOption).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(history).toBeHidden();
    }
    await writeFile(path.join(output, "cells.json"), JSON.stringify(records, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("inspect populated routes at a 200 percent zoom-equivalent width", async ({ page }) => {
  test.setTimeout(240_000);
  const output = path.resolve("../docs/plans/visual-quality-evidence/zoom-200");
  await mkdir(output, { recursive: true });
  const records: unknown[] = [];
  for (const theme of ["minimal", "swizzin"]) {
    for (const mode of ["light", "dark"]) {
      const workspace = await createE2EWorkspace({ screenshotCount: 2, preparedMediaInfo: true });
      let app: AppServer | undefined;
      try {
        app = await startApp(workspace);
        await page.goto(app.url);
        await page.evaluate(() => {
          localStorage.clear();
          sessionStorage.clear();
        });
        await fetchMetadata(page, app.url, workspace.sourcePath);
        await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
        await page.getByRole("checkbox", { name: "BTN" }).uncheck();
        await page.getByRole("checkbox", { name: "HDS" }).check();
        await page.getByRole("button", { name: "Run dupe check" }).click();
        await expect(page.getByRole("button", { name: "Screenshots", exact: true })).toBeEnabled({
          timeout: 30_000,
        });
        await page.getByRole("button", { name: "Screenshots", exact: true }).click();
        await page.getByRole("button", { name: "Generate screenshots" }).click();
        await expect(page.getByAltText("Screenshot 1")).toBeVisible();
        await page.getByRole("button", { name: "Descriptions", exact: true }).click();
        await page.getByRole("button", { name: "Refresh descriptions" }).click();
        await page.getByRole("button", { name: "Expand" }).first().waitFor();
        await page.setViewportSize({ width: 640, height: 900 });
        await page.evaluate(
          ({ theme, mode }) =>
            localStorage.setItem(
              "upbrr:appearance:v1",
              JSON.stringify({ version: 1, theme, mode, accents: {} }),
            ),
          { theme, mode },
        );
        for (const route of Object.keys(populatedRouteHeadings)) {
          await page.goto(new URL(route, app.url).toString());
          await waitForPopulatedRoute(page, route);
          const overflow = await page.evaluate(
            () => document.documentElement.scrollWidth - innerWidth,
          );
          expect(overflow, `${theme} ${mode} ${route} at 640 CSS px`).toBeLessThanOrEqual(0);
          const screenshot = `${theme}-${mode}-${route}-640.png`;
          await page.screenshot({ path: path.join(output, screenshot), animations: "disabled" });
          records.push({ theme, mode, route, width: 640, overflow, screenshot });
        }
      } finally {
        await app?.stop();
        await workspace.cleanup();
      }
    }
  }
  await writeFile(path.join(output, "cells.json"), JSON.stringify(records, null, 2));
});

test("inspect Qui demo reference controls", async ({ page }) => {
  const output = path.resolve("../docs/plans/visual-quality-evidence/qui-reference");
  await mkdir(output, { recursive: true });
  await page.goto("http://127.0.0.1:4174/demo/");
  await page.getByText("torrents loaded").first().waitFor();
  const controls = await page
    .locator("button, select, [role='combobox'], [role='switch'], [role='checkbox']")
    .evaluateAll((els) =>
      els
        .map((el) => ({
          tag: el.tagName.toLowerCase(),
          role: el.getAttribute("role"),
          ariaLabel: el.getAttribute("aria-label"),
          title: el.getAttribute("title"),
          text: el.textContent?.trim().slice(0, 80),
        }))
        .filter((item) => item.ariaLabel || item.title || item.text),
    );
  await writeFile(path.join(output, "controls.json"), JSON.stringify(controls, null, 2));
  await page.screenshot({ path: path.join(output, "light-list.png") });
  await page.getByRole("button", { name: "Instance settings" }).click();
  await page.getByText("Instance Configuration").waitFor();
  await page.waitForTimeout(250);
  await page.screenshot({ path: path.join(output, "light-settings-entry.png") });
  const select = page.getByRole("combobox").first();
  if (await select.count()) {
    await select.click();
    await page.screenshot({ path: path.join(output, "light-select-open.png") });
    await page.keyboard.press("Escape");
  }
  await page.evaluate(() => {
    localStorage.setItem("theme", "dark");
    localStorage.setItem("qui-demo-theme", "dark");
  });
  await page.reload();
  await page.getByText("torrents loaded").first().waitFor();
  await page.getByRole("button", { name: "Instance settings" }).click();
  await page.getByText("Instance Configuration").waitFor();
  await page.waitForTimeout(250);
  await page.screenshot({ path: path.join(output, "dark-settings-entry.png") });
});

test("automatic appearance follows OS preference and Napster stays light", async ({ page }) => {
  const output = path.resolve("../docs/plans/visual-quality-evidence/auto-os");
  await mkdir(output, { recursive: true });
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  const records: unknown[] = [];
  try {
    app = await startApp(workspace);
    await page.goto(app.url);
    for (const colorScheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme });
      await page.evaluate(() =>
        localStorage.setItem(
          "upbrr:appearance:v1",
          JSON.stringify({ version: 1, theme: "minimal", mode: "auto", accents: {} }),
        ),
      );
      await page.reload();
      await expect(page.locator("html")).toHaveAttribute("data-theme", "minimal");
      await expect
        .poll(() => page.locator("html").evaluate((el) => getComputedStyle(el).colorScheme))
        .toBe(colorScheme);
      const effective = await page.locator("html").evaluate((el) => ({
        dark: el.classList.contains("dark"),
        colorScheme: getComputedStyle(el).colorScheme,
      }));
      expect(effective.dark).toBe(colorScheme === "dark");
      await captureFullContent(page, path.join(output, `minimal-auto-os-${colorScheme}.png`));
      records.push({ theme: "minimal", requestedMode: "auto", os: colorScheme, effective });
    }
    await page.evaluate(() =>
      localStorage.setItem(
        "upbrr:appearance:v1",
        JSON.stringify({ version: 1, theme: "napster", mode: "auto", accents: {} }),
      ),
    );
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "napster");
    await expect
      .poll(() => page.locator("html").evaluate((el) => getComputedStyle(el).colorScheme))
      .toBe("light");
    await captureFullContent(page, path.join(output, "napster-auto-os-dark.png"));
    records.push({
      theme: "napster",
      requestedMode: "auto",
      os: "dark",
      effective: await page.locator("html").evaluate((el) => ({
        dark: el.classList.contains("dark"),
        colorScheme: getComputedStyle(el).colorScheme,
      })),
    });
    await page.goto(new URL("settings", app.url).toString());
    await page
      .getByRole("navigation", { name: "Settings sections" })
      .getByRole("button", { name: "Appearance" })
      .click();
    await page.getByRole("radio", { name: "Minimal" }).locator("..").click();
    await expect
      .poll(() => page.locator("html").evaluate((el) => getComputedStyle(el).colorScheme))
      .toBe("dark");
    expect(
      await page.evaluate(
        () => JSON.parse(localStorage.getItem("upbrr:appearance:v1") || "{}").mode,
      ),
    ).toBe("auto");
    records.push({
      theme: "minimal",
      requestedMode: "auto",
      os: "dark",
      restoredAfterNapster: true,
    });
    await writeFile(path.join(output, "cells.json"), JSON.stringify(records, null, 2));
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});
