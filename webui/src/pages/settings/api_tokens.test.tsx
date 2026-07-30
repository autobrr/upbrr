// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { screen } from "@testing-library/dom";
import { cleanup, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { clearAppOperationMocks, installAppOperationMocks } from "../../test/appRequestMock";

import APITokensSettings from "./api_tokens";

const activeRecord = {
  id: "token-id-1",
  name: "Automation",
  ownerId: "default",
  scopes: ["workflow:read"],
  createdAt: "2026-07-22T10:00:00Z",
};

describe("APITokensSettings", () => {
  afterEach(() => {
    cleanup();
    clearAppOperationMocks();
    vi.restoreAllMocks();
  });

  it("lists safe metadata and shows generated plaintext once", async () => {
    const create = vi.fn().mockResolvedValue({
      record: { ...activeRecord, id: "token-id-2", name: "Web automation" },
      token: "synthetic-generated-api-token-value",
    });
    installAppOperationMocks({
      ListAPITokens: vi.fn().mockResolvedValue([activeRecord]),
      CreateAPIToken: create,
    });

    render(<APITokensSettings />);

    await screen.findByText("token-id-1");
    expect(screen.queryByLabelText("Generated API token")).not.toBeInTheDocument();

    const nameInput = screen.getByLabelText("Name");
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "Web automation");
    await userEvent.click(screen.getByRole("button", { name: "Generate token" }));

    await waitFor(() =>
      expect(create).toHaveBeenCalledWith("Web automation", "default", [
        "workflow:read",
        "workflow:write",
        "workflow:execute",
      ]),
    );
    const generatedInput = screen.getByLabelText("Generated API token") as HTMLInputElement;
    expect(Boolean(generatedInput.value)).toBe(true);
    expect(screen.getByText("Copy this token now. It cannot be shown again.")).toBeInTheDocument();
  });

  it("revokes after explicit confirmation and reloads status", async () => {
    const list = vi
      .fn()
      .mockResolvedValueOnce([activeRecord])
      .mockResolvedValueOnce([{ ...activeRecord, revokedAt: "2026-07-22T10:05:00Z" }]);
    const revoke = vi.fn().mockResolvedValue(undefined);
    installAppOperationMocks({
      ListAPITokens: list,
      RevokeAPIToken: revoke,
    });

    render(<APITokensSettings />);

    await userEvent.click(await screen.findByRole("button", { name: "Revoke" }));
    expect(screen.getByText("Revoke Automation?")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Revoke token" }));

    await waitFor(() => expect(revoke).toHaveBeenCalledWith("token-id-1"));
    await waitFor(() => expect(screen.getByText("Revoked")).toBeInTheDocument());
  });
});
