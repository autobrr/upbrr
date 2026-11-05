// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef, useState } from "react";
import type { ProviderSearchResult } from "../types";
import type { ReleaseSessionPorts } from "./ports";

/** Keeps advisory search results separate from preparation and correction state. */
export const useProviderSearch = (
  port: ReleaseSessionPorts["providerSearch"],
  contextKey: string,
  category: string,
) => {
  const currentContext = useRef(contextKey);
  currentContext.current = contextKey;
  const controller = useRef<AbortController | null>(null);
  const [state, setState] = useState<{
    contextKey: string;
    status: "idle" | "running" | "ready" | "error";
    result: ProviderSearchResult | null;
    error: string;
  }>({ contextKey, status: "idle", result: null, error: "" });

  useEffect(() => {
    setState((previous) =>
      previous.contextKey === contextKey &&
      (previous.status !== "running" || controller.current !== null)
        ? previous
        : { contextKey, status: "idle", result: null, error: "" },
    );
    return () => {
      controller.current?.abort();
      controller.current = null;
    };
  }, [contextKey, port]);

  const view =
    state.contextKey === contextKey
      ? state
      : { contextKey, status: "idle" as const, result: null, error: "" };

  const search = async (query: string) => {
    if (!query.trim()) return;
    controller.current?.abort();
    const attempt = new AbortController();
    controller.current = attempt;
    const previous = view.result;
    setState({ contextKey, status: "running", result: previous, error: "" });
    try {
      const result = await port.search(query.trim(), category, attempt.signal);
      if (
        attempt.signal.aborted ||
        controller.current !== attempt ||
        currentContext.current !== contextKey
      )
        return;
      setState({ contextKey, status: "ready", result, error: "" });
    } catch {
      if (
        attempt.signal.aborted ||
        controller.current !== attempt ||
        currentContext.current !== contextKey
      )
        return;
      setState({
        contextKey,
        status: "error",
        result: previous,
        error: "Provider search could not complete. Retry the search.",
      });
    } finally {
      if (controller.current === attempt) controller.current = null;
    }
  };

  const cancel = () => {
    controller.current?.abort();
    controller.current = null;
    setState({
      contextKey,
      status: view.result ? "ready" : "idle",
      result: view.result,
      error: "Search canceled.",
    });
  };
  return { view, search, cancel };
};
