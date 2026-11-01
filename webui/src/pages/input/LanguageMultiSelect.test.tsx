// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { LanguageMultiSelect } from "./LanguageMultiSelect";

afterEach(cleanup);

function Languages({
  initial = ["English", "Example Dialect"],
  onChange = vi.fn(),
  coverage = false,
}) {
  const [value, setValue] = useState<readonly string[]>(initial);
  return (
    <LanguageMultiSelect
      coverage={coverage}
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

it("keeps a compact marked summary while searching, selecting, deselecting by label and clearing", async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  const selected = ["English", "French", "Spanish", "Example Dialect"];
  render(<Languages initial={selected} onChange={onChange} />);
  const trigger = screen.getByRole("button", { name: "Languages" });
  expect(trigger).toHaveTextContent(selected.join(", "));
  await user.click(trigger);
  expect(screen.queryByRole("checkbox", { name: "English (Forced)" })).not.toBeInTheDocument();
  expect(screen.queryByRole("checkbox", { name: "English (Full)" })).not.toBeInTheDocument();
  expect(screen.getByRole("checkbox", { name: "Example Dialect" })).toBeChecked();
  const search = screen.getByRole("searchbox", { name: "Search Languages" });
  await user.click(search);
  fireEvent.change(search, { target: { value: "japan" } });
  await user.click(screen.getByRole("checkbox", { name: "Japanese" }));
  expect(onChange).toHaveBeenLastCalledWith([...selected, "Japanese"]);
  fireEvent.change(search, { target: { value: "" } });
  await user.click(screen.getByText("English", { selector: "label span" }));
  expect(onChange).toHaveBeenLastCalledWith(["French", "Spanish", "Example Dialect", "Japanese"]);
  await user.click(screen.getByRole("button", { name: "Clear Languages" }));
  expect(onChange).toHaveBeenLastCalledWith([]);
  expect(trigger).toHaveTextContent("None");
});

it("supports keyboard navigation and retains focus when custom controls disappear", async () => {
  const user = userEvent.setup();
  render(<Languages />);
  await user.tab();
  const trigger = screen.getByRole("button", { name: "Languages" });
  expect(trigger).toHaveFocus();
  await user.keyboard("{Enter}");
  await user.tab();
  const search = screen.getByRole("searchbox");
  expect(search).toHaveFocus();
  await user.keyboard("Example Dialect");
  await user.tab();
  await user.tab();
  expect(screen.getByRole("checkbox", { name: "Example Dialect" })).toHaveFocus();
  await user.keyboard(" ");
  expect(search).toHaveFocus();
  expect(screen.queryByRole("checkbox", { name: "Example Dialect" })).not.toBeInTheDocument();
  await user.tab();
  await user.tab();
  expect(screen.getByRole("button", { name: "Add Example Dialect" })).toHaveFocus();
  await user.keyboard("{Enter}");
  expect(search).toHaveFocus();
  expect(screen.getByRole("checkbox", { name: "Example Dialect" })).toBeChecked();
  expect(trigger).toHaveTextContent("English, Example Dialect");
  await user.keyboard("{Escape}");
  expect(trigger).toHaveFocus();
  expect(trigger).toHaveAttribute("aria-expanded", "false");
});

it("preserves hardcoded coverage choices and custom names independently", async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  render(
    <Languages initial={["English (Forced)", "Example Dialect"]} coverage onChange={onChange} />,
  );
  await user.click(screen.getByRole("button", { name: "Languages" }));
  expect(screen.getByRole("checkbox", { name: "English (Forced)" })).toBeChecked();
  await user.click(screen.getByRole("checkbox", { name: "English (Full)" }));
  expect(onChange).toHaveBeenLastCalledWith([
    "English (Forced)",
    "Example Dialect",
    "English (Full)",
  ]);
  await user.click(screen.getByRole("checkbox", { name: "English (Forced)" }));
  expect(onChange).toHaveBeenLastCalledWith(["Example Dialect", "English (Full)"]);
});
