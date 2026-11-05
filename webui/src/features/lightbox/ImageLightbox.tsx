// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";

/** Keeps shared preview state and a stable dismissal callback across route changes. */
export function useImageLightbox() {
  const [image, setImage] = useState("");
  const [alt, setAlt] = useState("");
  const close = useCallback(() => {
    setImage("");
    setAlt("");
  }, []);
  return { image, alt, setImage, setAlt, close };
}

/** Displays images at natural size, restoring the opener after dismissal. */
export function ImageLightbox({
  image,
  alt,
  onClose,
}: Readonly<{ image: string; alt: string; onClose: () => void }>) {
  const opener = useRef<HTMLElement | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (!image) return;
    window.addEventListener("popstate", onClose);
    return () => window.removeEventListener("popstate", onClose);
  }, [image, onClose]);
  return (
    <Dialog.Root open={Boolean(image)} onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-[9999] bg-black/70" />
        <Dialog.Content
          className="fixed top-2 left-2 z-[10000] flex h-[calc(100dvh-1rem)] w-[calc(100%-1rem)] flex-col gap-2 overflow-hidden rounded-[18px] border border-foreground/10 bg-card/90 p-2"
          onOpenAutoFocus={() => {
            opener.current =
              document.activeElement instanceof HTMLElement ? document.activeElement : null;
            setFailed(false);
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            if (opener.current?.isConnected) opener.current.focus();
          }}
        >
          <Dialog.Title className="sr-only">{alt || "Image preview"}</Dialog.Title>
          <Dialog.Description className="sr-only">
            Expanded release image preview.
          </Dialog.Description>
          <div
            className="min-h-0 flex-1 overflow-auto"
            role="region"
            aria-label="Image"
            tabIndex={0}
          >
            {failed ? (
              <p role="status">Image could not be loaded.</p>
            ) : (
              <img
                className="mx-auto h-auto max-w-none"
                src={image}
                alt={alt}
                onError={() => setFailed(true)}
              />
            )}
          </div>
          <Dialog.Close className="ghost shrink-0 self-center" aria-label="Close image preview">
            Close
          </Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
