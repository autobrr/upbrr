// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import type { ConfigMap, ConfigValue, FieldMeta } from "../../types";

type SettingsSection = { key: string; jsonKey: string; label: string };

type ConfigOpStatus = {
  type: "success" | "error" | "warning";
  title: string;
  message: string;
  warnings?: string[];
} | null;

type Props = {
  configData: ConfigMap | null;
  settingsLoading: boolean;
  settingsExporting: boolean;
  settingsImporting: boolean;
  settingsDirty: boolean;
  settingsSaved: string;
  settingsError: string;
  configOpStatus: ConfigOpStatus;
  dismissConfigOpStatus: () => void;
  settingsSection: string;
  settingsSections: SettingsSection[];
  showAdvancedToggle: boolean;
  advancedOpen: boolean;
  setSettingsSection: Dispatch<SetStateAction<string>>;
  setSettingsAdvanced: Dispatch<SetStateAction<Record<string, boolean>>>;
  loadSettings: () => void;
  handleExportSettings: () => void;
  handleImportConfig: () => void;
  handleSaveSettings: () => void;
  renderImageHostingSection: () => JSX.Element | null;
  renderTrackerSection: (advancedOpen: boolean) => JSX.Element | null;
  renderMapSection: (
    sectionKey: string,
    sectionValue: ConfigMap,
    options?: { entriesKey?: string; defaultKey?: string; fieldMeta?: Record<string, FieldMeta>; advancedOpen?: boolean }
  ) => JSX.Element;
  renderField: (label: string, value: ConfigValue, path: string[], meta?: FieldMeta) => JSX.Element;
  sectionFieldMeta: Record<string, Record<string, FieldMeta>>;
};

export default function SettingsPage(props: Props) {
  const {
    configData,
    settingsLoading,
    settingsExporting,
    settingsImporting,
    settingsDirty,
    settingsSaved,
    settingsError,
    configOpStatus,
    dismissConfigOpStatus,
    settingsSection,
    settingsSections,
    showAdvancedToggle,
    advancedOpen,
    setSettingsSection,
    setSettingsAdvanced,
    loadSettings,
    handleExportSettings,
    handleImportConfig,
    handleSaveSettings,
    renderImageHostingSection,
    renderTrackerSection,
    renderMapSection,
    renderField,
    sectionFieldMeta
  } = props;

  const [warningsExpanded, setWarningsExpanded] = useState(false);

  return (
    <div className="content-stack">
      <header className="hero">
        <p className="eyebrow">upbrr</p>
        <h1>Settings</h1>
        <p className="subtitle">
          Edit settings by section. Changes apply immediately and are saved to SQLite.
        </p>
      </header>

      <section className="panel">
        <div className="settings-header">
          <div className="settings-meta">
            <p className="label">Configuration</p>
            <p className="helper">
              Invalid changes will be rejected with a validation error.
            </p>
          </div>
          <div className="settings-actions">
            <button className="ghost" type="button" onClick={loadSettings} disabled={settingsLoading}>
              Reload
            </button>
            <button
              className="ghost"
              type="button"
              onClick={handleExportSettings}
              disabled={settingsLoading || settingsExporting || settingsImporting}
            >
              {settingsExporting ? "Exporting..." : "Export"}
            </button>
            <button
              className="ghost"
              type="button"
              onClick={handleImportConfig}
              disabled={settingsLoading || settingsExporting || settingsImporting}
            >
              {settingsImporting ? "Importing..." : "Import"}
            </button>
            <button
              className="primary"
              type="button"
              onClick={handleSaveSettings}
              disabled={settingsLoading || settingsExporting || settingsImporting || !settingsDirty}
            >
              Save
            </button>
          </div>
        </div>

        {configOpStatus ? (
          <div className={`config-status-banner config-status-banner--${configOpStatus.type}`}>
            <div className="config-status-banner__icon">
              {configOpStatus.type === "success" ? (
                <svg width="20" height="20" viewBox="0 0 20 20" fill="none"><path d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Z" fill="currentColor" opacity=".15"/><path d="M6.5 10.5 8.5 12.5 13.5 7.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/><circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.5"/></svg>
              ) : configOpStatus.type === "warning" ? (
                <svg width="20" height="20" viewBox="0 0 20 20" fill="none"><path d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Z" fill="currentColor" opacity=".15"/><path d="M10 7v4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/><circle cx="10" cy="13.5" r=".75" fill="currentColor"/><circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.5"/></svg>
              ) : (
                <svg width="20" height="20" viewBox="0 0 20 20" fill="none"><path d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Z" fill="currentColor" opacity=".15"/><path d="M12.5 7.5 7.5 12.5M7.5 7.5l5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/><circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.5"/></svg>
              )}
            </div>
            <div className="config-status-banner__body">
              <p className="config-status-banner__title">{configOpStatus.title}</p>
              <p className="config-status-banner__message">{configOpStatus.message}</p>
              {configOpStatus.warnings && configOpStatus.warnings.length > 0 ? (
                <div className="config-status-banner__warnings">
                  <button
                    type="button"
                    className="config-status-banner__toggle"
                    onClick={() => setWarningsExpanded((prev) => !prev)}
                  >
                    {warningsExpanded ? "Hide" : "Show"} {configOpStatus.warnings.length} warning{configOpStatus.warnings.length !== 1 ? "s" : ""}
                  </button>
                  {warningsExpanded ? (
                    <ul className="config-status-banner__warning-list">
                      {configOpStatus.warnings.map((w, i) => (
                        <li key={i}>{w}</li>
                      ))}
                    </ul>
                  ) : null}
                </div>
              ) : null}
            </div>
            <button type="button" className="config-status-banner__dismiss" onClick={dismissConfigOpStatus} aria-label="Dismiss">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M10.5 3.5 3.5 10.5M3.5 3.5l7 7" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/></svg>
            </button>
          </div>
        ) : null}

        <div className="settings-shell">
          <div className="settings-tags">
            {settingsSections.map((section) => (
              <button
                key={section.key}
                type="button"
                className={`settings-tag ${settingsSection === section.key ? "active" : ""}`}
                onClick={() => setSettingsSection(section.key)}
              >
                {section.label}
              </button>
            ))}
          </div>

          <div className="settings-body">
            {configData ? (
              <div className="settings-form">
                {showAdvancedToggle ? (
                  <label className="settings-toggle">
                    <span>Show advanced</span>
                    <input
                      type="checkbox"
                      checked={advancedOpen}
                      onChange={(event) =>
                        setSettingsAdvanced((prev) => ({
                          ...prev,
                          [settingsSection]: event.target.checked
                        }))
                      }
                    />
                    <span className="settings-toggle__pill" />
                  </label>
                ) : null}
                {settingsSection === "image_hosting" ? (
                  renderImageHostingSection()
                ) : settingsSection === "trackers" && configData.Trackers && typeof configData.Trackers === "object" && !Array.isArray(configData.Trackers) ? (
                  renderTrackerSection(advancedOpen)
                ) : settingsSection === "torrent_clients" && configData.TorrentClients && typeof configData.TorrentClients === "object" ? (
                  renderMapSection("TorrentClients", configData.TorrentClients as ConfigMap)
                ) : (
                  <div className="settings-grid">
                    {(() => {
                      const section = settingsSections.find((item) => item.key === settingsSection);
                      if (!section) return null;
                      const sectionData = configData[section.jsonKey];
                      if (!sectionData || typeof sectionData !== "object" || Array.isArray(sectionData)) {
                        return null;
                      }
                      const meta = sectionFieldMeta[section.jsonKey] || {};
                      return Object.entries(sectionData as ConfigMap)
                        .filter(([key]) => {
                          const fieldMeta = meta[key];
                          if (fieldMeta?.advanced && !advancedOpen) return false;
                          return true;
                        })
                        .map(([key, value]) =>
                          renderField(key, value, [section.jsonKey, key], meta[key])
                        );
                    })()}
                  </div>
                )}
              </div>
            ) : (
              <p className="muted">Loading configuration...</p>
            )}
          </div>
        </div>

        {settingsSaved ? <p className="settings-saved">{settingsSaved}</p> : null}
        {settingsError ? <p className="error">{settingsError}</p> : null}
      </section>
    </div>
  );
}
