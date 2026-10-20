import { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import type { CharityRole } from '@shared/operations/charity';
import {
  BACKOFF_PERIODS,
  endpointIdentityKey,
  endpointPolicyKeys,
  getCloudflareRoutes,
  getEndpointPolicies,
  getEndpointPolicy,
  getEndpointPolicyOptions,
  saveEndpointPolicy,
  type EndpointBackoffPolicy,
  type EndpointBackoffRule,
  type EndpointIdentity,
  type EndpointPolicyInput,
} from '@shared/operations/endpointPolicies';
import { useSearchState } from '@shared/operations/useSearchState';
import { usePagePager } from '@shared/operations/usePagePager';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { useDetailNavigation } from '@shared/operations/useDetailNavigation';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { PagePagination } from '@shared/operations/PagePagination';
import { validManagementSearch } from '@shared/operations/charityModelPages';
import { listReturnPath } from '@shared/operations/listReturn';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { formatCount } from '@shared/utils/formatNumber';
import { DraftLeaveGuard, DraftLeaveScope } from './charity/DraftLeaveScope';
import { EmptyState, ErrorState, LoadingState, PageHeader } from './States';
import {
  Button,
  DataTable,
  Field,
  FilterBar,
  Note,
  OutcomeNote,
  Panel,
  PanelBody,
  SaveBar,
  Tabs,
} from './ui';
import './EndpointPolicies.css';

type Props = { role: CharityRole; accountId: string; onCapabilityLoss?: () => void };
const denied = (error: unknown) => isForbidden(error) || isUnauthorized(error);
const periodKeys = {
  auto: 'common.endpointPolicies.periods.auto',
  five_hour: 'common.endpointPolicies.periods.five_hour',
  day: 'common.endpointPolicies.periods.day',
  week: 'common.endpointPolicies.periods.week',
  month: 'common.endpointPolicies.periods.month',
} as const;

export function EndpointPolicies(props: Props) {
  const { t } = useTranslation();
  const [lost, setLost] = useState(false);
  const session = useQuery<{ user?: { effective_level: number; is_banned?: boolean } }>({
    queryKey: [props.role === 'admin' ? 'admin' : 'user', 'session'],
    enabled: false,
  });
  const { onCapabilityLoss } = props;
  const reject = useCallback(() => {
    setLost(true);
    onCapabilityLoss?.();
  }, [onCapabilityLoss]);
  if (
    lost ||
    (props.role === 'steward' &&
      (session.data?.user?.effective_level !== 6 || session.data?.user?.is_banned))
  )
    return (
      <div className="page">
        <p role="alert">{t('common.endpointPolicies.accessDenied')}</p>
      </div>
    );
  return <EndpointPoliciesContent {...props} onCapabilityLoss={reject} />;
}

function EndpointPoliciesContent({ role, accountId, onCapabilityLoss }: Props) {
  const { t } = useTranslation();
  const location = useLocation();
  const [params, setParams] = useSearchState();
  const view = params.get('view') === 'routes' ? 'routes' : 'rules';
  const isNew = params.get('new') === '1';
  const connector = params.get('connector_type') ?? '';
  const url = params.get('base_url') ?? '';
  const selected = connector && url ? { connector_type: connector, base_url: url } : null;
  const selection = selected ? endpointIdentityKey(selected) : isNew ? 'new' : '';
  const [visited, setVisited] = useState<EndpointIdentity[]>(() => (selected ? [selected] : []));
  const [ready, setReady] = useState('');
  if (selected && !visited.some((item) => endpointIdentityKey(item) === selection)) {
    setVisited([...visited, selected]);
  }
  const q = params.get('q') ?? '';
  const [search, setSearch] = useState(q);
  const [previousQuery, setPreviousQuery] = useState(q);
  if (previousQuery !== q) {
    setPreviousQuery(q);
    setSearch(q);
  }
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'endpoint-policies',
    scopeKey: accountId,
  });
  const list = useQuery({
    queryKey: [...endpointPolicyKeys(role), 'list', accountId, q, pager.page, pager.pageSize],
    queryFn: ({ signal }) => getEndpointPolicies(role, q, pager.page, pager.pageSize, signal),
    retry: false,
  });
  useEffect(() => {
    if (denied(list.error)) onCapabilityLoss?.();
  }, [list.error, onCapabilityLoss]);
  const { listRef, detailRef, remember } = useDetailNavigation(
    selection,
    !selected || ready === selection,
  );
  const open = (endpoint: EndpointIdentity) => {
    remember();
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete('new');
      next.set('connector_type', endpoint.connector_type);
      next.set('base_url', endpoint.base_url);
      return next;
    });
  };
  const back = () =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete('connector_type');
      next.delete('base_url');
      next.delete('new');
      return next;
    });
  return (
    <DraftLeaveScope>
      <div className="page nb-form endpoint-policies">
        <PageHeader
          title={t('common.endpointPolicies.title')}
          description={t('common.endpointPolicies.description')}
          back={
            view === 'rules' && selection ? (
              <Button type="button" variant="ghost" onClick={back}>
                {t('common.endpointPolicies.backList')}
              </Button>
            ) : (
              <Link
                to={listReturnPath(
                  location.state,
                  role === 'admin' ? '/charity' : '/steward?tab=charity',
                )}
              >
                {t('common.endpointPolicies.backManagement')}
              </Link>
            )
          }
        />
        <Tabs
          label={t('common.endpointPolicies.sections')}
          value={view}
          tabs={[
            { value: 'rules', label: t('common.endpointPolicies.rulesTab') },
            { value: 'routes', label: t('common.endpointPolicies.routesTab') },
          ]}
          onChange={(value) =>
            setParams((previous) => {
              const next = new URLSearchParams(previous);
              next.set('view', value);
              return next;
            })
          }
        />
        <div
          role="tabpanel"
          aria-label={t('common.endpointPolicies.rulesTab')}
          hidden={view !== 'rules'}
        >
          <div hidden={Boolean(selection)} ref={listRef} tabIndex={-1} className="nb-form">
            <FilterBar
              secondaryLabel={t('common.filter')}
              activeCount={q ? 1 : 0}
              onSubmit={() => {
                if (validManagementSearch(search))
                  setParams((previous) => {
                    const next = new URLSearchParams(previous);
                    next.set('q', search.trim());
                    next.set('page', '1');
                    return next;
                  });
              }}
              search={
                <>
                  <Field label={t('common.endpointPolicies.search')}>
                    {(props) => (
                      <input
                        {...props}
                        type="search"
                        value={search}
                        maxLength={256}
                        placeholder={t('common.endpointPolicies.searchPlaceholder')}
                        onChange={(event) => setSearch(event.target.value)}
                      />
                    )}
                  </Field>
                  <Button type="submit" disabled={!validManagementSearch(search)}>
                    {t('common.search')}
                  </Button>
                </>
              }
            />
            <div className="nb-actions">
              <Button
                type="button"
                variant="primary"
                onClick={() => {
                  remember();
                  setParams((previous) => {
                    const next = new URLSearchParams(previous);
                    next.set('new', '1');
                    return next;
                  });
                }}
              >
                {t('common.endpointPolicies.configureEndpoint')}
              </Button>
            </div>
            {list.isPending ? (
              <LoadingState />
            ) : list.error ? (
              <ErrorState error={list.error} onRetry={() => void list.refetch()} />
            ) : (
              <>
                {list.data.data.length ? (
                  <DataTable
                    caption={t('common.endpointPolicies.endpoints')}
                    rows={list.data.data}
                    rowKey={endpointIdentityKey}
                    columns={[
                      {
                        key: 'endpoint',
                        header: t('common.endpointPolicies.endpoint'),
                        mobileLabel: t('common.endpointPolicies.endpoint'),
                        cell: 'title',
                        render: (row) => (
                          <>
                            <span className="mono endpoint-policy-url">{row.base_url}</span>
                            <small>{row.connector_type}</small>
                          </>
                        ),
                      },
                      {
                        key: 'rules',
                        header: t('common.endpointPolicies.ruleCount'),
                        mobileLabel: t('common.endpointPolicies.ruleCount'),
                        cell: 'meta',
                        align: 'num',
                        render: (row) => formatCount(row.rule_count).display,
                      },
                      {
                        key: 'keys',
                        header: t('common.endpointPolicies.keyCount'),
                        mobileLabel: t('common.endpointPolicies.keyCount'),
                        cell: 'meta',
                        align: 'num',
                        render: (row) => formatCount(row.key_count).display,
                      },
                      {
                        key: 'actions',
                        header: t('common.workflow.actions'),
                        mobileLabel: t('common.workflow.actions'),
                        cell: 'action',
                        render: (row) => (
                          <Button
                            type="button"
                            id={`endpoint-policy-${encodeURIComponent(endpointIdentityKey(row))}`}
                            onClick={() => open(row)}
                          >
                            {t('common.endpointPolicies.edit')}
                          </Button>
                        ),
                      },
                    ]}
                  />
                ) : (
                  <EmptyState
                    title={t('common.endpointPolicies.empty')}
                    body={t('common.endpointPolicies.emptyHelp')}
                  />
                )}
                <PagePagination
                  alwaysShowJump
                  metadata={list.data.pagination}
                  requestedPage={pager.page}
                  onPageChange={pager.setPage}
                  onPageSizeChange={pager.setPageSize}
                  busy={list.isFetching}
                />
              </>
            )}
          </div>
          <div ref={detailRef} tabIndex={-1} hidden={!selection} className="nb-form">
            <div hidden={!isNew || Boolean(selected)}>
              <NewEndpoint
                role={role}
                accountId={accountId}
                active={isNew && !selected}
                onChoose={open}
                onCapabilityLoss={onCapabilityLoss}
              />
            </div>
            {visited.map((endpoint) => (
              <div
                key={endpointIdentityKey(endpoint)}
                hidden={endpointIdentityKey(endpoint) !== selection}
              >
                <PolicyWorkspace
                  role={role}
                  accountId={accountId}
                  endpoint={endpoint}
                  active={endpointIdentityKey(endpoint) === selection}
                  onReady={setReady}
                  onCapabilityLoss={onCapabilityLoss}
                />
              </div>
            ))}
          </div>
        </div>
        <div
          role="tabpanel"
          aria-label={t('common.endpointPolicies.routesTab')}
          hidden={view !== 'routes'}
        >
          <LoadedRoutes
            role={role}
            accountId={accountId}
            active={view === 'routes'}
            onCapabilityLoss={onCapabilityLoss}
          />
        </div>
      </div>
    </DraftLeaveScope>
  );
}

function NewEndpoint({
  role,
  accountId,
  active,
  onChoose,
  onCapabilityLoss,
}: {
  role: CharityRole;
  accountId: string;
  active: boolean;
  onChoose: (endpoint: EndpointIdentity) => void;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [connector, setConnector] = useState('');
  const [url, setURL] = useState('');
  const [reading, setReading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => {
    if (!active) {
      controller.current?.abort();
    }
  }, [active]);
  const options = useQuery({
    queryKey: [...endpointPolicyKeys(role), 'options', accountId],
    queryFn: ({ signal }) => getEndpointPolicyOptions(role, signal),
    enabled: active,
    retry: false,
  });
  useEffect(() => {
    if (denied(options.error) || denied(error)) onCapabilityLoss?.();
  }, [options.error, error, onCapabilityLoss]);
  return (
    <Panel>
      <PanelBody>
        <form
          className="nb-form"
          onSubmit={(event) => {
            event.preventDefault();
            setReading(true);
            setError(null);
            const request = new AbortController();
            controller.current = request;
            void getEndpointPolicy(
              role,
              { connector_type: connector, base_url: url.trim() },
              request.signal,
            )
              .then((result) => {
                if (request.signal.aborted) return;
                client.setQueryData(
                  [...endpointPolicyKeys(role), 'detail', accountId, endpointIdentityKey(result)],
                  result,
                );
                setURL('');
                setConnector('');
                onChoose(result);
              })
              .catch((error) => {
                if (!request.signal.aborted) setError(error);
              })
              .finally(() => {
                setReading(false);
              });
          }}
        >
          <DraftLeaveGuard
            dirty={Boolean(url || connector)}
            allowNavigation={({ currentLocation, nextLocation }) =>
              currentLocation.pathname === nextLocation.pathname
            }
          />
          <h2>{t('common.endpointPolicies.configureEndpoint')}</h2>
          <p>{t('common.endpointPolicies.configureHelp')}</p>
          {options.isPending ? (
            <LoadingState />
          ) : options.error ? (
            <ErrorState error={options.error} onRetry={() => void options.refetch()} />
          ) : (
            <>
              <Field label={t('common.endpointPolicies.connector')}>
                {(props) => (
                  <select
                    {...props}
                    value={connector}
                    required
                    disabled={reading}
                    onChange={(event) => setConnector(event.target.value)}
                  >
                    <option value="">{t('common.endpointPolicies.chooseConnector')}</option>
                    {options.data.map((type) => (
                      <option key={type} value={type}>
                        {type}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
              <Field
                label={t('common.endpointPolicies.baseURL')}
                help={t('common.endpointPolicies.baseHelp')}
              >
                {(props) => (
                  <input
                    {...props}
                    type="url"
                    required
                    maxLength={4096}
                    value={url}
                    disabled={reading}
                    placeholder="https://provider.example/v1"
                    onChange={(event) => setURL(event.target.value)}
                  />
                )}
              </Field>
              <Button
                type="submit"
                variant="primary"
                disabled={reading || !connector || !url.trim()}
              >
                {t('common.endpointPolicies.openRules')}
              </Button>
            </>
          )}
          {error ? <ErrorState error={error} /> : null}
        </form>
      </PanelBody>
    </Panel>
  );
}

function PolicyWorkspace({
  role,
  accountId,
  endpoint,
  active,
  onReady,
  onCapabilityLoss,
}: Props & {
  endpoint: EndpointIdentity;
  active: boolean;
  onReady: (identity: string) => void;
}) {
  const key = endpointIdentityKey(endpoint);
  const detail = useQuery({
    queryKey: [...endpointPolicyKeys(role), 'detail', accountId, key],
    queryFn: ({ signal }) => getEndpointPolicy(role, endpoint, signal),
    retry: false,
    staleTime: 30_000,
  });
  useEffect(() => {
    if (active && !detail.isPending) onReady(key);
  }, [active, detail.isPending, key, onReady]);
  useEffect(() => {
    if (denied(detail.error)) onCapabilityLoss?.();
  }, [detail.error, onCapabilityLoss]);
  if (!detail.data)
    return detail.error ? (
      <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
    ) : (
      <LoadingState />
    );
  return (
    <PolicyEditor
      role={role}
      accountId={accountId}
      initial={detail.data}
      active={active}
      onCapabilityLoss={onCapabilityLoss}
    />
  );
}

function PolicyEditor({
  role,
  accountId,
  initial,
  active,
  onCapabilityLoss,
}: Props & { initial: EndpointBackoffPolicy; active: boolean }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [baseline, setBaseline] = useState(initial);
  const [draft, setDraft] = useState(initial.rules);
  const [reading, setReading] = useState(false);
  const [readError, setReadError] = useState<unknown>(null);
  const rereadController = useRef<AbortController | null>(null);
  useEffect(() => () => rereadController.current?.abort(), []);
  useEffect(() => {
    if (!active) rereadController.current?.abort();
  }, [active]);
  const root = endpointPolicyKeys(role);
  const save = useRetainedOperation<EndpointPolicyInput, EndpointBackoffPolicy>(
    async (input, key, context) => {
      const result = await saveEndpointPolicy(role, input, key, context.signal);
      context.commit(() =>
        client.setQueryData([...root, 'detail', accountId, endpointIdentityKey(result)], result),
      );
      return result;
    },
    () =>
      client.invalidateQueries({ queryKey: [...root, 'list', accountId] }, { throwOnError: true }),
    root,
  );
  useEffect(() => {
    if (denied(save.error) || denied(readError)) onCapabilityLoss?.();
  }, [save.error, readError, onCapabilityLoss]);
  const dirty = JSON.stringify(draft) !== JSON.stringify(baseline.rules);
  const locked =
    reading || save.isPending || save.outcome === 'unknown' || save.outcome === 'refresh-failed';
  const disabled = locked || save.outcome === 'conflict';
  const valid = draft.every(
    (rule) =>
      rule.value.trim().length > 0 &&
      Array.from(rule.value).length <= (rule.kind === 'code' ? 64 : 160),
  );
  const change = (index: number, patch: Partial<EndpointBackoffRule>) => {
    setDraft((previous) =>
      previous.map((rule, position) => (position === index ? { ...rule, ...patch } : rule)),
    );
    save.reset();
  };
  const move = (index: number, direction: number) => {
    setDraft((previous) => {
      const next = [...previous];
      [next[index], next[index + direction]] = [next[index + direction]!, next[index]!];
      return next;
    });
    save.reset();
  };
  const submit = (input: EndpointPolicyInput) =>
    save.mutate(input, {
      onSuccess: (result) => {
        setBaseline(result);
        setDraft(result.rules);
      },
    });
  const reread = async () => {
    setReading(true);
    setReadError(null);
    const request = new AbortController();
    rereadController.current = request;
    try {
      const latest = await getEndpointPolicy(role, baseline, request.signal);
      if (request.signal.aborted) return;
      setBaseline(latest);
      client.setQueryData([...root, 'detail', accountId, endpointIdentityKey(latest)], latest);
      save.reset();
    } catch (error) {
      if (!request.signal.aborted) setReadError(error);
    } finally {
      if (rereadController.current === request) setReading(false);
    }
  };
  return (
    <Panel>
      <PanelBody>
        <div className="nb-form">
          <DraftLeaveGuard
            dirty={dirty || save.isPending || save.outcome === 'unknown'}
            allowNavigation={({ currentLocation, nextLocation }) =>
              currentLocation.pathname === nextLocation.pathname
            }
          />
          <h2>{t('common.endpointPolicies.editRules')}</h2>
          <p className="mono endpoint-policy-url">{baseline.base_url}</p>
          <p>{baseline.connector_type}</p>
          <Note>{t('common.endpointPolicies.sharedHelp')}</Note>
          <p className="muted">{t('common.endpointPolicies.matchHelp')}</p>
          {draft.length ? (
            draft.map((rule, index) => (
              <fieldset key={index} className="endpoint-policy-rule" disabled={disabled}>
                <legend>{t('common.endpointPolicies.ruleNumber', { number: index + 1 })}</legend>
                <div className="endpoint-policy-rule__fields">
                  <Field label={t('common.endpointPolicies.kindLabel', { number: index + 1 })}>
                    {(props) => (
                      <select
                        {...props}
                        value={rule.kind}
                        onChange={(event) =>
                          change(index, { kind: event.target.value as EndpointBackoffRule['kind'] })
                        }
                      >
                        <option value="code">{t('common.endpointPolicies.code')}</option>
                        <option value="message">{t('common.endpointPolicies.message')}</option>
                      </select>
                    )}
                  </Field>
                  <Field label={t('common.endpointPolicies.valueLabel', { number: index + 1 })}>
                    {(props) => (
                      <input
                        {...props}
                        value={rule.value}
                        maxLength={rule.kind === 'code' ? 64 : 320}
                        required
                        onChange={(event) => change(index, { value: event.target.value })}
                      />
                    )}
                  </Field>
                  <Field label={t('common.endpointPolicies.periodLabel', { number: index + 1 })}>
                    {(props) => (
                      <select
                        {...props}
                        value={rule.period}
                        onChange={(event) =>
                          change(index, {
                            period: event.target.value as EndpointBackoffRule['period'],
                          })
                        }
                      >
                        {BACKOFF_PERIODS.map((period) => (
                          <option key={period} value={period}>
                            {t(periodKeys[period])}
                          </option>
                        ))}
                      </select>
                    )}
                  </Field>
                </div>
                <div className="nb-actions endpoint-policy-rule__actions">
                  <Button
                    type="button"
                    disabled={index === 0 || disabled}
                    aria-label={t('common.endpointPolicies.moveUpLabel', { number: index + 1 })}
                    onClick={() => move(index, -1)}
                  >
                    {t('common.endpointPolicies.moveUp')}
                  </Button>
                  <Button
                    type="button"
                    disabled={index === draft.length - 1 || disabled}
                    aria-label={t('common.endpointPolicies.moveDownLabel', { number: index + 1 })}
                    onClick={() => move(index, 1)}
                  >
                    {t('common.endpointPolicies.moveDown')}
                  </Button>
                  <Button
                    type="button"
                    disabled={disabled}
                    aria-label={t('common.endpointPolicies.removeLabel', { number: index + 1 })}
                    onClick={() => {
                      setDraft((previous) => previous.filter((_, position) => position !== index));
                      save.reset();
                    }}
                  >
                    {t('common.operations.charity.remove')}
                  </Button>
                </div>
              </fieldset>
            ))
          ) : (
            <EmptyState
              title={t('common.endpointPolicies.noRules')}
              body={t('common.endpointPolicies.noRulesHelp')}
            />
          )}
          <Button
            type="button"
            disabled={disabled || draft.length >= 8}
            onClick={() => {
              setDraft((previous) => [...previous, { kind: 'code', value: '', period: 'auto' }]);
              save.reset();
            }}
          >
            {t('common.endpointPolicies.addRule')}
          </Button>
          {draft.length >= 8 ? <Note>{t('common.endpointPolicies.limit')}</Note> : null}
          <p className="muted">{t('common.endpointPolicies.example')}</p>
          {!valid ? <Note tone="warn">{t('common.endpointPolicies.invalidRule')}</Note> : null}
          {save.error && save.outcome !== 'conflict' ? <ErrorState error={save.error} /> : null}
          {readError ? <ErrorState error={readError} onRetry={() => void reread()} /> : null}
          <OutcomeNote
            outcome={
              save.outcome === 'conflict'
                ? { kind: 'conflict', reload: () => void reread() }
                : save.outcome === 'unknown'
                  ? { kind: 'unknown' }
                  : save.outcome === 'refresh-failed'
                    ? { kind: 'savedRefreshFailed', recheck: () => void save.refresh() }
                    : save.isSuccess
                      ? { kind: 'saved' }
                      : { kind: 'idle' }
            }
            busy={reading || save.isPending}
          />
          {save.outcome === 'unknown' && save.variables ? (
            <Button type="button" disabled={save.isPending} onClick={() => submit(save.variables!)}>
              {t('common.donationReview.retrySame')}
            </Button>
          ) : null}
          <SaveBar
            dirtyCount={dirty ? 1 : 0}
            busy={disabled}
            saveDisabled={!valid}
            saveLabel={t('common.save')}
            discardLabel={t('common.cancel')}
            dirtyLabel={() => t('common.endpointPolicies.unsaved')}
            onDiscard={() => {
              setDraft(baseline.rules);
              save.reset();
            }}
            onSave={() =>
              submit({
                connector_type: baseline.connector_type,
                base_url: baseline.base_url,
                expected_revision: baseline.revision,
                rules: draft,
              })
            }
          />
        </div>
      </PanelBody>
    </Panel>
  );
}

function LoadedRoutes({ role, accountId, active, onCapabilityLoss }: Props & { active: boolean }) {
  const { t } = useTranslation();
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'loaded-cloudflare-routes',
    scopeKey: accountId,
  });
  const routes = useQuery({
    queryKey: [...endpointPolicyKeys(role), 'routes', accountId],
    queryFn: ({ signal }) => getCloudflareRoutes(role, signal),
    enabled: active,
    retry: false,
  });
  useEffect(() => {
    if (denied(routes.error)) onCapabilityLoss?.();
  }, [routes.error, onCapabilityLoss]);
  const count = routes.data?.routes.length ?? 0;
  const pages = Math.max(1, Math.ceil(count / pager.pageSize));
  const page = Math.min(Number(pager.page), pages);
  if (routes.data && String(page) !== pager.page) pager.setPage(String(page));
  return (
    <div className="nb-form">
      <Note>{t('common.endpointPolicies.routesHelp')}</Note>
      <div className="nb-actions">
        <Button type="button" disabled={routes.isFetching} onClick={() => void routes.refetch()}>
          {t('common.refresh')}
        </Button>
      </div>
      {routes.isPending ? (
        <LoadingState />
      ) : routes.error ? (
        <ErrorState error={routes.error} onRetry={() => void routes.refetch()} />
      ) : routes.data.routes.length ? (
        <>
          <p>{t('common.endpointPolicies.loadedCount', { count: routes.data.routes.length })}</p>
          <DataTable
            caption={t('common.endpointPolicies.loadedRoutes')}
            rows={routes.data.routes.slice((page - 1) * pager.pageSize, page * pager.pageSize)}
            rowKey={(row) => row.source_base_url}
            columns={[
              {
                key: 'source',
                header: t('common.endpointPolicies.sourceURL'),
                mobileLabel: t('common.endpointPolicies.sourceURL'),
                cell: 'title',
                render: (row) => (
                  <span className="mono endpoint-policy-url">{row.source_base_url}</span>
                ),
              },
              {
                key: 'gateway',
                header: t('common.endpointPolicies.gatewayOrigin'),
                mobileLabel: t('common.endpointPolicies.gatewayOrigin'),
                cell: 'meta',
                render: (row) => (
                  <span className="mono endpoint-policy-url">{row.gateway_origin}</span>
                ),
              },
              {
                key: 'credentials',
                header: t('common.endpointPolicies.credentialMode'),
                mobileLabel: t('common.endpointPolicies.credentialMode'),
                cell: 'meta',
                render: (row) =>
                  t(
                    row.credential_query_parameter
                      ? row.retain_credential_header
                        ? 'common.endpointPolicies.headerAndQuery'
                        : 'common.endpointPolicies.queryOnly'
                      : 'common.endpointPolicies.headerOnly',
                    { header: row.credential_header, query: row.credential_query_parameter },
                  ),
              },
            ]}
          />
          <PagePagination
            alwaysShowJump
            metadata={{
              page: String(page),
              page_size: pager.pageSize,
              total_items: String(count),
              total_pages: String(pages),
            }}
            requestedPage={String(page)}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={routes.isFetching}
          />
        </>
      ) : (
        <EmptyState
          title={t('common.endpointPolicies.routesEmpty')}
          body={t('common.endpointPolicies.routesEmptyHelp')}
        />
      )}
    </div>
  );
}
