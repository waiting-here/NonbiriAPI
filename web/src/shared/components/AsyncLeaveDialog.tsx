import { useContext } from 'react';
import {
  UNSAFE_DataRouterContext,
  useBeforeUnload,
  useBlocker,
  useLocation,
  type BlockerFunction,
} from 'react-router';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from './ConfirmDialog';
import { discardPendingSearchState } from '@shared/operations/useSearchState';

type LeaveOptions = {
  dirty: boolean;
  allowNavigation?: BlockerFunction;
  /** Synchronous cancellation/secret cleanup; leaving never waits for a request. */
  onLeave?: () => void;
  onStay?: () => void;
};

export function AsyncLeaveDialog(props: LeaveOptions) {
  const router = useContext(UNSAFE_DataRouterContext);
  useBeforeUnload((event) => {
    if (props.dirty) {
      event.preventDefault();
      event.returnValue = '';
    }
  });
  return router && props.dirty ? <RouteLeaveDialog {...props} /> : null;
}

function RouteLeaveDialog({ dirty, onLeave, onStay, allowNavigation }: LeaveOptions) {
  const { t } = useTranslation();
  const location = useLocation();
  const blocker = useBlocker((navigation) => dirty && !allowNavigation?.(navigation));
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