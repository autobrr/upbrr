// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef, useState } from "react";
import { Button } from "../../components/ui/button";
import { Select } from "../../components/ui/select";

type Choice = Readonly<{ value: string; label: string }>;
type Props = Readonly<{
  id: string;
  label: string;
  value: string;
  options: readonly Choice[];
  onChange: (value: string) => void;
}>;

/** Native choices retain unknown saved values and an explicit custom-entry path. */
export function CorrectionSelect({ id, label, value, options, onChange }: Props) {
  const [custom, setCustom] = useState(false);
  return (
    <div className="grid min-w-0 gap-2">
      <Select
        id={custom ? `${id}-choices` : id}
        aria-label={custom ? `${label} choices` : label}
        value={value}
        onChange={(event) => {
          const next = event.target.value;
          setCustom(false);
          onChange(next);
        }}
      >
        <option value="">Blank</option>
        {value && !options.some((option) => option.value === value) ? (
          <option value={value}>{value}</option>
        ) : null}
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </Select>
      <Button type="button" aria-label={`Enter custom ${label}`} onClick={() => setCustom(true)}>
        Custom value
      </Button>
      {custom ? (
        <input
          id={id}
          aria-label={label}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : null}
    </div>
  );
}

/** Editable suggestions preserve custom corrections; only a chosen option supplies its canonical value. */
export function CorrectionSearch({ id, label, value, options, onChange }: Props) {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const choices = options.filter((option) =>
    `${option.label} ${option.value}`.toLowerCase().includes(query.toLowerCase()),
  );
  const activeID = open && choices[active] ? `${id}-option-${active}` : undefined;
  useEffect(() => {
    if (activeID)
      list.current?.querySelector(`[id="${activeID}"]`)?.scrollIntoView({ block: "nearest" });
  }, [activeID]);
  const browse = () => {
    setQuery("");
    setActive(-1);
    setOpen(true);
  };
  const choose = (choice: Choice) => {
    onChange(choice.value);
    setOpen(false);
    setActive(-1);
  };
  return (
    <div
      className="relative min-w-0"
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
      }}
    >
      <div className="flex min-w-0 gap-1">
        <input
          ref={input}
          id={id}
          role="combobox"
          aria-label={label}
          aria-autocomplete="list"
          aria-expanded={open}
          aria-controls={open ? `${id}-options` : undefined}
          aria-activedescendant={activeID}
          autoComplete="off"
          value={value}
          onFocus={browse}
          onChange={(event) => {
            onChange(event.target.value);
            setQuery(event.target.value);
            setActive(-1);
            setOpen(true);
          }}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing) return;
            if (event.key === "ArrowDown" || event.key === "ArrowUp") {
              event.preventDefault();
              if (!open) browse();
              const count = open ? choices.length : options.length;
              setActive((previous) =>
                event.key === "ArrowDown"
                  ? Math.min((open ? previous : -1) + 1, count - 1)
                  : previous < 0 || !open
                    ? count - 1
                    : Math.max(previous - 1, 0),
              );
            } else if (event.key === "Enter" && open && choices[active]) {
              event.preventDefault();
              choose(choices[active]);
            } else if (event.key === "Escape" && open) {
              event.preventDefault();
              setOpen(false);
            } else if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) {
              setActive(-1);
            }
          }}
        />
        <Button
          type="button"
          tabIndex={-1}
          aria-label={`Browse ${label}`}
          aria-expanded={open}
          onPointerDown={(event) => event.preventDefault()}
          onClick={() => {
            if (open) setOpen(false);
            else {
              input.current?.focus();
              browse();
            }
          }}
        >
          ⌄
        </Button>
      </div>
      {open ? (
        <div className="absolute z-20 mt-1 max-h-60 w-full overflow-y-auto rounded-md border border-input bg-card text-card-foreground shadow-lg">
          <ul ref={list} id={`${id}-options`} role="listbox" aria-label={`${label} suggestions`}>
            {choices.map((option, index) => (
              <li
                key={option.value}
                id={`${id}-option-${index}`}
                role="option"
                aria-selected={index === active}
                className={`cursor-pointer break-words px-3 py-2 text-sm hover:bg-accent ${index === active ? "bg-accent" : ""}`}
                onPointerDown={(event) => event.preventDefault()}
                onClick={() => choose(option)}
              >
                {option.label}
              </li>
            ))}
          </ul>
          {!choices.length ? (
            <p className="px-3 py-2 text-sm text-muted-foreground">
              No matching choices. Custom values are allowed.
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
