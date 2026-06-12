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
    createElement("pre", { "data-testid": "payload" }, state.buildSavePayload() ?? ""),
  );
}

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
    expect(qbitScope.getByLabelText("qBit direct")).toBeChecked();

    fireEvent.change(watchScope.getByLabelText("Watch folder"), {
      target: { value: "/watch/new" },
    });

    await waitFor(() =>
      expect(watchScope.getByLabelText("Watch folder")).toHaveValue("/watch/new"),
    );

    const payload = JSON.parse(screen.getByTestId("payload").textContent ?? "{}") as {
      TorrentClients?: Record<string, Record<string, unknown>>;
    };
    expect(payload.TorrentClients?.watcher).toEqual({
      Type: "watch",
      WatchFolder: "/watch/new",
      StorageDir: "/storage",
    });
    expect(payload.TorrentClients?.qbit).toMatchObject({
      QbitURL: "http://localhost:8080",
      QbitUser: "user",
      QbitPass: "secret",
    });
  });
});
