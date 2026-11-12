// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { spawn } from "node:child_process";
import { expect, test } from "@playwright/test";
import type { HistoryOverview } from "../src/types";
import {
  createE2EWorkspace,
  createMultiBluraySourceFixture,
  e2eBinary,
  expectSingleCollectionTorrentUpload,
  readE2EAuthCounters,
  releaseWorkflowParityFixture,
  repoRoot,
  startApp,
} from "./helpers/e2eHarness";

test("CLI full upload approves trackers before downstream work", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  try {
    const result = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--no-seed",
        "--unattended_confirm",
        workspace.sourcePath,
      ],
      workspace.env,
      "y\ny\n",
    );
    expect(result.code, result.output).toBe(0);
    expect(result.output).toContain("Duplicate check:");
    expect(result.output).toMatch(/Use .* as .*\? \[Y\/n\]:/);
    expect(result.output).toMatch(/uploaded|complete|Upload/i);
    expect(workspace.fake.counters.trackerUploads).toBe(
      releaseWorkflowParityFixture.expectedTrackerUploads,
    );
    expect(workspace.fake.counters.clientSearches).toBe(
      releaseWorkflowParityFixture.expectedClientSearches,
    );
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await workspace.cleanup();
  }
});

for (const flags of [
  ["--season", "2"],
  ["--episode", "5"],
  ["--season", "5", "--episode", "5"],
]) {
  test(`CLI accepts plain episode coordinates: ${flags.join(" ")}`, async () => {
    const workspace = await createE2EWorkspace({ mediaKind: "tv" });
    try {
      const result = await runCLI(
        [
          "--config",
          workspace.configPath,
          "--input-only",
          "--unattended",
          ...flags,
          workspace.sourcePath,
        ],
        workspace.env,
      );
      expect(result.code, result.output).toBe(0);
      expect(workspace.fake.counters.trackerUploads).toBe(0);
      expect(workspace.fake.counters.clientInjections).toBe(0);
    } finally {
      await workspace.cleanup();
    }
  });
}

test("CLI uploads one collection torrent for two BDMV discs", async () => {
  const workspace = await createE2EWorkspace({ useLargestPlaylist: true, mediaKind: "tv" });
  try {
    const sourcePath = await createMultiBluraySourceFixture(workspace);
    const result = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--no-seed",
        "--unattended_confirm",
        sourcePath,
      ],
      workspace.env,
      "y\ny\n",
    );
    expect(result.code, result.output).toBe(0);
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expectSingleCollectionTorrentUpload(workspace, "Example BDMV Collection", [
      "Disc 1",
      "Disc 2",
      "BDMV",
      "00001.mpls",
    ]);
  } finally {
    await workspace.cleanup();
  }
});

test("CLI skips an auth-blocked tracker while uploading a ready sibling", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  workspace.env.UPBRR_E2E_AUTH_SCENARIOS = "HDS=validation_only_missing_cookies";
  try {
    const result = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        `${releaseWorkflowParityFixture.trackerID},HDS`,
        "--no-seed",
        "--unattended_confirm",
        workspace.sourcePath,
      ],
      workspace.env,
      "y\ny\n",
    );
    expect(result.code, result.output).toBe(0);
    expect(result.output).not.toContain("Skipping HDS before dupe check");
    expect(result.output).not.toContain("tracker authentication remains incomplete");
    expect(result.output).not.toMatch(
      /(?:login|two-factor|2fa|cookie file)[^\r\n]*(?:\?\s*$|:\s*$)/im,
    );
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expect(workspace.fake.counters.clientSearches).toBe(
      releaseWorkflowParityFixture.expectedClientSearches,
    );
    expect(workspace.fake.counters.clientInjections).toBe(0);
    expect(await readE2EAuthCounters(workspace)).toEqual({
      capabilityCalls: 1,
      validationCalls: 1,
      loginAttempts: 0,
      validations: { BTN: 1, HDS: 1 },
    });
  } finally {
    await workspace.cleanup();
  }
});

test("CLI strict unattended approves a clear tracker without input", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  try {
    const result = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--no-seed",
        "--unattended",
        workspace.sourcePath,
      ],
      workspace.env,
    );
    expect(result.code, result.output).toBe(0);
    expect(result.output).not.toMatch(/\[[yY]\/[nN]\]/);
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await workspace.cleanup();
  }
});

for (const mode of ["interactive", "confirmation", "unattended"] as const) {
  test(`CLI ${mode} skips duplicates and uploads the clear sibling`, async () => {
    const workspace = await createE2EWorkspace({ mediaKind: "tv" });
    workspace.env.UPBRR_E2E_DUPLICATE_TRACKERS = "HDS";
    try {
      const modeArgs = mode === "interactive" ? [] : [mode === "confirmation" ? "-uac" : "-ua"];
      const result = await runCLI(
        [
          "--config",
          workspace.configPath,
          "--trackers",
          "BTN,HDS",
          "--no-seed",
          "-sda",
          ...modeArgs,
          workspace.sourcePath,
        ],
        workspace.env,
        mode === "unattended" ? "" : "y\n\n",
      );
      expect(result.code, result.output).toBe(0);
      expect(result.output).not.toContain("despite duplicate evidence?");
      expect(result.output).not.toContain("Use HDS");
      expect(workspace.fake.counters.trackerUploads).toBe(1);
      expect(workspace.fake.trackerUploadBodies[0].toString()).toContain("BTN");
      expect(workspace.fake.counters.clientInjections).toBe(0);
    } finally {
      await workspace.cleanup();
    }
  });
}

test("CLI unattended skips all duplicate trackers without downstream work", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  workspace.env.UPBRR_E2E_DUPLICATE_TRACKERS = releaseWorkflowParityFixture.trackerID;
  try {
    const result = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "-ua",
        "-sda",
        "--no-seed",
        workspace.sourcePath,
      ],
      workspace.env,
    );
    expect(result.code, result.output).toBe(0);
    expect(result.output).toContain("All trackers skipped after duplicate checks.");
    expect(result.output).not.toMatch(/\[[yY]\/[nN]\]/);
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.imageUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await workspace.cleanup();
  }
});

test("CLI debug injects into the client by default", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  try {
    const debug = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--debug",
        "--unattended_confirm",
        workspace.sourcePath,
      ],
      workspace.env,
      "y\ny\n",
    );
    expect(debug.code, debug.output).toBe(0);
    expect(debug.output).toContain("client injection: completed:");
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(1);
  } finally {
    await workspace.cleanup();
  }
});

test("CLI debug -ns skips client injection", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  try {
    const noSeed = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--debug",
        "-ns",
        "--unattended_confirm",
        workspace.sourcePath,
      ],
      workspace.env,
      "y\ny\n",
    );
    expect(noSeed.code, noSeed.output).toBe(0);
    expect(noSeed.output).toContain("tracker uploads and client injection are disabled");
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await workspace.cleanup();
  }
});

function runCLI(
  args: string[],
  env: NodeJS.ProcessEnv,
  input = "",
): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve) => {
    const child = spawn(e2eBinary, args, {
      cwd: repoRoot,
      env,
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    });
    const chunks: string[] = [];
    child.stdout?.on("data", (chunk) => chunks.push(String(chunk)));
    child.stderr?.on("data", (chunk) => chunks.push(String(chunk)));
    child.stdin?.end(input);
    child.on("close", (code) => resolve({ code, output: chunks.join("") }));
  });
}

test("CLI metadata reports mixed source seasons", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  try {
    const mixed = path.join(workspace.root, "Example.Series.S01");
    await mkdir(mixed);
    for (const name of [
      "Example.Series.S00E01.mkv",
      "Example.Series.S01E01.mkv",
      "Example.Series.S01E02.mkv",
    ]) {
      await writeFile(path.join(mixed, name), "synthetic episode");
    }
    const result = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--no-seed",
        "--keep-folder",
        "--unattended",
        mixed,
      ],
      workspace.env,
    );
    expect(result.output).toContain("Multiple seasons detected: S00, S01");
    expect(result.output).toContain('Additional season file: "Example.Series.S00E01.mkv" — S00');
    expect(workspace.fake.counters.trackerUploads).toBe(0);
  } finally {
    await workspace.cleanup();
  }
});

for (const keepExisting of [false, true]) {
  test(
    keepExisting
      ? "CLI keeps automatic provider IDs through an IMDb correction and restart"
      : "CLI preserves independent manual provider IDs across sequential edits and restart",
    async () => {
      const workspace = await createE2EWorkspace({ mediaKind: "tv" });
      try {
        const result = await runCLI(
          [
            "--config",
            workspace.configPath,
            ...(keepExisting
              ? ["--keep-existing-ids"]
              : ["--tmdb=1001", "--imdb=tt7654321", "--tvdb=2001"]),
            "--manual-year=2030",
            "--no-year",
            "--no-seed",
            workspace.sourcePath,
          ],
          workspace.env,
          keepExisting
            ? "n\n--imdb=tt8889999 --year=2031 --input-only\n"
            : "n\n--imdb=tt8889999\nn\n--tmdb=1001 --input-only\n",
        );
        expect(result.code, result.output).toBe(0);
        const facts = result.output.match(/^Input facts: (.+)$/m);
        expect(facts, result.output).toBeTruthy();
        expect(JSON.parse(facts![1]).identity).toMatchObject({
          TMDBID: 1001,
          IMDBID: 8889999,
          TVDBID: 2001,
        });
        const app = await startApp(workspace, { seed: false });
        try {
          const auth = (await (await fetch(new URL("api/auth/status", app.url))).json()) as {
            csrfToken: string;
          };
          const response = await fetch(new URL("api/app/GetHistoryOverview", app.url), {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              "X-Csrf-Token": auth.csrfToken,
              Origin: new URL(app.url).origin,
            },
            body: JSON.stringify({ SourcePath: workspace.sourcePath }),
          });
          expect(response.status, await response.clone().text()).toBe(200);
          const history = (await response.json()) as HistoryOverview;
          expect(history.Identity).toMatchObject({ TMDBID: 1001, IMDBID: 8889999, TVDBID: 2001 });
          expect(history.Display.TVDBNamingAvailable).toBe(true);
          expect(history.Identity.Provenance.TMDB).toBe(keepExisting ? "retained" : "explicit");
          expect(
            history.Display.Providers.find((provider) => provider.Provider === "tmdb"),
          ).toMatchObject({ ID: 1001, SummaryAvailable: true });
          expect(
            history.Display.Providers.find((provider) => provider.Provider === "tvdb"),
          ).toMatchObject({ ID: 2001, SummaryAvailable: true });
        } finally {
          await app.stop();
        }

        expect(workspace.fake.counters.trackerUploads).toBe(0);
        expect(workspace.fake.counters.clientInjections).toBe(0);
      } finally {
        await workspace.cleanup();
      }
    },
  );
}

test("CLI permits a TV year fallback when TVDB is disabled", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  try {
    const result = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers=AITHER",
        "--tvdb=0",
        "--year=2030",
        "--no-year=false",
        "--input-only",
        "--unattended",
        workspace.sourcePath,
      ],
      workspace.env,
    );
    expect(result.code, result.output).toBe(0);
    const facts = result.output.match(/^Input facts: (.+)$/m);
    expect(facts, result.output).toBeTruthy();
    expect(JSON.parse(facts![1]).identity.TVDBID).toBe(0);
    const app = await startApp(workspace, { seed: false });
    try {
      const auth = (await (await fetch(new URL("api/auth/status", app.url))).json()) as {
        csrfToken: string;
      };
      const response = await fetch(new URL("api/app/GetHistoryOverview", app.url), {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": auth.csrfToken,
          Origin: new URL(app.url).origin,
        },
        body: JSON.stringify({ SourcePath: workspace.sourcePath }),
      });
      expect(response.status, await response.clone().text()).toBe(200);
      const history = (await response.json()) as HistoryOverview;
      expect(history.Display.TVDBNamingAvailable).not.toBe(true);
      expect(history.ReleaseNameOverrides).toMatchObject({ ManualYear: 2030, NoYear: false });
    } finally {
      await app.stop();
    }
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await workspace.cleanup();
  }
});

for (const [mediaKind, provider, matchNumber] of [
  ["movie", "TMDB", 9],
  ["tv", "IMDb", 18],
  ["tv", "TVDB", 27],
] as const) {
  test(`CLI selects a ${mediaKind} ${provider} title and reviews prepared identity`, async () => {
    const workspace = await createE2EWorkspace({ mediaKind });
    workspace.env.UPBRR_E2E_SELECTED_PROVIDER_METADATA = "1";
    try {
      const result = await runCLI(
        [
          "--config",
          workspace.configPath,
          "--ui=plain",
          "--uac",
          "--trackers",
          releaseWorkflowParityFixture.trackerID,
          "--tmdb=1001",
          "--imdb=tt7654321",
          mediaKind === "tv" ? "--tvdb=2001" : "--tvdb=0",
          "--no-seed",
          workspace.sourcePath,
        ],
        workspace.env,
        `n\nsearch\n${mediaKind}\nExample Title 2026\nback\nn\nsearch\n${mediaKind}\nExample Title 2026\n${matchNumber}\n${provider === "TVDB" ? "y\ny\n" : "n\n--input-only\n"}`,
      );
      expect(result.code, result.output).toBe(0);
      expect(result.output).toContain("Example Candidate 9");
      expect(result.output).toContain("Example Director");
      expect(result.output).toContain("https://www.themoviedb.org/");
      const selectedID = provider === "IMDb" ? "tt0001010" : "1010";
      const review = result.output.indexOf(
        `\n${provider}: ${selectedID}\n`,
        result.output.lastIndexOf("Match number"),
      );
      expect(review, result.output).toBeGreaterThan(result.output.lastIndexOf("Match number"));
      expect(result.output.indexOf("Metadata correct?", review), result.output).toBeGreaterThan(
        review,
      );
      const facts =
        provider === "TVDB"
          ? workspace.fake.trackerUploadBodies[0]
              ?.toString("utf8")
              .match(/name="identity"\r\n\r\n([^\r\n]+)/)
          : result.output.match(/^Input facts: (.+)$/m);
      expect(facts, result.output).toBeTruthy();
      const identity = provider === "TVDB" ? JSON.parse(facts![1]) : JSON.parse(facts![1]).identity;
      expect(identity).toMatchObject({
        TMDBID: provider === "TMDB" ? 1010 : 1001,
        IMDBID: provider === "IMDb" ? 1010 : 7654321,
        TVDBID: provider === "TVDB" ? 1010 : mediaKind === "tv" ? 2001 : 0,
      });
      const app = await startApp(workspace, { seed: false });
      try {
        const auth = (await (await fetch(new URL("api/auth/status", app.url))).json()) as {
          csrfToken: string;
        };
        const response = await fetch(new URL("api/app/GetHistoryOverview", app.url), {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-Csrf-Token": auth.csrfToken,
            Origin: new URL(app.url).origin,
          },
          body: JSON.stringify({ SourcePath: workspace.sourcePath }),
        });
        expect(response.status, await response.clone().text()).toBe(200);
        const history = (await response.json()) as HistoryOverview;
        expect(
          history.Display.Providers.find((item) => item.Provider === provider.toLowerCase()),
        ).toMatchObject({
          ID: 1010,
          SummaryAvailable: true,
          Summary: { Title: "Example Candidate 9", Year: 2025 },
        });
      } finally {
        await app.stop();
      }
      expect(workspace.fake.counters.trackerUploads).toBe(provider === "TVDB" ? 1 : 0);
      expect(workspace.fake.counters.clientInjections).toBe(0);
    } finally {
      await workspace.cleanup();
    }
  });
}
