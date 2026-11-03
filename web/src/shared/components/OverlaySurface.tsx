import {
  createElement,
  useEffect,
  useLayoutEffect,
  useRef,
  type ReactNode,
  type RefObject,
} from 'react';
import {
  Dialog,
  PopoverContext,
  useContextProps,
  useSlottedContext,
  type DialogProps,
} from 'react-aria-components';
import { FocusScope, mergeProps, useModalOverlay, usePopover, useFocusManager } from 'react-aria';
import { OverlayPortal } from './OverlayPortal';

function overlayState(open: boolean, onClose: () => void) {
  return {
    point: null,
    setPoint: () => undefined,
    isOpen: open,
    setOpen: (next: boolean) => {
      if (!next) onClose();
    },
    open: () => undefined,
    close: onClose,
    toggle: () => {
      if (open) onClose();
    },
  };
}

export function ModalSurface({
  open,
  onClose,
  busy = false,
  dismissable = true,
  backdropClassName = 'dialog-backdrop nb-dialog-backdrop',
  initialFocusRef,
  children,
  ...props
}: Omit<DialogProps, 'children'> & {
  open: boolean;
  onClose: () => void;
  busy?: boolean;
  dismissable?: boolean;
  backdropClassName?: string;
  initialFocusRef?: RefObject<HTMLElement | null>;
  children: ReactNode;
}) {
  const ref = useRef<HTMLElement>(null);
  const previous = useRef<HTMLElement | null>(null);
  const state = overlayState(open, onClose);
  const { modalProps, underlayProps } = useModalOverlay(
    {
      isDismissable: dismissable && !busy,
      isKeyboardDismissDisabled: busy,
    },
    state,
    ref,
  );
  useLayoutEffect(() => {
    if (!open) return;
    previous.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    return () => {
      const target = previous.current;
      previous.current = null;
      queueMicrotask(() => {
        if (target?.isConnected) target.focus({ preventScroll: true });
      });
    };
  }, [open]);
  useEffect(() => {
    if (open) (initialFocusRef?.current ?? ref.current)?.focus({ preventScroll: true });
  }, [open, initialFocusRef]);
  useEffect(() => {
    if (
      open &&
      busy &&
      document.activeElement instanceof HTMLElement &&
      document.activeElement.matches(':disabled')
    ) {
      ref.current?.focus({ preventScroll: true });
    }
  }, [open, busy]);
  return (
    <OverlayPortal>
      <div
        {...underlayProps}
        className={backdropClassName}
        hidden={!open}
        style={open ? undefined : { display: 'none' }}
      >
        <FocusScope contain={open}>
          <ModalKeyboard>
            <Dialog
              {...props}
              ref={ref}
              render={(dialogProps) => (
                <section
                  {...mergeProps(dialogProps, modalProps)}
                  aria-modal={open || undefined}
                  aria-busy={busy}
                />
              )}
            >
              {children}
            </Dialog>
          </ModalKeyboard>
        </FocusScope>
      </div>
    </OverlayPortal>
  );
}

/** Stable positioning and dismissal for RAC collections and mixed dialog content. */
export function AnchoredSurface({
  open,
  onClose,
  triggerRef,
  children,
  className,
  nonModal = false,
}: {
  open: boolean;
  onClose: () => void;
  triggerRef: RefObject<Element | null>;
  children: ReactNode;
  className?: string;
  nonModal?: boolean;
}) {
  const context = useSlottedContext(PopoverContext);
  const localRef = useRef<HTMLElement>(null);
  const [, ref] = useContextProps({ triggerRef }, localRef, PopoverContext);
  const { popoverProps, underlayProps } = usePopover(
    {
      triggerRef,
      popoverRef: ref,
      placement: 'bottom end',
      offset: 4,
      isNonModal: nonModal,
      shouldCloseOnInteractOutside: (target) => !triggerRef.current?.contains(target),
    },
    overlayState(open, onClose),
  );
  if (!open) return null;
  const content = [PopoverContext, ...(context?.clearContexts ?? [])].reduce<ReactNode>(
    (node, Context) => createElement(Context.Provider, { value: null }, node),
    children,
  );
  return (
    <OverlayPortal>
      {!nonModal && <div {...underlayProps} className="nb-popover-underlay" />}
      <FocusScope restoreFocus contain={!nonModal} autoFocus={!nonModal}>
        <section {...popoverProps} ref={ref} className={className}>
          {content}
        </section>
      </FocusScope>
    </OverlayPortal>
  );
}

export function useDrawerOverlay(
  open: boolean,
  onClose: () => void,
  ref: RefObject<HTMLElement | null>,
) {
  const { modalProps } = useModalOverlay({ isDismissable: true }, overlayState(open, onClose), ref);
  useEffect(() => {
    if (!open) return;
    const node = ref.current;
    if (node) node.scrollTop = 0;
    node
      ?.querySelector<HTMLElement>('a[href], button:not(:disabled), select:not(:disabled)')
      ?.focus({ preventScroll: true });
  }, [open, ref]);
  return open ? modalProps : {};
}

function ModalKeyboard({ children }: { children: ReactNode }) {
  const manager = useFocusManager();
  return (
    <div
      data-nb-focus-boundary=""
      style={{ display: 'contents' }}
      onKeyDown={(event) => {
        if (
          event.defaultPrevented ||
          event.key !== 'Tab' ||
          event.altKey ||
          event.ctrlKey ||
          event.metaKey ||
          event.nativeEvent.isComposing
        )
          return;
        if (
          !(event.target instanceof Element) ||
          event.target.closest('[data-nb-focus-boundary]') !== event.currentTarget
        )
          return;
        event.preventDefault();
        event.stopPropagation();
        // Native fieldset disabling is inherited and not reflected by the control's disabled attribute.
        const options = {
          tabbable: true,
          wrap: true,
          accept: (node: Element) => !node.matches(':disabled'),
        };
        if (event.shiftKey) manager?.focusPrevious(options);
        else manager?.focusNext(options);
      }}
    >
      {children}
    </div>
  );
}

// Dialog semantics initially focus the container. After the portal connects and native
// showModal runs, retain these small action dialogs' first-control keyboard contract.
export function FirstActionFocus({ enabled = true }: { enabled?: boolean }) {
  const manager = useFocusManager();
  useEffect(() => {
    if (!enabled) return;
    let active = true;
    queueMicrotask(() => {
      if (active)
        manager?.focusFirst({
          tabbable: true,
          accept: (node) => !node.matches(':disabled'),
        });
    });
    return () => {
      active = false;
    };
  }, [enabled, manager]);
  return null;
}
