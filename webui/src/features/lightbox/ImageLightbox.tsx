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
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="lightbox-content">
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
