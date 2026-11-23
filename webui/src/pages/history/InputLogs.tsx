// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useQuery } from "@tanstack/react-query";
import { historyClient } from "../../api/app";

const coverageMessage = {
  complete: "Complete retained file log.",
  partial: "Partial log. Some entries may not have been retained or may have rotated away.",
  memory: "Recent entries from this session only. Earlier entries may not be retained.",
  expired: "No entries remain. The log may have rotated away or file logging was off.",
};

/** Shows and downloads retained processing logs for the selected input. */
export default function InputLogs({ sourcePath }: { sourcePath: string }) {
  const log = useQuery({
    queryKey: ["history", "log", sourcePath],
    queryFn: ({ signal }) => historyClient.log(sourcePath, signal),
  });

  return (
    <section
      className="mb-3 grid gap-2 rounded-lg border border-border p-3"
      aria-label="Input logs"
    >
      <h3>Logs</h3>
      {log.isPending && <p role="status">Loading log…</p>}
      {log.isError && <p role="alert">{String(log.error)}</p>}
      {log.data && (
        <>
          <p role="status">{coverageMessage[log.data.Coverage]}</p>
          {log.data.DownloadID && (
            <>
              <a href={historyClient.logDownload(log.data.DownloadID)} download>
                Download log
              </a>
              <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words">
                {log.data.Text}
              </pre>
            </>
          )}
        </>
      )}
    </section>
  );
}
