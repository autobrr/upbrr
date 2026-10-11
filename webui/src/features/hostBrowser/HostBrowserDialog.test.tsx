// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAppRequestHandlerForTests } from "../../api/client";
import type {
  BrowseDirectoryEntry,
  BrowseDirectoryRequest,
  BrowseDirectoryResponse,
} from "../../types";
import { HostBrowserDialog } from "./HostBrowserDialog";

const entry = (name: string, path = `/media/${name}`, isDir = true): BrowseDirectoryEntry => ({
  name,
  path,
  isDir,
  size: 1024,
  modifiedAt: "2026-01-01T00:00:00Z",
});
const roots: BrowseDirectoryResponse = {
  scope: "roots",
  currentPath: "",
  parentPath: "",
  mode: "folder",
  entries: [entry("media", "/media")],
};
const response = (
  entries: BrowseDirectoryEntry[],
  extra: Partial<BrowseDirectoryResponse> = {},
): BrowseDirectoryResponse => ({
  ...roots,
  entries,
  ...extra,
});
const deferred = () => {
  let resolve!: (value: BrowseDirectoryResponse) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<BrowseDirectoryResponse>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};
const flush = () => act(async () => {});
const advanceSearch = () =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(250);
  });
const search = (value: string) =>
  fireEvent.change(screen.getByLabelText("Search all browse roots"), { target: { value } });

const installBrowser = (
  handler: (
    request: BrowseDirectoryRequest,
    signal?: AbortSignal,
  ) => Promise<BrowseDirectoryResponse>,
) => {
  const list = vi.fn(handler);
  setAppRequestHandlerForTests((method, body, options) => {
    expect(method).toBe("BrowseDirectory");
    return list(body as BrowseDirectoryRequest, options?.signal);
  });
  return list;
};

describe("HostBrowserDialog", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it.each(["", "   "])(
    "requests explicit roots for a blank draft %j even with one root",
    async (initialPath) => {
      const list = installBrowser(async () => roots);
      render(
        <HostBrowserDialog
          mode="folder"
          initialPath={initialPath}
          onClose={vi.fn()}
          onSelect={vi.fn()}
        />,
      );
      await flush();
      expect(list).toHaveBeenCalledWith(
        { path: "", mode: "folder", scope: "roots" },
        expect.any(AbortSignal),
      );
      expect(screen.getByText("All browse roots")).toBeVisible();
      expect(screen.getByRole("button", { name: "Open media" })).toBeVisible();
      expect(screen.queryByRole("button", { name: "Select folder" })).not.toBeInTheDocument();
    },
  );

  it("debounces roots queries and invalidates stale replies immediately while typing", async () => {
    const old = deferred();
    const list = installBrowser(async (request) =>
      request.query === "old"
        ? old.promise
        : request.query
          ? response([entry("New.Result")])
          : roots,
    );
    render(<HostBrowserDialog mode="folder" initialPath="" onClose={vi.fn()} onSelect={vi.fn()} />);
    await flush();
    search("o");
    search("old");
    expect(list).toHaveBeenCalledTimes(1);
    await advanceSearch();
    expect(list).toHaveBeenCalledTimes(2);
    const oldSignal = list.mock.calls[1][1];
    search("new");
    expect(oldSignal?.aborted).toBe(true);
    expect(screen.getByLabelText("Search all browse roots")).toBeEnabled();
    await act(async () => old.resolve(response([entry("Old.Result")])));
    expect(screen.queryByText("[DIR] Old.Result")).not.toBeInTheDocument();
    expect(screen.getByText("Searching all browse roots...")).toBeVisible();
    await advanceSearch();
    expect(screen.getByText("[DIR] New.Result")).toBeVisible();
    expect(list).toHaveBeenLastCalledWith(
      { path: "", mode: "folder", scope: "roots", query: "new" },
      expect.any(AbortSignal),
    );
  });

  it("clears and repeats a root query without accepting its stale error", async () => {
    const old = deferred();
    let searches = 0;
    const list = installBrowser(async (request) =>
      request.query ? (++searches === 1 ? old.promise : response([entry("Beacon.Voyage")])) : roots,
    );
    render(<HostBrowserDialog mode="folder" initialPath="" onClose={vi.fn()} onSelect={vi.fn()} />);
    await flush();
    search("Beacon Voyage");
    await advanceSearch();
    fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
    expect(list.mock.calls[1][1]?.aborted).toBe(true);
    await act(async () => old.reject(new Error("obsolete failure")));
    expect(screen.getByRole("button", { name: "Open media" })).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    search("Beacon Voyage");
    await advanceSearch();
    expect(screen.getByText("[DIR] Beacon.Voyage")).toBeVisible();
    expect(searches).toBe(2);
  });

  it("filters loaded directory entries locally and clears the filter on navigation", async () => {
    const list = installBrowser(async (request) =>
      response([entry("Beacon.Voyage"), entry("Other")], {
        scope: "directory",
        currentPath: request.path,
        parentPath: "/",
      }),
    );
    const onSelect = vi.fn();
    render(
      <HostBrowserDialog
        mode="folder"
        initialPath="/media"
        onClose={vi.fn()}
        onSelect={onSelect}
      />,
    );
    await flush();
    fireEvent.change(screen.getByLabelText("Filter this directory"), {
      target: { value: "voyage beacon" },
    });
    expect(screen.getByText("[DIR] Beacon.Voyage")).toBeVisible();
    expect(screen.queryByText("[DIR] Other")).not.toBeInTheDocument();
    await advanceSearch();
    expect(list).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Open Beacon.Voyage" }));
    await flush();
    expect(list).toHaveBeenLastCalledWith(
      { path: "/media/Beacon.Voyage", mode: "folder", scope: "directory" },
      expect.any(AbortSignal),
    );
    expect(screen.getByLabelText("Filter this directory")).toHaveValue("");
    expect(onSelect).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Select folder" }));
    expect(onSelect).toHaveBeenCalledWith("/media/Beacon.Voyage", true, "folder");
  });

  it("shows exact paths for duplicate results and preserves file selection", async () => {
    const first = entry("Beacon.Voyage.mkv", "/media/first/Beacon.Voyage.mkv", false);
    const second = entry("Beacon.Voyage.mkv", "/archive/second/Beacon.Voyage.mkv", false);
    installBrowser(async (request) =>
      request.query
        ? response([first, second, entry("Beacon.Voyage")], { mode: "file", incomplete: true })
        : { ...roots, mode: "file" },
    );
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(<HostBrowserDialog mode="file" initialPath="" onClose={onClose} onSelect={onSelect} />);
    await flush();
    search("Beacon Voyage");
    await advanceSearch();
    expect(screen.getByText(first.path)).toBeVisible();
    expect(screen.getByText(second.path)).toBeVisible();
    expect(screen.getByRole("status")).toHaveTextContent("Search results are incomplete");
    expect(
      screen.queryByRole("button", { name: "Select /media/Beacon.Voyage" }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: `Select ${second.path}` }));
    expect(onSelect).toHaveBeenCalledWith(second.path, false, "file");
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("cancels pending searches on Roots navigation and close, including queued debounce", async () => {
    const pending = deferred();
    const list = installBrowser(async (request) => (request.query ? pending.promise : roots));
    const onClose = vi.fn();
    const { rerender } = render(
      <HostBrowserDialog mode="folder" initialPath="" onClose={onClose} onSelect={vi.fn()} />,
    );
    await flush();
    search("pending");
    await advanceSearch();
    fireEvent.click(screen.getByRole("button", { name: "Roots" }));
    expect(list.mock.calls[1][1]?.aborted).toBe(true);
    await flush();
    expect(screen.getByLabelText("Search all browse roots")).toHaveValue("");
    search("queued");
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledOnce();
    await advanceSearch();
    expect(list).toHaveBeenCalledTimes(3);
    rerender(<HostBrowserDialog mode={null} initialPath="" onClose={onClose} onSelect={vi.fn()} />);
    await act(async () => pending.resolve(response([entry("Obsolete")])));
    rerender(
      <HostBrowserDialog mode="folder" initialPath="" onClose={onClose} onSelect={vi.fn()} />,
    );
    await flush();
    expect(screen.getByRole("button", { name: "Open media" })).toBeVisible();
    expect(screen.queryByText("[DIR] Obsolete")).not.toBeInTheDocument();
  });

  it("aborts active requests on close or unmount", async () => {
    const pending = deferred();
    const list = installBrowser(async () => pending.promise);
    const { unmount } = render(
      <HostBrowserDialog mode="folder" initialPath="" onClose={vi.fn()} onSelect={vi.fn()} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(list.mock.calls[0][1]?.aborted).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Roots" }));
    unmount();
    expect(list.mock.calls[1][1]?.aborted).toBe(true);
    await act(async () => pending.resolve(roots));
  });
});
