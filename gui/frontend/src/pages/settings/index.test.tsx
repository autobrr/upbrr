// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { screen, within } from "@testing-library/dom";
import { cleanup, render } from "@testing-library/react";
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

describe("SettingsPage", () => {
  afterEach(() => {
    cleanup();
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
});
