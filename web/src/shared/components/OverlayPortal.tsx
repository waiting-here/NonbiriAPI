import type { ReactNode } from 'react';
import { createPortal } from 'react-dom';

// Escape sticky, transformed and clipped page ancestors while retaining overlays
// inside an active fullscreen scene.
export function OverlayPortal({ children }: { children: ReactNode }) {
  return createPortal(children, document.fullscreenElement ?? document.body);
}
