// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach } from "vitest";
import { setAppRequestHandlerForTests } from "../api/client";
import {
  clearAppOperationMocks,
  getAppOperationMocks,
  invokeAppRequestMock,
} from "./appRequestMock";

beforeEach(() => {
  setAppRequestHandlerForTests(async (method, body, options) => {
    const operations = getAppOperationMocks();
    if (!operations) throw new Error(`missing app request mock for ${method}`);
    return invokeAppRequestMock(operations, method, body, options);
  });
});

afterEach(() => {
  setAppRequestHandlerForTests(null);
  clearAppOperationMocks();
});
