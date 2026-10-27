// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { FieldHelp } from "./FieldHelp";

afterEach(cleanup);

it("opens on hover and keyboard focus, dismisses with Escape, and never submits or toggles a setting", async () => {
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
  screen.getByRole("checkbox").focus();
  await user.hover(help);
  await user.keyboard("{Escape}");
  expect(help).toHaveAttribute("aria-expanded", "false");
  await user.unhover(help);
  screen.getByRole("checkbox").blur();
  await user.hover(help);
  expect(help).toHaveAttribute("aria-expanded", "true");
  await user.unhover(help);
  expect(help).toHaveAttribute("aria-expanded", "false");
  await user.tab();
  expect(help).toHaveFocus();
  expect(help).toHaveAttribute("aria-expanded", "true");
  await user.keyboard("{Escape}");
  expect(help).toHaveAttribute("aria-expanded", "false");
  await user.click(help);
  expect(help).toHaveAttribute("aria-expanded", "true");
  await user.click(help);
  expect(help).toHaveAttribute("aria-expanded", "false");
  expect(change).not.toHaveBeenCalled();
  expect(submit).not.toHaveBeenCalled();
  await user.click(screen.getByText("Capture overlay", { selector: "label" }));
  expect(change).toHaveBeenCalledOnce();
});

it("keeps repeated dynamic help independently associated and supports touch activation", async () => {
  const user = userEvent.setup();
  render(
    <>
      <FieldHelp label="API key">First account credential.</FieldHelp>
      <FieldHelp label="API key">Second account credential.</FieldHelp>
    </>,
  );
  const [first, second] = screen.getAllByRole("button", { name: "Help for API key" });
  expect(first.getAttribute("aria-controls")).not.toBe(second.getAttribute("aria-controls"));
  await user.pointer([
    { keys: "[TouchA>]", target: second },
    { keys: "[/TouchA]", target: second },
  ]);
  expect(second).toHaveAttribute("aria-expanded", "true");
  expect(first).toHaveAttribute("aria-expanded", "false");
  expect(screen.getByText("Second account credential.")).toBeVisible();
});
