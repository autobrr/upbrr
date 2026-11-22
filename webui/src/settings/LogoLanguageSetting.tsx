// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { LanguageOptionsPicker } from "../components/LanguageOptionsPicker";
import choices from "../pages/input/correctionChoices.json";

const logoChoices = [...choices.LogoLanguages].sort((a, b) => a.label.localeCompare(b.label));
const logoCodes = new Set(logoChoices.map((choice) => choice.value));

export function LogoLanguageSetting({
  id,
  describedBy,
  label,
  value,
  onChange,
}: Readonly<{
  id: string;
  describedBy?: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
}>) {
  const selected = new Set<string>();
  const unsupported = new Set<string>();
  for (const raw of value.split(/[,;]/)) {
    const entry = raw.trim();
    if (!entry) continue;
    const code = /^([a-z]{2})(?:[_-](?:[a-z]{2}|[0-9]{3}))?$/i.exec(entry)?.[1]?.toLowerCase();
    if (code && logoCodes.has(code)) selected.add(code);
    else unsupported.add(entry);
  }
  const warningId = `${id}-unsupported`;
  return (
    <div className="min-w-0">
      <LanguageOptionsPicker
        allowCustom={false}
        choices={logoChoices}
        id={id}
        label={label}
        describedBy={[describedBy, unsupported.size ? warningId : undefined]
          .filter(Boolean)
          .join(" ")}
        value={[...selected, ...unsupported]}
        onChange={(next) => onChange(next.join(","))}
      />
      {unsupported.size > 0 ? (
        <p id={warningId} className="mt-1 text-sm text-destructive">
          Unsupported logo languages are ignored: {[...unsupported].join(", ")}.
        </p>
      ) : null}
    </div>
  );
}
