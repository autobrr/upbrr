// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";
import { Checkbox } from "./ui/checkbox";
import { Select } from "./ui/select";
import { Switch } from "./ui/switch";
import { loggingClient } from "../api/app";
import { subscribeWebEvent } from "../api/client";
import type { ConfigMap, ConfigValue, FieldMeta } from "../types";
import { cn } from "../utils/cn";

type LogEntry = {
  ID: number;
  Time: string;
  Level: string;
  Message: string;
};

type LogSettingsPanelProps = Readonly<{
  configData: ConfigMap;
  renderField: (
    label: string,
    value: ConfigValue,
    path: string[],
    meta?: FieldMeta,
  ) => ReactElement;
  updateConfigValue: (path: string[], value: ConfigValue) => void;
  fieldMeta: Record<string, FieldMeta>;
}>;

const LOG_SOFT_CAP = 1000;
const LOG_HARD_CAP = 10000;

const levelOrder = ["trace", "debug", "info", "warn", "error"];

const levelBadgeClass = (level: string) => {
  switch (level.toLowerCase()) {
    case "error":
      return "bg-destructive/15 text-destructive-text";
    case "warn":
      return "bg-muted text-foreground";
    case "debug":
      return "bg-secondary text-secondary-foreground";
    case "trace":
      return "bg-muted text-muted-foreground";
    default:
      return "bg-primary/15 text-foreground";
  }
};

const normalizeEntry = (payload: unknown): LogEntry | null => {
  if (!payload) return null;
  if (typeof payload === "string") {
    return {
      ID: Date.now(),
      Time: new Date().toISOString(),
      Level: "info",
      Message: payload,
    };
  }
  if (typeof payload !== "object" || Array.isArray(payload)) return null;
  const values = payload as Record<string, unknown>;
  const level = String(values.Level ?? values.level ?? "info").toLowerCase();
  return {
    ID: Number(values.ID ?? values.id ?? Date.now()),
    Time: String(values.Time ?? values.time ?? new Date().toISOString()),
    Level: level,
    Message: String(values.Message ?? values.message ?? ""),
  };
};

const normalizeLevels = () =>
  levelOrder.reduce(
    (acc, level) => {
      acc[level] = true;
      return acc;
    },
    {} as Record<string, boolean>,
  );

const formatTime = (iso: string) => {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "--:--:--";
  return date.toLocaleTimeString();
};

/** Builds the dedupe/render identity for log rows across logger restarts. */
const logEntryKey = (entry: LogEntry) =>
  `${Number.isFinite(entry.ID) ? entry.ID : "no-id"}|${entry.Time}|${entry.Level}|${entry.Message}`;

export default function LogSettingsPanel({
  configData,
  renderField,
  updateConfigValue,
  fieldMeta,
}: LogSettingsPanelProps) {
  const [logPath, setLogPath] = useState("");
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [search, setSearch] = useState("");
  const [autoScroll, setAutoScroll] = useState(true);
  const [connected, setConnected] = useState(false);
  const [bufferWarning, setBufferWarning] = useState("");
  const [mutedPatterns, setMutedPatterns] = useState<string[]>([]);
  const [pendingMute, setPendingMute] = useState("");
  const [levelFilter, setLevelFilter] = useState<Record<string, boolean>>(normalizeLevels());

  /** Keeps buffer trimming policy current without restarting the log stream. */
  const autoScrollRef = useRef(autoScroll);
  const streamStopRef = useRef<null | (() => void)>(null);
  const logEndRef = useRef<HTMLDivElement | null>(null);
  const logStreamRef = useRef<HTMLDivElement | null>(null);

  const loggingConfigValue = configData.Logging;
  const loggingConfig: ConfigMap =
    loggingConfigValue &&
    typeof loggingConfigValue === "object" &&
    !Array.isArray(loggingConfigValue)
      ? loggingConfigValue
      : {};
  const levelValue = String(loggingConfig.Level ?? "info");

  const filteredEntries = useMemo(() => {
    const searchTerm = search.trim().toLowerCase();
    return entries.filter((entry) => {
      const levelKey = entry.Level.toLowerCase();
      if (!levelFilter[levelKey]) return false;
      if (mutedPatterns.includes(entry.Message)) return false;
      if (searchTerm && !entry.Message.toLowerCase().includes(searchTerm)) return false;
      return true;
    });
  }, [entries, search, levelFilter, mutedPatterns]);

  useEffect(() => {
    autoScrollRef.current = autoScroll;
  }, [autoScroll]);

  const persistMuted = async (patterns: string[]) => {
    const updater = loggingClient.updateExclusions;
    try {
      await updater(patterns);
    } catch (err) {
      console.error("Failed to update log exclusions", err);
    }
  };

  const appendEntries = useCallback((incoming: LogEntry[]) => {
    if (incoming.length === 0) return;
    setEntries((prev) => {
      // Recent-log backfills can overlap live stream events; logger IDs keep
      // the rendered buffer idempotent across both sources. IDs restart with
      // each logger instance, so include row content to survive stream rebinds.
      const seenRows = new Set(prev.map(logEntryKey));
      const uniqueIncoming = incoming.filter((entry) => {
        const key = logEntryKey(entry);
        if (seenRows.has(key)) return false;
        seenRows.add(key);
        return true;
      });
      if (uniqueIncoming.length === 0) return prev;
      let next = [...prev, ...uniqueIncoming];
      if (autoScrollRef.current && next.length > LOG_SOFT_CAP) {
        next = next.slice(-LOG_SOFT_CAP);
      } else if (!autoScrollRef.current && next.length > LOG_HARD_CAP) {
        next = next.slice(-LOG_HARD_CAP);
        setBufferWarning("Log buffer capped. Oldest entries were dropped.");
      }
      return next;
    });
  }, []);

  /** Backfills buffered log entries only while the caller's effect is still active. */
  const fetchRecentLogs = useCallback(
    async (isActive: () => boolean = () => true) => {
      const getRecent = loggingClient.getRecent;
      try {
        const payload = await getRecent(LOG_SOFT_CAP);
        if (!isActive()) return;
        const normalized = Array.isArray(payload)
          ? payload.map(normalizeEntry).filter((entry): entry is LogEntry => entry !== null)
          : [];
        appendEntries(normalized);
      } catch (err) {
        console.error("Failed to load recent logs", err);
      }
    },
    [appendEntries],
  );

  useEffect(() => {
    const fetchLogPath = async () => {
      const getLogPath = loggingClient.getPath;
      try {
        const path = await getLogPath();
        setLogPath(path);
      } catch (err) {
        console.error("Failed to load log path", err);
      }
    };
    fetchLogPath();
  }, []);

  useEffect(() => {
    let active = true;
    fetchRecentLogs(() => active);
    return () => {
      active = false;
    };
  }, [fetchRecentLogs]);

  useEffect(() => {
    const fetchMuted = async () => {
      const getter = loggingClient.getExclusions;
      try {
        const patterns = await getter();
        if (Array.isArray(patterns)) {
          setMutedPatterns(patterns);
        }
      } catch (err) {
        console.error("Failed to load log exclusions", err);
      }
    };
    fetchMuted();
  }, []);

  useEffect(() => {
    let active = true;
    const startStream = async () => {
      const start = loggingClient.startStream;
      const stop = loggingClient.stopStream;
      // Tracks a stream that exists before event subscription cleanup is installed.
      let pendingStreamID = "";

      try {
        const streamID = await start();
        pendingStreamID = streamID;
        if (!active) {
          pendingStreamID = "";
          await stop(streamID);
          return;
        }
        const eventName = `log:stream:${streamID}`;
        const off = subscribeWebEvent(eventName, (payload: unknown) => {
          const entry = normalizeEntry(payload);
          if (entry) appendEntries([entry]);
        });
        setConnected(true);
        streamStopRef.current = () => {
          off();
          stop(streamID).catch(() => undefined);
        };
        pendingStreamID = "";
        // Backfill after subscribing to cover entries emitted between
        // StartLogStream resolving and the event listener attaching.
        await fetchRecentLogs(() => active);
      } catch (err) {
        if (pendingStreamID) {
          await stop(pendingStreamID).catch(() => undefined);
        }
        setConnected(false);
        console.error("Failed to start log stream", err);
      }
    };

    startStream();

    return () => {
      active = false;
      setConnected(false);
      if (streamStopRef.current) {
        streamStopRef.current();
        streamStopRef.current = null;
      }
    };
  }, [appendEntries, fetchRecentLogs]);

  useEffect(() => {
    if (!autoScroll) return;
    const container = logStreamRef.current;
    if (!container) return;
    container.scrollTop = container.scrollHeight;
  }, [filteredEntries.length, autoScroll]);

  const handleAddMute = () => {
    const trimmed = pendingMute.trim();
    if (!trimmed) return;
    if (mutedPatterns.includes(trimmed)) {
      setPendingMute("");
      return;
    }
    const next = [...mutedPatterns, trimmed];
    setMutedPatterns(next);
    setPendingMute("");
    persistMuted(next);
  };

  const handleRemoveMute = (pattern: string) => {
    const next = mutedPatterns.filter((entry) => entry !== pattern);
    setMutedPatterns(next);
    persistMuted(next);
  };

  const handleClearLogs = () => {
    setEntries([]);
    setBufferWarning("");
  };

  const toggleLevel = (level: string) => {
    setLevelFilter((prev) => ({ ...prev, [level]: !prev[level] }));
  };

  const handleMuteMessage = (message: string) => {
    if (!message.trim()) return;
    if (mutedPatterns.includes(message)) return;
    const next = [...mutedPatterns, message];
    setMutedPatterns(next);
    persistMuted(next);
  };

  return (
    <div className="grid gap-3 lg:grid-cols-[minmax(260px,360px)_minmax(0,1fr)]">
      <div className="panel grid gap-3">
        <div className="flex items-start justify-between gap-3">
          <div>
            <p className="label">Logging</p>
            <p className="helper">Adjust log verbosity and file rotation.</p>
          </div>
        </div>
        <div className="settings-grid">
          {["Level", "FileEnabled", "MaxTotalSizeMB", "MaxFiles"].map((key) => {
            const meta = fieldMeta[key];
            if (key === "Level") {
              const label = meta?.label ?? "Level";
              return (
                <label className="settings-field" key="Logging.Level">
                  <span>{label}</span>
                  <Select
                    value={levelValue}
                    onChange={(event) =>
                      updateConfigValue(["Logging", "Level"], event.target.value)
                    }
                  >
                    {!levelOrder.includes(levelValue) ? (
                      <option value={levelValue}>{levelValue} (saved)</option>
                    ) : null}
                    {levelOrder.map((level) => (
                      <option key={level} value={level}>
                        {level.toUpperCase()}
                      </option>
                    ))}
                  </Select>
                </label>
              );
            }
            return renderField(key, loggingConfig[key], ["Logging", key], meta);
          })}
        </div>
        <div className="grid min-w-0 gap-1 rounded-md border border-border bg-muted px-3 py-2 text-foreground">
          <span className="text-sm font-medium text-muted-foreground">Log path</span>
          <span className="text-sm font-medium [overflow-wrap:anywhere]">
            {logPath || "Unavailable"}
          </span>
        </div>
      </div>

      <div className="panel grid gap-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2 font-semibold">
            <span
              className={cn(
                "h-2.5 w-2.5 rounded-full",
                connected ? "bg-[var(--status-success)]" : "bg-destructive",
              )}
            />
            <span>{connected ? "Connected" : "Disconnected"}</span>
          </div>
          <div className="flex items-center gap-2">
            <button className="ghost" type="button" onClick={handleClearLogs}>
              Clear
            </button>
            <label className="inline-flex cursor-pointer items-center gap-2 rounded-md border border-border bg-muted px-2 py-1 text-sm font-semibold text-foreground">
              <span>Auto-scroll</span>
              <Switch
                aria-label="Auto-scroll logs"
                checked={autoScroll}
                onChange={(event) => setAutoScroll(event.target.checked)}
              />
            </label>
          </div>
        </div>

        <div className="flex flex-wrap items-end gap-3">
          <fieldset className="m-0 min-w-0 border-0 p-0">
            <legend className="mb-1 text-sm font-semibold text-foreground">Visible levels</legend>
            <div className="flex flex-wrap gap-1.5">
              {levelOrder.map((level) => (
                <label
                  key={level}
                  className={cn(
                    "inline-flex min-h-9 cursor-pointer items-center gap-2 rounded-md border px-2 py-1 text-xs font-semibold uppercase",
                    levelFilter[level]
                      ? "border-primary bg-accent text-accent-foreground"
                      : "border-border bg-card text-card-foreground",
                  )}
                  htmlFor={`log-level-${level}`}
                >
                  <Checkbox
                    id={`log-level-${level}`}
                    checked={levelFilter[level]}
                    onCheckedChange={() => toggleLevel(level)}
                  />
                  {level.toUpperCase()}
                </label>
              ))}
            </div>
          </fieldset>
          <input
            className="min-w-[200px] flex-1 rounded-md border border-input bg-card px-2 py-1.5 text-sm text-card-foreground"
            aria-label="Search logs"
            placeholder="Search logs"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>

        {bufferWarning ? <p className="warning">{bufferWarning}</p> : null}

        <div
          className="grid max-h-[328px] gap-1.5 overflow-y-auto rounded-lg border border-border bg-card p-2 font-mono text-[0.82rem]"
          aria-live="polite"
          ref={logStreamRef}
        >
          {filteredEntries.length === 0 ? (
            <p className="muted">No log entries yet.</p>
          ) : (
            filteredEntries.map((entry) => (
              <div
                key={logEntryKey(entry)}
                className="grid grid-cols-[72px_64px_minmax(0,1fr)] items-center gap-2"
              >
                <span className="text-xs text-muted-foreground">{formatTime(entry.Time)}</span>
                <button
                  className={cn(
                    "rounded-full border-none px-1.5 py-1 text-[0.68rem] font-bold uppercase tracking-[0.08em] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
                    levelBadgeClass(entry.Level),
                  )}
                  type="button"
                  onClick={() => handleMuteMessage(entry.Message)}
                  aria-label={`Mute ${entry.Level.toUpperCase()} message: ${entry.Message || "(empty message)"}`}
                  title="Mute this message"
                >
                  {entry.Level.toUpperCase()}
                </button>
                <span className="min-w-0 [overflow-wrap:anywhere]">
                  {entry.Message || "(empty message)"}
                </span>
              </div>
            ))
          )}
          <div ref={logEndRef} />
        </div>

        <div className="grid gap-2">
          <div>
            <p className="label">Muted patterns</p>
            <p className="helper">Mute exact message matches.</p>
          </div>
          <div className="flex flex-wrap gap-2">
            <input
              className="min-w-[220px] flex-1 rounded-md border border-input bg-card px-2 py-1.5 text-sm text-card-foreground"
              placeholder="Message to mute"
              value={pendingMute}
              onChange={(event) => setPendingMute(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") handleAddMute();
              }}
            />
            <button className="ghost" type="button" onClick={handleAddMute}>
              Add
            </button>
          </div>
          {mutedPatterns.length === 0 ? (
            <p className="muted">No muted patterns.</p>
          ) : (
            <div className="grid gap-1.5">
              {mutedPatterns.map((pattern) => (
                <div
                  key={pattern}
                  className="flex items-center justify-between gap-2 rounded-md border border-border bg-muted px-2 py-1.5 text-foreground"
                >
                  <span className="min-w-0 [overflow-wrap:anywhere]">{pattern}</span>
                  <button
                    className="ghost shrink-0"
                    type="button"
                    onClick={() => handleRemoveMute(pattern)}
                  >
                    Remove
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
