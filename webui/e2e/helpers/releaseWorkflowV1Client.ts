// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

/** Minimal retained workflow projection consumed by HTTP-only E2E scenarios. */
export type WorkflowV1Current = Readonly<{
  workflow: Readonly<{
    id: string;
    revision: number;
    status: string;
    requiredActions?: readonly Readonly<{
      id: string;
      kind: string;
      trackerId?: string;
      workflowRevision: number;
      options?: readonly Readonly<{ value: string; label: string }>[];
    }>[];
  }>;
  release?: Readonly<{ release: Readonly<{ Generation: number }> }>;
  projections?: Readonly<{
    status: string;
    projections: readonly Readonly<{
      trackerId: string;
      readiness: string;
      dupeReady: boolean;
      uploadReady: boolean;
    }>[];
  }>;
  preflight?: Readonly<{
    status: string;
    results: readonly Readonly<{
      trackerId: string;
      state: string;
      authReady: boolean;
      requiredActions?: readonly unknown[];
      failures?: readonly Readonly<{
        failure: Readonly<{ Code: string }>;
      }>[];
    }>[];
  }>;
  dupes?: Readonly<{ status: string }>;
  media?: Readonly<{ status: string }>;
  descriptions?: Readonly<{ status: string }>;
  dryRun?: Readonly<{
    id: string;
    revision: number;
    status: string;
    reports: readonly Readonly<{
      trackerId: string;
      uploadReleaseName: string;
      status: string;
    }>[];
  }>;
  uploadResult?: Readonly<{
    id: string;
    revision: number;
    status: string;
    results: readonly Readonly<{ trackerId: string; status: string }>[];
  }>;
  operation?: WorkflowV1Operation;
}>;

/** Durable operation projection consumed by HTTP-only E2E polling. */
export type WorkflowV1Operation = Readonly<{
  id: string;
  workflowId: string;
  status: string;
  message?: string;
  failures?: readonly unknown[];
}>;

const activeOperationStatuses = new Set(["queued", "running"]);

/** Strict HTTP-only E2E client for composite workflow requests and polling. */
export class ReleaseWorkflowV1Client {
  current: WorkflowV1Current | null = null;

  constructor(
    private readonly baseURL: string,
    private readonly token: string,
  ) {}

  /** Fetches one exact workflow projection without polling. */
  async get(workflowID: string): Promise<Response> {
    return fetch(this.url(`/workflows/${workflowID}`), {
      headers: { Authorization: `Bearer ${this.token}` },
    });
  }

  /** Sends one authenticated v1 request with optional concurrency authority. */
  async raw(
    path: string,
    options: Readonly<{
      method?: string;
      body?: object;
      revision?: number;
      idempotencyKey?: string;
      token?: string;
    }> = {},
  ): Promise<Response> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${options.token ?? this.token}`,
    };
    if (options.body !== undefined) headers["Content-Type"] = "application/json";
    if (options.revision !== undefined) headers["If-Match"] = `"${options.revision}"`;
    if (options.idempotencyKey) headers["Idempotency-Key"] = options.idempotencyKey;
    return fetch(this.url(path), {
      method: options.method ?? "GET",
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    });
  }

  /** Requires an accepted operation, polls it to success, then returns current state. */
  async accepted(response: Response): Promise<WorkflowV1Current> {
    const payload: unknown = await response.json();
    if (!response.ok) {
      throw new Error(`API ${response.status}: ${JSON.stringify(payload)}`);
    }
    if (response.status !== 202) {
      throw new Error(`API ${response.status}: expected an accepted workflow operation`);
    }
    return this.awaitOperation(assertAcceptedWorkflowOperation(payload));
  }

  private async awaitOperation(initial: WorkflowV1Operation): Promise<WorkflowV1Current> {
    let operation = initial;
    const deadline = Date.now() + 30_000;
    while (activeOperationStatuses.has(operation.status)) {
      if (Date.now() >= deadline) {
        throw new Error(`workflow operation ${operation.id} did not finish`);
      }
      await new Promise((resolve) => setTimeout(resolve, 25));
      const response = await fetch(
        this.url(`/workflows/${operation.workflowId}/operations/${operation.id}`),
        { headers: { Authorization: `Bearer ${this.token}` } },
      );
      const payload: unknown = await response.json();
      if (!response.ok) {
        throw new Error(`API ${response.status}: ${JSON.stringify(payload)}`);
      }
      operation = assertWorkflowOperation(payload);
    }
    if (["failed", "interrupted", "canceled"].includes(operation.status)) {
      throw new Error(
        `workflow operation ${operation.status}: ${operation.message || ""} ${JSON.stringify(operation.failures || [])}`,
      );
    }
    const response = await this.get(operation.workflowId);
    const payload: unknown = await response.json();
    if (!response.ok) {
      throw new Error(`API ${response.status}: ${JSON.stringify(payload)}`);
    }
    this.current = assertWorkflowCurrent(payload);
    this.assertCurrentETag(response, this.current);
    return this.current;
  }

  private assertCurrentETag(response: Response, current: WorkflowV1Current) {
    const etag = response.headers.get("etag");
    if (etag !== `"${current.workflow.revision}"`) {
      throw new Error(`ETag ${etag} does not match revision ${current.workflow.revision}`);
    }
  }

  private url(path: string) {
    return new URL(`api/v1${path.replace(/^\//, "/")}`, this.baseURL).toString();
  }
}

function assertWorkflowCurrent(value: unknown): WorkflowV1Current {
  if (!value || typeof value !== "object" || !("workflow" in value)) {
    throw new Error("response does not contain a workflow");
  }
  const workflow = value.workflow;
  if (
    !workflow ||
    typeof workflow !== "object" ||
    !("id" in workflow) ||
    typeof workflow.id !== "string" ||
    !("revision" in workflow) ||
    typeof workflow.revision !== "number"
  ) {
    throw new Error("response workflow does not match the v1 schema");
  }
  return value as WorkflowV1Current;
}

function assertWorkflowOperation(value: unknown): WorkflowV1Operation {
  if (
    !value ||
    typeof value !== "object" ||
    !("id" in value) ||
    typeof value.id !== "string" ||
    !("workflowId" in value) ||
    typeof value.workflowId !== "string" ||
    !("status" in value) ||
    typeof value.status !== "string"
  ) {
    throw new Error("response does not contain a workflow operation");
  }
  return value as WorkflowV1Operation;
}

function assertAcceptedWorkflowOperation(value: unknown): WorkflowV1Operation {
  if (!value || typeof value !== "object" || !("operation" in value)) {
    throw new Error("accepted response does not contain a workflow operation");
  }
  return assertWorkflowOperation(value.operation);
}
