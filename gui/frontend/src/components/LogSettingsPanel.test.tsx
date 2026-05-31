// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { screen, waitFor } from "@testing-library/dom";
import { cleanup, render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import LogSettingsPanel from "./LogSettingsPanel";

vi.mock("../utils/runtime", () => ({
  EventsOn: vi.fn(() => vi.fn()),
}));

const installGoAPI = () => {
  (globalThis as any).go = {
    guiapp: {
      App: {
        GetLogPath: vi.fn().mockResolvedValue("C:/logs/upbrr.log"),
        GetRecentLogs: vi.fn().mockResolvedValue([]),
        GetLogExclusions: vi.fn().mockResolvedValue([]),
        StartLogStream: vi.fn().mockResolvedValue("stream-1"),
        StopLogStream: vi.fn().mockResolvedValue(undefined),
        UpdateLogExclusions: vi.fn().mockResolvedValue(undefined),
      },
    },
  };
};

describe("LogSettingsPanel", () => {
  afterEach(() => {
    cleanup();
    delete (globalThis as any).go;
  });

  it("renders log path and updates logging level", async () => {
    installGoAPI();
    const updateConfigValue = vi.fn();

    render(
      <LogSettingsPanel
        configData={{ Logging: { Level: "info" } }}
        fieldMeta={{ Level: { label: "Verbosity" } }}
        renderField={(label) => <div key={label}>{label}</div>}
        updateConfigValue={updateConfigValue}
      />,
    );

    await waitFor(() => expect(screen.getByText("C:/logs/upbrr.log")).toBeInTheDocument());

    await userEvent.selectOptions(screen.getByLabelText("Verbosity"), "debug");

    expect(updateConfigValue).toHaveBeenCalledWith(["Logging", "Level"], "debug");
  });
});
