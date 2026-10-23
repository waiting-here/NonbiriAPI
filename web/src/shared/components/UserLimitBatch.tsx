import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  type AdminUser,
  type ManagementRole,
  managementRoot,
  mutateManagedUser,
} from '@shared/operations/managedUsers';
import { operationKey } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { formatCount } from '@shared/utils/formatNumber';
import { Button, Field, Fold, Note } from './ui';
import { ErrorState } from './States';
import {
  INFERENCE_LIMITS,
  parseInferenceLimit,
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
type ItemResult = { item: BatchItem; saved: boolean; error?: unknown };
export function UserLimitBatch({
  role,
  users,
  selected,
  onSelected,
  refresh,
  onBusy,
  loading,
}: {
  role: ManagementRole;
  users: AdminUser[];
  selected: string[];
  onSelected: (ids: string[]) => void;
  refresh: () => Promise<unknown>;
  onBusy: (busy: boolean) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(
    () =>
      Object.fromEntries(
        INFERENCE_LIMITS.map(({ key }) => [key, { mode: 'keep', value: '' }]),
      ) as Record<InferenceLimitKey, DraftValue>,
  );
  const [results, setResults] = useState<ItemResult[]>([]);
  const operation = useRetainedOperation(
    async (items: BatchItem[], _key, context) => {
      for (const item of items) {
        context.assertCurrent();
        try {
          await mutateManagedUser(role, item.id, item.body, item.key);
          context.commit(() =>
            setResults((current) => [
              ...current.filter((r) => r.item.id !== item.id),
              { item, saved: true },
            ]),
          );
        } catch (error) {
          context.commit(() =>
            setResults((current) => [
              ...current.filter((r) => r.item.id !== item.id),
              { item, saved: false, error },
            ]),
          );
          if (isUnauthorized(error) || isForbidden(error)) throw error;
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
      const value = parseInferenceLimit(field.value, max);
      if (value === undefined || value === null) invalid = true;
      else values[key] = value;
    }
  }
  const chosen = users.filter((user) => selected.includes(user.id));
  const failed = results.filter((result) => !result.saved);
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
    setResults(items.map((item) => ({ item, saved: false })));
    void execute(items);
  };
  return (
    <Fold title={t('management.users.batchLimits')}>
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
          <span className="nb-num">
            {t('management.users.limitSelectionCount', { count: chosen.length })}
          </span>
        </div>
        <fieldset className="nb-form user-limit-batch-fields" disabled={locked}>
          {INFERENCE_LIMITS.map(({ key, label }) => (
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
                <Field label={t(label)}>
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
              {failed.length > 0 && (
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
            <p>{t('management.users.batchLimitRecovery')}</p>
            <ul>
              {results.map(({ item, saved, error }) => (
                <li key={item.id}>
                  <strong>{item.username}</strong>
                  {' · '}
                  {t(
                    saved
                      ? 'management.users.batchLimitSaved'
                      : 'management.users.batchLimitFailed',
                  )}
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
          <Fold title={t('management.users.inferenceLimitRules')}>
            <p>{t('management.users.inferenceLimitHelp')}</p>
            <ul>
              {chosen.map((user) => (
                <li key={user.id}>
                  <strong>{user.username}</strong>:{' '}
                  {INFERENCE_LIMITS.map(({ key, label }, index) => (
                    <span className="nb-num" key={key}>
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
