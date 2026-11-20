// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import RenderedDescription from "../../components/RenderedDescription";
import { pageStyle } from "../../components/ui/pageStyle";
import type { DescriptionsFacet } from "../../releaseSession/types";

const formats = [
  { label: "Bold", tag: "b" },
  { label: "Italic", tag: "i" },
  { label: "Underline", tag: "u" },
] as const;

type Props = {
  facet: DescriptionsFacet;
  savedSource: string;
  canSave: boolean;
  onImageOpen?: (url: string, alt: string) => void;
};

/** Edits shared BBCode without changing the per-group drafts until a bulk save succeeds. */
export default function MainDescriptionEditor({ facet, savedSource, canSave, onImageOpen }: Props) {
  const id = useId();
  const textarea = useRef<HTMLTextAreaElement>(null);
  const pendingSelection = useRef<Parameters<HTMLTextAreaElement["setSelectionRange"]> | null>(
    null,
  );
  const previewRequest = useRef<AbortController>(null);
  const [draft, setDraft] = useState<string>();
  const [preview, setPreview] = useState<{ source: string; html: string }>();
  const [previewErrorSource, setPreviewErrorSource] = useState<string>();
  const source = draft ?? savedSource;
  const previewHTML = preview?.source === source ? preview.html : undefined;
  const busy = facet.view.status === "running";

  // Restore the browser selection after React commits the wrapped text.
  useLayoutEffect(() => {
    const input = textarea.current;
    if (input && pendingSelection.current) {
      input.focus();
      input.setSelectionRange(...pendingSelection.current);
      pendingSelection.current = null;
    }
  }, [source]);

  useEffect(() => () => previewRequest.current?.abort(), []);

  const clearPreview = () => {
    previewRequest.current?.abort();
    setPreview(undefined);
    setPreviewErrorSource(undefined);
  };

  const edit = (text: string) => {
    clearPreview();
    setDraft(text);
  };

  const format = (tag: (typeof formats)[number]["tag"]) => {
    const input = textarea.current;
    if (!input) return;
    const {
      selectionStart: start,
      selectionEnd: end,
      selectionDirection: direction,
      value,
    } = input;
    if (start === end) return;
    const open = `[${tag}]`;
    pendingSelection.current = [start + open.length, end + open.length, direction];
    edit(value.slice(0, start) + open + value.slice(start, end) + `[/${tag}]` + value.slice(end));
  };

  const renderPreview = async () => {
    clearPreview();
    const request = new AbortController();
    previewRequest.current = request;
    try {
      const html = await facet.renderMain(source, request.signal);
      if (!request.signal.aborted) setPreview({ source, html });
    } catch {
      if (!request.signal.aborted) setPreviewErrorSource(source);
    } finally {
      if (previewRequest.current === request) previewRequest.current = null;
    }
  };

  const save = async () => {
    clearPreview();
    if (await facet.saveMain(source))
      setDraft((current) => (current === source ? undefined : current));
  };

  return (
    <section aria-labelledby={`${id}-heading`} className={`${pageStyle.panel} grid gap-3`}>
      <h2 id={`${id}-heading`}>Main Description</h2>
      <p className="text-muted-foreground">
        Saving reapplies this shared content to every selected tracker description, replacing group
        edits. Tracker-specific formatting and generated sections are applied separately. Save an
        empty main description to clear the shared text.
      </p>
      <div role="group" aria-label="BBCode formatting" className="flex flex-wrap gap-2">
        {formats.map(({ label, tag }) => (
          <button
            className="ghost"
            key={tag}
            type="button"
            disabled={busy}
            onClick={() => format(tag)}
          >
            {label}
          </button>
        ))}
      </div>
      <label htmlFor={id}>Raw main description</label>
      <textarea
        id={id}
        ref={textarea}
        className="min-h-[170px] w-full resize-y rounded-lg border border-border bg-card px-3 py-2 text-[0.95rem] leading-6 text-foreground"
        value={source}
        disabled={busy}
        onChange={(event) => edit(event.target.value)}
      />
      <div className="flex flex-wrap gap-2">
        <button
          className="ghost"
          type="button"
          disabled={busy}
          onClick={() => void renderPreview()}
        >
          Render main preview
        </button>
        <button
          className="primary"
          type="button"
          disabled={busy || !canSave}
          onClick={() => void save()}
        >
          Save main description
        </button>
      </div>
      <section className={pageStyle.panel}>
        <h3>Rendered Main Preview</h3>
        {previewErrorSource === source ? (
          <p role="alert" className={pageStyle.error}>
            Main preview could not be rendered. Try again.
          </p>
        ) : null}
        {previewHTML ? (
          <RenderedDescription html={previewHTML} onImageOpen={onImageOpen} />
        ) : (
          <p className="text-muted-foreground">
            {previewHTML === "" ? "The main description is empty." : "No rendered preview yet."}
          </p>
        )}
      </section>
    </section>
  );
}
