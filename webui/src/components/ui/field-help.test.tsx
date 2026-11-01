// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { FieldHelp } from "./field-help";

afterEach(cleanup);

it("opens on hover and focus, dismisses with Escape, and never submits or toggles a setting", async () => {
  const user = userEvent.setup();
  const submit = vi.fn((event) => event.preventDefault());
  const change = vi.fn();
  render(
    <form onSubmit={submit}>
      <div>
        <label htmlFor="setting">Capture overlay</label>
        <FieldHelp label="Capture overlay" id="setting-help">
          Adds frame information.
        </FieldHelp>
        <input id="setting" type="checkbox" onChange={change} aria-describedby="setting-help" />
      </div>
    </form>,
  );
  const help = screen.getByRole("button", { name: "Help for Capture overlay" });
  expect(screen.getByRole("checkbox")).toHaveAccessibleDescription("Adds frame information.");
  await user.hover(help);
  expect(await screen.findByRole("tooltip")).toHaveTextContent("Adds frame information.");
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  await user.unhover(help);
  await user.tab();
  expect(help).toHaveFocus();
  expect(screen.getByRole("tooltip")).toHaveTextContent("Adds frame information.");
  await user.keyboard("{Escape}");
  expect(help).toHaveFocus();
  expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  await user.click(help);
  expect(change).not.toHaveBeenCalled();
  expect(submit).not.toHaveBeenCalled();
  await user.click(screen.getByText("Capture overlay", { selector: "label" }));
  expect(change).toHaveBeenCalledOnce();
});

it("keeps repeated descriptions independent and toggles or dismisses help with touch", async () => {
  const user = userEvent.setup();
  render(
    <>
      <FieldHelp label="API key" id="first-help">
        First account credential.
      </FieldHelp>
      <input aria-label="First API key" aria-describedby="first-help" />
      <FieldHelp label="API key" id="second-help">
        Second account credential.
      </FieldHelp>
      <input aria-label="Second API key" aria-describedby="second-help" />
    </>,
  );
  const [first, second] = screen.getAllByRole("button", { name: "Help for API key" });
  expect(screen.getByRole("textbox", { name: "First API key" })).toHaveAccessibleDescription(
    "First account credential.",
  );
  expect(screen.getByRole("textbox", { name: "Second API key" })).toHaveAccessibleDescription(
    "Second account credential.",
  );
  const tap = (target: HTMLElement) =>
    user.pointer([
      { keys: "[TouchA>]", target },
      { keys: "[/TouchA]", target },
    ]);
  await tap(second);
  expect(screen.getByRole("tooltip")).toHaveTextContent("Second account credential.");
  expect(first).toHaveAttribute("aria-expanded", "false");
  await tap(second);
  expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  await tap(first);
  expect(screen.getByRole("tooltip")).toHaveTextContent("First account credential.");
  await tap(screen.getByRole("textbox", { name: "Second API key" }));
  await waitFor(() => expect(screen.queryByRole("tooltip")).not.toBeInTheDocument());
});
