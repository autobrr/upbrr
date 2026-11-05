// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { act, renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { useProviderSearch } from "./providerSearch";
import type { ProviderSearchResult } from "../types";

const resultFor = (Query: string): ProviderSearchResult => ({
  Query,
  Category: "tv",
  Providers: [],
});

it("rejects late searches, canceled requests and responses for another source/category", async () => {
  const requests: {
    signal: AbortSignal;
    resolve(value: ProviderSearchResult): void;
    reject(): void;
  }[] = [];
  const port = {
    search: vi.fn(
      (_query: string, _category: string, signal: AbortSignal) =>
        new Promise<ProviderSearchResult>((resolve, reject) =>
          requests.push({ signal, resolve, reject: () => reject(new Error("failure")) }),
        ),
    ),
  };
  const { result, rerender, unmount } = renderHook(
    ({ context, category }) => useProviderSearch(port, context, category),
    { initialProps: { context: "source-1-tv", category: "TV" } },
  );
  let first!: Promise<void>;
  let second!: Promise<void>;
  act(() => {
    first = result.current.search("First");
  });
  act(() => {
    second = result.current.search("Second 2025");
  });
  expect(requests[0].signal.aborted).toBe(true);
  await act(async () => {
    requests[1].resolve(resultFor("Second 2025"));
    await second;
  });
  await act(async () => {
    requests[0].resolve(resultFor("First"));
    await first;
  });
  expect(result.current.view.result?.Query).toBe("Second 2025");

  let failed!: Promise<void>;
  act(() => {
    failed = result.current.search("Failed");
  });
  await act(async () => {
    requests[2].reject();
    await failed;
  });
  expect(result.current.view.result?.Query).toBe("Second 2025");
  expect(result.current.view.status).toBe("error");

  let canceled!: Promise<void>;
  act(() => {
    canceled = result.current.search("Canceled");
  });
  act(() => result.current.cancel());
  await act(async () => {
    requests[3].resolve(resultFor("Canceled"));
    await canceled;
  });
  expect(result.current.view.result?.Query).toBe("Second 2025");

  let stale!: Promise<void>;
  act(() => {
    stale = result.current.search("Old source");
  });
  rerender({ context: "source-2-movie", category: "MOVIE" });
  expect(requests[4].signal.aborted).toBe(true);
  expect(result.current.view.result).toBeNull();
  await act(async () => {
    requests[4].resolve(resultFor("Old source"));
    await stale;
  });
  expect(result.current.view.result).toBeNull();

  act(() => {
    void result.current.search("Unmounted");
  });
  unmount();
  expect(requests[5].signal.aborted).toBe(true);
});
