// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { Checkbox } from "../../components/ui/checkbox";

type DiscCandidate = { discId?: string; discName?: string };

export function groupProgrammes<T extends DiscCandidate>(candidates: readonly T[]) {
  const groups: Array<{ discID: string; discName: string; candidates: T[] }> = [];
  for (const candidate of candidates) {
    const discID = candidate.discId || "single-disc";
    const existing = groups.find((group) => group.discID === discID);
    if (existing) existing.candidates.push(candidate);
    else
      groups.push({
        discID,
        discName: candidate.discName || "Blu-ray disc",
        candidates: [candidate],
      });
  }
  return groups;
}

type Candidate = DiscCandidate & {
  id: string;
  label: string;
  description: string;
  limitation?: string;
};

/** Shared disc grouping and candidate presentation with caller-owned choices. */
export function ProgrammeCandidates({
  candidates,
  selected,
  name,
  multiple = false,
  disabled = false,
  onChoose,
}: Readonly<{
  candidates: readonly Candidate[];
  selected: readonly string[];
  name: string;
  multiple?: boolean;
  disabled?: boolean;
  onChoose: (id: string) => void;
}>) {
  return (
    <div className="overflow-hidden rounded-md border border-border">
      {groupProgrammes(candidates).map((group) => (
        <section key={group.discID} aria-label={group.discName}>
          <h3 className="border-b border-border bg-muted px-3 py-2 text-sm font-semibold text-foreground">
            {group.discName}
          </h3>
          {group.candidates.map((candidate) => {
            const id = `${name}-${encodeURIComponent(candidate.id)}`;
            const unavailable = disabled || Boolean(candidate.limitation);
            return (
              <div
                key={candidate.id}
                className="grid gap-1 border-b border-border px-3 py-2 last:border-b-0 hover:bg-muted"
              >
                <div className="flex select-none items-center gap-2">
                  {multiple ? (
                    <Checkbox
                      id={id}
                      checked={selected.includes(candidate.id)}
                      disabled={unavailable}
                      onCheckedChange={() => onChoose(candidate.id)}
                    />
                  ) : (
                    <input
                      id={id}
                      type="radio"
                      name={name}
                      checked={selected.includes(candidate.id)}
                      disabled={unavailable}
                      onChange={() => onChoose(candidate.id)}
                    />
                  )}
                  <label className="cursor-pointer font-semibold" htmlFor={id}>
                    {candidate.label}
                  </label>
                </div>
                <span className="ml-6 text-xs text-muted-foreground">{candidate.description}</span>
                {candidate.limitation && (
                  <span className="ml-6 text-xs text-muted-foreground">{candidate.limitation}</span>
                )}
              </div>
            );
          })}
        </section>
      ))}
    </div>
  );
}
