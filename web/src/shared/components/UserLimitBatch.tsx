import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  type AdminUser,
  type ManagementRole,
  managementRoot,
  mutateManagedUser,
} from '@shared/operations/managedUsers';
import type { StewardPermissions } from '@shared/operations/stewardPermissions';
import { ConfirmDialog } from './ConfirmDialog';
import { responseOutcomeUnknown, operationKey } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { isManagementForbidden, isUnauthorized } from '@shared/query/http';
import { formatCount } from '@shared/utils/formatNumber';
import { Button, Field, Fold, Note } from './ui';
import { ErrorState } from './States';
import {
  INFERENCE_LIMITS,
  parseInferenceLimit,
  inferenceCeiling,
  type InferenceLimitKey,
  type InferenceLimitPatch,
} from '@shared/operations/userInferenceLimits';

type DraftValue = { mode: 'keep' | 'inherit' | 'set'; value: string };
type BatchItem = {
  id: string;
  username: string;
  key: string;
  body: InferenceLimitPatch & { mode: 'profile'; expected_revision: string };
};
type ItemState = 'queued' | 'running' | 'saved' | 'failed' | 'unknown';
type ItemResult = { item: BatchItem; state: ItemState; error?: unknown };
const statusLabels: Record<ItemState, string> = {
  queued: 'common.governance.queued',
  running: 'common.governance.saving',
  saved: 'management.users.batchLimitSaved',
  failed: 'management.users.batchLimitFailed',
  unknown: 'common.connections.unknownResult',
};
export function UserLimitBatch({
  role,
  users,
  selected,
  onSelected,
  refresh,
  onBusy,
  loading,
  limits,
}: {
  role: ManagementRole;
  users: AdminUser[];
  selected: string[];
  onSelected: (ids: string[]) => void;
  refresh: () => Promise<unknown>;
  onBusy: (busy: boolean) => void;
  loading: boolean;
  limits?: StewardPermissions['limits'];
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(
    () =>
      Object.fromEntries(
        INFERENCE_LIMITS.map(({ key }) => [key, { mode: 'keep', value: '' }]),
      ) as Record<InferenceLimitKey, DraftValue>,
  );
  const [results, setResults] = useState<ItemResult[]>([]);
  const [confirmation, setConfirmation] = useState<BatchItem[] | null>(null);
  const confirmedValues = confirmation?.[0]?.body;
  const update = (item: BatchItem, state: ItemState, error?: unknown) =>
    setResults((current) =>
      current.map((row) => (row.item.id === item.id ? { item, state, error } : row)),
    );
  const operation = useRetainedOperation(
    async (items: BatchItem[], _key, context) => {
      for (const item of items) {
        context.assertCurrent();
        context.commit(() => update(item, 'running'));
        try {
          await mutateManagedUser(role, item.id, item.body, item.key, context.signal);
          context.commit(() => update(item, 'saved'));
        } catch (error) {
          context.commit(() =>
            update(item, responseOutcomeUnknown(error) ? 'unknown' : 'failed', error),
          );
          if (isUnauthorized(error) || isManagementForbidden(error)) throw error;
        }
      }
    },
    refresh,
    managementRoot(role),
  );
  const values: InferenceLimitPatch = {};
  let invalid = false;
  for (const { key, max } of INFERENCE_LIMITS) {
    const field = draft[key];
    if (field.mode === 'inherit') values[key] = null;
    else if (field.mode === 'set') {
      const value = parseInferenceLimit(field.value, inferenceCeiling(key, max, limits));
      if (value === undefined || value === null) invalid = true;
      else values[key] = value;
    }
  }
  const chosen = users.filter((user) => selected.includes(user.id));
  const failed = results.filter(
    ({ state }) => state === 'failed' || state === 'unknown' || state === 'queued',
  );
  const complete = results.filter(
    ({ state }) => state === 'saved' || state === 'failed' || state === 'unknown',
  ).length;
  const locked = operation.isPending || results.length > 0 || loading;
  const execute = async (items: BatchItem[]) => {
    onBusy(true);
    try {
      await operation.mutateAsync(items);
    } catch {
      /* The retained operation owns error recovery. */
    } finally {
      onBusy(false);
    }
  };
  const start = () => {
    if (loading || invalid || Object.keys(values).length === 0 || chosen.length === 0) return;
    const items = chosen.map((user) => ({
      id: user.id,
      username: user.username,
      key: operationKey(),
      body: { ...values, mode: 'profile' as const, expected_revision: user.revision },
    }));
    setConfirmation(items);
  };
  return (
    <Fold
      plain
      title={t('management.users.batchLimits')}
      summary={
        chosen.length
          ? t('management.users.limitSelectionCount', { count: chosen.length })
          : undefined
      }
    >
      <ConfirmDialog
        open={confirmation !== null}
        title={t('management.users.batchLimitApply')}
        description={t('common.governance.batchUsers', { count: confirmation?.length ?? 0 })}
        confirmLabel={t('management.users.batchLimitApply')}
        onCancel={() => setConfirmation(null)}
        onConfirm={() => {
          if (!confirmation || loading) return;
          setResults(confirmation.map((item) => ({ item, state: 'queued' })));
          void execute(confirmation);
          setConfirmation(null);
        }}
      >
        <dl>
          {confirmedValues &&
            INFERENCE_LIMITS.filter(({ key }) => key in confirmedValues).map(({ key, label }) => (
              <div key={key}>
                <dt>{t(label)}</dt>
                <dd>{confirmedValues[key] ?? t('management.users.batchLimitInherit')}</dd>
              </div>
            ))}
        </dl>
        <ul>
          {confirmation?.map((item) => (
            <li key={item.id}>
              {item.username} · {item.id}
            </li>
          ))}
        </ul>
      </ConfirmDialog>
      <div className="nb-form">
        <p className="nb-sub">{t('management.users.batchScope')}</p>
        <div className="nb-actions">
          <Button
            type="button"
            disabled={locked || users.length === 0}
            onClick={() => onSelected(users.map((user) => user.id))}
          >
            {t('management.users.selectPageLimits')}
          </Button>
          <Button
            type="button"
            disabled={locked || selected.length === 0}
            onClick={() => onSelected([])}
          >
            {t('management.users.clearLimitSelection')}
          </Button>
          <span className="nb-amount">
            {t('management.users.limitSelectionCount', { count: chosen.length })}
          </span>
        </div>
        <fieldset className="nb-form user-limit-batch-fields" disabled={locked}>
          {INFERENCE_LIMITS.map(({ key, label, max }) => (
            <div className="nb-grid nb-grid--2" key={key}>
              <Field label={t('management.users.batchLimitAction', { label: t(label) })}>
                {(props) => (
                  <select
                    {...props}
                    value={draft[key].mode}
                    onChange={(event) =>
                      setDraft({
                        ...draft,
                        [key]: { ...draft[key], mode: event.target.value as DraftValue['mode'] },
                      })
                    }
                  >
                    <option value="keep">{t('management.users.batchLimitKeep')}</option>
                    <option value="inherit">{t('management.users.batchLimitInherit')}</option>
                    <option value="set">{t('management.users.batchLimitSet')}</option>
                  </select>
                )}
              </Field>
              {draft[key].mode === 'set' && (
                <Field
                  label={t(label)}
                  help={
                    limits &&
                    t('common.governance.ceiling', { max: inferenceCeiling(key, max, limits) })
                  }
                >
                  {(props) => (
                    <input
                      {...props}
                      inputMode="numeric"
                      value={draft[key].value}
                      onChange={(event) =>
                        setDraft({ ...draft, [key]: { ...draft[key], value: event.target.value } })
                      }
                    />
                  )}
                </Field>
              )}
            </div>
          ))}
        </fieldset>
        {invalid && <Note tone="bad">{t('management.users.limitInvalid')}</Note>}
        <div className="nb-actions">
          {results.length === 0 ? (
            <Button
              variant="primary"
              disabled={
                operation.isPending ||
                loading ||
                invalid ||
                chosen.length === 0 ||
                Object.keys(values).length === 0
              }
              onClick={start}
            >
              {t('management.users.batchLimitApply')}
            </Button>
          ) : (
            <>
              {failed.length > 0 && !operation.isPending && (
                <Button
                  variant="primary"
                  disabled={operation.isPending || loading}
                  onClick={() => void execute(failed.map((r) => r.item))}
                >
                  {t('management.users.batchLimitRetry')}
                </Button>
              )}
              <Button
                disabled={operation.isPending || loading}
                onClick={() => {
                  operation.reset();
                  setResults([]);
                  onSelected(failed.map((r) => r.item.id));
                }}
              >
                {t('management.users.batchLimitNew')}
              </Button>
            </>
          )}
        </div>
        {operation.outcome === 'pending' && (
          <p role="status">{t('management.users.batchLimitPending')}</p>
        )}
        {results.length > 0 && (
          <>
            <p role="status">
              {complete} / {results.length} · {t('management.users.batchLimitRecovery')}
            </p>
            <ul>
              {results.map(({ item, state, error }) => (
                <li key={item.id}>
                  <strong>{item.username}</strong>
                  {' · '}
                  {t(statusLabels[state])}
                  {error ? <ErrorState error={error} /> : null}
                </li>
              ))}
            </ul>
          </>
        )}
        {operation.error ? <ErrorState error={operation.error} /> : null}
        {operation.refreshError ? (
          <ErrorState error={operation.refreshError} onRetry={() => void operation.refresh()} />
        ) : null}
        {chosen.length > 0 && !locked && (
          <Fold plain title={t('management.users.inferenceLimitRules')}>
            <p>{t('management.users.inferenceLimitHelp')}</p>
            <ul>
              {chosen.map((user) => (
                <li key={user.id}>
                  <strong>{user.username}</strong>:{' '}
                  {INFERENCE_LIMITS.map(({ key, label }, index) => (
                    <span className="nb-amount" key={key}>
                      {t(label)}{' '}
                      {formatCount(user[('effective_' + key) as keyof AdminUser] as string).display}
                      {index < INFERENCE_LIMITS.length - 1 ? ' · ' : ''}
                    </span>
                  ))}
                </li>
              ))}
            </ul>
          </Fold>
        )}
      </div>
    </Fold>
  );
}
