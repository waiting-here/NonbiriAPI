import { useEffect, useRef, useState } from 'react';
import { AsyncLeaveDialog } from '@shared/components/AsyncLeaveDialog';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { useFatFishText } from './copy';

export function useWorkspaceConfirm(options: { title?: string; confirmLabel?: string; danger?: boolean } = {}) {
  const text = useFatFishText();
  const [message, setMessage] = useState<string | null>(null);
  const resolve = useRef<((accepted: boolean) => void) | null>(null);
  useEffect(() => () => { resolve.current?.(false); resolve.current = null; }, []);
  const finish = (accepted: boolean) => { resolve.current?.(accepted); resolve.current = null; setMessage(null); };
  const confirm = (body: string) => new Promise<boolean>((done) => {
    resolve.current?.(false); resolve.current = done; setMessage(body);
  });
  return { confirm, dialog: <ConfirmDialog open={message !== null} title={options.title ?? text('discard_changes')} description={message}
    danger={options.danger} confirmLabel={options.confirmLabel ?? text('discard_changes')} onConfirm={() => finish(true)} onCancel={() => finish(false)} /> };
}
export function useDraftGuard(dirty: boolean) {
  const guard = useWorkspaceConfirm();
  return { mayReplace: (message: string) => dirty ? guard.confirm(message) : Promise.resolve(true),
    dialog: <><AsyncLeaveDialog dirty={dirty} />{guard.dialog}</> };
}
