import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  useRef,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import { Icon } from './Icon';

export type ToastTone = 'info' | 'success' | 'warning' | 'error';

export interface ToastMessage {
  id: string;
  message: ReactNode;
  tone?: ToastTone;
  title?: ReactNode;
  /** Override the tone default; null keeps the toast until explicit dismiss. */
  durationMs?: number | null;
}

interface ToastContextValue {
  toasts: ToastMessage[];
  push: (toast: Omit<ToastMessage, 'id'>) => string;
  dismiss: (id: string) => void;
  clear: () => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

const ICON_BY_TONE: Record<ToastTone, 'info' | 'check' | 'warning' | 'error'> = {
  info: 'info',
  success: 'check',
  warning: 'warning',
  error: 'error',
};

/**
 * Toasts are transient feedback only. Errors remain until acknowledged so a
 * failed mutation cannot disappear before it can be read; warning notices
 * stay longer than ordinary success/info confirmations.
 */
export const TOAST_DEFAULT_DURATIONS = {
  info: 5_000,
  success: 5_000,
  warning: 8_000,
  error: null,
} as const satisfies Readonly<Record<ToastTone, number | null>>;

function durationFor(toast: ToastMessage): number | null {
  if (toast.durationMs === null) return null;
  if (toast.durationMs !== undefined && Number.isFinite(toast.durationMs)) {
    return Math.max(0, Math.floor(toast.durationMs));
  }
  return TOAST_DEFAULT_DURATIONS[toast.tone ?? 'info'];
}

function ToastItem({ toast, onDismiss }: { toast: ToastMessage; onDismiss: (id: string) => void }) {
  const { t } = useTranslation();
  const tone = toast.tone ?? 'info';
  const duration = durationFor(toast);
  const remaining = useRef(duration);
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const paused = hovered || focused;
  useEffect(() => {
    if (duration === null || paused) return undefined;
    const started = Date.now();
    const timer = window.setTimeout(() => onDismiss(toast.id), remaining.current ?? duration);
    return () => {
      window.clearTimeout(timer);
      remaining.current = Math.max(0, (remaining.current ?? duration) - (Date.now() - started));
    };
  }, [duration, onDismiss, paused, toast.id]);

  return (
    <div
      className={['nb-toast', 'nb-toast--' + tone].join(' ')}
      role={tone === 'error' ? 'alert' : 'status'}
      onPointerEnter={() => setHovered(true)}
      onPointerLeave={() => setHovered(false)}
      onFocusCapture={() => setFocused(true)}
      onBlurCapture={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setFocused(false);
      }}
    >
      <Icon name={ICON_BY_TONE[tone]} />
      <div>
        {toast.title ? <strong>{toast.title}</strong> : null}
        <div>{toast.message}</div>
      </div>
      <button
        type="button"
        className="nb-icon-button"
        aria-label={t('common.close')}
        onClick={() => onDismiss(toast.id)}
      >
        <Icon name="close" />
      </button>
    </div>
  );
}

export function ToastViewport({
  toasts,
  onDismiss,
}: {
  toasts: ToastMessage[];
  onDismiss: (id: string) => void;
}) {
  return (
    <div className="nb-toast-viewport">
      {toasts.map((toast) => (
        <ToastItem key={toast.id} toast={toast} onDismiss={onDismiss} />
      ))}
    </div>
  );
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastMessage[]>([]);
  const dismiss = useCallback(
    (id: string) => setToasts((current) => current.filter((toast) => toast.id !== id)),
    [],
  );
  const clear = useCallback(() => setToasts([]), []);
  const push = useCallback((toast: Omit<ToastMessage, 'id'>) => {
    const id = `toast-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    setToasts((current) => [...current.slice(-3), { ...toast, id }]);
    return id;
  }, []);
  const value = useMemo(() => ({ toasts, push, dismiss, clear }), [clear, dismiss, push, toasts]);
  return (
    <ToastContext.Provider value={value}>
      {children}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const value = useContext(ToastContext);
  if (!value) throw new Error('useToast must be used within a ToastProvider');
  return value;
}

/** Shells may be rendered in isolated tests or public states without a toast provider. */
export function useOptionalToast(): ToastContextValue | null {
  return useContext(ToastContext);
}
