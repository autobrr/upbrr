// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { lazy, Suspense, useState } from "react";
import { configClient } from "../api/app";
import { createSettingsRenderers } from "../settings/renderers";
import { useRouteViews } from "./RouteViews";

const SettingsPage = lazy(() => import("../pages/settings"));
const LoggingPage = lazy(() => import("../pages/logging"));

type ConfigOpStatus = {
  type: "success" | "error" | "warning";
  title: string;
  message: string;
  warnings?: string[];
} | null;

export function SettingsRoute() {
  const {
    settings,
    applicationInfo,
    applicationInfoFetchedAt,
    applicationInfoLoading,
    applicationInfoError,
  } = useRouteViews();
  const editors = createSettingsRenderers(settings.editorContext);
  const [settingsExporting, setSettingsExporting] = useState(false);
  const [settingsImporting, setSettingsImporting] = useState(false);
  const [importConfirmOpen, setImportConfirmOpen] = useState(false);
  const [configOpStatus, setConfigOpStatus] = useState<ConfigOpStatus>(null);
  const handleExportSettings = async () => {
    settings.clearSettingsStatus();
    setConfigOpStatus(null);
    setSettingsExporting(true);
    try {
      const file = await configClient.exportDownload();
      setConfigOpStatus({ type: "success", title: "Configuration exported", message: file });
    } catch (error) {
      setConfigOpStatus({ type: "error", title: "Export failed", message: String(error) });
    } finally {
      setSettingsExporting(false);
    }
  };
  const handleImportConfigConfirm = async () => {
    setSettingsImporting(true);
    try {
      const result = await configClient.importFile();
      if (result.message) {
        setConfigOpStatus({
          type: result.warnings.length ? "warning" : "success",
          title: result.warnings.length ? "Imported with warnings" : "Configuration imported",
          message: result.message,
          warnings: result.warnings,
        });
        settings.loadSettings(true);
      }
    } catch (error) {
      setConfigOpStatus({ type: "error", title: "Import failed", message: String(error) });
    } finally {
      setSettingsImporting(false);
      setImportConfirmOpen(false);
    }
  };
  return (
    <Suspense fallback={<p role="status">Loading settings…</p>}>
      <SettingsPage
        applicationInfo={applicationInfo}
        applicationInfoFetchedAt={applicationInfoFetchedAt}
        applicationInfoLoading={applicationInfoLoading}
        applicationInfoError={applicationInfoError}
        configData={settings.settingsConfigData}
        settingsLoading={settings.settingsLoading}
        settingsExporting={settingsExporting}
        settingsImporting={settingsImporting}
        settingsDirty={settings.settingsDirty}
        settingsSaved={settings.settingsSaved}
        settingsError={settings.settingsError}
        configOpStatus={configOpStatus}
        dismissConfigOpStatus={() => setConfigOpStatus(null)}
        settingsSection={settings.settingsSection}
        settingsSections={settings.settingsSections}
        trackerSelectionNames={settings.settingsTrackerSelectionNames}
        showAdvancedToggle={settings.showAdvancedToggle}
        advancedOpen={settings.advancedOpen}
        setSettingsSection={settings.setSettingsSection}
        setSettingsAdvanced={settings.setSettingsAdvanced}
        loadSettings={settings.loadSettings}
        handleExportSettings={() => void handleExportSettings()}
        handleImportConfig={() => setImportConfirmOpen(true)}
        importConfirmOpen={importConfirmOpen}
        handleImportConfigConfirm={handleImportConfigConfirm}
        handleImportConfigCancel={() => !settingsImporting && setImportConfirmOpen(false)}
        handleSaveSettings={settings.handleSaveSettings}
        renderImageHostingSection={editors.renderImageHostingSection}
        renderTrackerSection={editors.renderTrackerSection}
        renderTorrentClientsSection={editors.renderTorrentClientsSection}
        renderField={editors.renderField}
        sectionFieldMeta={settings.sectionFieldMeta}
      />
    </Suspense>
  );
}

export function LoggingRoute() {
  const { settings } = useRouteViews();
  const { renderField } = createSettingsRenderers(settings.editorContext);
  return (
    <Suspense fallback={<p role="status">Loading logging…</p>}>
      <LoggingPage
        configData={settings.settingsConfigData}
        settingsLoading={settings.settingsLoading}
        settingsDirty={settings.settingsDirty}
        settingsSaved={settings.settingsSaved}
        settingsError={settings.settingsError}
        loadSettings={settings.loadSettings}
        handleSaveSettings={settings.handleSaveSettings}
        renderField={renderField}
        updateConfigValue={settings.updateConfigValue}
        sectionFieldMeta={settings.sectionFieldMeta}
      />
    </Suspense>
  );
}
