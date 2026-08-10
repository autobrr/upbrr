// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { spawn, type ChildProcess } from "node:child_process";
import { access, mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { expect, type Page } from "@playwright/test";

const here = path.dirname(fileURLToPath(import.meta.url));
export const repoRoot = path.resolve(here, "../../..");
export const e2eBinary = path.join(
  repoRoot,
  "dist",
  process.platform === "win32" ? "upbrr-e2e.exe" : "upbrr-e2e",
);

/** Shared semantic fixture used to compare CLI, embedded WebUI, and HTTP-only behavior. */
export const releaseWorkflowParityFixture = {
  trackerID: "BTN",
  releaseDisplayName: "E2E.Movie.2026.1080p.WEB-DL",
  expectedTrackerUploads: 1,
  expectedClientSearches: 1,
} as const;

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
  clientSearches: number;
  clientInjections: number;
};

type FakeServer = {
  url: string;
  counters: FakeCounters;
  delayTrackerUploads: (delayMs: number) => void;
  delayClientInjections: (delayMs: number) => void;
  close: () => Promise<void>;
};

export type E2EWorkspace = {
  root: string;
  configPath: string;
  dbPath: string;
  authCounterPath: string;
  sourcePath: string;
  screenshotPath: string;
  fake: FakeServer;
  env: NodeJS.ProcessEnv;
  cleanup: () => Promise<void>;
};

export type AppServer = {
  url: string;
  output: () => string;
  stop: () => Promise<void>;
  /** Kills the child without graceful shutdown to exercise restart recovery. */
  crash: () => Promise<void>;
};

type StartAppOptions = {
  /** External base path passed to `upbrr serve --base-url`; empty or "/" uses root mode. */
  baseURL?: string;
  /** Set false when restarting the same persisted workflow database. */
  seed?: boolean;
};

type E2EWorkspaceOptions = {
  screenshotCount?: number;
  /** Selects the fake metadata and source-name shape; defaults to a movie. */
  mediaKind?: "movie" | "tv";
};

/** Creates an isolated E2E workspace with temp config, selected media fixture, and fake services. */
export async function createE2EWorkspace(options: E2EWorkspaceOptions = {}): Promise<E2EWorkspace> {
  const root = await mkdtemp(path.join(tmpdir(), "upbrr-e2e-"));
  const mediaDir = path.join(root, "media");
  await mkdir(mediaDir, { recursive: true });
  const mediaKind = options.mediaKind ?? "movie";
  const sourceName =
    mediaKind === "tv"
      ? "E2E.Show.2026.S01E01.1080p.WEB-DL.DD5.1.H264-UPBRR.mkv"
      : "E2E.Movie.2026.1080p.WEB-DL.DD5.1.H264-UPBRR.mkv";
  const sourcePath = path.join(mediaDir, sourceName);
  const screenshotPath = path.join(mediaDir, "shot-01.png");
  const dbPath = path.join(root, "upbrr-e2e.db");
  const authCounterPath = path.join(root, "auth-counters.json");
  const configPath = path.join(root, "config.yaml");
  await writeFile(sourcePath, "e2e media fixture\n");
  await writeFile(screenshotPath, png1x1);
  const fake = await startFakeServer();
  await writeFile(configPath, buildConfig(dbPath, options.screenshotCount));
  const env = {
    ...process.env,
    UPBRR_E2E_FAKE_SERVICES: "1",
    UPBRR_E2E_TRACKER_URL: fake.url,
    UPBRR_E2E_IMAGE_URL: fake.url,
    UPBRR_E2E_CLIENT_URL: fake.url,
    UPBRR_E2E_SCREENSHOT_PATH: screenshotPath,
    UPBRR_E2E_AUTH_COUNTER_PATH: authCounterPath,
    UPBRR_E2E_MEDIA_KIND: mediaKind,
  };
  return {
    root,
    configPath,
    dbPath,
    authCounterPath,
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

export type E2EAuthCounters = Readonly<{
  capabilityCalls: number;
  validationCalls: number;
  loginAttempts: number;
  validations: Readonly<Record<string, number>>;
}>;

/** Reads deterministic in-process auth fake counters without tracker network I/O. */
export async function readE2EAuthCounters(workspace: E2EWorkspace): Promise<E2EAuthCounters> {
  try {
    return JSON.parse(await readFile(workspace.authCounterPath, "utf8")) as E2EAuthCounters;
  } catch {
    return {
      capabilityCalls: 0,
      validationCalls: 0,
      loginAttempts: 0,
      validations: {},
    };
  }
}

/** Adds a minimal parseable BDMV source without invoking any external disc tool. */
export async function createBluraySourceFixture(workspace: E2EWorkspace): Promise<string> {
  const discRoot = path.join(workspace.root, "media", "Example Disc");
  const bdmvRoot = path.join(discRoot, "BDMV");
  const playlistDir = path.join(bdmvRoot, "PLAYLIST");
  const streamDir = path.join(bdmvRoot, "STREAM");
  await mkdir(playlistDir, { recursive: true });
  await mkdir(streamDir, { recursive: true });

  const playlist = Buffer.alloc(64);
  playlist.write("MPLS", 0, "ascii");
  playlist.write("0200", 4, "ascii");
  playlist.writeUInt32BE(32, 8);
  playlist.writeUInt32BE(64, 12);
  playlist.writeUInt32BE(0, 16);
  playlist.writeUInt32BE(30, 32);
  playlist.writeUInt16BE(1, 38);
  playlist.writeUInt16BE(0, 40);
  playlist.writeUInt16BE(20, 42);
  playlist.write("00001", 44, "ascii");
  playlist.write("M2TS", 49, "ascii");
  playlist.writeUInt32BE(0, 56);
  playlist.writeUInt32BE(45_000 * 120, 60);
  await writeFile(path.join(playlistDir, "00001.mpls"), playlist);
  await writeFile(path.join(streamDir, "00001.m2ts"), "synthetic Blu-ray stream\n");
  return discRoot;
}

/** Adds a minimal DVD folder layout without invoking external disc tooling. */
export async function createDVDSourceFixture(workspace: E2EWorkspace): Promise<string> {
  const discRoot = path.join(workspace.root, "media", "Example DVD");
  const videoRoot = path.join(discRoot, "VIDEO_TS");
  await mkdir(videoRoot, { recursive: true });
  await writeFile(path.join(videoRoot, "VIDEO_TS.IFO"), "synthetic DVD control data\n");
  await writeFile(path.join(videoRoot, "VTS_01_1.VOB"), "synthetic DVD video data\n");
  return discRoot;
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
  if (options.seed !== false) await seedConfigDatabase(workspace);
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
    output: () => output.join(""),
    stop: async () => {
      await stopProcess(child);
    },
    crash: async () => {
      await crashProcess(child);
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
  await ensureE2EWebAuth(workspace);
}

async function ensureE2EWebAuth(workspace: E2EWorkspace) {
  try {
    await access(path.join(workspace.root, "web-auth.json"));
    return;
  } catch {
    // Create isolated synthetic browser auth material used by persistent API keys.
  }
  const result = await runProcess(
    e2eBinary,
    ["--create-auth", "--config", workspace.configPath],
    workspace.env,
    "e2e-user\nsynthetic-e2e-password\nsynthetic-e2e-password\n",
  );
  if (result.code !== 0) {
    throw new Error(`failed to create e2e web auth:\n${result.output}`);
  }
}

/** Generates one persistent API token through the public CLI without exposing it in diagnostics. */
export async function createE2EAPIToken(
  workspace: E2EWorkspace,
  owner = "e2e-client",
): Promise<string> {
  const result = await runProcess(
    e2eBinary,
    [
      "api-token",
      "create",
      "--config",
      workspace.configPath,
      "--name",
      `E2E ${owner}`,
      "--owner",
      owner,
      "--scopes",
      "workflow:read,workflow:write,workflow:execute",
    ],
    workspace.env,
  );
  if (result.code !== 0) {
    throw new Error(`failed to generate e2e API credential:\n${result.output}`);
  }
  const match = /^Token:\s*(\S+)$/m.exec(result.output);
  if (!match?.[1]) {
    throw new Error("API credential command did not return a one-time value");
  }
  return match[1];
}

export async function fetchMetadata(page: Page, appUrl: string, sourcePath: string) {
  await page.goto(appUrl);
  await expect(page.getByRole("heading", { name: "Build Release Name" })).toBeVisible();
  await page.getByLabel("Source path").fill(sourcePath);
  await page.getByRole("button", { name: "Fetch metadata" }).click();
  await expect(page.getByText(releaseWorkflowParityFixture.releaseDisplayName)).toBeVisible();
  await page.getByText("Select Trackers").click();
  await expect(page.getByText("BTN").first()).toBeVisible();
  await page.keyboard.press("Escape");
}

function buildConfig(dbPath: string, screenshotCount = 1): string {
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
  skip_auto_torrent: false
  keep_images: true
screenshot_handling:
  screens: ${Math.max(1, Math.trunc(screenshotCount))}
  min_successful_image_uploads: 1
  cutoff_screens: 1
post_upload:
  max_concurrent_tracker_uploads: 1
logging:
  level: "debug"
  file_enabled: false
trackers:
  default_trackers: ["${releaseWorkflowParityFixture.trackerID}"]
  AITHER:
    api_key: "e2e"
    image_host: "imgbb"
  ANT:
    api_key: "e2e"
    image_host: "imgbb"
  FF:
    username: "e2e"
    password: "e2e"
  HDS:
    announce_url: "http://tracker.invalid/announce"
    image_host: "imgbb"
  PTP:
    username: "e2e"
    password: "e2e"
    image_host: "imgbb"
  BTN:
    api_key: "e2e"
    username: "e2e"
    password: "e2e"
    url: "http://127.0.0.1"
    image_host: "imgbb"
  OLD:
    api_key: "preserved"
    url: "http://retired.invalid"
torrent_clients: {}
`;
}

async function startFakeServer(): Promise<FakeServer> {
  const counters: FakeCounters = {
    trackerUploads: 0,
    imageUploads: 0,
    clientSearches: 0,
    clientInjections: 0,
  };
  let trackerUploadDelayMs = 0;
  let clientInjectionDelayMs = 0;
  const server = createServer(async (req, res) => {
    if (req.method === "POST" && req.url === "/client-search") {
      counters.clientSearches++;
      writeJSON(res, 200, { ok: true });
      return;
    }
    if (req.method === "POST" && req.url === "/client-inject") {
      counters.clientInjections++;
      await delay(clientInjectionDelayMs);
      writeJSON(res, 200, { ok: true });
      return;
    }
    if (req.method === "POST" && req.url === "/upload") {
      const body = await readBody(req);
      if (body.includes(Buffer.from('name="tracker"'))) {
        counters.trackerUploads++;
        await delay(trackerUploadDelayMs);
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
    delayTrackerUploads: (delayMs) => {
      trackerUploadDelayMs = Math.max(0, delayMs);
    },
    delayClientInjections: (delayMs) => {
      clientInjectionDelayMs = Math.max(0, delayMs);
    },
    close: () => closeServer(server),
  };
}

function delay(delayMs: number): Promise<void> {
  return delayMs > 0 ? new Promise((resolve) => setTimeout(resolve, delayMs)) : Promise.resolve();
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

function crashProcess(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null) {
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    child.once("exit", () => resolve());
    child.kill("SIGKILL");
  });
}

function runProcess(
  command: string,
  args: string[],
  env: NodeJS.ProcessEnv,
  input?: string,
): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve) => {
    const child = spawn(command, args, {
      cwd: repoRoot,
      env,
      stdio: [input === undefined ? "ignore" : "pipe", "pipe", "pipe"],
      windowsHide: true,
    });
    const output: string[] = [];
    child.stdout?.on("data", (chunk) => output.push(String(chunk)));
    child.stderr?.on("data", (chunk) => output.push(String(chunk)));
    child.on("close", (code) => resolve({ code, output: output.join("") }));
    if (input !== undefined) child.stdin?.end(input);
  });
}
