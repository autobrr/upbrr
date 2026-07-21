// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { MouseEvent } from "react";

const normalizeExternalHTTPURL = (value: string): string | null => {
  const href = value.trim();
  if (!href) return null;

  let parsed: URL;
  try {
    parsed = new URL(href);
  } catch {
    return null;
  }

  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return null;
  }

  return parsed.toString();
};

const openExternalURL = async (url: string) => {
  window.open(url, "_blank", "noopener,noreferrer");
};

export const handleExternalLinkClick = (event: MouseEvent<HTMLElement>) => {
  const target = event.target;
  if (!(target instanceof Element)) {
    return;
  }

  const anchor = target.closest("a[href]");
  if (!(anchor instanceof HTMLAnchorElement)) {
    return;
  }

  const href = anchor.getAttribute("href");
  if (!href) {
    return;
  }

  const normalized = normalizeExternalHTTPURL(href);
  if (!normalized) {
    return;
  }

  event.preventDefault();
  event.stopPropagation();
  void openExternalURL(normalized);
};
