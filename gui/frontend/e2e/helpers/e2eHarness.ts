// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { expect, type Page } from "@playwright/test";

const here = path.dirname(fileURLToPath(import.meta.url));
export const repoRoot = path.resolve(here, "../../../..");
export const e2eBinary = path.join(
  repoRoot,
  "dist",
  process.platform === "win32" ? "upbrr-e2e.exe" : "upbrr-e2e",
);

const png1x1 = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=",
  "base64",
);
const e2eTorrentFixture =
  "d8:announce13:http://e2e.ee4:infod6:lengthi0e4:name8:test.txt12:piece lengthi16384e6:pieces0:ee";
const startAppBindAttempts = 5;

type FakeCounters = {
  trackerUploads: number;
  imageUploads: number;
};

type FakeServer = {
  url: string;
  counters: FakeCounters;
  close: () => Promise<void>;
};

export type E2EWorkspace = {
  root: string;
  configPath: string;
  dbPath: string;
  sourcePath: string;
  screenshotPath: string;
  fake: FakeServer;
  env: NodeJS.ProcessEnv;
  cleanup: () => Promise<void>;
};

export type AppServer = {
  url: string;
  stop: () => Promise<void>;
};

type StartAppOptions = {
  /** External base path passed to `upbrr serve --base-url`; empty or "/" uses root mode. */
  baseURL?: string;
};

/** Creates an isolated E2E workspace with temp config, media fixtures, and fake services. */
export async function createE2EWorkspace(): Promise<E2EWorkspace> {
  const root = await mkdtemp(path.join(tmpdir(), "upbrr-e2e-"));
  const mediaDir = path.join(root, "media");
  await mkdir(mediaDir, { recursive: true });
  const sourcePath = path.join(mediaDir, "E2E.Movie.2026.1080p.WEB-DL.DD5.1.H264-UPBRR.mkv");
  const screenshotPath = path.join(mediaDir, "shot-01.png");
  const dbPath = path.join(root, "upbrr-e2e.db");
  const configPath = path.join(root, "config.yaml");
  await writeFile(sourcePath, "e2e media fixture\n");
  await writeFile(screenshotPath, png1x1);
  const fake = await startFakeServer();
  await writeFile(configPath, buildConfig(dbPath));
  const env = {
    ...process.env,
    UPBRR_E2E_FAKE_SERVICES: "1",
    UPBRR_E2E_TRACKER_URL: fake.url,
    UPBRR_E2E_IMAGE_URL: fake.url,
    UPBRR_E2E_SCREENSHOT_PATH: screenshotPath,
  };
  return {
    root,
    configPath,
    dbPath,
    sourcePath,
    screenshotPath,
    fake,
    env,
    cleanup: async () => {
      await fake.close();
      await rm(root, { recursive: true, force: true });
    },
  };
}

/**
 * Starts the embedded web server for a workspace and waits for auth status at
 * the configured base path before returning its browser URL. Startup retries
 * address-in-use failures because the reserved port is released before the
 * child process can bind it.
 */
export async function startApp(
  workspace: E2EWorkspace,
  options: StartAppOptions = {},
): Promise<AppServer> {
  await seedConfigDatabase(workspace);
  for (let attempt = 1; attempt <= startAppBindAttempts; attempt++) {
    try {
      return await startAppOnce(workspace, options);
    } catch (error) {
      if (attempt === startAppBindAttempts || !isAddressInUseStartupError(error)) {
        throw error;
      }
    }
  }
  throw new Error("server did not start");
}

async function startAppOnce(
  workspace: E2EWorkspace,
  options: StartAppOptions = {},
): Promise<AppServer> {
  const port = await reserveLoopbackPort();
  const origin = `http://127.0.0.1:${port}`;
  const basePath = normalizeBasePath(options.baseURL);
  const args = [
    "serve",
    "--config",
    workspace.configPath,
    "--host",
    "127.0.0.1",
    "--port",
    String(port),
    "--dev-no-auth",
  ];
  if (basePath) {
    args.push("--base-url", basePath);
  }
  const child = spawn(e2eBinary, args, {
    cwd: repoRoot,
    env: workspace.env,
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  });
  const output: string[] = [];
  child.stdout?.on("data", (chunk) => output.push(String(chunk)));
  child.stderr?.on("data", (chunk) => output.push(String(chunk)));
  try {
    await waitForHTTP(`${origin}${basePath}/api/auth/status`, child, output);
  } catch (error) {
    await stopProcess(child);
    throw error;
  }
  return {
    url: `${origin}${basePath ? `${basePath}/` : "/"}`,
    stop: async () => {
      await stopProcess(child);
    },
  };
}

function isAddressInUseStartupError(error: unknown): boolean {
  if (!(error instanceof Error)) {
    return false;
  }
  return /address already in use|only one usage of each socket address|EADDRINUSE/i.test(
    error.message,
  );
}

function normalizeBasePath(value: string | undefined): string {
  const trimmed = value?.trim() ?? "";
  if (!trimmed || trimmed === "/") {
    return "";
  }
  const prefixed = trimmed.startsWith("/") ? trimmed : `/${trimmed}`;
  return prefixed.endsWith("/") ? prefixed.slice(0, -1) : prefixed;
}

async function seedConfigDatabase(workspace: E2EWorkspace) {
  const result = await runProcess(
    e2eBinary,
    ["--config", workspace.configPath, "--cleanup"],
    workspace.env,
  );
  if (result.code !== 0) {
    throw new Error(`failed to seed e2e config DB:\n${result.output}`);
  }
}

export async function fetchMetadata(page: Page, appUrl: string, sourcePath: string) {
  await page.goto(appUrl);
  await expect(page.getByRole("heading", { name: "Build Release Name" })).toBeVisible();
  await page.getByLabel("Source path").fill(sourcePath);
  await page.getByRole("button", { name: "Fetch metadata" }).click();
  await expect(page.getByText("E2E.Movie.2026.1080p.WEB-DL")).toBeVisible();
  await page.getByText("Select Trackers").click();
  await expect(page.getByText("BTN").first()).toBeVisible();
  await page.keyboard.press("Escape");
}

function buildConfig(dbPath: string): string {
  const yamlPath = dbPath.replaceAll("\\", "\\\\");
  return `main_settings:
  tmdb_api: "e2e"
  tracker_pass_checks: 1
  input_history_limit: 20
  db_path: "${yamlPath}"
image_hosting:
  img_host_1: "imgbb"
  imgbb_api: "e2e"
metadata:
  skip_auto_torrent: true
  keep_images: true
screenshot_handling:
  screens: 1
  min_successful_image_uploads: 1
  cutoff_screens: 1
post_upload:
  max_concurrent_tracker_uploads: 1
logging:
  level: "debug"
  file_enabled: false
trackers:
  default_trackers: ["BTN"]
  preferred_tracker: "BTN"
  BTN:
    api_key: "e2e"
    username: "e2e"
    password: "e2e"
    url: "http://127.0.0.1"
    image_host: "imgbb"
torrent_clients: {}
`;
}

async function startFakeServer(): Promise<FakeServer> {
  const counters: FakeCounters = { trackerUploads: 0, imageUploads: 0 };
  const server = createServer(async (req, res) => {
    if (req.method === "POST" && req.url === "/upload") {
      const body = await readBody(req);
      if (body.includes(Buffer.from('name="tracker"'))) {
        counters.trackerUploads++;
      } else {
        counters.imageUploads++;
      }
      writeJSON(res, 200, { ok: true });
      return;
    }
    if (req.method === "GET" && req.url?.startsWith("/download/")) {
      res.writeHead(200, { "Content-Type": "application/x-bittorrent" });
      res.end(e2eTorrentFixture);
      return;
    }
    writeJSON(res, 404, { error: "not found" });
  });
  await listen(server);
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("fake server did not bind to a TCP port");
  }
  return {
    url: `http://127.0.0.1:${address.port}`,
    counters,
    close: () => closeServer(server),
  };
}

async function reserveLoopbackPort(): Promise<number> {
  const server = createServer();
  await listen(server);
  const address = server.address();
  await closeServer(server);
  if (!address || typeof address === "string") {
    throw new Error("failed to reserve a TCP port");
  }
  return address.port;
}

function listen(server: Server): Promise<void> {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      server.off("error", reject);
      resolve();
    });
  });
}

function closeServer(server: Server): Promise<void> {
  return new Promise((resolve, reject) => {
    server.close((error) => (error ? reject(error) : resolve()));
  });
}

function readBody(req: IncomingMessage): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    req.on("data", (chunk) => chunks.push(Buffer.from(chunk)));
    req.on("end", () => resolve(Buffer.concat(chunks)));
    req.on("error", reject);
  });
}

function writeJSON(res: ServerResponse, status: number, payload: unknown) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(payload));
}

async function waitForHTTP(url: string, child: ChildProcess, output: string[]) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`server exited with ${child.exitCode}:\n${output.join("")}`);
    }
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {
      // Retry until the server is ready or the deadline expires.
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`server did not become ready:\n${output.join("")}`);
}

function stopProcess(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null) {
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      resolve();
    }, 5_000);
    child.once("exit", () => {
      clearTimeout(timer);
      resolve();
    });
    child.kill("SIGTERM");
  });
}

function runProcess(
  command: string,
  args: string[],
  env: NodeJS.ProcessEnv,
): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve) => {
    const child = spawn(command, args, {
      cwd: repoRoot,
      env,
      stdio: ["ignore", "pipe", "pipe"],
      windowsHide: true,
    });
    const output: string[] = [];
    child.stdout?.on("data", (chunk) => output.push(String(chunk)));
    child.stderr?.on("data", (chunk) => output.push(String(chunk)));
    child.on("close", (code) => resolve({ code, output: output.join("") }));
  });
}
