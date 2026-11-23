// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { historyClient } from "../../api/app";

const coverageMessage = {
  complete: "Complete retained file log.",
  partial: "Partial log. Some entries may have rotated away, or the log changed during the read.",
  memory: "Recent entries from this session only. Earlier entries and TRACE may not be retained.",
  expired: "No entries remain. The log may have rotated away or file logging was off.",
};

/** Shows the selected input's attempts and links to a server-side browser download. */
export default function InputLogs({ sourcePath }: { sourcePath: string }) {
  const [attemptId, setAttemptId] = useState("");
  const attempts = useQuery({
    queryKey: ["history", "log-attempts", sourcePath],
    queryFn: ({ signal }) => historyClient.logAttempts(sourcePath, signal),
  });
  const current = attempts.data?.find((attempt) => attempt.ID === attemptId) ?? attempts.data?.[0];
  const id = current?.ID ?? "";
  const log = useQuery({
    queryKey: ["history", "log", id],
    queryFn: ({ signal }) => historyClient.log(id, signal),
    enabled: id !== "",
  });

  return (
    <section
      className="mb-3 grid gap-2 rounded-lg border border-border p-3"
      aria-label="Input logs"
    >
      <h3>Logs</h3>
      {attempts.isPending && <p role="status">Loading attempts…</p>}
      {attempts.isError && <p role="alert">{String(attempts.error)}</p>}
      {attempts.data?.length === 0 && <p>No tagged logs are recorded for this input.</p>}
      {current && (
        <>
          <label className="grid gap-1">
            <span>Processing attempt</span>
            <select value={id} onChange={(event) => setAttemptId(event.target.value)}>
              {attempts.data?.map((attempt) => (
                <option key={attempt.ID} value={attempt.ID}>
                  {new Date(attempt.StartedAt).toLocaleString()} · {attempt.Kind} ·{" "}
                  {attempt.Outcome || "No recorded outcome"}
                </option>
              ))}
            </select>
          </label>
          {log.isPending && <p role="status">Loading log…</p>}
          {log.isError && <p role="alert">{String(log.error)}</p>}
          {log.data && (
            <>
              <p role="status">{coverageMessage[log.data.Coverage]}</p>
              {log.data.Text && (
                <>
                  <a href={historyClient.logDownload(id)} download>
                    Download log
                  </a>
                  <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words">
                    {log.data.Text}
                  </pre>
                </>
              )}
            </>
          )}
        </>
      )}
    </section>
  );
}
