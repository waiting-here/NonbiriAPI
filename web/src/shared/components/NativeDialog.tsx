import { useEffect, useLayoutEffect, useRef, type ComponentProps, type ReactNode } from 'react';
import { FocusScope, useDialog } from 'react-aria';
import { FirstActionFocus } from './OverlaySurface';
import { NativeOverlayContext } from './OverlayPortal';

/** Preserve browser top-layer and local game CSS while sharing dialog semantics and lifecycle. */
export function NativeDialog({
  open = true,
  onDismiss,
  busy = false,
  children,
  ...props
}: Omit<ComponentProps<'dialog'>, 'open' | 'onClose' | 'onCancel' | 'children'> & {
  open?: boolean;
  onDismiss: () => void;
  busy?: boolean;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const { dialogProps } = useDialog(
    {
      'aria-label': props['aria-label'],
      'aria-labelledby': props['aria-labelledby'],
      role: props.role === 'alertdialog' ? 'alertdialog' : 'dialog',
    },
    ref,
  );
  const previousFocus = useRef<Element | null>(null);
  useLayoutEffect(() => {
    if (open) previousFocus.current = document.activeElement;
  }, [open]);
  useEffect(() => {
    const node = ref.current;
    if (!open || !node) return;
    // Parent OverlayPortal connects its host in a layout effect; showModal requires connectivity.
    const previous = previousFocus.current;
    node.showModal();
    return () => {
      node.close();
      queueMicrotask(() => {
        if (previous instanceof HTMLElement && previous.isConnected)
          previous.focus({ preventScroll: true });
      });
    };
  }, [open]);
  return (
    <FocusScope contain={open}>
      <dialog
        {...dialogProps}
        {...props}
        ref={ref}
        data-nb-focus-boundary=""
        aria-modal={open || undefined}
        aria-busy={busy}
        onCancel={(event) => {
          event.preventDefault();
          event.stopPropagation();
          if (!busy) onDismiss();
        }}
      >
        <FirstActionFocus enabled={open} />
        <NativeOverlayContext.Provider value={ref}>{children}</NativeOverlayContext.Provider>
      </dialog>
    </FocusScope>
  );
}
