// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { createElement } from "react";
import type { ReactElement } from "react";
import {
  act,
  cleanup,
  fireEvent,
  render as testingRender,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  installAppOperationMocks as installRawAppOperationMocks,
  type AppOperationMocks,
} from "../test/appRequestMock";
import type { ConfigMap, ConfigValue, TrackerCatalog, TrackerCatalogEntry } from "../types";
import { createSettingsRenderers } from "../settings/renderers";
import { TrackerCatalogProvider, useTrackerCatalog } from "../trackerCatalog";

import {
  nextQbitDirectState,
  normalizeTorrentClientForSave,
  normalizeTorrentClientsForSave,
  useSettingsState as useRawSettingsState,
} from "./useSettingsState";

const useSettingsState = (options: Parameters<typeof useRawSettingsState>[0]) => {
  const state = useRawSettingsState(options);
  return { ...state, ...createSettingsRenderers(state.editorContext) };
};

const installAppOperationMocks = (operations: AppOperationMocks) =>
  installRawAppOperationMocks({
    GetConfigActivation: async () => ({
      status: "active",
      activeGeneration: 1,
      impacts: [],
      updatedAt: "2026-09-19T00:00:00Z",
    }),
    ...operations,
  });

const render = (element: ReactElement) =>
  testingRender(
    createElement(
      QueryClientProvider,
      { client: new QueryClient({ defaultOptions: { queries: { retry: false } } }) },
      element,
    ),
  );

afterEach(() => {
  cleanup();
  latestPayload = "";
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
  const clientSetup = state.settingsConfigData?.ClientSetup;

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

function TorrentCreationHarness() {
  const state = useSettingsState({ activeTab: "settings" });
  const torrentCreation = state.settingsConfigData?.TorrentCreation;

  if (!torrentCreation || typeof torrentCreation !== "object" || Array.isArray(torrentCreation)) {
    return createElement("div", null);
  }

  const meta = state.sectionFieldMeta.TorrentCreation ?? {};

  return createElement(
    "div",
    null,
    ...Object.entries(torrentCreation).map(([key, value]) =>
      state.renderField(key, value, ["TorrentCreation", key], meta[key]),
    ),
  );
}

function TrackerSettingsHarness() {
  const state = useSettingsState({ activeTab: "settings" });
  const trackerAnonymous = (config: ConfigMap | null) => {
    const trackers = config?.Trackers;
    if (!trackers || typeof trackers !== "object" || Array.isArray(trackers)) return false;
    const entries = trackers.Trackers;
    if (!entries || typeof entries !== "object" || Array.isArray(entries)) return false;
    const tracker = entries.AITHER;
    return Boolean(
      tracker && typeof tracker === "object" && !Array.isArray(tracker) && tracker.Anon,
    );
  };

  return createElement(
    "div",
    null,
    state.renderTrackerSection(false),
    createElement("button", { type: "button", onClick: state.handleSaveSettings }, "Save settings"),
    createElement(
      "button",
      { type: "button", disabled: !state.settingsDirty, onClick: state.handleSaveSettings },
      "Save changes",
    ),
    createElement("span", { "data-testid": "settings-dirty" }, String(state.settingsDirty)),
    createElement("span", { "data-testid": "settings-saved" }, state.settingsSaved),
    createElement("span", { "data-testid": "settings-error" }, state.settingsError),
    createElement(
      "span",
      { "data-testid": "active-anonymous" },
      String(trackerAnonymous(state.configData)),
    ),
    createElement(
      "span",
      { "data-testid": "draft-anonymous" },
      String(trackerAnonymous(state.settingsConfigData)),
    ),
    createElement(PayloadCapture, { value: state.buildSavePayload() }),
  );
}

function TrackerReloadRaceHarness() {
  const state = useSettingsState({ activeTab: "settings" });

  return createElement(
    "div",
    null,
    createElement(
      "button",
      { type: "button", disabled: state.settingsLoading, onClick: state.loadSettings },
      "Reload",
    ),
    createElement("span", { "data-testid": "settings-dirty" }, String(state.settingsDirty)),
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

function TrackerSettingsErrorHarness() {
  const state = useSettingsState({ activeTab: "settings" });
  return createElement("div", null, state.settingsError);
}

function InputTrackerSelectionHarness() {
  const state = useSettingsState({ activeTab: "input" });
  return createElement(
    "div",
    { "data-testid": "tracker-selection" },
    state.trackerSelectionNames.join(","),
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
  const screenshotHandling = state.settingsConfigData?.ScreenshotHandling;
  const config =
    screenshotHandling &&
    typeof screenshotHandling === "object" &&
    !Array.isArray(screenshotHandling)
      ? screenshotHandling
      : null;
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

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

type TestTrackerField = [key: string, defaultValue: ConfigValue, activation?: boolean];

/** Builds a typed tracker-catalog fixture without tracker-name logic in the hook. */
function trackerCatalogEntry(
  name: string,
  fields: TestTrackerField[],
  configured = false,
  family: TrackerCatalogEntry["family"] = "unit3d",
): TrackerCatalogEntry {
  return {
    name,
    family,
    baseURL: `https://${name.toLowerCase()}.example.invalid`,
    uploadContentMode: "description",
    configured,
    fields: fields.map(([key, defaultValue, activation = false]) => ({
      key,
      yamlKey: key,
      default: defaultValue,
      activation,
    })),
  };
}

/** Wraps catalog entries in the backend response shape. */
function trackerCatalog(...entries: TrackerCatalogEntry[]): TrackerCatalog {
  return { entries, unsupported: [] };
}

function aitherConfig(anonymous: boolean) {
  return JSON.stringify({
    Trackers: {
      DefaultTrackers: [],
      PreferredTracker: "",
      Trackers: { AITHER: { APIKey: "stored-token", Anon: anonymous } },
    },
  });
}

function aitherCatalog() {
  return trackerCatalog(
    trackerCatalogEntry("AITHER", [
      ["APIKey", "", true],
      ["Anon", false],
    ]),
  );
}

/** Captures save payloads without rendering secret-shaped values into DOM snapshots. */
function PayloadCapture({ value }: { value: string | null }) {
  latestPayload = value ?? "";
  return null;
}

function CatalogDefaultHarness() {
  const settings = useSettingsState({ activeTab: "settings" });
  const { catalog } = useTrackerCatalog();
  const defaults = catalog?.entries
    .filter((entry) => entry.configured && entry.default)
    .map((entry) => entry.name)
    .join(",");
  return createElement(
    "div",
    null,
    createElement("span", { "data-testid": "catalog-defaults" }, defaults),
    createElement("span", { "data-testid": "save-status" }, settings.settingsSaved),
    createElement(
      "button",
      { type: "button", disabled: settings.settingsLoading, onClick: settings.handleSaveSettings },
      "Save",
    ),
  );
}

describe("settings catalog invalidation", () => {
  it("refreshes release defaults after an active settings save", async () => {
    let activeDefault = "AITHER";
    const refresh = deferred<TrackerCatalog>();
    const catalogFor = (defaultName: string) =>
      trackerCatalog(
        ...["AITHER", "BLU"].map((name) => ({
          ...trackerCatalogEntry(name, [["APIKey", "", true]], true),
          default: name === defaultName,
        })),
      );
    const listCatalog = vi
      .fn<() => Promise<TrackerCatalog>>()
      .mockResolvedValueOnce(catalogFor("AITHER"))
      .mockImplementation(() => refresh.promise);
    installAppOperationMocks({
      GetConfig: async () => JSON.stringify({ Trackers: { DefaultTrackers: [activeDefault] } }),
      GetDefaultConfig: async () => JSON.stringify({}),
      GetImageHostPolicyMetadata: async () => ({}),
      ListTrackerCatalog: listCatalog,
      SaveConfig: async () => {
        activeDefault = "BLU";
        return { status: "active", activeGeneration: 2, impacts: [], updatedAt: "2026-09-25" };
      },
    });

    render(createElement(TrackerCatalogProvider, null, createElement(CatalogDefaultHarness)));
    await waitFor(() => expect(screen.getByTestId("catalog-defaults")).toHaveTextContent("AITHER"));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(listCatalog).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(screen.getByTestId("save-status")).toHaveTextContent("saved and applied"),
    );
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    await act(async () => refresh.resolve(catalogFor(activeDefault)));
    await waitFor(() => expect(screen.getByTestId("catalog-defaults")).toHaveTextContent("BLU"));
  });
});

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

describe("torrent creation settings", () => {
  it("identifies the 16 MiB preference as reusable-torrent selection", async () => {
    installAppOperationMocks({
      GetConfig: async () => JSON.stringify({ TorrentCreation: { PreferMax16: false } }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TorrentCreationHarness));

    expect(await screen.findByLabelText("Prefer reusable torrents up to 16 MiB")).not.toBeChecked();
  });
});

describe("DVD menu screenshot settings", () => {
  it("loads the default maximum and preserves edits in the save payload", async () => {
    installAppOperationMocks({
      GetConfig: async () => JSON.stringify({ ScreenshotHandling: { MaxMenuItems: 6 } }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
    });

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
    installAppOperationMocks({
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
              AutomaticManagementPaths: ["/media", "/archive"],
            },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TorrentClientsHarness));

    await waitFor(() => expect(screen.getByText("watcher")).toBeInTheDocument());

    const watchCard = screen.getByText("watcher").closest(".settings-card");
    const qbitCard = screen.getByText("qbit").closest(".settings-card");
    expect(watchCard).toBeTruthy();
    expect(qbitCard).toBeTruthy();
    expect(screen.getByRole("group", { name: "watcher" })).toBe(watchCard);
    expect(screen.getByRole("group", { name: "qbit" })).toBe(qbitCard);

    const watchScope = within(watchCard as HTMLElement);
    const qbitScope = within(qbitCard as HTMLElement);
    expect(watchScope.getByRole("button", { name: "Remove watcher" })).toBeInTheDocument();
    expect(qbitScope.getByRole("button", { name: "Remove qbit" })).toBeInTheDocument();

    expect(watchScope.getByLabelText("Type")).toHaveValue("watch");
    expect(watchScope.getByLabelText("Watch folder")).toHaveValue("/watch");
    expect(watchScope.getByLabelText("Storage directory")).toHaveValue("/storage");
    expect(qbitScope.getByLabelText("qBit URL")).toHaveValue("http://localhost:8080");
    expect(qbitScope.getByLabelText("Automatic management paths 1")).toHaveValue("/media");
    expect(qbitScope.getByLabelText("Automatic management paths 2")).toHaveValue("/archive");
    expect(
      qbitScope.getByRole("button", { name: "Remove Automatic management paths 1" }),
    ).toBeInTheDocument();
    expect(
      qbitScope.getByRole("button", { name: "Remove Automatic management paths 2" }),
    ).toBeInTheDocument();
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
      AutomaticManagementPaths: ["/media", "/archive"],
    });
  });

  it("clears selectors that reference a removed client", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          ClientSetup: {
            DefaultClient: "primary",
            InjectClients: ["primary", "backup"],
            SearchClients: [" PRIMARY ", "backup"],
          },
          Trackers: {
            Trackers: {
              AITHER: { TorrentClient: "PRIMARY" },
              BLU: { TorrentClient: "backup" },
            },
          },
          TorrentClients: {
            primary: {
              Type: "watch",
              WatchFolder: "incoming-primary",
              StorageDir: "library-primary",
            },
            backup: {
              Type: "watch",
              WatchFolder: "incoming-backup",
              StorageDir: "library-backup",
            },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("AITHER", [["TorrentClient", ""]]),
          trackerCatalogEntry("BLU", [["TorrentClient", ""]]),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TorrentClientsHarness));

    await waitFor(() => expect(screen.getByText("primary")).toBeInTheDocument());
    const primaryCard = screen.getByText("primary").closest(".settings-card");
    expect(primaryCard).toBeTruthy();
    fireEvent.click(
      within(primaryCard as HTMLElement).getByRole("button", { name: "Remove primary" }),
    );

    await waitFor(() => expect(screen.queryByText("primary")).not.toBeInTheDocument());
    const payload = readPayload<{
      ClientSetup?: {
        DefaultClient?: string;
        InjectClients?: string[];
        SearchClients?: string[];
      };
      Trackers?: { Trackers?: Record<string, { TorrentClient?: string }> };
      TorrentClients?: Record<string, Record<string, unknown>>;
    }>();
    expect(payload.TorrentClients?.primary).toBeUndefined();
    expect(payload.TorrentClients?.backup).toBeDefined();
    expect(payload.ClientSetup).toEqual({
      DefaultClient: "",
      InjectClients: ["backup"],
      SearchClients: ["backup"],
    });
    expect(payload.Trackers?.Trackers?.AITHER?.TorrentClient).toBe("");
    expect(payload.Trackers?.Trackers?.BLU?.TorrentClient).toBe("backup");
  });
});

describe("ClientSetup client selectors", () => {
  it("renders default client empty option without a none sentinel", async () => {
    installAppOperationMocks({
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
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
    });

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
    installAppOperationMocks({
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
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
    });

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
    installAppOperationMocks({
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
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("CZT", [
            ["LinkDirName", ""],
            ["Passkey", "", true],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

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
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: {},
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("CZT", [
            ["LinkDirName", ""],
            ["Passkey", "", true],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

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
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: {
              AITHER: {
                LinkDirName: "",
                APIKey: "tracker-token",
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
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("AITHER", [
            ["LinkDirName", ""],
            ["APIKey", "", true],
            ["ImageHost", ""],
            ["TorrentClient", ""],
            ["Anon", false],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

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
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: ["AITHER", "BLU", "BHD"],
            PreferredTracker: "",
            Trackers: {
              AITHER: {
                APIKey: "",
                Anon: false,
              },
              BLU: {
                APIKey: "",
                Anon: false,
              },
              BHD: {
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
                APIKey: "",
                Anon: false,
              },
              BLU: {
                APIKey: "",
                Anon: false,
              },
              BHD: {
                APIKey: "",
                Anon: false,
              },
            },
          },
        }),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          ...["AITHER", "BLU", "BHD"].map((name) =>
            trackerCatalogEntry(name, [
              ["APIKey", "", true],
              ["Anon", false],
            ]),
          ),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

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

  it("adopts authoritative normalized settings after an immediately active save", async () => {
    const encryptedAPIKey = "upbrr-enc:v1:encrypted-btn-api-key";
    const authoritativeAPIKey = "upbrr-enc:v1:normalized-btn-api-key";
    const savedAPIKeys: unknown[] = [];
    let getConfigCalls = 0;
    installAppOperationMocks({
      GetConfig: async () => {
        getConfigCalls += 1;
        const authoritative = getConfigCalls > 2;
        return JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: {
              BTN: {
                APIKey: authoritative ? authoritativeAPIKey : encryptedAPIKey,
                Username: authoritative ? "normalized-user" : "",
                Password: "",
              },
            },
          },
        });
      },
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry(
            "BTN",
            [
              ["APIKey", "", true],
              ["Username", "", true],
              ["Password", "", true],
            ],
            true,
            "standalone",
          ),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
      SaveConfig: async (payload: string) => {
        const saved = JSON.parse(payload) as {
          Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
        };
        savedAPIKeys.push(saved.Trackers?.Trackers?.BTN?.APIKey);
        return {
          status: "active",
          activeGeneration: 2,
          impacts: [],
          updatedAt: "2026-09-19T00:00:00Z",
        };
      },
    });

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("BTN", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("BTN", { selector: ".settings-card__summary-name" }));

    expect(screen.getByLabelText("API key")).toHaveValue("[REDACTED]");

    let payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.BTN?.APIKey === encryptedAPIKey).toBe(true);

    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "replacement-api-key" },
    });

    await waitFor(() =>
      expect(screen.getByLabelText("API key")).toHaveValue("replacement-api-key"),
    );

    payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.BTN?.APIKey === "replacement-api-key").toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Save settings" }));

    await waitFor(() => expect(screen.getByLabelText("API key")).toHaveValue("[REDACTED]"));
    await waitFor(() => expect(screen.getByLabelText("Username")).toHaveValue("normalized-user"));
    expect(savedAPIKeys).toEqual(["replacement-api-key"]);

    fireEvent.click(screen.getByRole("button", { name: "Save settings" }));

    await waitFor(() => expect(savedAPIKeys).toHaveLength(2));
    await waitFor(() => expect(getConfigCalls).toBe(4));
    expect(savedAPIKeys[1] === authoritativeAPIKey).toBe(true);
  });

  it("preserves newer edits while an immediately active config refresh completes", async () => {
    const config = aitherConfig(false);
    const activeRefresh = deferred<string>();
    const getConfig = vi
      .fn()
      .mockResolvedValueOnce(config)
      .mockResolvedValueOnce(config)
      .mockImplementationOnce(() => activeRefresh.promise);
    installAppOperationMocks({
      GetConfig: getConfig,
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => aitherCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
      SaveConfig: async () => ({
        status: "active" as const,
        activeGeneration: 2,
        impacts: ["trackers" as const],
        updatedAt: "2026-09-19T00:00:01Z",
      }),
    });

    render(createElement(TrackerSettingsHarness));
    await userEvent.click(
      await screen.findByText("AITHER", { selector: ".settings-card__summary-name" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
    await waitFor(() => expect(getConfig).toHaveBeenCalledTimes(2));

    fireEvent.click(screen.getByLabelText("Anonymous"));
    expect(screen.getByTestId("draft-anonymous")).toHaveTextContent("true");
    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");

    await act(async () => activeRefresh.resolve(config));

    expect(screen.getByTestId("active-anonymous")).toHaveTextContent("false");
    expect(screen.getByTestId("draft-anonymous")).toHaveTextContent("true");
    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");
  });

  it("refreshes config after the first activation lookup applies a pending candidate", async () => {
    const oldConfig = aitherConfig(false);
    const activeConfig = aitherConfig(true);
    const oldInitialRead = deferred<string>();
    const getConfig = vi
      .fn()
      .mockImplementationOnce(() => oldInitialRead.promise)
      .mockResolvedValueOnce(activeConfig);
    const getActivation = vi.fn(async () => ({
      status: "active" as const,
      activeGeneration: 2,
      impacts: ["trackers" as const],
      updatedAt: "2026-09-19T00:00:01Z",
    }));
    installAppOperationMocks({
      GetConfig: getConfig,
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => aitherCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
      GetConfigActivation: getActivation,
    });

    render(createElement(TrackerSettingsHarness));
    await waitFor(() => expect(getActivation).toHaveBeenCalledOnce());
    await act(async () => oldInitialRead.resolve(oldConfig));

    await waitFor(() => expect(getConfig).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId("active-anonymous")).toHaveTextContent("true");
    expect(screen.getByTestId("draft-anonymous")).toHaveTextContent("true");
    expect(screen.getByTestId("settings-saved")).toBeEmptyDOMElement();
  });

  it("preserves initial editor changes while the activation refresh completes", async () => {
    const oldConfig = aitherConfig(false);
    const activeConfig = aitherConfig(true);
    const oldInitialRead = deferred<string>();
    const activeRefresh = deferred<string>();
    const activationLookup = deferred<{
      status: "active";
      activeGeneration: number;
      impacts: Array<"trackers">;
      updatedAt: string;
    }>();
    const getConfig = vi
      .fn()
      .mockImplementationOnce(() => oldInitialRead.promise)
      .mockImplementationOnce(() => activeRefresh.promise);
    installAppOperationMocks({
      GetConfig: getConfig,
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => aitherCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
      GetConfigActivation: () => activationLookup.promise,
    });

    render(createElement(TrackerSettingsHarness));
    await act(async () => oldInitialRead.resolve(oldConfig));
    await userEvent.click(
      await screen.findByText("AITHER", { selector: ".settings-card__summary-name" }),
    );
    fireEvent.click(screen.getByLabelText("Anonymous"));
    fireEvent.click(screen.getByLabelText("Anonymous"));
    expect(screen.getByTestId("draft-anonymous")).toHaveTextContent("false");
    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");

    await act(async () =>
      activationLookup.resolve({
        status: "active",
        activeGeneration: 2,
        impacts: ["trackers"],
        updatedAt: "2026-09-19T00:00:01Z",
      }),
    );
    await waitFor(() => expect(getConfig).toHaveBeenCalledTimes(2));
    await act(async () => activeRefresh.resolve(activeConfig));

    await waitFor(() => expect(screen.getByTestId("active-anonymous")).toHaveTextContent("true"));
    expect(screen.getByTestId("draft-anonymous")).toHaveTextContent("false");
    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");
    expect(screen.getByTestId("settings-saved")).toBeEmptyDOMElement();
  });

  it("keeps a pending candidate in the editor until activation publishes it", async () => {
    let getConfigCalls = 0;
    const getActivation = vi
      .fn()
      .mockResolvedValueOnce({
        status: "active" as const,
        activeGeneration: 1,
        impacts: [],
        updatedAt: "2026-09-19T00:00:00Z",
      })
      .mockResolvedValueOnce({
        status: "active" as const,
        activeGeneration: 2,
        impacts: ["trackers" as const],
        updatedAt: "2026-09-19T00:00:02Z",
      });
    installAppOperationMocks({
      GetConfig: async () => {
        getConfigCalls += 1;
        return aitherConfig(getConfigCalls > 2);
      },
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => aitherCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
      SaveConfig: async () => ({
        status: "pending" as const,
        activeGeneration: 1,
        pendingGeneration: 2,
        impacts: ["trackers" as const],
        updatedAt: "2026-09-19T00:00:01Z",
      }),
      GetConfigActivation: getActivation,
    });

    render(createElement(TrackerSettingsHarness));
    await userEvent.click(
      await screen.findByText("AITHER", { selector: ".settings-card__summary-name" }),
    );
    await waitFor(() => expect(getActivation).toHaveBeenCalledOnce());
    vi.useFakeTimers();
    try {
      fireEvent.click(screen.getByLabelText("Anonymous"));
      fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
      await act(async () => Promise.resolve());

      expect(screen.getByTestId("draft-anonymous")).toHaveTextContent("true");
      expect(screen.getByTestId("active-anonymous")).toHaveTextContent("false");

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });

      expect(getActivation).toHaveBeenCalledTimes(2);
      expect(getConfigCalls).toBe(3);
      expect(screen.getByTestId("active-anonymous")).toHaveTextContent("true");
      expect(screen.getByTestId("settings-saved")).toHaveTextContent("Settings saved and applied.");
    } finally {
      vi.useRealTimers();
    }
  });

  it("retries a transient activation lookup until the durable config becomes active", async () => {
    let activationChecks = 0;
    const getActivation = vi.fn(async () => {
      activationChecks += 1;
      if (activationChecks === 2) throw new Error("activation status temporarily unavailable");
      return {
        status: "active" as const,
        activeGeneration: 2,
        impacts: ["trackers" as const],
        updatedAt: "2026-09-19T00:00:01Z",
      };
    });
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: { DefaultTrackers: [], PreferredTracker: "", Trackers: {} },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
      SaveConfig: async () => ({
        status: "pending" as const,
        activeGeneration: 1,
        pendingGeneration: 2,
        impacts: ["trackers" as const],
        updatedAt: "2026-09-19T00:00:00Z",
      }),
      GetConfigActivation: getActivation,
    });

    render(createElement(TrackerSettingsHarness));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Save settings" })).toBeEnabled(),
    );
    await waitFor(() => expect(getActivation).toHaveBeenCalledOnce());
    getActivation.mockClear();
    vi.useFakeTimers();
    try {
      fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
      await act(async () => Promise.resolve());
      expect(screen.getByTestId("settings-saved")).toHaveTextContent(
        "Settings saved. Waiting for the active input to close before applying.",
      );

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });
      expect(getActivation).toHaveBeenCalledOnce();
      expect(screen.getByTestId("settings-error")).toHaveTextContent(
        "activation status temporarily unavailable",
      );

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });
      expect(getActivation).toHaveBeenCalledTimes(2);
      expect(screen.getByTestId("settings-error")).toBeEmptyDOMElement();
      expect(screen.getByTestId("settings-saved")).toHaveTextContent("Settings saved and applied.");
    } finally {
      vi.useRealTimers();
    }
  });

  it("rediscovers a pending activation after an error and remount", async () => {
    const pending = {
      status: "pending" as const,
      activeGeneration: 1,
      pendingGeneration: 2,
      impacts: ["trackers" as const],
      updatedAt: "2026-09-19T00:00:00Z",
    };
    const active = {
      status: "active" as const,
      activeGeneration: 2,
      impacts: ["trackers" as const],
      updatedAt: "2026-09-19T00:00:02Z",
    };
    const firstDiscovery = deferred<typeof pending>();
    const resumedDiscovery = deferred<typeof pending>();
    const getActivation = vi
      .fn()
      .mockImplementationOnce(() => firstDiscovery.promise)
      .mockRejectedValueOnce(new Error("activation status temporarily unavailable"))
      .mockImplementationOnce(() => resumedDiscovery.promise)
      .mockResolvedValueOnce(active);
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: { DefaultTrackers: [], PreferredTracker: "", Trackers: {} },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
      SaveConfig: async () => active,
      GetConfigActivation: getActivation,
    });

    let mounted = render(createElement(TrackerSettingsHarness));
    await waitFor(() => expect(getActivation).toHaveBeenCalledOnce());
    vi.useFakeTimers();
    try {
      await act(async () => firstDiscovery.resolve(pending));
      expect(screen.getByTestId("settings-saved")).toHaveTextContent(
        "Settings saved. Waiting for the active input to close before applying.",
      );
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });
      expect(screen.getByTestId("settings-error")).toHaveTextContent(
        "activation status temporarily unavailable",
      );

      mounted.unmount();
      vi.clearAllTimers();
      vi.useRealTimers();
      mounted = render(createElement(TrackerSettingsHarness));
      await waitFor(() => expect(getActivation).toHaveBeenCalledTimes(3));

      vi.useFakeTimers();
      await act(async () => resumedDiscovery.resolve(pending));
      expect(screen.getByTestId("settings-saved")).toHaveTextContent(
        "Settings saved. Waiting for the active input to close before applying.",
      );
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });
      expect(getActivation).toHaveBeenCalledTimes(4);
      expect(screen.getByTestId("settings-saved")).toHaveTextContent("Settings saved and applied.");
      expect(screen.getByTestId("settings-error")).toBeEmptyDOMElement();
    } finally {
      mounted.unmount();
      vi.clearAllTimers();
      vi.useRealTimers();
    }
  });

  it("stops on terminal activation failure and keeps the candidate as a correctable draft", async () => {
    const getConfig = vi.fn(async () => aitherConfig(false));
    const getActivation = vi
      .fn()
      .mockResolvedValueOnce({
        status: "active" as const,
        activeGeneration: 1,
        impacts: [],
        updatedAt: "2026-09-19T00:00:00Z",
      })
      .mockResolvedValueOnce({
        status: "failed" as const,
        activeGeneration: 1,
        activationId: "activation-failed",
        failureCode: "validate_runtime" as const,
        impacts: ["trackers" as const],
        updatedAt: "2026-09-19T00:00:02Z",
      });
    let saveCalls = 0;
    installAppOperationMocks({
      GetConfig: getConfig,
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => aitherCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
      SaveConfig: async () => {
        saveCalls += 1;
        return saveCalls === 1
          ? {
              status: "pending" as const,
              activeGeneration: 1,
              pendingGeneration: 2,
              activationId: "activation-failed",
              impacts: ["trackers" as const],
              updatedAt: "2026-09-19T00:00:01Z",
            }
          : {
              status: "active" as const,
              activeGeneration: 2,
              activationId: "activation-corrected",
              impacts: ["trackers" as const],
              updatedAt: "2026-09-19T00:00:03Z",
            };
      },
      GetConfigActivation: getActivation,
    });

    render(createElement(TrackerSettingsHarness));
    await userEvent.click(
      await screen.findByText("AITHER", { selector: ".settings-card__summary-name" }),
    );
    await waitFor(() => expect(getActivation).toHaveBeenCalledOnce());
    getActivation.mockClear();
    vi.useFakeTimers();
    try {
      fireEvent.click(screen.getByLabelText("Anonymous"));
      fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
      await act(async () => Promise.resolve());
      expect(screen.getByTestId("settings-dirty")).toHaveTextContent("false");
      expect(screen.getByTestId("draft-anonymous")).toHaveTextContent("true");
      expect(screen.getByTestId("active-anonymous")).toHaveTextContent("false");

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });
      expect(getActivation).toHaveBeenCalledOnce();
      expect(screen.getByTestId("settings-error")).toHaveTextContent(
        "Settings could not be applied during runtime configuration validation. Review the settings and save again.",
      );
      expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");
      expect(screen.getByLabelText("Anonymous")).toBeChecked();
      expect(screen.getByTestId("draft-anonymous")).toHaveTextContent("true");
      expect(screen.getByTestId("active-anonymous")).toHaveTextContent("false");

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000);
      });
      expect(getActivation).toHaveBeenCalledOnce();

      fireEvent.click(screen.getByLabelText("Anonymous"));
      fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
      await act(async () => Promise.resolve());
      expect(saveCalls).toBe(2);
      expect(getConfig).toHaveBeenCalledTimes(3);
      expect(screen.getByTestId("settings-error")).toBeEmptyDOMElement();
      expect(screen.getByTestId("settings-saved")).toHaveTextContent("Settings saved and applied.");
      expect(screen.getByTestId("settings-dirty")).toHaveTextContent("false");
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps polling an accepted config when a newer save conflicts", async () => {
    const getActivation = vi.fn(async () => ({
      status: "active" as const,
      activeGeneration: 2,
      impacts: ["trackers" as const],
      updatedAt: "2026-09-19T00:00:02Z",
    }));
    let saveCalls = 0;
    installAppOperationMocks({
      GetConfig: async () => aitherConfig(false),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => aitherCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
      SaveConfig: async () => {
        saveCalls += 1;
        if (saveCalls === 1) {
          return {
            status: "pending" as const,
            activeGeneration: 1,
            pendingGeneration: 2,
            impacts: ["trackers" as const],
            updatedAt: "2026-09-19T00:00:01Z",
          };
        }
        throw new Error("another validated configuration is already pending activation");
      },
      GetConfigActivation: getActivation,
    });

    render(createElement(TrackerSettingsHarness));
    await userEvent.click(
      await screen.findByText("AITHER", { selector: ".settings-card__summary-name" }),
    );
    await waitFor(() => expect(getActivation).toHaveBeenCalledOnce());
    getActivation.mockClear();
    vi.useFakeTimers();
    try {
      fireEvent.click(screen.getByLabelText("Anonymous"));
      expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");

      fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
      await act(async () => Promise.resolve());
      expect(screen.getByTestId("settings-saved")).toHaveTextContent(
        "Settings saved. Waiting for the active input to close before applying.",
      );
      expect(screen.getByTestId("settings-dirty")).toHaveTextContent("false");

      fireEvent.click(screen.getByLabelText("Anonymous"));
      expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");

      fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
      await act(async () => Promise.resolve());
      expect(screen.getByTestId("settings-error")).toHaveTextContent(
        "another validated configuration is already pending activation",
      );
      expect(getActivation).not.toHaveBeenCalled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });
      expect(getActivation).toHaveBeenCalledOnce();
      expect(screen.getByTestId("settings-saved")).toHaveTextContent(
        "Earlier changes applied. Newer edits remain unsaved.",
      );
      expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");
      expect(screen.getByLabelText("Anonymous")).not.toBeChecked();
    } finally {
      vi.useRealTimers();
    }
  });

  it("renders BTN announce URL from tracker schema when stored config lacks the key", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: {
              BTN: {
                APIKey: "tracker-token",
                Username: "",
                Password: "",
              },
            },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry(
            "BTN",
            [
              ["APIKey", "", true],
              ["Username", "", true],
              ["Password", "", true],
              ["AnnounceURL", "", true],
            ],
            false,
            "standalone",
          ),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("BTN", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("BTN", { selector: ".settings-card__summary-name" }));

    expect(screen.getByLabelText("Announce URL")).toHaveValue("");
  });

  it("hides LST image host selection when Lostimg is enabled", async () => {
    installAppOperationMocks({
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
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("LST", [
            ["LinkDirName", ""],
            ["APIKey", "", true],
            ["ImageHost", ""],
            ["Anon", false],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({
        TrackerUploadHosts: { LST: ["lostimg"] },
        OwnedHosts: { lostimg: "LST" },
      }),
    });

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("LST", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("LST", { selector: ".settings-card__summary-name" }));

    expect(screen.queryByLabelText("Image host")).not.toBeInTheDocument();
  });

  it("shows configured global hosts for LST when Lostimg is disabled", async () => {
    installAppOperationMocks({
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
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("LST", [
            ["LinkDirName", ""],
            ["APIKey", "", true],
            ["ImageHost", ""],
            ["Anon", false],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({
        TrackerUploadHosts: { LST: ["lostimg"] },
        OwnedHosts: { lostimg: "LST" },
      }),
    });

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

  it("hides RF image host selection when ReelFliX is enabled", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          ImageHosting: {
            ReelflixEnabled: true,
            ReelflixAPI: "secret",
          },
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: {
              RF: {
                LinkDirName: "",
                APIKey: "tracker-token",
                ImageHost: "",
                Anon: false,
              },
            },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("RF", [
            ["LinkDirName", ""],
            ["APIKey", "", true],
            ["ImageHost", ""],
            ["Anon", false],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({
        TrackerUploadHosts: { RF: ["reelflix"] },
        OwnedHosts: { reelflix: "RF" },
      }),
    });

    render(createElement(TrackerSettingsAdvancedHarness));

    await waitFor(() =>
      expect(
        screen.getByText("RF", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("RF", { selector: ".settings-card__summary-name" }));

    expect(screen.queryByLabelText("Image host")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Image API")).not.toBeInTheDocument();
  });

  it("shows configured global hosts for RF when Reelflix is disabled", async () => {
    installAppOperationMocks({
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
                ImageHost: "",
                Anon: false,
              },
            },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("RF", [
            ["LinkDirName", ""],
            ["APIKey", "", true],
            ["ImageHost", ""],
            ["Anon", false],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({
        TrackerUploadHosts: { RF: ["reelflix"] },
        OwnedHosts: { reelflix: "RF" },
      }),
    });

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

describe("tracker catalog loading", () => {
  it("loads configured tracker selections outside the settings page", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: { Trackers: { BTN: { APIKey: "configured" } } },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(trackerCatalogEntry("BTN", [["APIKey", "", true]], true, "standalone")),
    });

    render(createElement(InputTrackerSelectionHarness));

    expect(await screen.findByTestId("tracker-selection")).toHaveTextContent("BTN");
  });
});

describe("tracker catalog interactions", () => {
  it("preserves a tracker removal completed while a reload is in flight", async () => {
    const config = JSON.stringify({
      Trackers: {
        DefaultTrackers: [],
        PreferredTracker: "",
        Trackers: { BLU: { APIKey: "tracker-token" } },
      },
    });
    const pendingReload = deferred<string>();
    let request = 0;
    const getConfig = vi.fn(() => {
      request += 1;
      return request <= 2 ? Promise.resolve(config) : pendingReload.promise;
    });
    installAppOperationMocks({
      GetConfig: getConfig,
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(trackerCatalogEntry("BLU", [["APIKey", "", true]])),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerReloadRaceHarness));

    const cardName = await screen.findByText("BLU", {
      selector: ".settings-card__summary-name",
    });
    await waitFor(() => expect(screen.getByRole("button", { name: "Reload" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(getConfig).toHaveBeenCalledTimes(3));

    const card = cardName.closest(".settings-card");
    fireEvent.click(within(card as HTMLElement).getByRole("button", { name: "Remove BLU" }));
    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");

    pendingReload.resolve(config);

    await waitFor(() => expect(screen.getByRole("button", { name: "Reload" })).toBeEnabled());
    expect(
      screen.queryByText("BLU", { selector: ".settings-card__summary-name" }),
    ).not.toBeInTheDocument();
    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");
  });

  it("renders configured RHD without a frontend tracker schema entry", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: { RHD: { APIKey: "tracker-token", Anon: false } },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("RHD", [
            ["APIKey", "", true],
            ["Anon", false],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));

    expect(
      await screen.findByText("RHD", { selector: ".settings-card__summary-name" }),
    ).toBeInTheDocument();
  });

  it("adds a synthetic Unit3D tracker and preserves catalog field order", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: { DefaultTrackers: [], PreferredTracker: "", Trackers: {} },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("EXAMPLE", [
            ["UploaderName", ""],
            ["APIKey", "", true],
            ["ImageHost", ""],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));

    const trackerSelect = await screen.findByDisplayValue("Select tracker");
    fireEvent.change(trackerSelect, { target: { value: "EXAMPLE" } });
    fireEvent.click(screen.getByRole("button", { name: "Add entry" }));

    const card = (
      await screen.findByText("EXAMPLE", { selector: ".settings-card__summary-name" })
    ).closest(".settings-card");
    expect(card).toBeTruthy();
    const labels = Array.from(
      (card as HTMLElement).querySelectorAll<HTMLSpanElement>("label.settings-field > span"),
    ).map((label) => label.textContent);
    expect(labels).toEqual(["Uploader name", "API key", "Image host"]);
  });

  it("shows a tracker as configured when only one required credential is present", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: { BTN: { Username: "user", Password: "" } },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry(
            "BTN",
            [
              ["Username", "", true],
              ["Password", "", true],
            ],
            true,
            "standalone",
          ),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));

    expect(
      await screen.findByText("BTN", { selector: ".settings-card__summary-name" }),
    ).toBeInTheDocument();
  });

  it("removes an entry by resetting defaults and returning it to the selector", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: ["AITHER"],
            PreferredTracker: "AITHER",
            Trackers: { AITHER: { APIKey: "tracker-token", Anon: true } },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("AITHER", [
            ["APIKey", "", true],
            ["Anon", false],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));

    const cardName = await screen.findByText("AITHER", {
      selector: ".settings-card__summary-name",
    });
    const card = cardName.closest(".settings-card");
    fireEvent.click(within(card as HTMLElement).getByRole("button", { name: "Remove AITHER" }));

    await waitFor(() =>
      expect(
        screen.queryByText("AITHER", { selector: ".settings-card__summary-name" }),
      ).not.toBeInTheDocument(),
    );
    const availableSelector = screen.getByDisplayValue("Select tracker");
    expect(within(availableSelector).getByRole("option", { name: "AITHER" })).toBeInTheDocument();
    const payload = readPayload<{
      Trackers?: {
        DefaultTrackers?: string[];
        PreferredTracker?: string;
        Trackers?: Record<string, Record<string, unknown>>;
      };
    }>();
    expect(payload.Trackers?.DefaultTrackers).toEqual([]);
    expect(payload.Trackers?.PreferredTracker).toBe("");
    expect(payload.Trackers?.Trackers?.AITHER).toEqual({ APIKey: "", Anon: false });
  });

  it("names tracker actions by their configured or unsupported entry", async () => {
    const catalog = trackerCatalog(
      trackerCatalogEntry("AITHER", [["APIKey", "", true]]),
      trackerCatalogEntry("BTN", [["APIKey", "", true]]),
    );
    catalog.unsupported = ["OLD", "LEGACY"];
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            Trackers: {
              AITHER: { APIKey: "token" },
              BTN: { APIKey: "token" },
              OLD: {},
              LEGACY: {},
            },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => catalog,
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));
    expect(await screen.findByRole("button", { name: "Remove AITHER" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove BTN" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete OLD" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete LEGACY" })).toBeInTheDocument();
  });

  it("shows and toggles a saved default tracker regardless of its casing", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: { DefaultTrackers: ["aither"], Trackers: { AITHER: { APIKey: "token" } } },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(trackerCatalogEntry("AITHER", [["APIKey", "", true]])),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));
    await screen.findByText("AITHER", { selector: ".settings-card__summary-name" });
    await userEvent.click(
      screen.getByText("Default trackers", { selector: ".tracker-summary-heading span" }),
    );

    const tracker = screen.getByRole("checkbox", { name: "AITHER" });
    expect(tracker).toBeChecked();
    expect(screen.getByText("1/1")).toBeInTheDocument();

    await userEvent.click(tracker);
    expect(
      readPayload<{ Trackers: { DefaultTrackers: string[] } }>().Trackers.DefaultTrackers,
    ).toEqual([]);
    await userEvent.click(tracker);
    expect(
      readPayload<{ Trackers: { DefaultTrackers: string[] } }>().Trackers.DefaultTrackers,
    ).toEqual(["AITHER"]);
  });

  it("separates unsupported entries and deletes them without making them selectable", async () => {
    const catalog = trackerCatalog(trackerCatalogEntry("AITHER", [["APIKey", "", true]]));
    catalog.unsupported = ["OLD"];
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: { OLD: { APIKey: "preserved" } },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => catalog,
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));

    const unsupported = await screen.findByText("Unsupported tracker entries");
    expect(unsupported).toBeInTheDocument();
    const oldCard = screen
      .getByText("OLD", { selector: ".settings-card__summary-name" })
      .closest(".settings-card");
    expect(screen.queryByRole("option", { name: "OLD" })).not.toBeInTheDocument();
    fireEvent.click(within(oldCard as HTMLElement).getByRole("button", { name: "Delete OLD" }));

    await waitFor(() =>
      expect(
        screen.queryByText("OLD", { selector: ".settings-card__summary-name" }),
      ).not.toBeInTheDocument(),
    );
    const payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.OLD).toBeUndefined();
  });

  it("edits tracker group policies as comma-separated lists and preserves legacy Internal", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: {
              NBL: {
                APIKey: "tracker-token",
                Internal: false,
                DupeBypassGroups: ["NTb"],
                PersonalReleaseGroups: [],
                InternalGroups: ["GRP"],
              },
            },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("NBL", [
            ["APIKey", "", true],
            ["DupeBypassGroups", []],
            ["PersonalReleaseGroups", []],
            ["InternalGroups", []],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));

    const cardName = await screen.findByText("NBL", {
      selector: ".settings-card__summary-name",
    });
    fireEvent.click(cardName);

    expect(screen.queryByLabelText("Internal")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Duplicate bypass groups")).toHaveValue("NTb");
    fireEvent.blur(screen.getByLabelText("Duplicate bypass groups"));
    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("false");
    fireEvent.change(screen.getByLabelText("Duplicate bypass groups"), {
      target: { value: " NTb, -GRP, ntb " },
    });
    fireEvent.blur(screen.getByLabelText("Duplicate bypass groups"));

    await waitFor(() =>
      expect(screen.getByLabelText("Duplicate bypass groups")).toHaveValue("NTb, GRP"),
    );
    const payload = readPayload<{
      Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
    }>();
    expect(payload.Trackers?.Trackers?.NBL?.DupeBypassGroups).toEqual(["NTb", "GRP"]);
    expect(payload.Trackers?.Trackers?.NBL?.Internal).toBe(false);
    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");
  });

  it.each([
    ["Duplicate bypass groups", "DupeBypassGroups"],
    ["Personal release groups", "PersonalReleaseGroups"],
    ["Internal groups", "InternalGroups"],
  ] as const)("enables and saves while typing in %s", async (label, key) => {
    const user = userEvent.setup();
    let saved: Record<string, unknown> | undefined;
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: {
              NBL: {
                APIKey: "tracker-token",
                DupeBypassGroups: [],
                PersonalReleaseGroups: [],
                InternalGroups: [],
              },
            },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("NBL", [
            ["APIKey", "", true],
            ["DupeBypassGroups", []],
            ["PersonalReleaseGroups", []],
            ["InternalGroups", []],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
      SaveConfig: async (payload: string) => {
        const config = JSON.parse(payload) as {
          Trackers?: { Trackers?: Record<string, Record<string, unknown>> };
        };
        saved = config.Trackers?.Trackers?.NBL;
        return {
          status: "active",
          activeGeneration: 2,
          impacts: [],
          updatedAt: "2026-09-19T00:00:00Z",
        };
      },
    });

    render(createElement(TrackerSettingsHarness));

    const cardName = await screen.findByText("NBL", {
      selector: ".settings-card__summary-name",
    });
    await user.click(cardName);
    const saveButton = screen.getByRole("button", { name: "Save changes" });
    expect(saveButton).toBeDisabled();
    await user.type(screen.getByLabelText(label), "GRP");

    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");
    expect(saveButton).toBeEnabled();
    await user.click(saveButton);

    await waitFor(() => expect(saved?.[key]).toEqual(["GRP"]));
    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("false");
  });

  it("preserves a sequentially typed group that temporarily matches an existing group", async () => {
    const user = userEvent.setup();
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: { NBL: { APIKey: "tracker-token", InternalGroups: ["GRP"] } },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry("NBL", [
            ["APIKey", "", true],
            ["InternalGroups", []],
          ]),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));

    await user.click(await screen.findByText("NBL", { selector: ".settings-card__summary-name" }));
    const input = screen.getByLabelText("Internal groups");
    await user.type(input, ", GRP2");

    expect(input).toHaveValue("GRP, GRP2");
    expect(screen.getByTestId("settings-dirty")).toHaveTextContent("true");
    fireEvent.blur(input);
    expect(
      readPayload<{ Trackers?: { Trackers?: { NBL?: { InternalGroups?: string[] } } } }>().Trackers
        ?.Trackers?.NBL?.InternalGroups,
    ).toEqual(["GRP", "GRP2"]);
  });

  it("reports a stable error for an unknown catalog field", async () => {
    installAppOperationMocks({
      GetConfig: async () => JSON.stringify({ Trackers: { Trackers: {} } }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(trackerCatalogEntry("BROKEN", [["UnknownField", "", true]])),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsErrorHarness));

    expect(
      await screen.findByText(/Unsupported tracker config field: UnknownField/),
    ).toBeInTheDocument();
  });
});

describe("tracker advanced fields", () => {
  it("hides only the tracker advanced allowlist when advanced is closed", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          Trackers: {
            DefaultTrackers: [],
            PreferredTracker: "",
            Trackers: {
              BTN: {
                FaviconURL: "https://example.test/favicon.ico",
                LinkDirName: "btn",
                APIKey: "api-key",
                Username: "user",
                Password: "pass",
                AnnounceURL: "https://example.test/announce",
                Anon: false,
                OTPURI: "otpauth://totp/example",
                SkipIfRehash: true,
              },
            },
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () =>
        trackerCatalog(
          trackerCatalogEntry(
            "BTN",
            [
              ["FaviconURL", ""],
              ["LinkDirName", ""],
              ["APIKey", "", true],
              ["Username", "", true],
              ["Password", "", true],
              ["AnnounceURL", "", true],
              ["Anon", false],
              ["OTPURI", ""],
              ["SkipIfRehash", false],
            ],
            false,
            "standalone",
          ),
        ),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));

    await waitFor(() =>
      expect(
        screen.getByText("BTN", { selector: ".settings-card__summary-name" }),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("BTN", { selector: ".settings-card__summary-name" }));

    expect(screen.queryByLabelText("Favicon URL")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Link dir name")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Skip if rehash")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Announce URL")).toBeInTheDocument();
    expect(screen.getByLabelText("OTP URI")).toBeInTheDocument();
  });
});

describe("Image hosting settings", () => {
  it("shows a saved host missing from current priority options", async () => {
    installAppOperationMocks({
      GetConfig: async () => JSON.stringify({ ImageHosting: { Host1: "lostimg" } }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(ImageHostingHarness));

    const hostOne = await screen.findByRole("combobox", { name: "Host 1" });
    expect(hostOne).toHaveValue("lostimg");
    expect(within(hostOne).getByRole("option", { name: "lostimg (saved)" })).toBeInTheDocument();
    expect(readPayload<{ ImageHosting: { Host1: string } }>().ImageHosting.Host1).toBe("lostimg");
  });

  it("renders Lostimg config and keeps it out of global host priority", async () => {
    installAppOperationMocks({
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
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(ImageHostingHarness));

    await waitFor(() => expect(screen.getByLabelText("Lostimg enabled")).toBeInTheDocument());

    const hostOne = screen.getByLabelText("Host 1") as HTMLSelectElement;
    expect(Array.from(hostOne.options).map((option) => option.value)).not.toContain("lostimg");

    fireEvent.click(screen.getByLabelText("Lostimg enabled"));
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "secret" },
    });

    await waitFor(() => expect(screen.getByLabelText("Lostimg enabled")).toBeChecked());

    const payload = readPayload<{
      ImageHosting?: {
        LostimgEnabled?: boolean;
        LostimgAPI?: string;
      };
    }>();
    expect(payload.ImageHosting?.LostimgEnabled).toBe(true);
    expect(payload.ImageHosting?.LostimgAPI).toBe("secret");
  });

  it("renders ReelFliX config and keeps it out of global host priority", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({
          ImageHosting: {
            Host1: "",
            Host2: "",
            Host3: "",
            Host4: "",
            Host5: "",
            Host6: "",
            ReelflixEnabled: false,
            ReelflixAPI: "",
          },
        }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(ImageHostingHarness));

    await waitFor(() => expect(screen.getByLabelText("Host 1")).toBeInTheDocument());

    const hostOne = screen.getByLabelText("Host 1") as HTMLSelectElement;
    expect(Array.from(hostOne.options).map((option) => option.value)).not.toContain("reelflix");

    fireEvent.click(screen.getByLabelText("ReelFliX enabled"));
    fireEvent.change(screen.getByLabelText("ReelFliX API key"), {
      target: { value: "secret" },
    });

    await waitFor(() => expect(screen.getByLabelText("ReelFliX enabled")).toBeChecked());

    const payload = readPayload<{
      ImageHosting?: {
        ReelflixEnabled?: boolean;
        ReelflixAPI?: string;
      };
    }>();
    expect(payload.ImageHosting?.ReelflixEnabled).toBe(true);
    expect(payload.ImageHosting?.ReelflixAPI === "secret").toBe(true);
    expect(screen.queryByLabelText("Image API")).not.toBeInTheDocument();
  });
});

describe("Saved tracker selection", () => {
  it("shows a preferred tracker absent from the current catalog", async () => {
    installAppOperationMocks({
      GetConfig: async () =>
        JSON.stringify({ Trackers: { PreferredTracker: "LEGACY", Trackers: {} } }),
      GetDefaultConfig: async () => JSON.stringify({}),
      ListTrackerCatalog: async () => trackerCatalog(),
      GetImageHostPolicyMetadata: async () => ({}),
    });

    render(createElement(TrackerSettingsHarness));
    await userEvent.click(await screen.findByText("Preferred tracker data source"));

    const preferred = screen.getByRole("combobox", { name: "Preferred tracker data source" });
    expect(preferred).toHaveValue("LEGACY");
    expect(within(preferred).getByRole("option", { name: "LEGACY (saved)" })).toBeInTheDocument();
    expect(
      readPayload<{ Trackers: { PreferredTracker: string } }>().Trackers.PreferredTracker,
    ).toBe("LEGACY");
  });
});
