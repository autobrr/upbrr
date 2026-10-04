// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { CorrectionSearch, CorrectionSelect } from "./CorrectionChoice";
import correctionChoices from "./correctionChoices.json";

afterEach(cleanup);
const options = [
  { value: "AMZN", label: "Amazon Prime Video (AMZN)" },
  { value: "NF", label: "Netflix (NF)" },
];

function Search({ initial = "NF", onChange = vi.fn() }) {
  const [value, setValue] = useState(initial);
  return (
    <CorrectionSearch
      id="service"
      label="Service"
      options={options}
      value={value}
      onChange={(next) => {
        setValue(next);
        onChange(next);
      }}
    />
  );
}

it("browses every suggestion, filters names and codes, and restores all after clearing", () => {
  const onChange = vi.fn();
  render(<Search onChange={onChange} />);
  const input = screen.getByRole("combobox", { name: "Service" });
  fireEvent.focus(input);
  expect(screen.getAllByRole("option")).toHaveLength(2);
  expect(onChange).not.toHaveBeenCalled();
  for (const value of ["Ama", "aMz"]) {
    fireEvent.change(input, { target: { value } });
    expect(screen.getAllByRole("option")).toHaveLength(1);
    expect(screen.getByRole("option")).toHaveTextContent("Amazon Prime Video (AMZN)");
  }
  fireEvent.change(input, { target: { value: "" } });
  expect(screen.getAllByRole("option")).toHaveLength(2);
  fireEvent.click(screen.getByRole("option", { name: "Amazon Prime Video (AMZN)" }));
  expect(input).toHaveValue("AMZN");
  expect(onChange).toHaveBeenLastCalledWith("AMZN");
  expect(input).toHaveAttribute("aria-expanded", "false");
});

it("supports keyboard selection, dismissal, native text editing and custom values", () => {
  const onChange = vi.fn();
  render(<Search onChange={onChange} />);
  const input = screen.getByRole("combobox", { name: "Service" });
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(input).toHaveAttribute("aria-activedescendant", "service-option-0");
  expect(input).toHaveValue("NF");
  fireEvent.keyDown(input, { key: "Enter" });
  expect(input).toHaveValue("AMZN");
  fireEvent.keyDown(input, { key: "ArrowUp" });
  expect(input).toHaveAttribute("aria-activedescendant", "service-option-1");
  fireEvent.keyDown(input, { key: "Escape" });
  expect(input).toHaveValue("AMZN");
  fireEvent.change(input, { target: { value: "Custom service" } });
  expect(screen.queryByRole("option")).not.toBeInTheDocument();
  expect(input).not.toHaveAttribute("aria-activedescendant");
  fireEvent.keyDown(input, { key: "Enter" });
  expect(input).toHaveValue("Custom service");
  expect(onChange).toHaveBeenLastCalledWith("Custom service");
  fireEvent.blur(input);
  expect(input).toHaveAttribute("aria-expanded", "false");
});

it.each(["Category", "Type", "Source", "Resolution"] as const)(
  "%s offers only supported choices without changing an unsupported or missing current value",
  (label) => {
    const onChange = vi.fn();
    const options = correctionChoices[label];
    const { rerender } = render(
      <CorrectionSelect
        id="choice"
        label={label}
        value="Legacy"
        options={options}
        onChange={onChange}
      />,
    );
    const choice = screen.getByRole("combobox", { name: label });
    expect(choice.tagName).toBe("SELECT");
    expect(choice).toHaveValue("Legacy");
    expect(
      within(choice).getByRole("option", { name: "Unsupported current value: Legacy" }),
    ).toBeDisabled();
    expect(screen.queryByRole("button", { name: `Enter custom ${label}` })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(within(choice).queryByRole("option", { name: "Blank" })).not.toBeInTheDocument();
    expect(
      Array.from(
        choice.querySelectorAll("option:not(:disabled)"),
        (option) => (option as HTMLOptionElement).value,
      ),
    ).toEqual(options.map((option) => option.value));
    expect(onChange).not.toHaveBeenCalled();
    rerender(
      <CorrectionSelect id="choice" label={label} value="" options={options} onChange={onChange} />,
    );
    expect(choice).toHaveValue("");
    expect(within(choice).getByRole("option", { name: `Choose ${label}` })).toBeDisabled();
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.change(choice, { target: { value: "" } });
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.change(choice, { target: { value: options[0].value } });
    expect(onChange).toHaveBeenCalledExactlyOnceWith(options[0].value);
  },
);

it("displays supported automatic casing without writing a correction", () => {
  const onChange = vi.fn();
  render(
    <CorrectionSelect
      id="type"
      label="Type"
      value="encode"
      options={correctionChoices.Type}
      onChange={onChange}
    />,
  );
  expect(screen.getByRole("combobox", { name: "Type" })).toHaveValue("ENCODE");
  expect(screen.queryByRole("option", { name: /Unsupported/ })).not.toBeInTheDocument();
  expect(onChange).not.toHaveBeenCalled();
});
