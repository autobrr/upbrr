// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";

/** Keeps image preview state local to the one authenticated lightbox. */
export function useImageLightbox() {
  const [image, setImage] = useState("");
  const [alt, setAlt] = useState("");
  const close = () => {
    setImage("");
    setAlt("");
  };
  return { image, alt, setImage, setAlt, close };
}

/** Displays natural-size images in a viewport-bounded, keyboard-scrollable dialog. */
export function ImageLightbox({
  image,
  alt,
  onClose,
}: Readonly<{ image: string; alt: string; onClose: () => void }>) {
  return (
    <Dialog.Root open={Boolean(image)} onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-[9999] bg-black/70" />
        <Dialog.Content className="fixed top-2 left-2 z-[10000] flex h-[calc(100dvh-1rem)] w-[calc(100%-1rem)] flex-col gap-2 overflow-hidden rounded-[18px] border border-foreground/10 bg-card/90 p-2">
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
            <img className="mx-auto h-auto max-w-none" src={image} alt={alt} />
          </div>
          <Dialog.Close className="ghost shrink-0 self-center" aria-label="Close image preview">
            Close
          </Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
