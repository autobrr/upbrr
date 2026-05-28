// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import * as AlertDialog from "@radix-ui/react-alert-dialog";
import { Button } from "../../components/ui/button";
import { Switch } from "../../components/ui/switch";
import { cn } from "../../utils/cn";
import type { ConfigMap, ConfigValue, FieldMeta, WebAuthStatus } from "../../types";

type SettingsSection = { key: string; jsonKey: string; label: string };

const settingsInputClass =
  "h-8 rounded-md border border-white/10 bg-slate-950/45 px-2.5 text-sm text-[var(--text)] outline-none transition placeholder:text-[var(--muted)] focus:border-[var(--accent-2)] focus:ring-2 focus:ring-[rgba(53,194,193,0.18)]";

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
  importConfirmOpen: boolean;
  handleImportConfigConfirm: () => void;
  handleImportConfigCancel: () => void;
  handleSaveSettings: () => void;
  webAuthAvailable: boolean;
  webAuthStatus: WebAuthStatus | null;
  webAuthLoading: boolean;
  webAuthCreating: boolean;
  webAuthUsername: string;
  webAuthPassword: string;
  webAuthConfirm: string;
  webAuthError: string;
  setWebAuthUsername: Dispatch<SetStateAction<string>>;
  setWebAuthPassword: Dispatch<SetStateAction<string>>;
  setWebAuthConfirm: Dispatch<SetStateAction<string>>;
  handleCreateWebAuth: () => void;
  renderImageHostingSection: () => JSX.Element | null;
  renderTrackerSection: (advancedOpen: boolean) => JSX.Element | null;
  renderMapSection: (
    sectionKey: string,
    sectionValue: ConfigMap,
    options?: {
      entriesKey?: string;
      defaultKey?: string;
      fieldMeta?: Record<string, FieldMeta>;
      advancedOpen?: boolean;
    },
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
    importConfirmOpen,
    handleImportConfigConfirm,
    handleImportConfigCancel,
    handleSaveSettings,
    webAuthAvailable,
    webAuthStatus,
    webAuthLoading,
    webAuthCreating,
    webAuthUsername,
    webAuthPassword,
    webAuthConfirm,
    webAuthError,
    setWebAuthUsername,
    setWebAuthPassword,
    setWebAuthConfirm,
    handleCreateWebAuth,
    renderImageHostingSection,
    renderTrackerSection,
    renderMapSection,
    renderField,
    sectionFieldMeta,
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
            <p className="helper">Invalid changes will be rejected with a validation error.</p>
          </div>
          <div className="settings-actions">
            <Button type="button" onClick={loadSettings} disabled={settingsLoading}>
              Reload
            </Button>
            <Button
              type="button"
              onClick={handleExportSettings}
              disabled={settingsLoading || settingsExporting || settingsImporting}
            >
              {settingsExporting ? "Exporting..." : "Export"}
            </Button>
            <Button
              type="button"
              onClick={handleImportConfig}
              disabled={settingsLoading || settingsExporting || settingsImporting}
            >
              {settingsImporting ? "Importing..." : "Import"}
            </Button>
            <Button
              variant="primary"
              type="button"
              onClick={handleSaveSettings}
              disabled={settingsLoading || settingsExporting || settingsImporting || !settingsDirty}
            >
              Save
            </Button>
          </div>
        </div>

        {configOpStatus ? (
          <div className={`config-status-banner config-status-banner--${configOpStatus.type}`}>
            <div className="config-status-banner__icon">
              {configOpStatus.type === "success" ? (
                <svg width="20" height="20" viewBox="0 0 20 20" fill="none">
                  <path
                    d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Z"
                    fill="currentColor"
                    opacity=".15"
                  />
                  <path
                    d="M6.5 10.5 8.5 12.5 13.5 7.5"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                  <circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.5" />
                </svg>
              ) : configOpStatus.type === "warning" ? (
                <svg width="20" height="20" viewBox="0 0 20 20" fill="none">
                  <path
                    d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Z"
                    fill="currentColor"
                    opacity=".15"
                  />
                  <path d="M10 7v4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                  <circle cx="10" cy="13.5" r=".75" fill="currentColor" />
                  <circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.5" />
                </svg>
              ) : (
                <svg width="20" height="20" viewBox="0 0 20 20" fill="none">
                  <path
                    d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Z"
                    fill="currentColor"
                    opacity=".15"
                  />
                  <path
                    d="M12.5 7.5 7.5 12.5M7.5 7.5l5 5"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                  />
                  <circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.5" />
                </svg>
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
                    {warningsExpanded ? "Hide" : "Show"} {configOpStatus.warnings.length} warning
                    {configOpStatus.warnings.length !== 1 ? "s" : ""}
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
            <button
              type="button"
              className="config-status-banner__dismiss"
              onClick={dismissConfigOpStatus}
              aria-label="Dismiss"
            >
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
                <path
                  d="M10.5 3.5 3.5 10.5M3.5 3.5l7 7"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                />
              </svg>
            </button>
          </div>
        ) : null}

        <div className="settings-shell">
          <div className="settings-tags">
            {settingsSections.map((section) => (
              <button
                key={section.key}
                type="button"
                className={cn(
                  "flex h-8 w-full items-center rounded-md px-3 text-left text-sm font-medium transition",
                  settingsSection === section.key
                    ? "bg-[var(--accent)] text-slate-950 shadow-[0_8px_24px_rgba(245,185,66,0.16)]"
                    : "text-[var(--muted)] hover:bg-white/10 hover:text-[var(--text)]",
                )}
                onClick={() => setSettingsSection(section.key)}
              >
                {section.label}
              </button>
            ))}
          </div>

          <div className="settings-body">
            {webAuthAvailable ? (
              <details className="settings-subgroup settings-subgroup--collapsible settings-subgroup--auth">
                <summary>Secret Encryption</summary>
                <div>
                  <p className="helper">
                    Desktop installs can keep using plaintext secrets, or you can create
                    <code> web-auth.json </code>
                    to enable encrypted secret storage for future saves and exports.
                  </p>
                  <div className="settings-auth-status">
                    <span
                      className={`settings-auth-badge ${webAuthStatus?.usable ? "is-ready" : webAuthStatus?.exists ? "is-warning" : "is-idle"}`}
                    >
                      {webAuthLoading
                        ? "Checking..."
                        : webAuthStatus?.usable
                          ? "Encryption enabled"
                          : webAuthStatus?.exists
                            ? "Auth file invalid"
                            : "Plaintext fallback active"}
                    </span>
                    {webAuthStatus?.path ? (
                      <p className="muted">Path: {webAuthStatus.path}</p>
                    ) : null}
                    {webAuthStatus?.message ? (
                      <p className="muted">{webAuthStatus.message}</p>
                    ) : null}
                    {webAuthStatus?.usable && webAuthStatus.username ? (
                      <p className="muted">Configured user: {webAuthStatus.username}</p>
                    ) : null}
                    {webAuthStatus?.browseRoot ? (
                      <p className="muted">Web browse root: {webAuthStatus.browseRoot}</p>
                    ) : null}
                    {webAuthStatus?.allowUnrestrictedBrowse ? (
                      <p className="muted">Web browse access: Unrestricted</p>
                    ) : null}
                  </div>
                  {webAuthStatus?.canCreate ? (
                    <div className="settings-grid">
                      <label className="settings-field">
                        <span>Username</span>
                        <input
                          className={settingsInputClass}
                          value={webAuthUsername}
                          onChange={(event) => setWebAuthUsername(event.target.value)}
                          autoComplete="username"
                        />
                      </label>
                      <label className="settings-field">
                        <span>Password</span>
                        <input
                          className={settingsInputClass}
                          type="password"
                          value={webAuthPassword}
                          onChange={(event) => setWebAuthPassword(event.target.value)}
                          autoComplete="new-password"
                        />
                      </label>
                      <label className="settings-field">
                        <span>Confirm password</span>
                        <input
                          className={settingsInputClass}
                          type="password"
                          value={webAuthConfirm}
                          onChange={(event) => setWebAuthConfirm(event.target.value)}
                          autoComplete="new-password"
                        />
                      </label>
                    </div>
                  ) : null}
                  <div className="settings-auth-actions">
                    <Button
                      variant="primary"
                      type="button"
                      onClick={handleCreateWebAuth}
                      disabled={
                        webAuthLoading ||
                        webAuthCreating ||
                        !webAuthStatus?.canCreate ||
                        !webAuthUsername.trim() ||
                        !webAuthPassword.trim() ||
                        !webAuthConfirm.trim()
                      }
                    >
                      {webAuthCreating ? "Creating..." : "Create web-auth.json"}
                    </Button>
                  </div>
                  {webAuthError ? <p className="error">{webAuthError}</p> : null}
                </div>
              </details>
            ) : null}
            {configData ? (
              <div className="settings-form">
                {showAdvancedToggle ? (
                  <div className="settings-switch-row">
                    <span>Show advanced</span>
                    <Switch
                      aria-label="Show advanced"
                      checked={advancedOpen}
                      onChange={(event) =>
                        setSettingsAdvanced((prev) => ({
                          ...prev,
                          [settingsSection]: event.target.checked,
                        }))
                      }
                    />
                  </div>
                ) : null}
                {settingsSection === "image_hosting" ? (
                  renderImageHostingSection()
                ) : settingsSection === "trackers" &&
                  configData.Trackers &&
                  typeof configData.Trackers === "object" &&
                  !Array.isArray(configData.Trackers) ? (
                  renderTrackerSection(advancedOpen)
                ) : settingsSection === "torrent_clients" &&
                  configData.TorrentClients &&
                  typeof configData.TorrentClients === "object" ? (
                  renderMapSection("TorrentClients", configData.TorrentClients as ConfigMap)
                ) : (
                  <div className="settings-grid">
                    {(() => {
                      const section = settingsSections.find((item) => item.key === settingsSection);
                      if (!section) return null;
                      const sectionData = configData[section.jsonKey];
                      if (
                        !sectionData ||
                        typeof sectionData !== "object" ||
                        Array.isArray(sectionData)
                      ) {
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
                          renderField(key, value, [section.jsonKey, key], meta[key]),
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

      <AlertDialog.Root
        open={importConfirmOpen}
        onOpenChange={(open) => {
          if (!open) handleImportConfigCancel();
        }}
      >
        <AlertDialog.Portal>
          <AlertDialog.Overlay className="import-confirm-overlay" />
          <AlertDialog.Content className="import-confirm-dialog">
            <div className="import-confirm-dialog__icon">
              <svg width="28" height="28" viewBox="0 0 24 24" fill="none">
                <path d="M12 3 1.5 21h21L12 3Z" fill="currentColor" opacity=".12" />
                <path
                  d="M12 3 1.5 21h21L12 3Z"
                  stroke="currentColor"
                  strokeWidth="1.6"
                  strokeLinejoin="round"
                />
                <path d="M12 10v5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
                <circle cx="12" cy="18" r="1" fill="currentColor" />
              </svg>
            </div>
            <div className="import-confirm-dialog__body">
              <AlertDialog.Title asChild>
                <h2 className="import-confirm-dialog__title">Replace current configuration?</h2>
              </AlertDialog.Title>
              <AlertDialog.Description asChild>
                <p className="import-confirm-dialog__message">
                  Importing a configuration file will overwrite your current settings in the
                  database. This action cannot be undone.
                </p>
              </AlertDialog.Description>
              <p className="import-confirm-dialog__hint">
                We strongly recommend exporting your current configuration first so you can restore
                it if the imported file isn&apos;t what you expected.
              </p>
            </div>
            <div className="import-confirm-dialog__actions">
              <AlertDialog.Cancel asChild>
                <Button type="button" disabled={settingsImporting}>
                  Cancel
                </Button>
              </AlertDialog.Cancel>
              <Button
                type="button"
                onClick={handleExportSettings}
                disabled={settingsExporting || settingsImporting}
              >
                {settingsExporting ? "Exporting..." : "Export current config"}
              </Button>
              <AlertDialog.Action asChild>
                <Button
                  type="button"
                  variant="primary"
                  className="import-confirm-dialog__confirm"
                  onClick={(event) => {
                    event.preventDefault();
                    handleImportConfigConfirm();
                  }}
                  disabled={settingsImporting}
                >
                  {settingsImporting ? "Importing..." : "Choose file & import"}
                </Button>
              </AlertDialog.Action>
            </div>
          </AlertDialog.Content>
        </AlertDialog.Portal>
      </AlertDialog.Root>
    </div>
  );
}
