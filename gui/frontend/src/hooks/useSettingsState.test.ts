// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { createElement } from "react";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import {
  nextQbitDirectState,
  normalizeTorrentClientForSave,
  normalizeTorrentClientsForSave,
  useSettingsState,
} from "./useSettingsState";

afterEach(() => {
  cleanup();
  latestPayload = "";
  delete (globalThis as typeof globalThis & { go?: any }).go;
});

describe("normalizeTorrentClientForSave", () => {
  it("preserves watch client fields", () => {
    expect(
      normalizeTorrentClientForSave({
        Type: "watch",
        WatchFolder: "/watch",
        StorageDir: "/storage",
      }),
    ).toEqual({
      Type: "watch",
      WatchFolder: "/watch",
      StorageDir: "/storage",
    });
  });

  it("migrates legacy qbit fields and removes aliases", () => {
    expect(
      normalizeTorrentClientForSave({
        TorrentClient: "qbit",
        URL: "http://localhost:8080",
        Username: "user",
        Password: "secret",
        Category: "movies",
        Tags: ["AITHER", "BLU"],
      }),
    ).toEqual({
      QbitURL: "http://localhost:8080",
      QbitUser: "user",
      QbitPass: "secret",
      QbitCategoryValue: "movies",
      QbitTag: "AITHER,BLU",
    });
  });

  it("maps legacy TLS skip verify to certificate verification before removing aliases", () => {
    expect(
      normalizeTorrentClientForSave({
        TorrentClient: "qbit",
        TLSSkipVerify: true,
      }),
    ).toEqual({
      VerifyWebUICertificate: false,
    });
  });
});

describe("normalizeTorrentClientsForSave", () => {
  it("normalizes each configured client without dropping watch-folder config", () => {
    expect(
      normalizeTorrentClientsForSave({
        TorrentClients: {
          qbit: {
            Type: "qbit",
            URL: "http://localhost:8080",
            Username: "user",
            Password: "secret",
          },
          watch: {
            Type: "watch",
            WatchFolder: "/watch",
            StorageDir: "/storage",
          },
        },
      }),
    ).toEqual({
      TorrentClients: {
        qbit: {
          QbitURL: "http://localhost:8080",
          QbitUser: "user",
          QbitPass: "secret",
        },
        watch: {
          Type: "watch",
          WatchFolder: "/watch",
          StorageDir: "/storage",
        },
      },
    });
  });
});

describe("nextQbitDirectState", () => {
  it("clears proxy and direct credentials when qbit direct is disabled", () => {
    expect(
      nextQbitDirectState(
        {
          QuiProxyURL: "http://proxy.local",
          QbitURL: "http://localhost:8080",
          QbitPort: 8080,
          QbitUser: "user",
          QbitPass: "secret",
          URL: "http://legacy.local",
          Username: "legacy-user",
          Password: "legacy-pass",
        },
        false,
      ),
    ).toEqual({
      QuiProxyURL: "",
      QbitURL: "",
      QbitPort: 0,
      QbitUser: "",
      QbitPass: "",
      URL: "",
      Username: "",
      Password: "",
    });
  });
});

function TorrentClientsHarness() {
  const state = useSettingsState({ activeTab: "settings" });

  return createElement(
    "div",
    null,
    state.renderTorrentClientsSection(false),
    createElement(PayloadCapture, { value: state.buildSavePayload() }),
  );
}

function ClientSetupHarness() {
  const state = useSettingsState({ activeTab: "settings" });
  const clientSetup = state.configData?.ClientSetup;

  if (!clientSetup || typeof clientSetup !== "object" || Array.isArray(clientSetup)) {
    return createElement("div", null);
  }

  const meta = state.sectionFieldMeta.ClientSetup ?? {};

  return createElement(
    "div",
    null,
    ...Object.entries(clientSetup).map(([key, value]) =>
      state.renderField(key, value, ["ClientSetup", key], meta[key]),
    ),
    createElement(PayloadCapture, { value: state.buildSavePayload() }),
  );
}

function TrackerSettingsHarness() {
  const state = useSettingsState({ activeTab: "settings" });

  return createElement(
    "div",
    null,
    state.renderTrackerSection(false),
    createElement(PayloadCapture, { value: state.buildSavePayload() }),
  );
}

function TrackerSettingsAdvancedHarness() {
  const state = useSettingsState({ activeTab: "settings" });

  return createElement(
    "div",
    null,
    state.renderTrackerSection(true),
    createElement(PayloadCapture, { value: state.buildSavePayload() }),
  );
}

function ImageHostingHarness() {
  const state = useSettingsState({ activeTab: "settings" });

  return createElement(
    "div",
    null,
    state.renderImageHostingSection(),
    createElement(PayloadCapture, { value: state.buildSavePayload() }),
  );
}

function ScreenshotSettingsHarness() {
  const state = useSettingsState({ activeTab: "settings" });
  const config = state.screenshotConfig;
  if (!config) {
    return createElement("div");
  }
  return createElement(
    "div",
    null,
    state.renderField(
      "MaxMenuItems",
      config.MaxMenuItems,
      ["ScreenshotHandling", "MaxMenuItems"],
      state.sectionFieldMeta.ScreenshotHandling?.MaxMenuItems,
    ),
    createElement(PayloadCapture, { value: state.buildSavePayload() }),
  );
}

let latestPayload = "";

/** Captures save payloads without rendering secret-shaped values into DOM snapshots. */
function PayloadCapture({ value }: { value: string | null }) {
  latestPayload = value ?? "";
  return null;
}

/** Parses the latest captured payload for focused assertions outside matcher output. */
function readPayload<T>() {
  return JSON.parse(latestPayload || "{}") as T;
}

function AdvancedFieldMetaHarness() {
  const state = useSettingsState({ activeTab: "settings" });
  const advancedBySection = Object.fromEntries(
    Object.entries(state.sectionFieldMeta).map(([section, fields]) => [
      section,
      Object.values(fields)
        .filter((field) => field.advanced)
        .map((field) => field.key)
        .sort(),
    ]),
  );

  return createElement(
    "pre",
    { "data-testid": "advanced-fields" },
    JSON.stringify(advancedBySection),
  );
}

describe("settings advanced fields", () => {
  it("matches the configured per-section advanced allowlist", () => {
    render(createElement(AdvancedFieldMetaHarness));

    const advancedBySection = JSON.parse(
      screen.getByTestId("advanced-fields").textContent ?? "{}",
    ) as Record<string, string[]>;

    expect(advancedBySection.MainSettings).toEqual([]);
    expect(advancedBySection.Metadata).toEqual([
      "BTNAPI",
      "BlurayScore",
      "BluraySingleScore",
      "CheckPredb",
      "SkipAutoTorrent",
      "SkipTrackerFilenameLookup",
      "UserOverrides",
    ]);
    expect(advancedBySection.ScreenshotHandling).toEqual([
      "Desat",
      "FFmpegCompression",
      "FFmpegLimit",
      "MaxConcurrentUploads",
      "ProcessLimit",
      "TonemapAlgorithm",
    ]);
    expect(advancedBySection.Description).toEqual([
      "CharLimit",
      "CustomSignature",
      "FileLimit",
      "LogoLanguage",
      "LogoSize",
      "ProcessLimit",
    ]);
    expect(advancedBySection.PostUpload).toEqual(["InjectDelay", "MaxConcurrentTrackers"]);
    expect(advancedBySection.TorrentCreation).toEqual([]);
    expect(advancedBySection.TorrentClients).toEqual(["VerifyWebUICertificate"]);
  });
});

describe("DVD menu screenshot settings", () => {
  it("loads the default maximum and preserves edits in the save payload", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () => JSON.stringify({ ScreenshotHandling: { MaxMenuItems: 6 } }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => [],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(ScreenshotSettingsHarness));
    const input = await screen.findByLabelText("Maximum DVD menu images");
    expect(input).toHaveValue(6);
    fireEvent.change(input, { target: { value: "8" } });
    await waitFor(() => {
      const payload = readPayload<{ ScreenshotHandling?: { MaxMenuItems?: number } }>();
      expect(payload.ScreenshotHandling?.MaxMenuItems).toBe(8);
    });
  });
});

describe("renderTorrentClientsSection", () => {
  it("renders watch client fields and preserves qbit clients on update", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              TorrentClients: {
                watcher: {
                  Type: "watch",
                  WatchFolder: "/watch",
                  StorageDir: "/storage",
                },
                qbit: {
                  Type: "qbit",
                  QbitURL: "http://localhost:8080",
                  QbitUser: "user",
                  QbitPass: "secret",
                  AutomaticManagementPaths: ["/media"],
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => [],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(TorrentClientsHarness));

    await waitFor(() => expect(screen.getByText("watcher")).toBeInTheDocument());

    const watchCard = screen.getByText("watcher").closest(".settings-card");
    const qbitCard = screen.getByText("qbit").closest(".settings-card");
    expect(watchCard).toBeTruthy();
    expect(qbitCard).toBeTruthy();

    const watchScope = within(watchCard as HTMLElement);
    const qbitScope = within(qbitCard as HTMLElement);

    expect(watchScope.getByLabelText("Type")).toHaveValue("watch");
    expect(watchScope.getByLabelText("Watch folder")).toHaveValue("/watch");
    expect(watchScope.getByLabelText("Storage directory")).toHaveValue("/storage");
    expect(qbitScope.getByLabelText("qBit URL")).toHaveValue("http://localhost:8080");
    expect(qbitScope.getByLabelText("Automatic management paths 1")).toHaveValue("/media");
    expect(qbitScope.getByRole("button", { name: "Add Linked folder item" })).toBeInTheDocument();
    expect(qbitScope.getByRole("button", { name: "Add Local path item" })).toBeInTheDocument();
    expect(qbitScope.getByRole("button", { name: "Add Remote path item" })).toBeInTheDocument();
    expect(
      qbitScope.getByRole("button", { name: "Add Automatic management paths item" }),
    ).toBeInTheDocument();
    expect(qbitScope.getByLabelText("qBit direct")).toBeChecked();

    fireEvent.change(watchScope.getByLabelText("Watch folder"), {
      target: { value: "/watch/new" },
    });

    await waitFor(() =>
      expect(watchScope.getByLabelText("Watch folder")).toHaveValue("/watch/new"),
    );

    const payload = readPayload<{
      TorrentClients?: Record<string, Record<string, unknown>>;
    }>();
    expect(payload.TorrentClients?.watcher).toEqual({
      Type: "watch",
      WatchFolder: "/watch/new",
      StorageDir: "/storage",
    });
    expect(payload.TorrentClients?.qbit).toMatchObject({
      QbitURL: "http://localhost:8080",
      QbitUser: "user",
      QbitPass: "secret",
      AutomaticManagementPaths: ["/media"],
    });
  });
});

describe("ClientSetup client selectors", () => {
  it("renders default client empty option without a none sentinel", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              ClientSetup: {
                DefaultClient: "",
              },
              TorrentClients: {
                qbit: {
                  Type: "qbit",
                  QbitURL: "http://localhost:8080",
                  QbitUser: "user",
                  QbitPass: "secret",
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => [],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(ClientSetupHarness));

    await waitFor(() => expect(screen.getByLabelText("Default client")).toHaveValue(""));

    const defaultClientSelect = screen.getByLabelText("Default client") as HTMLSelectElement;
    expect(Array.from(defaultClientSelect.options).map((option) => option.value)).toEqual([
      "",
      "qbit",
    ]);
    expect(Array.from(defaultClientSelect.options).map((option) => option.textContent)).toEqual([
      "",
      "qbit",
    ]);

    fireEvent.change(defaultClientSelect, { target: { value: "qbit" } });
    await waitFor(() => expect(defaultClientSelect).toHaveValue("qbit"));

    fireEvent.change(defaultClientSelect, { target: { value: "" } });
    await waitFor(() => expect(defaultClientSelect).toHaveValue(""));

    const payload = readPayload<{
      ClientSetup?: { DefaultClient?: string };
    }>();
    expect(payload.ClientSetup?.DefaultClient).toBe("");
  });

  it("renders default, injected, and searching clients as torrent client dropdowns", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              ClientSetup: {
                DefaultClient: "qbit",
                InjectClients: ["qbit"],
                SearchClients: ["watcher"],
              },
              TorrentClients: {
                qbit: {
                  Type: "qbit",
                  QbitURL: "http://localhost:8080",
                  QbitUser: "user",
                  QbitPass: "secret",
                },
                watcher: {
                  Type: "watch",
                  WatchFolder: "/watch",
                  StorageDir: "/storage",
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => [],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(ClientSetupHarness));

    await waitFor(() => expect(screen.getByLabelText("Default client")).toHaveValue("qbit"));

    expect(screen.getByLabelText("Injected clients 1")).toHaveValue("qbit");
    expect(screen.getByLabelText("Searching clients 1")).toHaveValue("watcher");

    fireEvent.change(screen.getByLabelText("Default client"), {
      target: { value: "watcher" },
    });
    fireEvent.change(screen.getByLabelText("Injected clients 1"), {
      target: { value: "watcher" },
    });

    await waitFor(() => expect(screen.getByLabelText("Default client")).toHaveValue("watcher"));

    const payload = readPayload<{
      ClientSetup?: {
        DefaultClient?: string;
        InjectClients?: string[];
        SearchClients?: string[];
      };
    }>();
    expect(payload.ClientSetup?.DefaultClient).toBe("watcher");
    expect(payload.ClientSetup?.InjectClients).toEqual(["watcher"]);
    expect(payload.ClientSetup?.SearchClients).toEqual(["watcher"]);
  });
});

describe("Tracker client selectors", () => {
  it("renders CZT passkey field without preserving stale URL or API key", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              Trackers: {
                DefaultTrackers: [],
                PreferredTracker: "",
                Trackers: {
                  CZT: {
                    LinkDirName: "",
                    URL: "https://czteam.example",
                    APIKey: "service-token",
                    AnnounceURL: "https://czteam.me/announce.php?passkey=stale",
                    Passkey: "user-passkey",
                  },
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => ["CZT"],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("CZT", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("CZT", { selector: ".settings-card__summary-name" }));

    expect(screen.queryByLabelText("URL")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("API key")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Passkey")).toHaveValue("[REDACTED]");

    const payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.CZT).toMatchObject({
      Passkey: "user-passkey",
    });
    expect(payload.Trackers?.Trackers?.CZT?.URL).toBeUndefined();
    expect(payload.Trackers?.Trackers?.CZT?.APIKey).toBeUndefined();
    expect(payload.Trackers?.Trackers?.CZT?.AnnounceURL).toBeUndefined();
  });

  it("creates CZT entries with passkey defaults", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              Trackers: {
                DefaultTrackers: [],
                PreferredTracker: "",
                Trackers: {},
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => ["CZT"],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(TrackerSettingsHarness));

    await waitFor(() => expect(screen.getAllByRole("combobox").length).toBeGreaterThan(0));

    const trackerSelects = screen.getAllByRole("combobox");
    const trackerSelect = trackerSelects[trackerSelects.length - 1] as HTMLSelectElement;
    fireEvent.change(trackerSelect, { target: { value: "CZT" } });
    fireEvent.click(screen.getByRole("button", { name: "Add entry" }));

    await waitFor(() =>
      expect(
        screen.getByText("CZT", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );

    const payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.CZT).toMatchObject({
      Passkey: "",
    });
    expect(payload.Trackers?.Trackers?.CZT?.URL).toBeUndefined();
    expect(payload.Trackers?.Trackers?.CZT?.APIKey).toBeUndefined();
  });

  it("renders tracker torrent client as a configured client dropdown", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              Trackers: {
                DefaultTrackers: [],
                PreferredTracker: "",
                Trackers: {
                  AITHER: {
                    LinkDirName: "",
                    APIKey: "",
                    ImageHost: "",
                    TorrentClient: "qbit",
                    Anon: false,
                  },
                },
              },
              TorrentClients: {
                qbit: {
                  Type: "qbit",
                  QbitURL: "http://localhost:8080",
                  QbitUser: "user",
                  QbitPass: "secret",
                },
                watcher: {
                  Type: "watch",
                  WatchFolder: "/watch",
                  StorageDir: "/storage",
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => ["AITHER"],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(TrackerSettingsAdvancedHarness));

    await waitFor(() =>
      expect(
        screen.getByText("AITHER", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("AITHER", { selector: ".settings-card__summary-name" }));

    await waitFor(() => expect(screen.getByLabelText("Torrent client")).toHaveValue("qbit"));

    const torrentClientSelect = screen.getByLabelText("Torrent client") as HTMLSelectElement;
    expect(Array.from(torrentClientSelect.options).map((option) => option.textContent)).toEqual([
      "",
      "qbit",
      "watcher",
    ]);

    fireEvent.change(screen.getByLabelText("Torrent client"), {
      target: { value: "watcher" },
    });

    await waitFor(() => expect(screen.getByLabelText("Torrent client")).toHaveValue("watcher"));

    const payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.AITHER?.TorrentClient).toBe("watcher");
  });

  it("does not treat catalog tracker entries or default tracker membership as enabled config", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              Trackers: {
                DefaultTrackers: ["AITHER", "BLU", "BHD"],
                PreferredTracker: "",
                Trackers: {
                  AITHER: {
                    URL: "https://aither.cc",
                    APIKey: "",
                    Anon: false,
                  },
                  BLU: {
                    URL: "https://blutopia.cc",
                    APIKey: "",
                    Anon: false,
                  },
                  BHD: {
                    URL: "https://beyond-hd.me",
                    APIKey: "tracker-token",
                    Anon: false,
                  },
                },
              },
            }),
          GetDefaultConfig: async () =>
            JSON.stringify({
              Trackers: {
                DefaultTrackers: ["AITHER", "BLU", "BHD"],
                PreferredTracker: "",
                Trackers: {
                  AITHER: {
                    URL: "https://aither.cc",
                    APIKey: "",
                    Anon: false,
                  },
                  BLU: {
                    URL: "https://blutopia.cc",
                    APIKey: "",
                    Anon: false,
                  },
                  BHD: {
                    URL: "https://beyond-hd.me",
                    APIKey: "",
                    Anon: false,
                  },
                },
              },
            }),
          ListKnownTrackers: async () => ["AITHER", "BLU", "BHD"],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("BHD", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );

    expect(screen.queryByText("AITHER", { selector: ".settings-card__summary-name" })).toBeNull();
    expect(screen.queryByText("BLU", { selector: ".settings-card__summary-name" })).toBeNull();
    expect(screen.getByText("1/1")).toBeInTheDocument();
  });

  it("masks encrypted envelopes on tracker URL fields and preserves them for saves", async () => {
    const encryptedURL = "upbrr-enc:v1:encrypted-btn-url";
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              Trackers: {
                DefaultTrackers: [],
                PreferredTracker: "",
                Trackers: {
                  BTN: {
                    APIKey: "tracker-token",
                    URL: encryptedURL,
                    Username: "",
                    Password: "",
                  },
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => ["BTN"],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("BTN", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("BTN", { selector: ".settings-card__summary-name" }));

    expect(screen.getByLabelText("URL")).toHaveValue("[REDACTED]");

    let payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.BTN?.URL === encryptedURL).toBe(true);

    fireEvent.change(screen.getByLabelText("URL"), {
      target: { value: "https://btn.example" },
    });

    await waitFor(() => expect(screen.getByLabelText("URL")).toHaveValue("https://btn.example"));

    payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.BTN?.URL).toBe("https://btn.example");
  });

  it("renders BTN announce URL from tracker schema when stored config lacks the key", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              Trackers: {
                DefaultTrackers: [],
                PreferredTracker: "",
                Trackers: {
                  BTN: {
                    APIKey: "tracker-token",
                    URL: "https://backup.landof.tv",
                    Username: "",
                    Password: "",
                  },
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => ["BTN"],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("BTN", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("BTN", { selector: ".settings-card__summary-name" }));

    expect(screen.getByLabelText("Announce URL")).toHaveValue("");
  });

  it("shows Lostimg as an LST image host only when configured in image hosting", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              ImageHosting: {
                LostimgEnabled: true,
                LostimgAPI: "secret",
              },
              Trackers: {
                DefaultTrackers: [],
                PreferredTracker: "",
                Trackers: {
                  LST: {
                    LinkDirName: "",
                    APIKey: "tracker-token",
                    ImageHost: "",
                    Anon: false,
                  },
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => ["LST"],
          GetImageHostPolicyMetadata: async () => ({
            TrackerUploadHosts: { LST: ["lostimg"] },
            OwnedHosts: { lostimg: "LST" },
          }),
        },
      },
    };

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("LST", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("LST", { selector: ".settings-card__summary-name" }));

    const imageHostSelect = screen.getByLabelText("Image host") as HTMLSelectElement;
    expect(Array.from(imageHostSelect.options).map((option) => option.value)).toContain("lostimg");
  });

  it("shows configured global hosts for LST when Lostimg is disabled", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              ImageHosting: {
                Host1: "imgbb",
                LostimgEnabled: false,
                LostimgAPI: "",
              },
              Trackers: {
                DefaultTrackers: [],
                PreferredTracker: "",
                Trackers: {
                  LST: {
                    LinkDirName: "",
                    APIKey: "tracker-token",
                    ImageHost: "",
                    Anon: false,
                  },
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => ["LST"],
          GetImageHostPolicyMetadata: async () => ({
            TrackerUploadHosts: { LST: ["lostimg"] },
            OwnedHosts: { lostimg: "LST" },
          }),
        },
      },
    };

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("LST", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("LST", { selector: ".settings-card__summary-name" }));

    const values = Array.from(
      (screen.getByLabelText("Image host") as HTMLSelectElement).options,
    ).map((option) => option.value);
    expect(values).toContain("imgbb");
    expect(values).not.toContain("lostimg");
  });

  it("shows Reelflix as an RF image host and exposes RF image API", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              ImageHosting: {},
              Trackers: {
                DefaultTrackers: [],
                PreferredTracker: "",
                Trackers: {
                  RF: {
                    LinkDirName: "",
                    APIKey: "",
                    ImgAPI: "",
                    ImageHost: "reelflix",
                    Anon: false,
                  },
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => ["RF"],
          GetImageHostPolicyMetadata: async () => ({
            TrackerUploadHosts: { RF: ["reelflix"] },
            OwnedHosts: { reelflix: "RF" },
          }),
        },
      },
    };

    render(createElement(TrackerSettingsAdvancedHarness));

    await waitFor(() =>
      expect(
        screen.getByText("RF", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("RF", { selector: ".settings-card__summary-name" }));

    const imageHostSelect = screen.getByLabelText("Image host") as HTMLSelectElement;
    expect(Array.from(imageHostSelect.options).map((option) => option.value)).toContain("reelflix");

    fireEvent.change(screen.getByLabelText("Image API"), {
      target: { value: "secret" },
    });

    await waitFor(() => expect(screen.getByLabelText("Image API")).toHaveValue("secret"));

    const payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.RF?.ImgAPI).toBe("secret");
  });

  it("shows configured global hosts for RF when Reelflix is disabled", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              ImageHosting: {
                Host1: "imgbb",
              },
              Trackers: {
                DefaultTrackers: [],
                PreferredTracker: "",
                Trackers: {
                  RF: {
                    LinkDirName: "",
                    APIKey: "tracker-token",
                    ImgAPI: "",
                    ImageHost: "",
                    Anon: false,
                  },
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => ["RF"],
          GetImageHostPolicyMetadata: async () => ({
            TrackerUploadHosts: { RF: ["reelflix"] },
            OwnedHosts: { reelflix: "RF" },
          }),
        },
      },
    };

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("RF", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("RF", { selector: ".settings-card__summary-name" }));

    const values = Array.from(
      (screen.getByLabelText("Image host") as HTMLSelectElement).options,
    ).map((option) => option.value);
    expect(values).toContain("imgbb");
    expect(values).not.toContain("reelflix");
  });
});

describe("tracker advanced fields", () => {
  it("hides only the tracker advanced allowlist when advanced is closed", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              Trackers: {
                DefaultTrackers: [],
                PreferredTracker: "",
                Trackers: {
                  MTV: {
                    FaviconURL: "https://example.test/favicon.ico",
                    LinkDirName: "mtv",
                    APIKey: "api-key",
                    Username: "user",
                    Password: "pass",
                    AnnounceURL: "https://example.test/announce",
                    Anon: false,
                    OTPURI: "otpauth://totp/example",
                    SkipIfRehash: true,
                    PreferMTV: true,
                  },
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => ["MTV"],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("MTV", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("MTV", { selector: ".settings-card__summary-name" }));

    expect(screen.queryByLabelText("Favicon URL")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Link dir name")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Skip if rehash")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Prefer MTV torrent")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Announce URL")).toBeInTheDocument();
    expect(screen.getByLabelText("OTP URI")).toBeInTheDocument();
  });
});

describe("Image hosting settings", () => {
  it("renders Lostimg as tracker-specific and keeps it out of global host priority", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              ImageHosting: {
                Host1: "",
                Host2: "",
                Host3: "",
                Host4: "",
                Host5: "",
                Host6: "",
                LostimgEnabled: false,
                LostimgAPI: "",
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => [],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(ImageHostingHarness));

    await waitFor(() => expect(screen.getByLabelText("LST Lostimg")).toBeInTheDocument());

    const hostOne = screen.getByLabelText("Host 1") as HTMLSelectElement;
    expect(Array.from(hostOne.options).map((option) => option.value)).not.toContain("lostimg");

    fireEvent.click(screen.getByLabelText("LST Lostimg"));
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "secret" },
    });

    await waitFor(() => expect(screen.getByLabelText("LST Lostimg")).toBeChecked());

    const payload = readPayload<{
      ImageHosting?: {
        LostimgEnabled?: boolean;
        LostimgAPI?: string;
      };
    }>();
    expect(payload.ImageHosting?.LostimgEnabled).toBe(true);
    expect(payload.ImageHosting?.LostimgAPI).toBe("secret");
  });

  it("renders Reelflix as an RF image host in image hosting settings", async () => {
    (globalThis as typeof globalThis & { go?: any }).go = {
      guiapp: {
        App: {
          GetConfig: async () =>
            JSON.stringify({
              ImageHosting: {
                Host1: "",
                Host2: "",
                Host3: "",
                Host4: "",
                Host5: "",
                Host6: "",
              },
              Trackers: {
                Trackers: {
                  RF: {
                    ImageHost: "",
                    ImgAPI: "",
                  },
                },
              },
            }),
          GetDefaultConfig: async () => JSON.stringify({}),
          ListKnownTrackers: async () => [],
          GetImageHostPolicyMetadata: async () => ({}),
        },
      },
    };

    render(createElement(ImageHostingHarness));

    await waitFor(() => expect(screen.getByLabelText("RF Reelflix")).toBeInTheDocument());

    const hostOne = screen.getByLabelText("Host 1") as HTMLSelectElement;
    expect(Array.from(hostOne.options).map((option) => option.value)).not.toContain("reelflix");

    fireEvent.click(screen.getByLabelText("RF Reelflix"));
    fireEvent.change(screen.getByLabelText("Image API"), {
      target: { value: "secret" },
    });

    await waitFor(() => expect(screen.getByLabelText("RF Reelflix")).toBeChecked());

    const payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.RF?.ImageHost).toBe("reelflix");
    expect(payload.Trackers?.Trackers?.RF?.ImgAPI).toBe("secret");
  });
});
