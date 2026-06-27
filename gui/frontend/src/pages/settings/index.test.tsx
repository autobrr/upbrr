// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { screen, within } from "@testing-library/dom";
import { cleanup, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import SettingsPage from ".";
import type { ConfigValue } from "../../types";

const baseProps = {
  configData: { MainSettings: { Instance: "default" }, Trackers: {} },
  settingsLoading: false,
  settingsExporting: false,
  settingsImporting: false,
  settingsDirty: false,
  settingsSaved: "",
  settingsError: "",
  trackerSelectionNames: [
    "AR",
    "COOKIE",
    "FAST",
    "MTV",
    "PTP",
    "RTF",
    "SLOW",
    "PLAINAPI",
    "PLAINPASS",
  ],
  configOpStatus: null,
  dismissConfigOpStatus: vi.fn(),
  settingsSection: "main_settings",
  settingsSections: [
    { key: "main_settings", jsonKey: "MainSettings", label: "Main" },
    { key: "trackers", jsonKey: "Trackers", label: "Trackers" },
  ],
  showAdvancedToggle: false,
  advancedOpen: false,
  setSettingsAdvanced: vi.fn(),
  loadSettings: vi.fn(),
  handleExportSettings: vi.fn(),
  handleImportConfig: vi.fn(),
  importConfirmOpen: false,
  handleImportConfigConfirm: vi.fn(),
  handleImportConfigCancel: vi.fn(),
  handleSaveSettings: vi.fn(),
  webAuthAvailable: false,
  webAuthStatus: null,
  webAuthLoading: false,
  webAuthCreating: false,
  webAuthUsername: "",
  webAuthPassword: "",
  webAuthConfirm: "",
  webAuthError: "",
  setWebAuthUsername: vi.fn(),
  setWebAuthPassword: vi.fn(),
  setWebAuthConfirm: vi.fn(),
  handleCreateWebAuth: vi.fn(),
  renderImageHostingSection: vi.fn(() => null),
  renderTrackerSection: vi.fn(() => null),
  renderTorrentClientsSection: vi.fn(() => null),
  renderField: vi.fn((label: string, _value: ConfigValue, path: string[]) => (
    <div key={path.join(".")}>{label}</div>
  )),
  sectionFieldMeta: {},
};

const trackerAuthCapability = {
  trackerID: "MTV",
  displayName: "MTV",
  authKind: "api_key_cookies_login",
  supportsCookieFile: true,
  supportsLogin: true,
  supportsAutoLogin: true,
  supportsTOTP: true,
  supportsManual2FA: true,
  requiresAPIKey: true,
  requiresPasskey: false,
};

const trackerAuthStatus = (message: string) => ({
  trackerID: "MTV",
  displayName: "MTV",
  state: "configured",
  cookieCount: 1,
  lastCheckedAt: "",
  lastError: "",
  encryptedStorage: true,
  needs2FA: false,
  challengeID: "",
  message,
});

const deferred = <T,>() => {
  let resolve: (value: T) => void = () => undefined;
  let reject: (reason?: unknown) => void = () => undefined;
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
};

describe("SettingsPage", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.clearAllMocks();
    delete (globalThis as any).go;
  });

  it("renders application details as the final tab", async () => {
    const setSettingsSection = vi.fn();
    const { container, rerender } = render(
      <SettingsPage {...baseProps} setSettingsSection={setSettingsSection} />,
    );
    const settingsTags = container.querySelector(".settings-tags");

    expect(settingsTags).not.toBeNull();
    expect(
      within(settingsTags as HTMLElement)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual(["Main", "Trackers", "Application Details", "Tracker Auth"]);
    expect(screen.queryByText("autobrr/upbrr")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Application Details" }));

    expect(setSettingsSection).toHaveBeenCalledWith("application_details");

    rerender(
      <SettingsPage
        {...baseProps}
        settingsSection="application_details"
        setSettingsSection={setSettingsSection}
      />,
    );

    expect(screen.getByText("autobrr/upbrr")).toBeInTheDocument();
    expect(screen.queryByText("Copyright")).not.toBeInTheDocument();
    expect(screen.queryByText("Copyright (c) 2026 autobrr")).not.toBeInTheDocument();
  });

  it("renders tracker auth as the bottom tab", async () => {
    const setSettingsSection = vi.fn();
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([]),
          GetTrackerAuthStatus: vi.fn(),
        },
      },
    });

    render(<SettingsPage {...baseProps} setSettingsSection={setSettingsSection} />);

    await userEvent.click(screen.getByRole("button", { name: "Tracker Auth" }));

    expect(setSettingsSection).toHaveBeenCalledWith("tracker_auth");
  });

  it("shows Test Auth only for adapter-backed tracker auth", async () => {
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([
            {
              trackerID: "MTV",
              displayName: "MTV",
              authKind: "api_key_cookies_login",
              supportsCookieFile: true,
              supportsLogin: true,
              supportsAutoLogin: true,
              supportsTOTP: true,
              supportsManual2FA: true,
              requiresAPIKey: true,
              requiresPasskey: false,
            },
            {
              trackerID: "AR",
              displayName: "AR",
              authKind: "cookies_login",
              supportsCookieFile: true,
              supportsLogin: true,
              supportsAutoLogin: true,
              supportsTOTP: false,
              supportsManual2FA: false,
              requiresAPIKey: false,
              requiresPasskey: false,
            },
          ]),
          GetTrackerAuthStatus: vi.fn().mockImplementation((trackerID: string) =>
            Promise.resolve({
              trackerID,
              displayName: trackerID,
              state: "configured",
              cookieCount: 0,
              lastCheckedAt: "",
              lastError: "",
              encryptedStorage: true,
              needs2FA: false,
              challengeID: "",
              message: "required config auth material is present",
            }),
          ),
          TestTrackerAuth: vi.fn(),
        },
      },
    });

    render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    const mtvTitle = await screen.findByText("MTV");
    const arTitle = await screen.findByText("AR");
    const mtvCard = mtvTitle.closest(".tracker-auth-card");
    const arCard = arTitle.closest(".tracker-auth-card");

    expect(mtvCard).not.toBeNull();
    expect(arCard).not.toBeNull();
    expect(
      within(mtvCard as HTMLElement).getByRole("button", { name: "Test Auth" }),
    ).toBeInTheDocument();
    expect(
      within(arCard as HTMLElement).queryByRole("button", { name: "Test Auth" }),
    ).not.toBeInTheDocument();
  });

  it("renders fast tracker auth statuses before slow statuses resolve", async () => {
    let resolveSlow: (value: unknown) => void = () => undefined;
    const slowStatus = new Promise((resolve) => {
      resolveSlow = resolve;
    });
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([
            {
              trackerID: "FAST",
              displayName: "FAST",
              authKind: "cookies",
              supportsCookieFile: true,
              supportsLogin: false,
              supportsAutoLogin: false,
              supportsTOTP: false,
              supportsManual2FA: false,
              requiresAPIKey: false,
              requiresPasskey: false,
            },
            {
              trackerID: "SLOW",
              displayName: "SLOW",
              authKind: "cookies",
              supportsCookieFile: true,
              supportsLogin: false,
              supportsAutoLogin: false,
              supportsTOTP: false,
              supportsManual2FA: false,
              requiresAPIKey: false,
              requiresPasskey: false,
            },
          ]),
          GetTrackerAuthStatus: vi.fn().mockImplementation((trackerID: string) => {
            if (trackerID === "SLOW") {
              return slowStatus;
            }
            return Promise.resolve({
              trackerID,
              displayName: trackerID,
              state: "configured",
              cookieCount: 0,
              lastCheckedAt: "",
              lastError: "",
              encryptedStorage: true,
              needs2FA: false,
              challengeID: "",
              message: "fast ready",
            });
          }),
        },
      },
    });

    render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    expect(await screen.findByText("fast ready")).toBeInTheDocument();
    expect(screen.queryByText("slow ready")).not.toBeInTheDocument();

    resolveSlow({
      trackerID: "SLOW",
      displayName: "SLOW",
      state: "configured",
      cookieCount: 0,
      lastCheckedAt: "",
      lastError: "",
      encryptedStorage: true,
      needs2FA: false,
      challengeID: "",
      message: "slow ready",
    });

    expect(await screen.findByText("slow ready")).toBeInTheDocument();
  });

  it("hides plain tracker auth capabilities before loading statuses", async () => {
    const getStatus = vi.fn().mockImplementation((trackerID: string) =>
      Promise.resolve({
        trackerID,
        displayName: trackerID,
        state: "configured",
        cookieCount: 0,
        lastCheckedAt: "",
        lastError: "",
        encryptedStorage: true,
        needs2FA: false,
        challengeID: "",
        message: `${trackerID} ready`,
      }),
    );
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([
            {
              trackerID: "PLAINAPI",
              displayName: "PLAINAPI",
              authKind: "api_key",
              supportsCookieFile: false,
              supportsLogin: false,
              supportsAutoLogin: false,
              supportsTOTP: false,
              supportsManual2FA: false,
              requiresAPIKey: true,
              requiresPasskey: false,
            },
            {
              trackerID: "PLAINPASS",
              displayName: "PLAINPASS",
              authKind: "passkey",
              supportsCookieFile: false,
              supportsLogin: false,
              supportsAutoLogin: false,
              supportsTOTP: false,
              supportsManual2FA: false,
              requiresAPIKey: false,
              requiresPasskey: true,
            },
            {
              trackerID: "COOKIE",
              displayName: "COOKIE",
              authKind: "cookies",
              supportsCookieFile: true,
              supportsLogin: false,
              supportsAutoLogin: false,
              supportsTOTP: false,
              supportsManual2FA: false,
              requiresAPIKey: false,
              requiresPasskey: false,
            },
            {
              trackerID: "RTF",
              displayName: "RTF",
              authKind: "api_key_credential_refresh",
              supportsCookieFile: false,
              supportsLogin: false,
              supportsAutoLogin: false,
              supportsTOTP: false,
              supportsManual2FA: false,
              requiresAPIKey: true,
              requiresPasskey: false,
            },
          ]),
          GetTrackerAuthStatus: getStatus,
        },
      },
    });

    render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    expect(await screen.findByText("COOKIE ready")).toBeInTheDocument();
    expect(await screen.findByText("RTF ready")).toBeInTheDocument();
    expect(screen.queryByText("PLAINAPI")).not.toBeInTheDocument();
    expect(screen.queryByText("PLAINPASS")).not.toBeInTheDocument();
    expect(getStatus).toHaveBeenCalledTimes(2);
    expect(getStatus).toHaveBeenCalledWith("COOKIE");
    expect(getStatus).toHaveBeenCalledWith("RTF");
  });

  it("hides managed tracker auth capabilities for trackers not configured in main trackers", async () => {
    const getStatus = vi.fn().mockImplementation((trackerID: string) =>
      Promise.resolve({
        trackerID,
        displayName: trackerID,
        state: "configured",
        cookieCount: 0,
        lastCheckedAt: "",
        lastError: "",
        encryptedStorage: true,
        needs2FA: false,
        challengeID: "",
        message: `${trackerID} ready`,
      }),
    );
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([
            {
              trackerID: "ASC",
              displayName: "ASC",
              authKind: "cookies",
              supportsCookieFile: true,
              supportsLogin: false,
              supportsAutoLogin: false,
              supportsTOTP: false,
              supportsManual2FA: false,
              requiresAPIKey: false,
              requiresPasskey: false,
            },
            trackerAuthCapability,
          ]),
          GetTrackerAuthStatus: getStatus,
        },
      },
    });

    render(
      <SettingsPage
        {...baseProps}
        settingsSection="tracker_auth"
        trackerSelectionNames={["MTV"]}
        setSettingsSection={vi.fn()}
      />,
    );

    expect(await screen.findByText("MTV ready")).toBeInTheDocument();
    expect(screen.queryByText("ASC")).not.toBeInTheDocument();
    expect(getStatus).toHaveBeenCalledTimes(1);
    expect(getStatus).toHaveBeenCalledWith("MTV");
  });

  it.each([
    ["Import Cookies", "ImportTrackerAuthCookies"],
    ["Test Auth", "TestTrackerAuth"],
    ["Delete Auth", "DeleteTrackerAuth"],
  ])(
    "keeps newer %s status when initial status resolves late",
    async (buttonName, bridgeMethod) => {
      const initialStatus = deferred<ReturnType<typeof trackerAuthStatus>>();
      vi.stubGlobal("go", {
        guiapp: {
          App: {
            ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
            GetTrackerAuthStatus: vi.fn().mockReturnValue(initialStatus.promise),
            ImportTrackerAuthCookies: vi.fn().mockResolvedValue(trackerAuthStatus("action ready")),
            TestTrackerAuth: vi.fn().mockResolvedValue(trackerAuthStatus("action ready")),
            DeleteTrackerAuth: vi.fn().mockResolvedValue(trackerAuthStatus("action ready")),
          },
        },
      });

      render(
        <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
      );

      expect(await screen.findByText("MTV")).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: buttonName }));

      expect((globalThis as any).go.guiapp.App[bridgeMethod]).toHaveBeenCalledWith("MTV");
      expect(await screen.findByText("action ready")).toBeInTheDocument();

      initialStatus.resolve(trackerAuthStatus("initial stale"));

      await waitFor(() => {
        expect(screen.getByText("action ready")).toBeInTheDocument();
        expect(screen.queryByText("initial stale")).not.toBeInTheDocument();
      });
    },
  );

  it("clears tracker auth cards when refresh fails after an action", async () => {
    const list = vi
      .fn()
      .mockResolvedValueOnce([trackerAuthCapability])
      .mockRejectedValueOnce(new Error("refresh failed"));
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: list,
          GetTrackerAuthStatus: vi.fn().mockResolvedValue(trackerAuthStatus("initial ready")),
          ImportTrackerAuthCookies: vi.fn().mockResolvedValue(trackerAuthStatus("action ready")),
        },
      },
    });

    const { rerender } = render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    expect(await screen.findByText("initial ready")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Import Cookies" }));
    expect(await screen.findByText("action ready")).toBeInTheDocument();

    rerender(
      <SettingsPage {...baseProps} settingsSection="main_settings" setSettingsSection={vi.fn()} />,
    );
    rerender(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    expect(await screen.findByText("Error: refresh failed")).toBeInTheDocument();
    expect(screen.queryByText("MTV")).not.toBeInTheDocument();
    expect(screen.queryByText("action ready")).not.toBeInTheDocument();
  });

  it("keeps stale tracker auth status failures from overwriting newer action state", async () => {
    const initialStatus = deferred<ReturnType<typeof trackerAuthStatus>>();
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
          GetTrackerAuthStatus: vi.fn().mockReturnValue(initialStatus.promise),
          ImportTrackerAuthCookies: vi.fn().mockResolvedValue(trackerAuthStatus("action ready")),
        },
      },
    });

    render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    expect(await screen.findByText("MTV")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Import Cookies" }));
    expect(await screen.findByText("action ready")).toBeInTheDocument();

    initialStatus.reject(new Error("stale failure"));

    await waitFor(() => {
      expect(screen.getByText("action ready")).toBeInTheDocument();
      expect(screen.queryByText("Error: stale failure")).not.toBeInTheDocument();
    });
  });

  it("reports tracker auth action failures only on the originating tracker", async () => {
    const staleAction = deferred<ReturnType<typeof trackerAuthStatus>>();
    const newerCapability = { ...trackerAuthCapability, trackerID: "PTP", displayName: "PTP" };
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: vi
            .fn()
            .mockResolvedValue([trackerAuthCapability, newerCapability]),
          GetTrackerAuthStatus: vi.fn().mockImplementation((trackerID: string) =>
            Promise.resolve({
              ...trackerAuthStatus(`${trackerID} initial ready`),
              trackerID,
              displayName: trackerID,
            }),
          ),
          TestTrackerAuth: vi.fn().mockImplementation((trackerID: string) => {
            if (trackerID === "MTV") {
              return staleAction.promise;
            }
            return Promise.resolve({
              ...trackerAuthStatus("new action ready"),
              trackerID,
              displayName: trackerID,
            });
          }),
        },
      },
    });

    render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    const mtvTitle = await screen.findByText("MTV");
    const ptpTitle = await screen.findByText("PTP");
    const mtvCard = mtvTitle.closest(".tracker-auth-card");
    const ptpCard = ptpTitle.closest(".tracker-auth-card");

    expect(mtvCard).not.toBeNull();
    expect(ptpCard).not.toBeNull();
    await userEvent.click(
      within(mtvCard as HTMLElement).getByRole("button", { name: "Test Auth" }),
    );
    await userEvent.click(
      within(ptpCard as HTMLElement).getByRole("button", { name: "Test Auth" }),
    );
    expect(await screen.findByText("new action ready")).toBeInTheDocument();

    staleAction.reject(new Error("stale failure"));

    await waitFor(() => {
      expect(within(ptpCard as HTMLElement).getByText("new action ready")).toBeInTheDocument();
      expect(within(mtvCard as HTMLElement).getByText("Error: stale failure")).toBeInTheDocument();
      expect(
        within(ptpCard as HTMLElement).queryByText("Error: stale failure"),
      ).not.toBeInTheDocument();
    });

    await userEvent.click(
      within(ptpCard as HTMLElement).getByRole("button", { name: "Test Auth" }),
    );

    await waitFor(() => {
      expect(within(mtvCard as HTMLElement).getByText("Error: stale failure")).toBeInTheDocument();
      expect(
        within(ptpCard as HTMLElement).queryByText("Error: stale failure"),
      ).not.toBeInTheDocument();
    });
  });

  it("ignores tracker auth action failures after leaving the tracker auth section", async () => {
    const action = deferred<ReturnType<typeof trackerAuthStatus>>();
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
          GetTrackerAuthStatus: vi.fn().mockResolvedValue(trackerAuthStatus("initial ready")),
          TestTrackerAuth: vi.fn().mockReturnValue(action.promise),
        },
      },
    });

    const { rerender } = render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    expect(await screen.findByText("initial ready")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Test Auth" }));

    rerender(
      <SettingsPage {...baseProps} settingsSection="main_settings" setSettingsSection={vi.fn()} />,
    );
    action.reject(new Error("late failure"));

    await waitFor(() => {
      expect(screen.queryByText("Error: late failure")).not.toBeInTheDocument();
    });
  });

  it("clears tracker auth loading after leaving and reentering with old status pending", async () => {
    const oldStatus = deferred<ReturnType<typeof trackerAuthStatus>>();
    const getStatus = vi
      .fn()
      .mockReturnValueOnce(oldStatus.promise)
      .mockResolvedValueOnce(trackerAuthStatus("after reenter"));
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
          GetTrackerAuthStatus: getStatus,
        },
      },
    });

    const { rerender } = render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    expect(await screen.findByText("MTV")).toBeInTheDocument();
    expect(screen.getByText("Loading tracker auth...")).toBeInTheDocument();

    rerender(
      <SettingsPage {...baseProps} settingsSection="main_settings" setSettingsSection={vi.fn()} />,
    );
    rerender(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    expect(await screen.findByText("after reenter")).toBeInTheDocument();
    oldStatus.resolve(trackerAuthStatus("old ready"));

    await waitFor(() => {
      expect(screen.queryByText("Loading tracker auth...")).not.toBeInTheDocument();
      expect(screen.getByText("after reenter")).toBeInTheDocument();
      expect(screen.queryByText("old ready")).not.toBeInTheDocument();
    });
  });

  it("refreshes tracker auth after saving settings while tracker auth is selected", async () => {
    const list = vi.fn().mockResolvedValue([trackerAuthCapability]);
    const getStatus = vi
      .fn()
      .mockResolvedValueOnce(trackerAuthStatus("initial ready"))
      .mockResolvedValueOnce(trackerAuthStatus("after save"));
    const handleSaveSettings = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: list,
          GetTrackerAuthStatus: getStatus,
        },
      },
    });

    render(
      <SettingsPage
        {...baseProps}
        settingsDirty
        settingsSection="tracker_auth"
        setSettingsSection={vi.fn()}
        handleSaveSettings={handleSaveSettings}
      />,
    );

    expect(await screen.findByText("initial ready")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(handleSaveSettings).toHaveBeenCalledTimes(1);
      expect(list).toHaveBeenCalledTimes(2);
      expect(getStatus).toHaveBeenCalledTimes(2);
      expect(screen.getByText("after save")).toBeInTheDocument();
    });
  });

  it("keeps pending old tracker auth status from overwriting save reload status", async () => {
    const initialStatus = deferred<ReturnType<typeof trackerAuthStatus>>();
    const list = vi.fn().mockResolvedValue([trackerAuthCapability]);
    const getStatus = vi
      .fn()
      .mockReturnValueOnce(initialStatus.promise)
      .mockResolvedValueOnce(trackerAuthStatus("after save"));
    const handleSaveSettings = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: list,
          GetTrackerAuthStatus: getStatus,
        },
      },
    });

    render(
      <SettingsPage
        {...baseProps}
        settingsDirty
        settingsSection="tracker_auth"
        setSettingsSection={vi.fn()}
        handleSaveSettings={handleSaveSettings}
      />,
    );

    expect(await screen.findByText("MTV")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("after save")).toBeInTheDocument();

    initialStatus.resolve(trackerAuthStatus("initial stale"));

    await waitFor(() => {
      expect(screen.getByText("after save")).toBeInTheDocument();
      expect(screen.queryByText("initial stale")).not.toBeInTheDocument();
      expect(list).toHaveBeenCalledTimes(2);
      expect(getStatus).toHaveBeenCalledTimes(2);
    });
  });

  it("clears pending tracker auth action busy state after save reload", async () => {
    const action = deferred<ReturnType<typeof trackerAuthStatus>>();
    const list = vi.fn().mockResolvedValue([trackerAuthCapability]);
    const getStatus = vi
      .fn()
      .mockResolvedValueOnce(trackerAuthStatus("initial ready"))
      .mockResolvedValueOnce(trackerAuthStatus("after save"));
    const handleSaveSettings = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: list,
          GetTrackerAuthStatus: getStatus,
          TestTrackerAuth: vi.fn().mockReturnValue(action.promise),
        },
      },
    });

    render(
      <SettingsPage
        {...baseProps}
        settingsDirty
        settingsSection="tracker_auth"
        setSettingsSection={vi.fn()}
        handleSaveSettings={handleSaveSettings}
      />,
    );

    expect(await screen.findByText("initial ready")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Test Auth" }));
    expect(screen.getByRole("button", { name: "Testing..." })).toBeDisabled();

    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("after save")).toBeInTheDocument();

    action.resolve(trackerAuthStatus("action stale"));

    await waitFor(() => {
      expect(screen.getByText("after save")).toBeInTheDocument();
      expect(screen.queryByText("action stale")).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Test Auth" })).toBeEnabled();
    });
  });

  it("refreshes tracker auth after confirming config import while tracker auth is selected", async () => {
    const list = vi.fn().mockResolvedValue([trackerAuthCapability]);
    const getStatus = vi
      .fn()
      .mockResolvedValueOnce(trackerAuthStatus("initial ready"))
      .mockResolvedValueOnce(trackerAuthStatus("after import"));
    const handleImportConfigConfirm = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: list,
          GetTrackerAuthStatus: getStatus,
        },
      },
    });

    render(
      <SettingsPage
        {...baseProps}
        settingsSection="tracker_auth"
        setSettingsSection={vi.fn()}
        importConfirmOpen
        handleImportConfigConfirm={handleImportConfigConfirm}
      />,
    );

    expect(await screen.findByText("initial ready")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Choose file & import" }));

    await waitFor(() => {
      expect(handleImportConfigConfirm).toHaveBeenCalledTimes(1);
      expect(list).toHaveBeenCalledTimes(2);
      expect(getStatus).toHaveBeenCalledTimes(2);
      expect(screen.getByText("after import")).toBeInTheDocument();
    });
  });

  it("keeps pending old tracker auth status from overwriting import reload status", async () => {
    const initialStatus = deferred<ReturnType<typeof trackerAuthStatus>>();
    const list = vi.fn().mockResolvedValue([trackerAuthCapability]);
    const getStatus = vi
      .fn()
      .mockReturnValueOnce(initialStatus.promise)
      .mockResolvedValueOnce(trackerAuthStatus("after import"));
    const handleImportConfigConfirm = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: list,
          GetTrackerAuthStatus: getStatus,
        },
      },
    });

    render(
      <SettingsPage
        {...baseProps}
        settingsSection="tracker_auth"
        setSettingsSection={vi.fn()}
        importConfirmOpen
        handleImportConfigConfirm={handleImportConfigConfirm}
      />,
    );

    expect(await screen.findByText("MTV")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Choose file & import" }));
    expect(await screen.findByText("after import")).toBeInTheDocument();

    initialStatus.resolve(trackerAuthStatus("initial stale"));

    await waitFor(() => {
      expect(screen.getByText("after import")).toBeInTheDocument();
      expect(screen.queryByText("initial stale")).not.toBeInTheDocument();
      expect(list).toHaveBeenCalledTimes(2);
      expect(getStatus).toHaveBeenCalledTimes(2);
    });
  });

  it("clears pending tracker auth action busy state after import reload", async () => {
    const action = deferred<ReturnType<typeof trackerAuthStatus>>();
    const list = vi.fn().mockResolvedValue([trackerAuthCapability]);
    const getStatus = vi
      .fn()
      .mockResolvedValueOnce(trackerAuthStatus("initial ready"))
      .mockResolvedValueOnce(trackerAuthStatus("after import"));
    const handleImportConfigConfirm = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: list,
          GetTrackerAuthStatus: getStatus,
          TestTrackerAuth: vi.fn().mockReturnValue(action.promise),
        },
      },
    });

    const props = {
      ...baseProps,
      settingsSection: "tracker_auth",
      setSettingsSection: vi.fn(),
      handleImportConfigConfirm,
    };
    const { rerender } = render(<SettingsPage {...props} />);

    expect(await screen.findByText("initial ready")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Test Auth" }));
    expect(screen.getByRole("button", { name: "Testing..." })).toBeDisabled();

    rerender(<SettingsPage {...props} importConfirmOpen />);

    await userEvent.click(screen.getByRole("button", { name: "Choose file & import" }));
    expect(await screen.findByText("after import")).toBeInTheDocument();

    rerender(<SettingsPage {...props} />);

    action.resolve(trackerAuthStatus("action stale"));

    await waitFor(() => {
      expect(screen.getByText("after import")).toBeInTheDocument();
      expect(screen.queryByText("action stale")).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Test Auth", hidden: true })).toBeEnabled();
    });
  });

  it("keeps config save refresh lazy outside tracker auth", async () => {
    const list = vi.fn().mockResolvedValue([trackerAuthCapability]);
    const handleSaveSettings = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("go", {
      guiapp: {
        App: {
          ListTrackerAuthCapabilities: list,
          GetTrackerAuthStatus: vi.fn().mockResolvedValue(trackerAuthStatus("ready")),
        },
      },
    });

    render(
      <SettingsPage
        {...baseProps}
        settingsDirty
        settingsSection="main_settings"
        setSettingsSection={vi.fn()}
        handleSaveSettings={handleSaveSettings}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(handleSaveSettings).toHaveBeenCalledTimes(1);
    });
    expect(list).not.toHaveBeenCalled();
  });
});
