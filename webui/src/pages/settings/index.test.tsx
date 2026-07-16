// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { screen, within } from "@testing-library/dom";
import { cleanup, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { installAppOperationMocks } from "../../test/appRequestMock";

import SettingsPage from ".";
import { trackerAuthClient } from "../../api/app";
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
    "BTN",
    "COOKIE",
    "FAST",
    "FF",
    "FL",
    "HDB",
    "MTV",
    "PTP",
    "RTF",
    "THR",
    "SLOW",
    "ASC",
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
  supportsRemoteValidation: true,
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

  it("shows path-free DVD engine and FFmpeg capability diagnostics", async () => {
    installAppOperationMocks({
      GetApplicationInfo: vi.fn().mockResolvedValue({
        version: "dev",
        buildIdentifier: "example-build",
        goVersion: "go1.26.4",
        goos: "windows",
        goarch: "amd64",
        uptime: "1s",
        uptimeSeconds: 1,
        dvdMenuEngine: {
          EngineVersion: "phase0a-1",
          SchemaVersion: 1,
          SupportedFeatures: ["ifo_inventory"],
          FFmpegVersion: "ffmpeg version example",
          FFmpegDVDVideo: true,
          MissingFFmpegOptions: [],
        },
        dvdMenuCapabilityStatus: "available",
        dvdMenuCapabilityMessage: "Compatible FFmpeg dvdvideo menu support detected.",
      }),
    });

    render(
      <SettingsPage
        {...baseProps}
        settingsSection="application_details"
        setSettingsSection={vi.fn()}
      />,
    );

    await waitFor(() => expect(screen.getByText("phase0a-1")).toBeInTheDocument());
    expect(screen.getByText("Available")).toBeInTheDocument();
    expect(screen.getByText("ffmpeg version example")).toBeInTheDocument();
    expect(
      screen.getByText("Compatible FFmpeg dvdvideo menu support detected."),
    ).toBeInTheDocument();
  });

  it("renders tracker auth as the bottom tab", async () => {
    const setSettingsSection = vi.fn();
    installAppOperationMocks({
      ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([]),
      GetTrackerAuthStatus: vi.fn(),
    });

    render(<SettingsPage {...baseProps} setSettingsSection={setSettingsSection} />);

    await userEvent.click(screen.getByRole("button", { name: "Tracker Auth" }));

    expect(setSettingsSection).toHaveBeenCalledWith("tracker_auth");
  });

  it("shows Check Auth only for remote-validation tracker auth", async () => {
    installAppOperationMocks({
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
          supportsRemoteValidation: true,
          requiresAPIKey: true,
          requiresPasskey: false,
        },
        {
          trackerID: "BTN",
          displayName: "BTN",
          authKind: "api_key_cookies_login_manual_2fa",
          supportsCookieFile: true,
          supportsLogin: true,
          supportsAutoLogin: true,
          supportsTOTP: true,
          supportsManual2FA: true,
          supportsRemoteValidation: true,
          requiresAPIKey: true,
          requiresPasskey: false,
        },
        {
          trackerID: "AR",
          displayName: "AR",
          authKind: "cookies_login",
          supportsCookieFile: true,
          supportsLogin: false,
          supportsAutoLogin: false,
          supportsTOTP: false,
          supportsManual2FA: false,
          supportsRemoteValidation: true,
          requiresAPIKey: false,
          requiresPasskey: false,
        },
        {
          trackerID: "HDB",
          displayName: "HDB",
          authKind: "passkey_cookies",
          supportsCookieFile: true,
          supportsLogin: false,
          supportsAutoLogin: false,
          supportsTOTP: false,
          supportsManual2FA: false,
          supportsRemoteValidation: true,
          requiresAPIKey: false,
          requiresPasskey: true,
        },
        {
          trackerID: "FF",
          displayName: "FF",
          authKind: "cookies_login",
          supportsCookieFile: true,
          supportsLogin: true,
          supportsAutoLogin: true,
          supportsTOTP: false,
          supportsManual2FA: false,
          supportsRemoteValidation: true,
          requiresAPIKey: false,
          requiresPasskey: false,
        },
        {
          trackerID: "FL",
          displayName: "FL",
          authKind: "cookies_login",
          supportsCookieFile: true,
          supportsLogin: true,
          supportsAutoLogin: true,
          supportsTOTP: false,
          supportsManual2FA: false,
          supportsRemoteValidation: true,
          requiresAPIKey: false,
          requiresPasskey: false,
        },
        {
          trackerID: "RTF",
          displayName: "RTF",
          authKind: "api_key_credential_refresh",
          supportsCookieFile: false,
          supportsLogin: true,
          supportsAutoLogin: true,
          supportsTOTP: false,
          supportsManual2FA: false,
          supportsRemoteValidation: true,
          requiresAPIKey: true,
          requiresPasskey: false,
        },
        {
          trackerID: "THR",
          displayName: "THR",
          authKind: "credential_login",
          supportsCookieFile: false,
          supportsLogin: true,
          supportsAutoLogin: true,
          supportsTOTP: false,
          supportsManual2FA: false,
          supportsRemoteValidation: true,
          requiresAPIKey: false,
          requiresPasskey: false,
        },
        {
          trackerID: "ASC",
          displayName: "ASC",
          authKind: "cookies",
          supportsCookieFile: true,
          supportsLogin: false,
          supportsAutoLogin: false,
          supportsTOTP: false,
          supportsManual2FA: false,
          supportsRemoteValidation: false,
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
    });

    render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    const mtvTitle = await screen.findByText("MTV");
    const btnTitle = await screen.findByText("BTN");
    const arTitle = await screen.findByText("AR");
    const hdbTitle = await screen.findByText("HDB");
    const ffTitle = await screen.findByText("FF");
    const flTitle = await screen.findByText("FL");
    const rtfTitle = await screen.findByText("RTF");
    const thrTitle = await screen.findByText("THR");
    const ascTitle = await screen.findByText("ASC");
    const mtvCard = mtvTitle.closest(".tracker-auth-card");
    const btnCard = btnTitle.closest(".tracker-auth-card");
    const arCard = arTitle.closest(".tracker-auth-card");
    const hdbCard = hdbTitle.closest(".tracker-auth-card");
    const ffCard = ffTitle.closest(".tracker-auth-card");
    const flCard = flTitle.closest(".tracker-auth-card");
    const rtfCard = rtfTitle.closest(".tracker-auth-card");
    const thrCard = thrTitle.closest(".tracker-auth-card");
    const ascCard = ascTitle.closest(".tracker-auth-card");

    expect(mtvCard).not.toBeNull();
    expect(btnCard).not.toBeNull();
    expect(arCard).not.toBeNull();
    expect(hdbCard).not.toBeNull();
    expect(ffCard).not.toBeNull();
    expect(flCard).not.toBeNull();
    expect(rtfCard).not.toBeNull();
    expect(thrCard).not.toBeNull();
    expect(ascCard).not.toBeNull();
    expect(
      within(mtvCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
    ).toBeInTheDocument();
    expect(
      within(btnCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
    ).toBeInTheDocument();
    expect(
      within(arCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
    ).toBeInTheDocument();
    expect(
      within(hdbCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
    ).toBeInTheDocument();
    expect(
      within(ffCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
    ).toBeInTheDocument();
    expect(
      within(flCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
    ).toBeInTheDocument();
    expect(
      within(rtfCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
    ).toBeInTheDocument();
    expect(
      within(thrCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
    ).toBeInTheDocument();
    expect(
      within(ascCard as HTMLElement).queryByRole("button", { name: "Check Auth" }),
    ).not.toBeInTheDocument();
  });

  it("renders tracker auth failure summary with distinct remote detail", async () => {
    installAppOperationMocks({
      ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
      GetTrackerAuthStatus: vi.fn().mockResolvedValue({
        ...trackerAuthStatus("stored session expired or invalid"),
        state: "login_required",
        lastError: "remote auth test failed status=401",
      }),
    });

    render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    const title = await screen.findByText("MTV");
    const card = title.closest(".tracker-auth-card");
    expect(card).not.toBeNull();
    expect(
      within(card as HTMLElement).getByText("stored session expired or invalid"),
    ).toBeInTheDocument();
    expect(
      within(card as HTMLElement).getByText("remote auth test failed status=401"),
    ).toBeInTheDocument();
  });

  it("does not render duplicate tracker auth failure detail", async () => {
    installAppOperationMocks({
      ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
      GetTrackerAuthStatus: vi.fn().mockResolvedValue({
        ...trackerAuthStatus("stored session expired or invalid"),
        state: "login_required",
        lastError: "stored session expired or invalid",
      }),
    });

    render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    const title = await screen.findByText("MTV");
    const card = title.closest(".tracker-auth-card");
    expect(card).not.toBeNull();
    expect(
      within(card as HTMLElement).getAllByText("stored session expired or invalid"),
    ).toHaveLength(1);
  });

  it("renders fast tracker auth statuses before slow statuses resolve", async () => {
    let resolveSlow: (value: unknown) => void = () => undefined;
    const slowStatus = new Promise((resolve) => {
      resolveSlow = resolve;
    });
    installAppOperationMocks({
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
    installAppOperationMocks({
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
    installAppOperationMocks({
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
    ["Import Cookies", "importCookies"],
    ["Check Auth", "test"],
    ["Delete Auth", "remove"],
  ])(
    "keeps newer %s status when initial status resolves late",
    async (buttonName, clientMethod) => {
      const initialStatus = deferred<ReturnType<typeof trackerAuthStatus>>();
      const action = vi
        .spyOn(trackerAuthClient, clientMethod as "importCookies" | "test" | "remove")
        .mockResolvedValue(trackerAuthStatus("action ready"));
      installAppOperationMocks({
        ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
        GetTrackerAuthStatus: vi.fn().mockReturnValue(initialStatus.promise),
      });

      render(
        <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
      );

      expect(await screen.findByText("MTV")).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: buttonName }));

      expect(action).toHaveBeenCalledWith("MTV");
      expect(await screen.findByText("action ready")).toBeInTheDocument();

      initialStatus.resolve(trackerAuthStatus("initial stale"));

      await waitFor(() => {
        expect(screen.getByText("action ready")).toBeInTheDocument();
        expect(screen.queryByText("initial stale")).not.toBeInTheDocument();
      });
    },
  );

  it("stores the post-validation tracker auth status returned by Check Auth", async () => {
    const testStatus = {
      ...trackerAuthStatus("fresh validation ready"),
      cookieCount: 3,
      lastCheckedAt: "2026-07-08T00:00:00Z",
      lastError: "",
      encryptedStorage: true,
    };
    installAppOperationMocks({
      ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
      GetTrackerAuthStatus: vi.fn().mockResolvedValue({
        ...trackerAuthStatus("stale before validation"),
        cookieCount: 1,
      }),
      TestTrackerAuth: vi.fn().mockResolvedValue(testStatus),
    });

    render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    expect(await screen.findByText("stale before validation")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Check Auth" }));

    await waitFor(() => {
      expect(screen.getByText("fresh validation ready")).toBeInTheDocument();
      expect(screen.getByText("Cookies: 3")).toBeInTheDocument();
      expect(
        screen.getByText((_content, element) =>
          Boolean(
            element?.textContent?.startsWith("Checked: ") &&
            element.textContent !== "Checked: Never",
          ),
        ),
      ).toBeInTheDocument();
      expect(screen.queryByText("stale before validation")).not.toBeInTheDocument();
    });
  });

  it("clears tracker auth cards when refresh fails after an action", async () => {
    const list = vi
      .fn()
      .mockResolvedValueOnce([trackerAuthCapability])
      .mockRejectedValueOnce(new Error("refresh failed"));
    vi.spyOn(trackerAuthClient, "importCookies").mockResolvedValue(
      trackerAuthStatus("action ready"),
    );
    installAppOperationMocks({
      ListTrackerAuthCapabilities: list,
      GetTrackerAuthStatus: vi.fn().mockResolvedValue(trackerAuthStatus("initial ready")),
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
    vi.spyOn(trackerAuthClient, "importCookies").mockResolvedValue(
      trackerAuthStatus("action ready"),
    );
    installAppOperationMocks({
      ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
      GetTrackerAuthStatus: vi.fn().mockReturnValue(initialStatus.promise),
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
    installAppOperationMocks({
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
      within(mtvCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
    );
    await userEvent.click(
      within(ptpCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
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
      within(ptpCard as HTMLElement).getByRole("button", { name: "Check Auth" }),
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
    installAppOperationMocks({
      ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
      GetTrackerAuthStatus: vi.fn().mockResolvedValue(trackerAuthStatus("initial ready")),
      TestTrackerAuth: vi.fn().mockReturnValue(action.promise),
    });

    const { rerender } = render(
      <SettingsPage {...baseProps} settingsSection="tracker_auth" setSettingsSection={vi.fn()} />,
    );

    expect(await screen.findByText("initial ready")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Check Auth" }));

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
    installAppOperationMocks({
      ListTrackerAuthCapabilities: vi.fn().mockResolvedValue([trackerAuthCapability]),
      GetTrackerAuthStatus: getStatus,
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
    installAppOperationMocks({
      ListTrackerAuthCapabilities: list,
      GetTrackerAuthStatus: getStatus,
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
    installAppOperationMocks({
      ListTrackerAuthCapabilities: list,
      GetTrackerAuthStatus: getStatus,
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
    installAppOperationMocks({
      ListTrackerAuthCapabilities: list,
      GetTrackerAuthStatus: getStatus,
      TestTrackerAuth: vi.fn().mockReturnValue(action.promise),
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
    await userEvent.click(screen.getByRole("button", { name: "Check Auth" }));
    expect(screen.getByRole("button", { name: "Checking..." })).toBeDisabled();

    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("after save")).toBeInTheDocument();

    action.resolve(trackerAuthStatus("action stale"));

    await waitFor(() => {
      expect(screen.getByText("after save")).toBeInTheDocument();
      expect(screen.queryByText("action stale")).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Check Auth" })).toBeEnabled();
    });
  });

  it("refreshes tracker auth after confirming config import while tracker auth is selected", async () => {
    const list = vi.fn().mockResolvedValue([trackerAuthCapability]);
    const getStatus = vi
      .fn()
      .mockResolvedValueOnce(trackerAuthStatus("initial ready"))
      .mockResolvedValueOnce(trackerAuthStatus("after import"));
    const handleImportConfigConfirm = vi.fn().mockResolvedValue(undefined);
    installAppOperationMocks({
      ListTrackerAuthCapabilities: list,
      GetTrackerAuthStatus: getStatus,
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
    installAppOperationMocks({
      ListTrackerAuthCapabilities: list,
      GetTrackerAuthStatus: getStatus,
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
    installAppOperationMocks({
      ListTrackerAuthCapabilities: list,
      GetTrackerAuthStatus: getStatus,
      TestTrackerAuth: vi.fn().mockReturnValue(action.promise),
    });

    const props = {
      ...baseProps,
      settingsSection: "tracker_auth",
      setSettingsSection: vi.fn(),
      handleImportConfigConfirm,
    };
    const { rerender } = render(<SettingsPage {...props} />);

    expect(await screen.findByText("initial ready")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Check Auth" }));
    expect(screen.getByRole("button", { name: "Checking..." })).toBeDisabled();

    rerender(<SettingsPage {...props} importConfirmOpen />);

    await userEvent.click(screen.getByRole("button", { name: "Choose file & import" }));
    expect(await screen.findByText("after import")).toBeInTheDocument();

    rerender(<SettingsPage {...props} />);

    action.resolve(trackerAuthStatus("action stale"));

    await waitFor(() => {
      expect(screen.getByText("after import")).toBeInTheDocument();
      expect(screen.queryByText("action stale")).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Check Auth", hidden: true })).toBeEnabled();
    });
  });

  it("keeps config save refresh lazy outside tracker auth", async () => {
    const list = vi.fn().mockResolvedValue([trackerAuthCapability]);
    const handleSaveSettings = vi.fn().mockResolvedValue(undefined);
    installAppOperationMocks({
      ListTrackerAuthCapabilities: list,
      GetTrackerAuthStatus: vi.fn().mockResolvedValue(trackerAuthStatus("ready")),
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
