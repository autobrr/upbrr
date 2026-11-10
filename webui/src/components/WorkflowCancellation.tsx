// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useReleaseSession } from "../releaseSession";
import { pageStyle } from "./ui/pageStyle";

/** Keeps cancellation controls stationary as availability changes; confirmation follows backend readback. */
export function WorkflowCancellation() {
  const { workflow } = useReleaseSession();
  const { available, pending, retry, message } = workflow.cancellation;
  return (
    <section
      aria-label="Active work"
      className={`${pageStyle.panel} mb-3 flex w-fit max-w-full flex-wrap items-center gap-2 py-2`}
    >
      <button
        className="danger px-3 py-1 text-sm"
        type="button"
        disabled={(!available && !pending) || (pending && !retry)}
        onClick={() => void workflow.cancel()}
      >
        {retry ? "Retry cancellation" : pending ? "Cancelling…" : "Cancel active work"}
      </button>
      {pending || message ? (
        <p role="status" className="text-muted-foreground text-sm">
          {message ||
            (pending
              ? "Cancellation requested. Waiting for active work to stop; torrent hashing or verification must finish first. Progress remains live."
              : "")}
        </p>
      ) : null}
    </section>
  );
}
