# Metadata artifacts

Follow `internal/AGENTS.md`. The authoritative primary `mediainfo.txt` and `MediaInfo.json` belong at the owning release root. Explicit secondary-file and per-disc reports stay within that owner without replacing the selected primary report.

Preparation, HDR eligibility and programme inspection share compatible reports. Reuse requires canonical analyzed-source identity, inventory/verified-content identity when supplied, and a coherent report pair. Missing or stale evidence must be regenerated. Keep the selected-primary-only default; never add automatic pack-wide inspection to fill evidence.

Use the MediaInfo exporter for report publication and its private identity/locking contract. Test meaningful changes through preparation and restart/hydration, not only a helper's path string.
