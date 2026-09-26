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

export function ImageLightbox({
  image,
  alt,
  onClose,
}: Readonly<{ image: string; alt: string; onClose: () => void }>) {
  return (
    <Dialog.Root open={Boolean(image)} onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-[9999] bg-black/70" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-[10000] flex max-h-[calc(100vh-38px)] max-w-[calc(100vw-38px)] -translate-x-1/2 -translate-y-1/2 flex-col items-center justify-center gap-[9px] overflow-auto rounded-[18px] border border-foreground/10 bg-card/90 p-3">
          <Dialog.Title className="sr-only">{alt || "Image preview"}</Dialog.Title>
          <Dialog.Description className="sr-only">
            Expanded release image preview.
          </Dialog.Description>
          <img src={image} alt={alt} />
          <Dialog.Close className="ghost" aria-label="Close image preview">
            Close
          </Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
