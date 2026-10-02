// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { clearAppOperationMocks, installAppOperationMocks } from "../test/appRequestMock";
import { TrackerCatalogProvider, useTrackerCatalog } from ".";

afterEach(clearAppOperationMocks);

describe("TrackerCatalogProvider", () => {
  it("shares one catalog request across consumers", async () => {
    const list = vi.fn(async () => ({
      entries: [
        {
          name: "BTN",
          family: "standalone",
          baseURL: "https://btn.example.invalid",
          uploadContentMode: "none",
          fields: [],
          configured: true,
        },
      ],
      unsupported: [],
    }));
    installAppOperationMocks({ ListTrackerCatalog: list });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: Readonly<{ children: ReactNode }>) => (
      <QueryClientProvider client={queryClient}>
        <TrackerCatalogProvider>{children}</TrackerCatalogProvider>
      </QueryClientProvider>
    );

    const { result } = renderHook(
      () => ({ first: useTrackerCatalog(), second: useTrackerCatalog() }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.first.loaded).toBe(true));
    expect(list).toHaveBeenCalledTimes(1);
    expect(result.current.first.catalog).toBe(result.current.second.catalog);
    expect(result.current.first.catalog?.entries[0]?.uploadContentMode).toBe("none");
  });
});
