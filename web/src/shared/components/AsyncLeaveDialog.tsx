import { useBeforeUnload, useBlocker, useLocation, type BlockerFunction } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from './ConfirmDialog';
import { discardPendingSearchState } from '@shared/operations/useSearchState';

/** Must be mounted within the existing data router. Browser unload remains native. */
export function AsyncLeaveDialog({
  dirty,
  onLeave,
  onStay,
  allowNavigation,
}: {
  dirty: boolean;
  allowNavigation?: BlockerFunction;
  /** Synchronous cancellation/secret cleanup; leaving never waits for a request. */
  onLeave?: () => void;
  onStay?: () => void;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const blocker = useBlocker((navigation) => dirty && !allowNavigation?.(navigation));
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
        if (blocker.state === 'blocked') {
          discardPendingSearchState(location);
          blocker.reset();
          onStay?.();
        }
      }}
      onConfirm={() => {
        if (blocker.state !== 'blocked') return;
        onLeave?.();
        blocker.proceed();
      }}
    />
  );
}
