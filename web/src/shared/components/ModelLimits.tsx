import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  getModelLimits,
  putModelLimits,
  type ModelLimitPayload,
  type ModelLimitView,
  type ModelMetric,
} from '@shared/operations/modelLimits';
import { PagePagination } from '@shared/operations/PagePagination';
import type { PageSize } from '@shared/operations/pageNumbers';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { formatCount } from '@shared/utils/formatNumber';
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

const metrics: { key: ModelMetric; labelKey: string }[] = [
  { key: 'tokens', labelKey: 'common.modelLimits.tokens' },
  { key: 'input_tokens', labelKey: 'common.modelLimits.input_tokens' },
  { key: 'output_tokens', labelKey: 'common.modelLimits.output_tokens' },
];

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
    <Fold title={t('common.modelLimits.title')} lazy>
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
  const count = (value: string | null) =>
    value === null ? t('common.modelLimits.unlimited') : formatCount(value).display;
  return (
    <div className="nb-stack">
      <p className="field-help">{t('common.modelLimits.scope')}</p>
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
                render: (item) => (
                  <div className="nb-stack nb-stack--sm">
                    {metrics.map(({ key: metric, labelKey }) => (
                      <span key={metric}>
                        {t(labelKey)}: {count(item.remaining[metric])}
                      </span>
                    ))}
                  </div>
                ),
              },
              {
                key: 'action',
                header: t('common.modelLimits.actions'),
                cell: 'action',
                render: (item) => (
                  <Button
                    size="sm"
                    onClick={() => {
                      setSelected(item.upstream_model_id);
                      setOpened((current) =>
                        current.some((entry) => entry.upstream_model_id === item.upstream_model_id)
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
  const [draft, setDraft] = useState<ModelLimitPayload | null>(null);
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
    (payload, key) =>
      putModelLimits(props.role, props.donationId, props.keyId, payload, key, charityModelID),
    async (_payload, error) => {
      if (error === null) setDraft(null);
      if (!responseOutcomeUnknown(error)) await client.invalidateQueries({ queryKey: root });
    },
    root,
  );
  const value = read.data?.data[0] ?? initial;
  const editable = props.role !== 'owner' && !props.readOnly;
  const uncertain = save.outcome === 'unknown';
  const busy = props.disabled || save.isPending || uncertain;
  const count = (amount: string | null) =>
    amount === null ? t('common.modelLimits.unlimited') : formatCount(amount).display;
  return (
    <section className="nb-stack" aria-label={model}>
      <Button variant="ghost" onClick={onBack}>
        {t('common.modelLimits.back')}
      </Button>
      <h3 className="model-limits__model">{model}</h3>
      <p className="field-help">
        {value.tracking_started_at === null
          ? t('common.modelLimits.trackingNew')
          : t('common.modelLimits.trackingSince', {
              time: formatDateTime(value.tracking_started_at),
            })}
      </p>
      {value.unattributed_total_tokens !== '0' ? (
        <p className="field-help">
          {t('common.modelLimits.unattributed', {
            tokens: count(value.unattributed_total_tokens),
          })}
        </p>
      ) : null}
      {read.error ? <ErrorState error={read.error} onRetry={() => void read.refetch()} /> : null}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (!read.data) return;
          save.mutate(
            draft ?? {
              upstream_model_id: model,
              expected_revision: read.data.donation_revision,
              ...value.limits,
            },
            {
              onSuccess: (receipt) => {
                setDraft(null);
                props.onSaved?.(receipt);
              },
            },
          );
        }}
      >
        <Fields columns={1} legend={t('common.modelLimits.cumulative')}>
          {metrics.map(({ key: metric, labelKey }) => (
            <Field
              key={metric}
              label={t(labelKey)}
              help={t('common.modelLimits.usage', {
                used: count(value.used[metric]),
                reserved: count(value.reserved[metric]),
                remaining: count(value.remaining[metric]),
              })}
            >
              {(field) => (
                <input
                  {...field}
                  inputMode="numeric"
                  value={(draft ? draft[metric] : value.limits[metric]) ?? ''}
                  disabled={!editable || busy || !read.data}
                  placeholder={t('common.modelLimits.unlimited')}
                  onChange={(event) => {
                    const text = event.target.value;
                    if (!read.data) return;
                    const revision = read.data.donation_revision;
                    setDraft((current) => ({
                      ...(current ?? {
                        upstream_model_id: model,
                        expected_revision: revision,
                        ...value.limits,
                      }),
                      [metric]: text || null,
                    }));
                    save.reset();
                  }}
                />
              )}
            </Field>
          ))}
        </Fields>
        {editable ? (
          <>
            <p className="field-help">{t('common.modelLimits.limitHelp')}</p>
            <FormActions>
              <Button type="submit" variant="primary" disabled={busy || !read.data}>
                {t('common.modelLimits.save')}
              </Button>
            </FormActions>
          </>
        ) : null}
        {save.error ? <ErrorState error={save.error} /> : null}
        {save.outcome === 'conflict' ||
        (draft && read.data && draft.expected_revision !== read.data.donation_revision) ? (
          <Button
            onClick={() => {
              setDraft(null);
              save.reset();
              void read.refetch();
            }}
          >
            {t('common.modelLimits.reload')}
          </Button>
        ) : null}
        {uncertain ? (
          <Button onClick={() => void save.check()}>{t('common.modelLimits.retry')}</Button>
        ) : null}
        {save.isSuccess ? <p role="status">{t('common.modelLimits.saved')}</p> : null}
      </form>
      <Fold title={t('common.modelLimits.recurring')} lazy>
        <RecurringLimits
          {...props}
          upstreamModelID={model}
          onSaved={(receipt) => {
            void read.refetch();
            void client.invalidateQueries({ queryKey: root });
            props.onSaved?.(receipt);
          }}
        />
      </Fold>
    </section>
  );
}
