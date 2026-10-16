// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef, useState } from "react";
import { Button } from "../../components/ui/button";
import { CorrectionSearch } from "./CorrectionChoice";
import correctionChoices from "./correctionChoices.json";

/** Editable language rows preserve every other selection and allow custom names. */
export function LanguageListInput({
  id,
  label,
  value,
  onChange,
  coverage = false,
}: Readonly<{
  coverage?: boolean;
  id: string;
  label: string;
  value: readonly string[] | null | undefined;
  onChange: (value: readonly string[]) => void;
}>) {
  const [draft, setDraft] = useState(() => [...(value || [])]);
  const focused = useRef(false);
  useEffect(() => {
    if (!focused.current) setDraft([...(value || [])]);
  }, [value]);
  const update = (next: string[]) => {
    setDraft(next);
    onChange(
      next.flatMap((entry) =>
        entry
          .split(",")
          .map((part) => part.trim())
          .filter(Boolean),
      ),
    );
  };
  return (
    <div
      className="grid min-w-0 gap-2"
      onFocusCapture={() => {
        focused.current = true;
      }}
      onBlurCapture={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) {
          focused.current = false;
          setDraft([...(value || [])]);
        }
      }}
    >
      {[...draft, ""].map((entry, index) => (
        <div className="flex min-w-0 items-start gap-1" key={index}>
          <div className="min-w-0 flex-1">
            <CorrectionSearch
              id={index === 0 ? id : `${id}-${index}`}
              label={
                index === 0
                  ? label
                  : index === draft.length
                    ? `Add ${label}`
                    : `${label} ${index + 1}`
              }
              value={entry}
              options={
                coverage
                  ? correctionChoices.HardcodedSubtitleLanguages
                  : correctionChoices.SubtitleLanguages
              }
              onChange={(next) => {
                const updated = [...draft];
                updated[index] = next;
                update(updated);
              }}
            />
          </div>
          {index < draft.length ? (
            <Button
              type="button"
              aria-label={`Remove ${label} ${index + 1}`}
              onClick={() => update(draft.filter((_, position) => position !== index))}
            >
              Remove
            </Button>
          ) : null}
        </div>
      ))}
    </div>
  );
}
