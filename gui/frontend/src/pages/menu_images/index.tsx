// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import type { ExternalIDOverrides, MetadataOverrides, ReleaseNameOverrides } from "../../types";

type Props = Readonly<{
  path: string;
  overrides: ExternalIDOverrides;
  nameOverrides: ReleaseNameOverrides;
  metadataOverrides: MetadataOverrides;
  browseAvailable: boolean;
  onImportComplete: () => void;
}>;

export default function MenuImagesPage(props: Props) {
  const { path, overrides, nameOverrides, metadataOverrides, browseAvailable, onImportComplete } =
    props;

  const [menuPaths, setMenuPaths] = useState<string[]>([]);
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState(false);

  const handleBrowseImages = async () => {
    try {
      const app = globalThis.go?.guiapp?.App;
      if (!app) return;

      // Prefer the image-specific picker, then fall back to older local builds.
      if (app.BrowseImageFiles || app.BrowseFiles) {
        const selected = await (app.BrowseImageFiles || app.BrowseFiles)?.();
        if (selected && selected.length > 0) {
          const valid = selected.filter((s) => s.trim() !== "");
          if (valid.length > 0) {
            setMenuPaths((prev) => Array.from(new Set([...prev, ...valid])));
            setSuccess(false);
          }
        }
      } else if (app.BrowseFile) {
        const selected = await app.BrowseFile();
        if (selected && selected.trim() !== "") {
          setMenuPaths((prev) => Array.from(new Set([...prev, selected.trim()])));
          setSuccess(false);
        }
      }
    } catch (err) {
      console.error(err);
    }
  };

  const handleRemoveItem = (itemToRemove: string) => {
    setMenuPaths((prev) => prev.filter((p) => p !== itemToRemove));
  };

  const handleImport = async () => {
    if (menuPaths.length === 0) return;
    setImporting(true);
    setError("");
    setSuccess(false);
    try {
      const importFn = globalThis.go?.guiapp?.App?.ImportMenuImages;
      if (!importFn) throw new Error("Import function not available");
      await importFn(path, overrides, nameOverrides, metadataOverrides, menuPaths);
      setSuccess(true);
      setMenuPaths([]);
      onImportComplete();
    } catch (err: any) {
      setError(err?.message || String(err));
    } finally {
      setImporting(false);
    }
  };

  return (
    <section className="menu-images-panel">
      <header className="menu-images-header">
        <p className="eyebrow">Disc Menus</p>
        <h1>Menu Images</h1>
        <p className="subtitle">
          Select existing menu images from your computer to be uploaded alongside your screenshots.
        </p>
      </header>

      <section className="panel menu-images-controls">
        <div>
          <p className="label">Source path</p>
          <p className="value dupe-path">{path || "No path selected"}</p>
        </div>

        <div className="upload-images-actions" style={{ marginTop: "1rem" }}>
          {browseAvailable ? (
            <>
              <button
                className="ghost"
                type="button"
                onClick={handleBrowseImages}
                disabled={importing}
              >
                Add images
              </button>
            </>
          ) : (
            <p className="muted">Native file browsing is only available locally.</p>
          )}

          <button
            className="primary"
            type="button"
            onClick={handleImport}
            disabled={importing || menuPaths.length === 0}
          >
            {importing ? "Importing..." : "Import Images"}
          </button>
        </div>

        {error ? (
          <p className="error" style={{ marginTop: "1rem" }}>
            {error}
          </p>
        ) : null}
        {success ? (
          <p
            className="success"
            style={{ marginTop: "1rem", color: "var(--success-color, #22c55e)" }}
          >
            Images imported successfully! They will appear in the Upload Images tab.
          </p>
        ) : null}

        {menuPaths.length > 0 ? (
          <div style={{ marginTop: "1.5rem" }}>
            <p className="label" style={{ marginBottom: "0.5rem" }}>
              Selected files for import:
            </p>
            <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
              {menuPaths.map((p) => (
                <li
                  key={p}
                  style={{
                    display: "flex",
                    justifyContent: "space-between",
                    padding: "0.5rem",
                    background: "var(--bg-secondary)",
                    marginBottom: "0.25rem",
                    borderRadius: "4px",
                  }}
                >
                  <span style={{ wordBreak: "break-all" }}>{p}</span>
                  <button
                    className="ghost"
                    type="button"
                    onClick={() => handleRemoveItem(p)}
                    style={{ padding: "0.25rem 0.5rem", height: "auto", minHeight: "0" }}
                  >
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          </div>
        ) : (
          <p className="muted" style={{ marginTop: "1.5rem" }}>
            No images selected yet.
          </p>
        )}
      </section>
    </section>
  );
}
