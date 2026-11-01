// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef, useState } from "react";
import { configClient } from "../api/app";
import type { ConnectionTestResult, ConnectionTestTarget } from "../api/app";
import { Button } from "../components/ui/button";

type Props = Readonly<{
  target: ConnectionTestTarget;
  label: string;
  identity: string;
  buildPayload: () => string | null;
}>;

/** Clears results and cancels requests when the tested values change. Values
 * stay in component memory; credentials are never used as React keys or markup.
 */
export function ConnectionTest(props: Props) {
  const [generation, setGeneration] = useState({ identity: props.identity, revision: 0 });
  if (generation.identity !== props.identity) {
    setGeneration({ identity: props.identity, revision: generation.revision + 1 });
  }
  return <ConnectionTestAction key={generation.revision} {...props} />;
}

function ConnectionTestAction({ target, label, buildPayload }: Props) {
  const controller = useRef<AbortController | null>(null);
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState<ConnectionTestResult | null>(null);

  useEffect(() => () => controller.current?.abort(), []);

  const run = async () => {
    if (controller.current) return;
    const payload = buildPayload();
    if (!payload) return;
    const request = new AbortController();
    controller.current = request;
    setPending(true);
    setResult(null);
    try {
      const response = await configClient.testConnection(payload, target, request.signal);
      if (!request.signal.aborted) setResult(response);
    } catch {
      if (!request.signal.aborted) {
        setResult({
          code: "unavailable",
          message: "The connection test could not complete. Try again.",
        });
      }
    } finally {
      if (!request.signal.aborted) {
        controller.current = null;
        setPending(false);
      }
    }
  };

  return (
    <div className="grid min-w-0 gap-2" aria-busy={pending}>
      <Button
        type="button"
        className="w-fit"
        aria-label={`Test connection to ${label}`}
        disabled={pending}
        onClick={() => void run()}
      >
        {pending ? "Testing…" : "Test connection"}
      </Button>
      <p className="m-0 text-sm text-muted-foreground [overflow-wrap:anywhere]" role="status">
        {pending ? "Testing the current form values…" : result?.message}
      </p>
    </div>
  );
}
