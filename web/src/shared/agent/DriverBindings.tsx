import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Button, Field, Fields, FilterBar, FormActions, Fold, Note } from '@shared/components/ui';
import { LoadingState } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import type { PageSize } from '@shared/operations/pageNumbers';
import { useOperation } from '@shared/operations/useOperation';
import { agentRoot, getMemoryOwners, pageMetadata, type MemoryOwner } from './api';
import { AgentError } from './AgentParts';
import {
  effectiveBinding,
  getDrivers,
  saveBinding,
  type BindingUpdate,
  type DriverBinding,
  type DriverConfiguration,
  type DriverScope,
} from './driversApi';

export function BindingEditor({
  config,
  scope,
  actor = '0',
  label,
  account,
  onConfiguration,
}: {
  config: DriverConfiguration;
  scope: DriverScope;
  actor?: string;
  label: string;
  account: string;
  onConfiguration: (value: DriverConfiguration) => void;
}) {
  const { t } = useTranslation();
  const saved = config.bindings.find(
    (binding) => binding.scope === scope && binding.actor_id === actor,
  );
  const [base, setBase] = useState<DriverBinding | undefined>(saved);
  const [value, setValue] = useState(saved?.driver_id ?? '');
  const [current, setCurrent] = useState<{ binding?: DriverBinding } | null>(null);
  const effective = effectiveBinding(config, scope, actor);
  const name =
    config.profiles.find((profile) => profile.id === effective.driver_id)?.name ??
    effective.driver_id;
  const source =
    effective.scope === 'legacy'
      ? t('common.agent.drivers.legacy')
      : effective.actor_id !== '0'
        ? t('common.agent.drivers.individual')
        : t(`common.agent.drivers.scopes.${effective.scope}`);
  const operation = useOperation<BindingUpdate, never, DriverBinding>({
    authorityRoot: agentRoot('admin', account),
    execute: async (input, _secret, _key, context) => {
      const result = await saveBinding(input, context.signal);
      context.commit(() => {
        setBase(result.revision === '0' ? undefined : result);
        setCurrent(null);
      });
      return result;
    },
    reconcile: async (input, failure, context) => {
      const latest = await getDrivers(context.signal);
      const binding = latest.bindings.find(
        (item) => item.scope === input.scope && item.actor_id === input.actor_id,
      );
      context.commit(() => {
        onConfiguration(latest);
        if (failure) setCurrent({ binding });
      });
    },
  });
  const unresolved = operation.outcome === 'unknown' || operation.outcome === 'conflict';
  return (
    <form
      className="nb-stack"
      onSubmit={(event) => {
        event.preventDefault();
        void operation
          .run({
            scope,
            actor_id: actor,
            driver_id: value || null,
            expected_revision: base?.revision ?? '0',
          })
          .catch(() => undefined);
      }}
    >
      <Fields>
        <Field
          label={label}
          help={t('common.agent.drivers.effective', {
            name: effective.driver_id === '0' ? t('common.agent.drivers.legacy') : name,
            source,
          })}
        >
          {(props) => (
            <select
              {...props}
              value={value}
              disabled={operation.isPending}
              onChange={(event) => setValue(event.target.value)}
            >
              <option value="">
                {t(
                  scope === 'default'
                    ? 'common.agent.drivers.useLegacy'
                    : 'common.agent.drivers.inherit',
                )}
              </option>
              {config.profiles
                .filter(
                  (profile) =>
                    profile.settings.provider !== 'codex' ||
                    scope === 'admin_chat' ||
                    (scope === 'steward_chat' && actor !== '0'),
                )
                .map((profile) => (
                  <option key={profile.id} value={profile.id}>
                    {profile.id === '0' ? t('common.agent.drivers.legacy') : profile.name}
                  </option>
                ))}
            </select>
          )}
        </Field>
      </Fields>
      <FormActions>
        <Button
          type="submit"
          variant="primary"
          size="sm"
          disabled={operation.isPending || unresolved || value === (base?.driver_id ?? '')}
        >
          {t('common.save')}
        </Button>
        {unresolved ? (
          <Button
            type="button"
            size="sm"
            disabled={operation.isPending}
            onClick={() => void operation.check()}
          >
            {t('common.agent.drivers.checkStatus')}
          </Button>
        ) : null}
      </FormActions>
      {operation.error ? <AgentError error={operation.error} /> : null}
      {operation.refreshError ? (
        <AgentError error={operation.refreshError} onRetry={() => void operation.check()} />
      ) : null}
      {current ? (
        <Note>
          <p>{t('common.agent.configRecovery')}</p>
          <div className="nb-actions">
            <Button
              type="button"
              size="sm"
              onClick={() => {
                setBase(current.binding);
                setCurrent(null);
                operation.reset();
              }}
            >
              {t('common.agent.keepDraft')}
            </Button>
            <Button
              type="button"
              size="sm"
              onClick={() => {
                setBase(current.binding);
                setValue(current.binding?.driver_id ?? '');
                setCurrent(null);
                operation.reset();
              }}
            >
              {t('common.agent.restoreSaved')}
            </Button>
          </div>
        </Note>
      ) : null}
      {operation.isSuccess ? <Note tone="ok">{t('common.outcome.saved')}</Note> : null}
    </form>
  );
}

export function DriverBindings({
  config,
  account,
  onConfiguration,
}: {
  config: DriverConfiguration;
  account: string;
  onConfiguration: (value: DriverConfiguration) => void;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const [page, setPage] = useState('1');
  const [size, setSize] = useState<PageSize>(10);
  const [expanded, setExpanded] = useState(false);
  const [owner, setOwner] = useState<MemoryOwner | null>(null);
  const [labels, setLabels] = useState<Record<string, string>>({});
  const users = useQuery({
    queryKey: [...agentRoot('admin', account), 'driver-stewards', query, page, size],
    queryFn: ({ signal }) => getMemoryOwners(query, page, size, signal),
    enabled: expanded,
    retry: false,
  });
  const select = (id: string) => {
    const item = users.data?.items.find((user) => user.id === id);
    if (item) {
      setOwner(item);
      setLabels((previous) => ({ ...previous, [id]: item.label }));
    } else
      setOwner(
        id ? { id, label: labels[id] || t('common.agent.targetUser', { id }), role: '' } : null,
      );
  };
  return (
    <div className="nb-stack">
      <p className="nb-small nb-muted">{t('common.agent.drivers.bindingHelp')}</p>
      {(['default', 'auto_review', 'admin_chat', 'steward_chat'] as const).map((scope) => (
        <BindingEditor
          key={scope}
          config={config}
          scope={scope}
          label={t(`common.agent.drivers.scopes.${scope}`)}
          account={account}
          onConfiguration={onConfiguration}
        />
      ))}
      <Fold
        title={t('common.agent.drivers.individual')}
        open={expanded}
        onOpenChange={setExpanded}
        lazy
      >
        <div className="nb-stack">
          <FilterBar
            activeCount={query ? 1 : 0}
            onSubmit={() => setPage('1')}
            onReset={() => {
              setQuery('');
              setPage('1');
            }}
            search={
              <Field label={t('common.agent.drivers.stewardSearch')}>
                {(props) => (
                  <input
                    {...props}
                    type="search"
                    placeholder={t('common.agent.drivers.stewardSearch')}
                    value={query}
                    onChange={(event) => {
                      setQuery(event.target.value);
                      setPage('1');
                    }}
                  />
                )}
              </Field>
            }
          />
          <Fields>
            <Field label={t('common.agent.drivers.steward')}>
              {(props) => (
                <select
                  {...props}
                  value={owner?.id ?? ''}
                  onChange={(event) => select(event.target.value)}
                >
                  <option value="">{t('common.agent.drivers.chooseSteward')}</option>
                  {owner &&
                  !users.data?.items.some(
                    (user) => user.id === owner.id && ['level5', 'level6'].includes(user.role),
                  ) ? (
                    <option value={owner.id}>{owner.label}</option>
                  ) : null}
                  {users.data?.items
                    .filter((user) => ['level5', 'level6'].includes(user.role))
                    .map((user) => (
                      <option key={user.id} value={user.id}>
                        {user.label || user.id} · {user.id}
                      </option>
                    ))}
                </select>
              )}
            </Field>
          </Fields>
          {users.error ? (
            <AgentError error={users.error} onRetry={() => void users.refetch()} />
          ) : users.data ? (
            <PagePagination
              metadata={pageMetadata(users.data)}
              requestedPage={page}
              onPageChange={setPage}
              onPageSizeChange={(value) => {
                setSize(value);
                setPage('1');
              }}
              busy={users.isFetching}
            />
          ) : (
            <LoadingState />
          )}
          {config.bindings.some((binding) => binding.actor_id !== '0') ? (
            <div className="nb-actions" aria-label={t('common.agent.drivers.existingOverrides')}>
              {config.bindings
                .filter((binding) => binding.scope === 'steward_chat' && binding.actor_id !== '0')
                .map((binding) => (
                  <Button
                    type="button"
                    size="sm"
                    key={binding.actor_id}
                    onClick={() => select(binding.actor_id)}
                  >
                    {labels[binding.actor_id] ||
                      t('common.agent.targetUser', { id: binding.actor_id })}
                  </Button>
                ))}
            </div>
          ) : null}
          {owner ? (
            <BindingEditor
              key={owner.id}
              config={config}
              scope="steward_chat"
              actor={owner.id}
              label={t('common.agent.drivers.forSteward', { name: owner.label || owner.id })}
              account={account}
              onConfiguration={onConfiguration}
            />
          ) : null}
        </div>
      </Fold>
    </div>
  );
}
