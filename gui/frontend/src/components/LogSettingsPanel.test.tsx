// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { screen, waitFor } from "@testing-library/dom";
import { cleanup, render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import LogSettingsPanel from "./LogSettingsPanel";
import { EventsOn } from "../utils/runtime";

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
    vi.restoreAllMocks();
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

  it("keeps distinct rows when a restarted log stream reuses IDs", async () => {
    installGoAPI();
    const app = (globalThis as any).go.guiapp.App;
    app.GetRecentLogs.mockResolvedValueOnce([
      {
        ID: 1,
        Time: "2026-06-15T00:00:00.000Z",
        Level: "info",
        Message: "before restart",
      },
    ]);
    app.GetRecentLogs.mockResolvedValueOnce([
      {
        ID: 1,
        Time: "2026-06-15T00:01:00.000Z",
        Level: "info",
        Message: "after restart",
      },
    ]);

    render(
      <LogSettingsPanel
        configData={{ Logging: { Level: "info" } }}
        fieldMeta={{ Level: { label: "Verbosity" } }}
        renderField={(label) => <div key={label}>{label}</div>}
        updateConfigValue={vi.fn()}
      />,
    );

    await waitFor(() => expect(screen.getByText("before restart")).toBeInTheDocument());
    await waitFor(() => expect(screen.getByText("after restart")).toBeInTheDocument());
  });

  it("dedupes exact recent/live overlap rows", async () => {
    installGoAPI();
    const app = (globalThis as any).go.guiapp.App;
    const row = {
      ID: 2,
      Time: "2026-06-15T00:00:00.000Z",
      Level: "info",
      Message: "overlap",
    };
    app.GetRecentLogs.mockResolvedValue([row]);
    vi.mocked(EventsOn).mockImplementationOnce((_eventName, callback) => {
      callback(row);
      return vi.fn();
    });

    render(
      <LogSettingsPanel
        configData={{ Logging: { Level: "info" } }}
        fieldMeta={{ Level: { label: "Verbosity" } }}
        renderField={(label) => <div key={label}>{label}</div>}
        updateConfigValue={vi.fn()}
      />,
    );

    await waitFor(() => expect(EventsOn).toHaveBeenCalled());
    await waitFor(() => expect(screen.getAllByText("overlap")).toHaveLength(1));
  });

  it("does not restart the log stream when auto-scroll changes", async () => {
    installGoAPI();
    const app = (globalThis as any).go.guiapp.App;

    render(
      <LogSettingsPanel
        configData={{ Logging: { Level: "info" } }}
        fieldMeta={{ Level: { label: "Verbosity" } }}
        renderField={(label) => <div key={label}>{label}</div>}
        updateConfigValue={vi.fn()}
      />,
    );

    await waitFor(() => expect(app.StartLogStream).toHaveBeenCalledTimes(1));

    await userEvent.click(screen.getByLabelText("Auto-scroll logs"));

    expect(app.StopLogStream).not.toHaveBeenCalled();
    expect(app.StartLogStream).toHaveBeenCalledTimes(1);
  });

  it("stops a started stream when event subscription fails", async () => {
    installGoAPI();
    const app = (globalThis as any).go.guiapp.App;
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.mocked(EventsOn).mockImplementationOnce(() => {
      throw new Error("subscribe failed");
    });

    render(
      <LogSettingsPanel
        configData={{ Logging: { Level: "info" } }}
        fieldMeta={{ Level: { label: "Verbosity" } }}
        renderField={(label) => <div key={label}>{label}</div>}
        updateConfigValue={vi.fn()}
      />,
    );

    try {
      await waitFor(() => expect(app.StopLogStream).toHaveBeenCalledWith("stream-1"));
      expect(consoleError).toHaveBeenCalledWith("Failed to start log stream", expect.any(Error));
    } finally {
      consoleError.mockRestore();
    }
  });
});
