// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ComponentProps } from "react";
import { LanguageOptionsPicker } from "../../components/LanguageOptionsPicker";
import correctionChoices from "./correctionChoices.json";

export function LanguageMultiSelect({
  coverage = false,
  ...props
}: Omit<ComponentProps<typeof LanguageOptionsPicker>, "choices" | "allowCustom"> & {
  coverage?: boolean;
}) {
  return (
    <LanguageOptionsPicker
      {...props}
      choices={
        coverage
          ? correctionChoices.HardcodedSubtitleLanguages
          : correctionChoices.SubtitleLanguages
      }
    />
  );
}
