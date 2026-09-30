import { useBeforeUnload, useBlocker } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from './ConfirmDialog';

/** Must be mounted within the existing data router. Browser unload remains native. */
export function AsyncLeaveDialog({
  dirty,
  onLeave,
}: {
  dirty: boolean;
  /** Synchronous cancellation/secret cleanup; leaving never waits for a request. */
  onLeave?: () => void;
}) {
  const { t } = useTranslation();
  const blocker = useBlocker(dirty);
  useBeforeUnload((event) => {
    if (dirty) {
      event.preventDefault();
      event.returnValue = '';
    }
  });
  return (
    <ConfirmDialog
      open={blocker.state === 'blocked'}
      title={t('common.leave.title')}
      description={t('common.leave.body')}
      confirmLabel={t('common.leave.confirm')}
      cancelLabel={t('common.leave.stay')}
      onCancel={() => {
        if (blocker.state === 'blocked') blocker.reset();
      }}
      onConfirm={() => {
        if (blocker.state !== 'blocked') return;
        onLeave?.();
        blocker.proceed();
      }}
    />
  );
}
