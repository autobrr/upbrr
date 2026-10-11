// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { LanguageListInput } from "./LanguageListInput";
afterEach(cleanup);

function Languages({ onChange = vi.fn() }) {
  const [value, setValue] = useState<readonly string[]>(["Spanish"]);
  return (
    <LanguageListInput
      coverage
      id="languages"
      label="Languages"
      value={value}
      onChange={(next) => {
        setValue(next);
        onChange(next);
      }}
    />
  );
}

it("adds full/forced and custom languages without discarding existing choices", () => {
  const onChange = vi.fn();
  render(<Languages onChange={onChange} />);
  const add = screen.getByRole("combobox", { name: "Add Languages" });
  fireEvent.focus(add);
  fireEvent.change(add, { target: { value: "English" } });
  fireEvent.click(screen.getByRole("option", { name: "English (Forced)" }));
  expect(onChange).toHaveBeenLastCalledWith(["Spanish", "English (Forced)"]);
  expect(screen.getByRole("combobox", { name: "Languages" })).toHaveValue("Spanish");
  fireEvent.click(screen.getByRole("button", { name: "Remove Languages 1" }));
  expect(onChange).toHaveBeenLastCalledWith(["English (Forced)"]);
  const custom = screen.getByRole("combobox", { name: "Add Languages" });
  fireEvent.focus(custom);
  fireEvent.change(custom, { target: { value: "Example Dialect" } });
  expect(onChange).toHaveBeenLastCalledWith(["English (Forced)", "Example Dialect"]);
  expect(screen.getByRole("combobox", { name: "Languages 2" })).toHaveValue("Example Dialect");
});

it("resets local language drafts and picker state on Auto remount", () => {
  const { rerender } = render(
    <LanguageListInput
      key="one"
      id="languages"
      label="Languages"
      value={["English (Full)"]}
      onChange={vi.fn()}
    />,
  );
  const input = screen.getByRole("combobox", { name: "Languages" });
  fireEvent.focus(input);
  fireEvent.change(input, { target: { value: "Custom " } });
  rerender(
    <LanguageListInput
      key="auto"
      id="languages"
      label="Languages"
      value={["French"]}
      onChange={vi.fn()}
    />,
  );
  expect(screen.getByRole("combobox", { name: "Languages" })).toHaveValue("French");
  expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
});

it("ordinary subtitle suggestions keep base-language labels", () => {
  render(
    <LanguageListInput id="ordinary" label="Subtitle languages" value={[]} onChange={vi.fn()} />,
  );
  fireEvent.focus(screen.getByRole("combobox", { name: "Subtitle languages" }));
  expect(screen.getByRole("option", { name: "English" })).toBeInTheDocument();
  expect(screen.queryByRole("option", { name: "English (Forced)" })).not.toBeInTheDocument();
  expect(screen.queryByRole("option", { name: "English (Full)" })).not.toBeInTheDocument();
});
