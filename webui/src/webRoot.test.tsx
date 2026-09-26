// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { act, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import WebRoot from "./webRoot";
import { authClient, subscribeWebSessionLoss, updateWebCSRFToken } from "./api/client";

vi.mock("./app", () => ({ default: () => <div>Private workspace</div> }));
vi.mock("./api/client", () => ({
  authClient: { status: vi.fn() },
  initializeWebClient: vi.fn(),
  sessionChangedMessage:
    "Web session changed in another tab. Reload this tab to continue with the active login.",
  subscribeWebSessionLoss: vi.fn(() => vi.fn()),
  updateWebCSRFToken: vi.fn(),
}));

it("offers a reload instead of another sign-in when another tab replaces the session", async () => {
  vi.mocked(authClient.status).mockResolvedValue({
    authenticated: true,
    csrfToken: "old-csrf",
    username: "operator",
  });
  render(<WebRoot />);
  expect(await screen.findByText("Private workspace")).toBeInTheDocument();

  const onSessionLoss = vi.mocked(subscribeWebSessionLoss).mock.calls[0][0];
  act(() => onSessionLoss("changed"));

  expect(screen.getByRole("heading", { name: "Session changed" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Reload tab" })).toBeInTheDocument();
  expect(screen.queryByRole("heading", { name: "Sign In" })).not.toBeInTheDocument();
  expect(screen.queryByText("Private workspace")).not.toBeInTheDocument();
  expect(updateWebCSRFToken).toHaveBeenCalledWith("");
});
