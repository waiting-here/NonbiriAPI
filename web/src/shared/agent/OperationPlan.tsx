import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Button, Field, Fold, Note } from '@shared/components/ui';
import { formatCount } from '@shared/utils/formatNumber';
import { captureStationSession, stationSessionMatches } from '@shared/charityManagement';
import { elevateAdmin, responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { ApiError } from '@shared/query/http';
import { agentRoot, decidePlan, type AgentPlan, type AgentRole, type PlanDecision } from './api';
import { AgentError, AgentState, MemoryDiff, RawData } from './AgentParts';
import { OperationSummary } from './OperationSummary';

export function OperationPlan({
  role,
  account,
  plan,
  readOnly = false,
}: {
  role: AgentRole;
  account: string;
  plan: AgentPlan;
  readOnly?: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [feedback, setFeedback] = useState('');
  const [confirmation, setConfirmation] = useState(false);
  const [password, setPassword] = useState('');
  const token = useRef<string | undefined>(undefined);
  const mounted = useRef(false);
  const [elevating, setElevating] = useState(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      token.current = undefined;
    };
  }, []);
  const refresh = () =>
    client.invalidateQueries({ queryKey: agentRoot(role, account) }, { throwOnError: true });
  const send = (input: Omit<PlanDecision, 'elevation_token'>, signal: AbortSignal) =>
    decidePlan(
      role,
      plan.id,
      { ...input, ...(input.approve && token.current ? { elevation_token: token.current } : {}) },
      signal,
    );
  const operation = useRetainedOperation<Omit<PlanDecision, 'elevation_token'>, unknown>(
    (input, _key, context) => send(input, context.signal),
    async (input, error, context) => {
      if (error && responseOutcomeUnknown(error)) {
        await send(input, context.signal);
        await refresh();
        token.current = undefined;
        return { operationConfirmed: true };
      }
      await refresh();
      token.current = undefined;
    },
    role === 'admin' ? ['admin'] : ['user'],
  );
  const elevatedRequired =
    role === 'admin' &&
    (plan.preview.some((row) => row.requires_elevation) ||
      (operation.error instanceof ApiError && operation.error.code === 'elevated_required'));
  const locked =
    elevating ||
    operation.isPending ||
    operation.outcome === 'unknown' ||
    Boolean(operation.refreshError);
  const submit = async (approve: boolean) => {
    if (approve && elevatedRequired && !token.current) {
      const submitted = password;
      setPassword('');
      const snapshot = captureStationSession(client, 'admin');
      setElevating(true);
      setElevationError(null);
      try {
        const elevated = await elevateAdmin(submitted);
        if (!mounted.current || !stationSessionMatches(client, 'admin', snapshot)) return;
        token.current = elevated.token;
      } catch (error) {
        if (mounted.current && stationSessionMatches(client, 'admin', snapshot))
          setElevationError(error);
        return;
      } finally {
        if (mounted.current) setElevating(false);
      }
    }
    operation.mutate({
      approve,
      feedback,
    });
    setConfirmation(false);
  };
  const [elevationError, setElevationError] = useState<unknown>(null);
  const content = (
    <section
      className={'agent-plan nb-stack' + (plan.state !== 'pending' ? ' agent-plan--history' : '')}
      aria-label={t('common.agent.plan')}
    >
      {plan.state === 'pending' ? (
        <div className="nb-inline">
          <h3>{t('common.agent.plan')}</h3>
          <AgentState state="waiting_confirmation" />
        </div>
      ) : null}
      <ol className="agent-operation-list">
        {plan.operations.map((op, index) => {
          const preview = plan.preview[index];
          return (
            <li key={index}>
              <OperationSummary operation={op} preview={preview} />
              {op.kind === 'memory.edit' && preview ? (
                <MemoryDiff before={preview.before ?? ''} after={preview.after ?? ''} />
              ) : null}
              <RawData
                title={t('common.agent.operationDetails')}
                value={{
                  kind: op.kind,
                  target_id: op.target_id,
                  input: op.input,
                  before: preview?.before,
                  after: preview?.after,
                  description: preview?.description,
                }}
              />
            </li>
          );
        })}
      </ol>
      {plan.result ? (
        <RawData title={t('common.agent.executionResult')} value={plan.result} />
      ) : null}
      {plan.state === 'pending' && !readOnly ? (
        <>
          <Field label={t('common.agent.feedback')} optional>
            {(props) => (
              <textarea
                {...props}
                value={feedback}
                disabled={locked}
                onChange={(event) => setFeedback(event.target.value)}
              />
            )}
          </Field>
          <div className="nb-actions">
            <Button
              variant="primary"
              disabled={locked}
              onClick={() =>
                plan.preview.some((row) => row.dangerous) || elevatedRequired
                  ? setConfirmation(true)
                  : void submit(true)
              }
            >
              {t('common.agent.approvePlan')}
            </Button>
            <Button variant="secondary" disabled={locked} onClick={() => void submit(false)}>
              {t('common.agent.rejectPlan')}
            </Button>
          </div>
        </>
      ) : null}
      {operation.error ? <AgentError error={operation.error} /> : null}
      {operation.outcome === 'unknown' ? (
        <Note tone="warn">
          <p>{t('common.agent.unknownWrite')}</p>
          <Button
            variant="secondary"
            disabled={operation.isPending}
            onClick={() => void operation.check()}
          >
            {t('common.connections.checkResult')}
          </Button>
        </Note>
      ) : null}
      {operation.refreshError ? (
        <AgentError error={operation.refreshError} onRetry={() => void operation.refresh()} />
      ) : null}
      <ConfirmDialog
        open={confirmation && plan.state === 'pending' && !readOnly}
        title={t('common.agent.approvePlan')}
        description={t('common.agent.planConsequence')}
        danger={plan.preview.some((row) => row.dangerous)}
        confirmLabel={t('common.agent.approvePlan')}
        busy={operation.isPending || elevating}
        confirmDisabled={elevatedRequired && !password}
        onCancel={() => {
          setConfirmation(false);
          setPassword('');
          setElevationError(null);
        }}
        onConfirm={() => void submit(true)}
      >
        {elevatedRequired ? (
          <Field
            label={t('management.users.elevatedPassword')}
            help={t('management.users.elevatedPasswordHint')}
          >
            {(props) => (
              <input
                {...props}
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            )}
          </Field>
        ) : null}
        {elevationError ? <AgentError error={elevationError} /> : null}
      </ConfirmDialog>
    </section>
  );
  return plan.state === 'pending' ? (
    content
  ) : (
    <Fold
      title={t('common.agent.plan')}
      summary={t('common.agent.activity.operationCount', {
        value: formatCount(plan.operations.length).display,
      })}
      meta={<AgentState state={plan.state} />}
      lazy
    >
      {content}
    </Fold>
  );
}
