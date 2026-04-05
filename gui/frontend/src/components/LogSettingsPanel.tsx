// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { EventsOn } from "../utils/runtime";

type LogEntry = {
  ID: number;
  Time: string;
  Level: string;
  Message: string;
};

type LogSettingsPanelProps = Readonly<{
  configData: any;
  renderField: (label: string, value: any, path: string[], meta?: any) => JSX.Element;
  updateConfigValue: (path: string[], value: any) => void;
  fieldMeta: Record<string, any>;
}>;

const LOG_SOFT_CAP = 1000;
const LOG_HARD_CAP = 10000;

const levelOrder = ["trace", "debug", "info", "warn", "error"];

const normalizeEntry = (payload: any): LogEntry | null => {
  if (!payload) return null;
  if (typeof payload === "string") {
    return {
      ID: Date.now(),
      Time: new Date().toISOString(),
      Level: "info",
      Message: payload
    };
  }
  const level = String(payload.Level ?? payload.level ?? "info").toLowerCase();
  return {
    ID: Number(payload.ID ?? payload.id ?? Date.now()),
    Time: String(payload.Time ?? payload.time ?? new Date().toISOString()),
    Level: level,
    Message: String(payload.Message ?? payload.message ?? "")
  };
};

const normalizeLevels = () =>
  levelOrder.reduce((acc, level) => {
    acc[level] = true;
    return acc;
  }, {} as Record<string, boolean>);

const formatTime = (iso: string) => {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "--:--:--";
  return date.toLocaleTimeString();
};

export default function LogSettingsPanel({
  configData,
  renderField,
  updateConfigValue,
  fieldMeta
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

  const streamStopRef = useRef<null | (() => void)>(null);
  const logEndRef = useRef<HTMLDivElement | null>(null);
  const logStreamRef = useRef<HTMLDivElement | null>(null);

  const loggingConfig = configData?.Logging || {};
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

  const persistMuted = async (patterns: string[]) => {
    const updater = globalThis.go?.guiapp?.App?.UpdateLogExclusions;
    if (!updater) return;
    try {
      await updater(patterns);
    } catch (err) {
      console.error("Failed to update log exclusions", err);
    }
  };

  const appendEntries = useCallback((incoming: LogEntry[]) => {
    if (incoming.length === 0) return;
    setEntries((prev) => {
      let next = [...prev, ...incoming];
      if (autoScroll && next.length > LOG_SOFT_CAP) {
        next = next.slice(-LOG_SOFT_CAP);
      } else if (!autoScroll && next.length > LOG_HARD_CAP) {
        next = next.slice(-LOG_HARD_CAP);
        setBufferWarning("Log buffer capped. Oldest entries were dropped.");
      }
      return next;
    });
  }, [autoScroll]);

  useEffect(() => {
    const fetchLogPath = async () => {
      const getLogPath = globalThis.go?.guiapp?.App?.GetLogPath;
      if (!getLogPath) return;
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
    const fetchRecent = async () => {
      const getRecent = globalThis.go?.guiapp?.App?.GetRecentLogs;
      if (!getRecent) return;
      try {
        const payload = await getRecent(LOG_SOFT_CAP);
        const normalized = Array.isArray(payload)
          ? payload.map(normalizeEntry).filter(Boolean)
          : [];
        appendEntries(normalized as LogEntry[]);
      } catch (err) {
        console.error("Failed to load recent logs", err);
      }
    };
    fetchRecent();
  }, [appendEntries]);

  useEffect(() => {
    const fetchMuted = async () => {
      const getter = globalThis.go?.guiapp?.App?.GetLogExclusions;
      if (!getter) return;
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
  }, [appendEntries]);

  useEffect(() => {
    let active = true;
    const startStream = async () => {
      const start = globalThis.go?.guiapp?.App?.StartLogStream;
      const stop = globalThis.go?.guiapp?.App?.StopLogStream;
      if (!start) return;

      try {
        const streamID = await start();
        if (!active) {
          if (stop) await stop(streamID);
          return;
        }
        const eventName = `log:stream:${streamID}`;
        const off = EventsOn(eventName, (payload: any) => {
          const entry = normalizeEntry(payload);
          if (entry) appendEntries([entry]);
        });
        setConnected(true);
        streamStopRef.current = () => {
          off();
          if (stop) {
            stop(streamID).catch(() => undefined);
          }
        };
      } catch (err) {
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
  }, [appendEntries]);

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
    <div className="log-settings-panel">
      <div className="panel log-settings-card">
        <div className="log-settings-header">
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
                  <select
                    value={levelValue}
                    onChange={(event) => updateConfigValue(["Logging", "Level"], event.target.value)}
                  >
                    {levelOrder.map((level) => (
                      <option key={level} value={level}>
                        {level.toUpperCase()}
                      </option>
                    ))}
                  </select>
                </label>
              );
            }
            return renderField(key, loggingConfig[key], ["Logging", key], meta);
          })}
        </div>
        <div className="log-path">
          <span className="label">Log path</span>
          <span className="value">{logPath || "Unavailable"}</span>
        </div>
      </div>

      <div className="panel log-viewer-card">
        <div className="log-toolbar">
          <div className="log-status">
            <span className={`log-dot ${connected ? "on" : "off"}`} />
            <span>{connected ? "Connected" : "Disconnected"}</span>
          </div>
          <div className="log-actions">
            <button className="ghost" type="button" onClick={handleClearLogs}>
              Clear
            </button>
            <label className="settings-toggle log-autoscroll">
              <span>Auto-scroll</span>
              <input type="checkbox" checked={autoScroll} onChange={(event) => setAutoScroll(event.target.checked)} />
              <span className="settings-toggle__pill" />
            </label>
          </div>
        </div>

        <div className="log-filters">
          <div className="log-levels">
            {levelOrder.map((level) => (
              <label key={level} className={`log-level-toggle ${level}`}>
                <input type="checkbox" checked={levelFilter[level]} onChange={() => toggleLevel(level)} />
                <span>{level.toUpperCase()}</span>
              </label>
            ))}
          </div>
          <input
            className="log-search"
            placeholder="Search logs"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>

        {bufferWarning ? <p className="warning">{bufferWarning}</p> : null}

        <div className="log-stream" aria-live="polite" ref={logStreamRef}>
          {filteredEntries.length === 0 ? (
            <p className="muted">No log entries yet.</p>
          ) : (
            filteredEntries.map((entry) => (
              <div key={entry.ID} className="log-entry">
                <span className="log-time">{formatTime(entry.Time)}</span>
                <button
                  className={`log-level-badge ${entry.Level}`}
                  type="button"
                  onClick={() => handleMuteMessage(entry.Message)}
                  title="Mute this message"
                >
                  {entry.Level.toUpperCase()}
                </button>
                <span className="log-message">{entry.Message || "(empty message)"}</span>
              </div>
            ))
          )}
          <div ref={logEndRef} />
        </div>

        <div className="log-mute-panel">
          <div className="log-mute-header">
            <p className="label">Muted patterns</p>
            <p className="helper">Mute exact message matches.</p>
          </div>
          <div className="log-mute-controls">
            <input
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
            <div className="log-mute-list">
              {mutedPatterns.map((pattern) => (
                <div key={pattern} className="log-mute-item">
                  <span>{pattern}</span>
                  <button className="ghost" type="button" onClick={() => handleRemoveMute(pattern)}>
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
