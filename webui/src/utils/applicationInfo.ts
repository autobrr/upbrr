// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ApplicationInfo } from "../types";

type ApplicationBuild = Pick<ApplicationInfo, "version" | "buildIdentifier" | "buildTime">;

/** Resolves the release tag, or the development revision and its known UTC commit date. */
export const getApplicationVersionDisplay = ({
  version,
  buildIdentifier,
  buildTime,
}: ApplicationBuild): { version: string; buildDate: string } => {
  const releaseVersion = version.trim();
  const normalizedVersion = releaseVersion.toLowerCase();
  if (releaseVersion && normalizedVersion !== "dev" && normalizedVersion !== "(devel)") {
    return { version: releaseVersion, buildDate: "" };
  }

  const identifier = buildIdentifier.trim() || "dev";
  const buildDate = buildTime.slice(0, 10);

  return { version: identifier, buildDate };
};

/** Formats the version with the known commit date for development builds. */
export const formatApplicationVersion = (build: ApplicationBuild): string => {
  const { version, buildDate } = getApplicationVersionDisplay(build);
  return buildDate ? `${version} (${buildDate})` : version;
};
