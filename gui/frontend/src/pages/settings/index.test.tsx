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
    ).toEqual(["Main", "Trackers", "Application Details"]);
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
});
