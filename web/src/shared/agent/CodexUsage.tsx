import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Button, Fold, FormActions, Note } from '@shared/components/ui';
import { SectionHead, LoadingState } from '@shared/components/States';
import { useSearchState } from '@shared/operations/useSearchState';
import { formatCount } from '@shared/utils/formatNumber';
import { AgentError, AgentTime } from './AgentParts';
import { DriverFailure } from './DriverFailure';
import { getCodexUsage, type CodexStatus, type QuotaWindow } from './codexApi';
import { agentRoot } from './api';
import { ApiError } from '@shared/query/http';

function WindowUsage({ window, now }: { window: QuotaWindow; now: number }) {
  const { t } = useTranslation();
  const used = window.used_percent;
  const left = used === null ? null : Math.max(0, Math.min(100, 100 - used));
  const seconds = window.window_seconds;
  const duration =
    seconds === null
      ? t('common.agent.quota.unknown')
      : seconds % 86400 === 0
        ? t('common.agent.quota.days', { count: seconds / 86400 })
        : seconds % 3600 === 0
          ? t('common.agent.quota.hours', { count: seconds / 3600 })
          : t('common.agent.quota.minutes', { count: Math.ceil(seconds / 60) });
  const reset = window.resets_at === null ? null : window.resets_at * 1000;
  const label = window.name.startsWith('primary')
    ? t('common.agent.quota.primary')
    : window.name.startsWith('secondary')
      ? t('common.agent.quota.secondary')
      : window.name;
  return (
    <div className="nb-stack nb-stack--sm">
      <strong>
        {label} · {duration}
      </strong>
      <p className="agent-number">
        {used === null
          ? t('common.agent.quota.unknown')
          : t('common.agent.quota.percent', { used, remaining: left })}
      </p>
      {used !== null ? (
        <progress
          className="agent-quota-progress"
          max={100}
          value={Math.max(0, Math.min(100, used))}
          aria-label={label}
        />
      ) : null}
      {used !== null && (used < 0 || used > 100) ? (
        <Note tone="warn">{t('common.agent.quota.unusualPercent')}</Note>
      ) : null}
      <p className="nb-small nb-muted">
        {t('common.agent.quota.reset')} <AgentTime value={reset} />
        {reset !== null ? (
          <>
            {' '}
            ·{' '}
            {reset <= now
              ? t('common.agent.quota.pendingRefresh')
              : t('common.agent.quota.countdown', { count: Math.ceil((reset - now) / 60000) })}
          </>
        ) : null}
      </p>
    </div>
  );
}

export function CodexUsageCard({
  account,
  active,
  status,
}: {
  account: string;
  active: boolean;
  status: CodexStatus;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const force = useRef(false);
  const [params, setParams] = useSearchState();
  const [now, setNow] = useState(() => Date.now());
  const key = [...agentRoot('admin', account), 'codex-usage', status.revision];
  const query = useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      const refresh = force.current;
      force.current = false;
      const value = await getCodexUsage(refresh, signal);
      if (value.official.revision !== status.revision) {
        void client.invalidateQueries({ queryKey: [...agentRoot('admin', account), 'codex'] });
        throw new ApiError(
          'connection_changed',
          t('common.agent.diagnostic.reasons.connection_changed'),
          409,
        );
      }
      return value;
    },
    enabled: active && status.connected,
    retry: false,
    staleTime: 30000,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => setNow(Date.now()), 30000);
    return () => clearInterval(timer);
  }, [active]);
  const refresh = () => {
    force.current = true;
    void query.refetch({ cancelRefetch: false });
  };
  const connectionChanged =
    query.error instanceof ApiError &&
    (query.error.status === 409 || query.error.code === 'connection_changed');
  useEffect(() => {
    if (connectionChanged)
      void client.invalidateQueries({ queryKey: [...agentRoot('admin', account), 'codex'] });
  }, [connectionChanged, client, account]);
  const official = connectionChanged ? undefined : query.data?.official;
  const data = official?.data;
  const local = query.data?.local;
  const localData = local?.data;
  const unknown = t('common.agent.quota.notProvided');
  const stale = official?.stale || Boolean(query.error && data);
  return (
    <div className="nb-stack">
      <SectionHead title={t('common.agent.quota.title')} />
      <FormActions>
        <Button type="button" disabled={query.isFetching} onClick={refresh}>
          {t(query.isFetching ? 'common.agent.quota.refreshing' : 'common.agent.quota.refresh')}
        </Button>
        {query.isFetching ? (
          <Button type="button" onClick={() => void client.cancelQueries({ queryKey: key })}>
            {t('common.cancel')}
          </Button>
        ) : null}
      </FormActions>
      {query.isPending && query.isFetching ? <LoadingState /> : null}
      {query.error ? <AgentError error={query.error} onRetry={refresh} /> : null}
      {official?.error ? <DriverFailure failure={official.error} onRetry={refresh} /> : null}
      {stale ? <Note tone="warn">{t('common.agent.quota.stale')}</Note> : null}
      <p>
        {t('common.agent.quota.plan')}: {data?.plan_type || unknown} ·{' '}
        {t('common.agent.quota.workspace')}: {data?.workspace_name || unknown}
      </p>
      {official?.attempted_at ? (
        <p className="nb-small nb-muted">
          {t('common.agent.quota.lastQuery')} <AgentTime value={official.attempted_at} />
        </p>
      ) : null}
      {official?.fetched_at ? (
        <p className="nb-small nb-muted">
          {t('common.agent.quota.updated')} <AgentTime value={official.fetched_at} />
        </p>
      ) : null}
      <p className="nb-small nb-muted">{t('common.agent.quota.timezone')}</p>
      {data?.partial ? <Note>{t('common.agent.quota.partial')}</Note> : null}
      {data?.buckets.map((bucket) => (
        <div className="agent-quota-bucket nb-stack" key={bucket.id}>
          <strong>{bucket.name || (bucket.id === 'codex' ? 'Codex' : bucket.id)}</strong>
          {bucket.plan_type && bucket.plan_type !== data.plan_type ? (
            <p>
              {t('common.agent.quota.plan')}: {bucket.plan_type}
            </p>
          ) : null}
          {bucket.windows.length ? (
            bucket.windows.map((window) => (
              <WindowUsage key={window.name} window={window} now={now} />
            ))
          ) : (
            <p>{t('common.agent.quota.unknown')}</p>
          )}
          {bucket.reached_type || bucket.limit_reached === true || bucket.allowed === false ? (
            <Note tone="warn">
              {t('common.agent.quota.limitReached')}
              {bucket.reached_type ? <> · {bucket.reached_type}</> : null}
            </Note>
          ) : null}
          {bucket.credits ? (
            <div className="nb-stack nb-stack--sm">
              <strong>{t('common.agent.quota.credits')}</strong>
              <p>
                {bucket.credits.unlimited === true
                  ? t('common.agent.quota.unlimited')
                  : bucket.credits.balance
                    ? bucket.credits.balance +
                      ' ' +
                      (bucket.credits.unit || t('common.agent.quota.unitUnknown'))
                    : t('common.agent.quota.unknown')}
              </p>
              {bucket.credits.has_credits !== null ? (
                <p>
                  {t(
                    bucket.credits.has_credits
                      ? 'common.agent.quota.hasCredits'
                      : 'common.agent.quota.noCredits',
                  )}
                </p>
              ) : null}
            </div>
          ) : null}
        </div>
      ))}
      {data?.reset_credits_available !== null && data?.reset_credits_available !== undefined ? (
        <p>{t('common.agent.quota.resetCredits', { count: data.reset_credits_available })}</p>
      ) : null}
      {!data && !query.isFetching && !query.error && !official?.error ? (
        <p>{t('common.agent.quota.unknown')}</p>
      ) : null}
      <Fold title={t('common.agent.quota.localTitle')} defaultOpen>
        <div className="nb-stack nb-stack--sm">
          <p>{t('common.agent.quota.localScope')}</p>
          {local?.error ? <DriverFailure failure={local.error} onRetry={refresh} /> : null}
          {localData ? (
            <>
              <p>
                <AgentTime value={localData.window_start} /> –{' '}
                <AgentTime value={localData.window_end} />
              </p>
              <div className="agent-token-grid">
                {(
                  [
                    'requests',
                    'input_tokens',
                    'output_tokens',
                    'cached_input_tokens',
                    'cache_write_input_tokens',
                    'reasoning_tokens',
                    'unknown_count',
                  ] as const
                ).map((name) => (
                  <div key={name}>
                    <span>{t(`common.agent.quota.localFields.${name}`)}</span>
                    <strong className="agent-number">{formatCount(localData[name]).display}</strong>
                  </div>
                ))}
              </div>
              <Button
                type="button"
                onClick={() => {
                  const next = new URLSearchParams(params);
                  next.set('ai_tab', 'requests');
                  next.set('ai_provider', 'codex');
                  next.set('ai_from', String(localData.window_start));
                  next.set('ai_to', String(localData.window_end));
                  for (const name of [
                    'ai_source',
                    'ai_kind',
                    'ai_state',
                    'ai_actor',
                    'ai_requests_page',
                  ])
                    next.delete(name);
                  setParams(next);
                }}
              >
                {t('common.agent.quota.records')}
              </Button>
            </>
          ) : null}
        </div>
      </Fold>
      <Fold title={t('common.agent.quota.connectionDetails')}>
        <p>
          {t('common.agent.quota.connectionUpdated')} <AgentTime value={status.updated_at} />
        </p>
        <p>
          {t('common.agent.quota.credentialExpiry')} <AgentTime value={status.expires_at * 1000} />
        </p>
        <p className="nb-small nb-muted">{t('common.agent.quota.credentialHelp')}</p>
      </Fold>
    </div>
  );
}
