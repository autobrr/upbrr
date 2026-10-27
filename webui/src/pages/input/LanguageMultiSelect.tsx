// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useRef, useState } from "react";
import { Button } from "../../components/ui/button";
import { Checkbox } from "../../components/ui/checkbox";
import correctionChoices from "./correctionChoices.json";

/** Compact language corrections retain custom names and optional hardcoded coverage choices. */
export function LanguageMultiSelect({
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
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const trigger = useRef<HTMLButtonElement>(null);
  const searchInput = useRef<HTMLInputElement>(null);
  const selected = value || [];
  const choices = coverage
    ? correctionChoices.HardcodedSubtitleLanguages
    : correctionChoices.SubtitleLanguages;
  const options = [
    ...choices,
    ...[...new Set(selected)]
      .filter((language) => !choices.some((option) => option.value === language))
      .map((language) => ({ value: language, label: language })),
  ];
  const search = query.trim();
  const matches = options.filter((option) =>
    option.label.toLowerCase().includes(search.toLowerCase()),
  );
  const custom =
    search && !options.some((option) => option.value.toLowerCase() === search.toLowerCase());
  const summary = selected.join(", ") || "None";
  return (
    <div
      className="relative min-w-0"
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape" && open) {
          event.preventDefault();
          event.stopPropagation();
          setOpen(false);
          trigger.current?.focus();
        }
      }}
    >
      <button
        ref={trigger}
        id={id}
        type="button"
        aria-label={label}
        aria-describedby={`${id}-summary`}
        aria-expanded={open}
        aria-controls={open ? `${id}-options` : undefined}
        className="flex h-9 min-w-0 w-full items-center justify-between gap-2 rounded-md border border-input bg-card px-3 text-left text-sm text-card-foreground shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
        onClick={() => {
          setOpen((previous) => !previous);
          setQuery("");
        }}
      >
        <span id={`${id}-summary`} className="truncate" title={summary}>
          {summary}
        </span>
        <span aria-hidden="true">▾</span>
      </button>
      {open ? (
        <fieldset
          id={`${id}-options`}
          aria-label={`${label} choices`}
          className="absolute inset-x-0 top-full z-20 mt-1 grid max-h-72 gap-2 overflow-y-auto rounded-md border border-input bg-card p-3 text-card-foreground shadow-md"
        >
          <input
            ref={searchInput}
            type="search"
            aria-label={`Search ${label}`}
            placeholder="Search or add a language"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <Button type="button" aria-label={`Clear ${label}`} onClick={() => onChange([])}>
            Clear selections
          </Button>
          {matches.map((option, index) => (
            <label
              key={option.value}
              htmlFor={`${id}-choice-${index}`}
              className="flex min-w-0 items-center gap-2"
              onPointerDown={(event) => {
                // Keep label activation from blurring the search and closing its options first.
                event.preventDefault();
              }}
            >
              <Checkbox
                id={`${id}-choice-${index}`}
                checked={selected.includes(option.value)}
                onCheckedChange={(checked) => {
                  onChange(
                    checked
                      ? [...selected, option.value]
                      : selected.filter((language) => language !== option.value),
                  );
                  if (!checked && !choices.some((choice) => choice.value === option.value)) {
                    searchInput.current?.focus();
                  }
                }}
              />
              <span className="min-w-0 [overflow-wrap:anywhere]">{option.label}</span>
            </label>
          ))}
          {custom ? (
            <Button
              type="button"
              className="whitespace-normal [overflow-wrap:anywhere]"
              onClick={() => {
                onChange([...selected, search]);
                setQuery("");
                searchInput.current?.focus();
              }}
            >
              Add {search}
            </Button>
          ) : null}
        </fieldset>
      ) : null}
    </div>
  );
}
