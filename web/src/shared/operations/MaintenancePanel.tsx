import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import {
  disableAdminMaintenance,
  enableMaintenance,
  getMaintenanceState,
  maintenanceKeys,
  type MaintenanceAction,
  type MaintenanceRole,
} from './maintenance';
import { useRetainedOperation } from './useRetainedOperation';

const REASON_MAX_BYTES = 4_096;

function validReason(value: string): boolean {
  return value.trim().length > 0 && new TextEncoder().encode(value).byteLength <= REASON_MAX_BYTES;
}

export function MaintenancePanel({
  role,
  onAuthorityLoss,
}: {
  role: MaintenanceRole;
  onAuthorityLoss?: () => void;
}) {
  const state = useQuery({
    queryKey: maintenanceKeys.state(role),
    queryFn: () => getMaintenanceState(role),
    retry: false,
  });
  const [reason, setReason] = useState('');
  const [confirmation, setConfirmation] = useState<MaintenanceAction | null>(null);
  const transition = useRetainedOperation<{
    action: MaintenanceAction;
    expectedRevision: string;
    reason: string;
  }, unknown>(
    (input, key) => input.action === 'enable'
      ? enableMaintenance(role, input.expectedRevision, input.reason, key)
      : disableAdminMaintenance(input.expectedRevision, input.reason, key),
    async (input) => {
      const refreshed = await state.refetch();
      const reached = input.action === 'enable'
        ? refreshed.data?.enabled === true
        : refreshed.data?.enabled === false;
      if (reached) {
        setConfirmation(null);
        setReason('');
      }
      return refreshed;
    },
    maintenanceKeys.root(role),
  );

  useEffect(() => {
    const error = state.error ?? transition.error;
    if (error && (isUnauthorized(error) || isForbidden(error))) onAuthorityLoss?.();
  }, [onAuthorityLoss, state.error, transition.error]);

  if (state.isPending && !state.data) return <Card className="ops-danger"><LoadingState /></Card>;
  if (state.error && !state.data) return <Card className="ops-danger"><ErrorState error={state.error} onRetry={() => void state.refetch()} /></Card>;

  const authority = state.data;
  const mayAct = Boolean(authority && (role === 'admin' || !authority.enabled));
  const action: MaintenanceAction = authority?.enabled ? 'disable' : 'enable';
  const reasonOK = validReason(reason);
  const title = role === 'steward' ? 'Emergency maintenance' : 'Maintenance control';
  const actionLabel = action === 'enable' ? 'Enable maintenance' : 'Disable maintenance';
  const description = action === 'enable'
    ? 'New business and ordinary user requests will be refused. Already accepted continuation work and the authenticated administrator station remain available.'
    : 'Ordinary admission will resume after the authoritative maintenance transition commits. Authentication and all resource gates remain unchanged.';

  return (
    <Card className="ops-danger">
      <div className="ops-toolbar">
        <div><h2>{title}</h2><p>{description}</p></div>
        {authority ? <StatusBadge active={!authority.enabled} danger={authority.enabled} label={authority.enabled ? 'maintenance on' : 'maintenance off'} /> : null}
      </div>
      {authority ? <p className="muted">Authority revision {authority.revision}. Success, conflict, or an unknown response reloads this singleton and never repeats the transition automatically.</p> : null}
      {role === 'steward' && authority?.enabled ? <p>Stewards cannot disable maintenance. Use the authenticated administrator station to restore ordinary admission.</p> : null}
      {state.error && state.data ? <ErrorState error={state.error} onRetry={() => void state.refetch()} /> : null}
      {transition.error ? <ErrorState error={transition.error} /> : null}
      {mayAct ? <>
        <label className="ops-form-field"><span>Reason</span><textarea rows={4} value={reason} onChange={(event) => setReason(event.target.value)} /></label>
        {!reasonOK && reason.length > 0 ? <p className="field-error" role="alert">Enter a non-blank reason no larger than 4,096 UTF-8 bytes.</p> : null}
        <button className="btn btn-danger" type="button" disabled={!reasonOK || transition.isPending} onClick={() => { transition.reset(); setConfirmation(action); }}>{actionLabel}</button>
      </> : null}
      <ConfirmDialog
        open={confirmation !== null}
        title={confirmation === 'enable' ? 'Enable maintenance?' : 'Disable maintenance?'}
        description={description}
        confirmLabel={confirmation === 'enable' ? 'Enable maintenance' : 'Disable maintenance'}
        busy={transition.isPending}
        danger
        onCancel={() => { if (!transition.isPending) setConfirmation(null); }}
        onConfirm={() => {
          if (!authority || !confirmation || !reasonOK || (confirmation === 'disable' && role !== 'admin')) return;
          transition.mutate({ action: confirmation, expectedRevision: authority.revision, reason });
        }}
      />
    </Card>
  );
}
