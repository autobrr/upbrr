// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { pageStyle } from "../../components/ui/pageStyle";
import type { ReactElement } from "react";
import LogSettingsPanel from "../../components/LogSettingsPanel";
import type { ConfigMap, ConfigValue, FieldMeta } from "../../types";

type Props = Readonly<{
  configData: ConfigMap | null;
  settingsLoading: boolean;
  settingsDirty: boolean;
  settingsSaved: string;
  settingsError: string;
  loadSettings: () => void;
  handleSaveSettings: () => void;
  renderField: (
    label: string,
    value: ConfigValue,
    path: string[],
    meta?: FieldMeta,
  ) => ReactElement;
  updateConfigValue: (path: string[], value: ConfigValue) => void;
  sectionFieldMeta: Record<string, Record<string, FieldMeta>>;
}>;

export default function LoggingPage(props: Props) {
  const {
    configData,
    settingsLoading,
    settingsDirty,
    settingsSaved,
    settingsError,
    loadSettings,
    handleSaveSettings,
    renderField,
    updateConfigValue,
    sectionFieldMeta,
  } = props;

  return (
    <div className="flex flex-col gap-4">
      <header className="relative z-[1] max-w-[720px]">
        <p className={pageStyle.eyebrow}>upbrr</p>
        <h1>Logging</h1>
        <p className={pageStyle.subtitle}>Monitor live logs and adjust logging settings.</p>
      </header>

      <section className={pageStyle.panel}>
        <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
          <div className="flex flex-col gap-1">
            <p className={pageStyle.label}>Logging controls</p>
            <p className="helper">Changes apply immediately and are saved to SQLite.</p>
          </div>
          <div className="flex items-center gap-2">
            <button
              className="ghost"
              type="button"
              onClick={() => {
                if (!settingsDirty || window.confirm("Discard unsaved settings and reload?")) {
                  loadSettings();
                }
              }}
              disabled={settingsLoading}
            >
              Reload
            </button>
            <button
              className="primary"
              type="button"
              onClick={handleSaveSettings}
              disabled={settingsLoading || !settingsDirty}
            >
              Save
            </button>
          </div>
        </div>

        <div className="min-w-0">
          {configData ? (
            <div className="flex flex-col gap-3">
              <LogSettingsPanel
                configData={configData}
                renderField={renderField}
                updateConfigValue={updateConfigValue}
                fieldMeta={sectionFieldMeta.Logging || {}}
              />
            </div>
          ) : (
            <p className="text-muted-foreground">Loading configuration...</p>
          )}
        </div>

        {settingsSaved ? (
          <p className="mt-[9px] text-[var(--status-success)]">{settingsSaved}</p>
        ) : null}
        {settingsError ? <p className={pageStyle.error}>{settingsError}</p> : null}
      </section>
    </div>
  );
}
