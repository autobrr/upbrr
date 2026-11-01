// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { setAppRequestHandlerForTests } from "../api/client";
import { ConnectionTest } from "./ConnectionTest";

afterEach(cleanup);

it("explains an unavailable safe check without offering an action or building a secret payload", () => {
  const buildPayload = vi.fn(() => "{}");
  const request = vi.fn();
  setAppRequestHandlerForTests(request);
  render(
    <ConnectionTest
      target={{ kind: "image_host", instance: "lostimg" }}
      label="Lostimg image hosting"
      identity="draft"
      unavailableReason="A safe connection test has not yet been verified. No request was sent."
      buildPayload={buildPayload}
    />,
  );
  expect(screen.queryByRole("button")).not.toBeInTheDocument();
  expect(screen.getByText(/Connection test unavailable: A safe connection test/)).toBeVisible();
  expect(buildPayload).not.toHaveBeenCalled();
  expect(request).not.toHaveBeenCalled();
});

it("cancels edited tests, discards late results and never revives an earlier success", async () => {
  const pending: Array<{ signal: AbortSignal; resolve: (result: unknown) => void }> = [];
  const request = vi.fn(
    (_method, _body, options) =>
      new Promise((resolve) => {
        pending.push({ signal: options.signal, resolve });
      }),
  );
  setAppRequestHandlerForTests(request);
  const props = {
    target: { kind: "sonarr", instance: "0" },
    label: "Sonarr instance 1",
    buildPayload: () => "{}",
  };
  const { rerender, unmount } = render(<ConnectionTest {...props} identity="first" />);
  const button = screen.getByRole("button", { name: "Test connection to Sonarr instance 1" });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(request).toHaveBeenCalledTimes(1);
  expect(button).toBeDisabled();
  expect(screen.getByRole("status")).toHaveTextContent("Testing the current form values");
  rerender(<ConnectionTest {...props} identity="edited" />);
  expect(pending[0].signal.aborted).toBe(true);
  expect(screen.getByRole("button")).toBeEnabled();
  await act(async () => pending[0].resolve({ code: "success", message: "Stale success" }));
  expect(screen.queryByText("Stale success")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button"));
  await act(async () => pending[1].resolve({ code: "success", message: "Current success" }));
  expect(screen.getByRole("status")).toHaveTextContent("Current success");
  rerender(<ConnectionTest {...props} identity="first" />);
  expect(screen.getByRole("status")).not.toHaveTextContent("Current success");
  fireEvent.click(screen.getByRole("button"));
  unmount();
  expect(pending[2].signal.aborted).toBe(true);
});

it("keeps results and pending requests isolated between instances", async () => {
  const pending = new Map<string, (result: unknown) => void>();
  setAppRequestHandlerForTests(
    (_method, body) =>
      new Promise((resolve) => {
        pending.set((body as { target: { instance: string } }).target.instance, resolve);
      }),
  );
  const instances = (first: string) =>
    [first, "second"].map((identity, index) => (
      <section key={index} aria-label={`instance ${index}`}>
        <ConnectionTest
          target={{ kind: "sonarr", instance: String(index) }}
          label={`Sonarr ${index}`}
          identity={identity}
          buildPayload={() => "{}"}
        />
      </section>
    ));
  const { rerender } = render(<>{instances("first")}</>);
  fireEvent.click(within(screen.getByRole("region", { name: "instance 0" })).getByRole("button"));
  fireEvent.click(within(screen.getByRole("region", { name: "instance 1" })).getByRole("button"));
  await act(async () => pending.get("1")?.({ code: "success", message: "Second instance passed" }));
  rerender(<>{instances("edited")}</>);
  expect(
    within(screen.getByRole("region", { name: "instance 1" })).getByRole("status"),
  ).toHaveTextContent("Second instance passed");
  expect(
    within(screen.getByRole("region", { name: "instance 0" })).getByRole("button"),
  ).toBeEnabled();
});

it("displays fixed transport failure guidance and permits retry", async () => {
  const request = vi
    .fn()
    .mockRejectedValueOnce(new Error("synthetic-private-remote-response"))
    .mockResolvedValue({ code: "credentials_rejected", message: "Check the API key." });
  setAppRequestHandlerForTests(request);
  render(
    <ConnectionTest
      target={{ kind: "tmdb" }}
      label="TMDB"
      identity="draft"
      buildPayload={() => "{}"}
    />,
  );
  fireEvent.click(screen.getByRole("button"));
  expect(
    await screen.findByText("The connection test could not complete. Try again."),
  ).toBeInTheDocument();
  expect(document.body.textContent?.includes("synthetic-private")).toBe(false);
  fireEvent.click(screen.getByRole("button"));
  expect(await screen.findByText("Check the API key.")).toBeInTheDocument();
  expect(screen.getByRole("button")).toBeEnabled();
});
