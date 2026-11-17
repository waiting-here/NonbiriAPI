import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Trans, useTranslation } from 'react-i18next';
import {
  getModelLimits,
  putModelLimits,
  type ModelLimitPayload,
  type ModelLimitView,
  type ModelMetric,
  type ModelBudgets,
} from '@shared/operations/modelLimits';
import { PagePagination } from '@shared/operations/PagePagination';
import type { PageSize } from '@shared/operations/pageNumbers';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { recurringLimitsKeys } from '@shared/operations/recurringLimits';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { displayQuotaAmount, quotaInput, validQuotaInput } from '@shared/utils/quotaAmounts';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { QuotaAmountField } from './QuotaAmountField';
import { CreditText } from './CreditText';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { RecurringLimits, type RecurringLimitsProps } from './RecurringLimits';
import { useCharityModelScope } from './charityModelScopeContext';
import { Button } from './ui/Button';
import { DataTable } from './ui/DataTable';
import { Field } from './ui/Field';
import { Fields, FormActions } from './ui/Fields';
import { FilterBar } from './ui/FilterBar';
import { Fold } from './ui/Fold';
import { EmptyState, ErrorState, LoadingState } from './States';
import './modelLimits.css';

const primaryMetrics = ['credits', 'calls', 'tokens'] as const;
const splitMetrics = ['input_tokens', 'output_tokens'] as const;
const metrics: readonly ModelMetric[] = [...primaryMetrics, ...splitMetrics];
const labelKeys = {
  credits: 'common.modelLimits.credits',
  calls: 'common.modelLimits.calls',
  tokens: 'common.modelLimits.tokens',
  input_tokens: 'common.modelLimits.input_tokens',
  output_tokens: 'common.modelLimits.output_tokens',
  limits: 'common.modelLimits.limits',
  used: 'common.modelLimits.used',
  reserved: 'common.modelLimits.reserved',
  remaining: 'common.modelLimits.remaining',
} as const;

function Amount({ value, metric }: { value: string | null; metric: ModelMetric }) {
  const { t } = useTranslation();
  if (value === null) return <>{t('common.modelLimits.unlimited')}</>;
  const display = displayQuotaAmount(value, metric);
  return metric === 'credits' ? (
    <CreditText i18nKey="common.modelLimits.creditAmount" amounts={{ amount: display }} />
  ) : (
    <>{display}</>
  );
}

export function ModelLimits(props: RecurringLimitsProps) {
  const { t } = useTranslation();
  const charityModelID = useCharityModelScope();
  const scope = JSON.stringify([
    props.role,
    props.accountId,
    props.donationId,
    props.keyId,
    charityModelID,
  ]);
  return (
    <Fold title={t('common.modelLimits.title')} className="model-limits" plain lazy>
      <ModelLimitList key={scope} {...props} />
    </Fold>
  );
}

function ModelLimitList(props: RecurringLimitsProps) {
  const { t } = useTranslation();
  const charityModelID = useCharityModelScope();
  const [page, setPage] = useState('1');
  const [pageSize, setPageSize] = useState<PageSize>(10);
  const [query, setQuery] = useState('');
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState('');
  const returnButton = useRef<HTMLButtonElement>(null);
  const [opened, setOpened] = useState<ModelLimitView[]>([]);
  const root = [
    props.role === 'admin' ? 'admin' : 'user',
    'operations',
    'model-limits',
    props.role,
    props.accountId,
    charityModelID,
    props.donationId,
    props.keyId,
  ];
  const list = useQuery({
    queryKey: [...root, page, pageSize, query],
    queryFn: ({ signal }) =>
      getModelLimits(
        props.role,
        props.donationId,
        props.keyId,
        page,
        pageSize,
        query,
        signal,
        charityModelID,
      ),
    retry: false,
  });
  useEffect(() => {
    if (!selected) returnButton.current?.focus();
  }, [selected]);
  return (
    <div className="nb-stack">
      <p className="field-help">{t('common.modelLimits.scope')}</p>
      <div hidden={Boolean(selected)}>
        <div className="nb-stack">
          <FilterBar
            query={list}
            search={
              <Field label={t('common.modelLimits.model')}>
                {(field) => (
                  <input
                    {...field}
                    value={search}
                    placeholder={t('common.modelLimits.search')}
                    onChange={(event) => setSearch(event.target.value)}
                  />
                )}
              </Field>
            }
            activeCount={query ? 1 : 0}
            onSubmit={() => {
              setQuery(search);
              setPage('1');
            }}
            onReset={() => {
              setSearch('');
              setQuery('');
              setPage('1');
            }}
            submitLabel={t('common.modelLimits.searchAction')}
          />
          {list.isPending ? (
            <LoadingState />
          ) : list.error ? (
            <ErrorState error={list.error} onRetry={() => void list.refetch()} />
          ) : list.data.data.length === 0 ? (
            <EmptyState
              title={t('common.modelLimits.empty')}
              body={t('common.modelLimits.emptyHelp')}
            />
          ) : (
            <>
              <DataTable
                caption={t('common.modelLimits.title')}
                rows={list.data.data}
                rowKey={(item) => item.upstream_model_id}
                selectedKey={selected}
                columns={[
                  {
                    key: 'model',
                    header: t('common.modelLimits.model'),
                    cell: 'title',
                    render: (item) => (
                      <span className="model-limits__model">{item.upstream_model_id}</span>
                    ),
                  },
                  {
                    key: 'remaining',
                    header: t('common.modelLimits.remaining'),
                    cell: 'meta',
                    render: (item) => {
                      const limited = metrics.filter((metric) => item.limits[metric] != null);
                      return limited.length ? (
                        <div className="nb-stack nb-stack--sm">
                          {limited.map((metric) => (
                            <span key={metric}>
                              {metric === 'credits' ? null : <>{t(labelKeys[metric])} · </>}
                              <Amount value={item.remaining[metric]} metric={metric} />
                            </span>
                          ))}
                        </div>
                      ) : (
                        t('common.modelLimits.unlimited')
                      );
                    },
                  },
                  {
                    key: 'action',
                    header: t('common.modelLimits.actions'),
                    cell: 'action',
                    render: (item) => (
                      <Button
                        size="sm"
                        onClick={(event) => {
                          returnButton.current = event.currentTarget;
                          setSelected(item.upstream_model_id);
                          setOpened((current) =>
                            current.some(
                              (entry) => entry.upstream_model_id === item.upstream_model_id,
                            )
                              ? current
                              : [...current, item],
                          );
                        }}
                      >
                        {t(
                          props.role === 'owner' || props.readOnly
                            ? 'common.modelLimits.view'
                            : 'common.modelLimits.edit',
                        )}
                      </Button>
                    ),
                  },
                ]}
              />
              <PagePagination
                metadata={list.data.pagination}
                requestedPage={page}
                onPageChange={setPage}
                onPageSizeChange={(size) => {
                  setPageSize(size);
                  setPage('1');
                }}
                busy={list.isFetching}
                alwaysShowJump
              />
            </>
          )}
        </div>
      </div>
      {opened.map((item) => (
        <div key={item.upstream_model_id} hidden={selected !== item.upstream_model_id}>
          <ModelLimitEditor
            {...props}
            initial={item}
            active={selected === item.upstream_model_id}
            onBack={() => setSelected('')}
          />
        </div>
      ))}
    </div>
  );
}

function ModelLimitEditor({
  initial,
  active,
  onBack,
  ...props
}: RecurringLimitsProps & { initial: ModelLimitView; active: boolean; onBack: () => void }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const formatDateTime = useDateTimeFormatter();
  const charityModelID = useCharityModelScope();
  const model = initial.upstream_model_id;
  const [draft, setDraft] = useState<{
    revision: string;
    changes: Partial<ModelBudgets>;
    original: ModelBudgets;
  } | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (active) heading.current?.focus();
  }, [active]);
  const root = [
    props.role === 'admin' ? 'admin' : 'user',
    'operations',
    'model-limits',
    props.role,
    props.accountId,
    charityModelID,
    props.donationId,
    props.keyId,
  ];
  const read = useQuery({
    queryKey: [...root, 'model', model],
    queryFn: ({ signal }) =>
      getModelLimits(
        props.role,
        props.donationId,
        props.keyId,
        '1',
        10,
        '',
        signal,
        charityModelID,
        model,
      ),
    enabled: active,
    retry: false,
  });
  const save = useRetainedOperation<ModelLimitPayload, Awaited<ReturnType<typeof putModelLimits>>>(
    (payload, key, context) =>
      putModelLimits(
        props.role,
        props.donationId,
        props.keyId,
        payload,
        key,
        charityModelID,
        context.signal,
      ),
    async (_payload, error, context) => {
      if (error === null) context.commit(() => setDraft(null));
      if (error === null) {
        void client.invalidateQueries({
          queryKey: recurringLimitsKeys.detail(
            props.role,
            props.accountId,
            props.donationId,
            props.keyId,
          ),
        });
      }
      if (!responseOutcomeUnknown(error)) {
        await client.invalidateQueries({ queryKey: root }, { throwOnError: true });
      }
    },
    root,
  );
  const denied = [read.error, save.error].some(
    (error) => isForbidden(error) || isUnauthorized(error),
  );
  const onCapabilityLoss = props.onCapabilityLoss;
  useEffect(() => {
    if (denied) onCapabilityLoss?.();
  }, [denied, onCapabilityLoss]);
  if (denied)
    return (
      <>
        <Button type="button" variant="ghost" onClick={onBack}>
          {t('common.modelLimits.back')}
        </Button>
        <ErrorState error={read.error ?? save.error} />
      </>
    );
  const current = read.data?.data[0];
  const value = current ?? initial;
  const editable = props.role !== 'owner' && !props.readOnly;
  const uncertain = save.outcome === 'unknown';
  const conflict =
    save.outcome === 'conflict' ||
    Boolean(
      draft &&
      read.data &&
      draft.revision !== read.data.donation_revision &&
      metrics.some(
        (metric) => metric in draft.changes && draft.original[metric] !== value.limits[metric],
      ),
    );
  const busy = props.disabled || save.isPending || uncertain;
  const budgets = { ...value.limits, ...draft?.changes };
  const invalid = (metric: ModelMetric) => {
    const amount = budgets[metric];
    return amount != null && !validQuotaInput(amount, metric);
  };
  function submit(payload: ModelLimitPayload) {
    save.mutate(payload, { onSuccess: (receipt) => props.onSaved?.(receipt) });
  }
  function amountField(metric: ModelMetric) {
    const amount = budgets[metric] ?? null;
    return (
      <QuotaAmountField
        key={metric}
        label={t(labelKeys[metric])}
        value={amount}
        inputMode={metric === 'credits' ? 'decimal' : 'numeric'}
        maxLength={128}
        placeholder={t('common.modelLimits.unlimited')}
        disabled={busy || !current}
        error={
          invalid(metric)
            ? t(
                metric === 'credits'
                  ? 'common.modelLimits.invalidCredits'
                  : 'common.modelLimits.invalidCount',
              )
            : undefined
        }
        help={
          <Trans
            i18nKey="common.modelLimits.usage"
            components={{
              used: <Amount metric={metric} value={value.used[metric]} />,
              reserved: <Amount metric={metric} value={value.reserved[metric]} />,
              remaining: <Amount metric={metric} value={value.remaining[metric]} />,
            }}
          />
        }
        onChange={(text) => {
          if (!read.data) return;
          const revision = read.data.donation_revision;
          setDraft((previous) => ({
            revision: previous?.revision ?? revision,
            original: previous?.original ?? value.limits,
            changes: { ...previous?.changes, [metric]: text || null },
          }));
          if (!conflict) save.reset();
        }}
      />
    );
  }

  return (
    <section className="nb-stack model-limits__editor" aria-label={model}>
      <Button variant="ghost" type="button" onClick={onBack}>
        {t('common.modelLimits.back')}
      </Button>
      <h3 ref={heading} tabIndex={-1} className="model-limits__model">
        {model}
      </h3>
      {read.error ? <ErrorState error={read.error} onRetry={() => void read.refetch()} /> : null}
      {read.data && !current ? (
        <EmptyState
          title={t('common.modelLimits.empty')}
          body={t('common.modelLimits.emptyHelp')}
        />
      ) : (
        <>
          {editable ? (
            <form
              className="nb-stack"
              onSubmit={(event) => {
                event.preventDefault();
                if (!read.data || !current || !draft || busy || conflict || metrics.some(invalid))
                  return;
                const payload: ModelLimitPayload = {
                  ...current.limits,
                  upstream_model_id: model,
                  expected_revision: read.data.donation_revision,
                };
                for (const metric of metrics) {
                  const input = draft.changes[metric];
                  if (input !== undefined)
                    payload[metric] = input === null ? null : quotaInput(input, metric);
                }
                submit(payload);
              }}
            >
              <Fields
                legend={t('common.modelLimits.cumulative')}
                hint={t('common.modelLimits.limitHelp')}
              >
                {primaryMetrics.map(amountField)}
              </Fields>
              <Fold
                title={t('common.modelLimits.split')}
                plain
                defaultOpen={splitMetrics.some((metric) => value.limits[metric] !== null)}
              >
                <p className="field-help">{t('common.modelLimits.splitHelp')}</p>
                <Fields columns={2}>{splitMetrics.map(amountField)}</Fields>
              </Fold>
              <FormActions>
                <Button
                  type="submit"
                  variant="primary"
                  disabled={busy || !current || !draft || conflict || metrics.some(invalid)}
                >
                  {t('common.modelLimits.save')}
                </Button>
              </FormActions>
              {save.error ? <ErrorState error={save.error} /> : null}
              {save.refreshError ? (
                <ErrorState error={save.refreshError} onRetry={() => void save.refresh()} />
              ) : null}
              {conflict && draft ? (
                <div className="nb-stack nb-stack--sm" role="status">
                  <p>{t('common.modelLimits.conflict')}</p>
                  {metrics
                    .filter((metric) => metric in draft.changes)
                    .map((metric) => (
                      <p key={metric}>
                        {t('common.modelLimits.currentLimit', {
                          metric: t(labelKeys[metric]),
                          limit:
                            value.limits[metric] === null
                              ? t('common.modelLimits.unlimited')
                              : displayQuotaAmount(value.limits[metric], metric),
                        })}
                      </p>
                    ))}
                  <Button
                    type="button"
                    disabled={read.isFetching}
                    onClick={async () => {
                      const latest = await read.refetch();
                      if (!latest.isSuccess || !latest.data.data[0]) return;
                      const revision = latest.data.donation_revision;
                      const original = latest.data.data[0].limits;
                      setDraft((previous) => previous && { ...previous, revision, original });
                      save.reset();
                    }}
                  >
                    {t('common.modelLimits.reload')}
                  </Button>
                </div>
              ) : null}
              {uncertain ? (
                <div role="status" className="nb-stack nb-stack--sm">
                  <p>{t('common.modelLimits.unknown')}</p>
                  <Button
                    type="button"
                    disabled={save.isPending}
                    onClick={() => {
                      if (save.variables) submit(save.variables);
                    }}
                  >
                    {t('common.modelLimits.retry')}
                  </Button>
                </div>
              ) : null}
              {save.isSuccess ? <p role="status">{t('common.modelLimits.saved')}</p> : null}
            </form>
          ) : (
            <>
              <h4>{t('common.modelLimits.cumulative')}</h4>
              <DataTable
                caption={t('common.modelLimits.cumulative')}
                rows={metrics}
                rowKey={(metric) => metric}
                columns={[
                  {
                    key: 'metric',
                    header: t('common.modelLimits.metric'),
                    cell: 'title',
                    render: (metric) => t(labelKeys[metric]),
                  },
                  ...(['limits', 'used', 'reserved', 'remaining'] as const).map((key) => ({
                    key,
                    header: t(labelKeys[key]),
                    cell: 'meta' as const,
                    mobileLabel: t(labelKeys[key]),
                    render: (metric: ModelMetric) => (
                      <Amount metric={metric} value={value[key][metric]} />
                    ),
                  })),
                ]}
              />
            </>
          )}
          <div className="nb-stack nb-stack--sm field-help">
            <p>{t('common.modelLimits.creditsHelp')}</p>
            <p>
              {value.tracking_started_at === null
                ? t('common.modelLimits.trackingNew')
                : t('common.modelLimits.trackingSince', {
                    time: formatDateTime(value.tracking_started_at),
                  })}
            </p>
            {value.historical_costs_partial ? <p>{t('common.modelLimits.partialCosts')}</p> : null}
            {value.unattributed_total_tokens !== '0' ? (
              <p>
                {t('common.modelLimits.unattributed', {
                  tokens: displayQuotaAmount(value.unattributed_total_tokens, 'tokens'),
                })}
              </p>
            ) : null}
          </div>
          <Fold
            title={t('common.modelLimits.recurring')}
            summary={t('common.modelLimits.recurringHelp')}
            plain
            lazy
          >
            <RecurringLimits
              {...props}
              upstreamModelID={model}
              onSaved={(receipt) => {
                void client.invalidateQueries({ queryKey: root });
                props.onSaved?.(receipt);
              }}
            />
          </Fold>
        </>
      )}
    </section>
  );
}
