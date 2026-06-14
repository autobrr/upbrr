// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import { normalizeJobStatus } from "./jobStatus";

describe("normalizeJobStatus", () => {
  it("normalizes whitespace-padded terminal statuses", () => {
    expect(normalizeJobStatus(" completed ")).toBe("completed");
    expect(normalizeJobStatus("\tFAILED\n")).toBe("failed");
    expect(normalizeJobStatus(" completed_with_errors ")).toBe("completed_with_errors");
  });
});
