import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { DataTable, Field, FilterBar, Fold, Note } from '@shared/components/ui';
import { PagePagination } from '@shared/operations/PagePagination';
import type { PageSize } from '@shared/operations/pageNumbers';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import {
  getRequestHeaders,
  getResponseHeaders,
  type DiagnosticRole,
  type HeaderEntry,
  type HeaderSnapshot,
  type ResponseHeaders,
} from '@shared/observability/api';
import { numberedLogKeys } from './numberedQueries';
import './headers.css';

interface HeaderFactsProps {
  role: DiagnosticRole;
  accountID: string;
  requestID: string;
  attempt?: number;
}

const STATE_COPY = {
  not_recorded: 'common.headers.state.not_recorded',
  capacity_exhausted: 'common.headers.state.capacity_exhausted',
  too_large: 'common.headers.state.too_large',
  unavailable: 'common.headers.state.unavailable',
} as const;

export function HeaderFacts(props: HeaderFactsProps) {
  const { t } = useTranslation();
  return (
    <Fold
      key={`${props.role}:${props.accountID}:${props.requestID}:${props.attempt ?? 0}`}
      plain
      lazy
      title={t(props.attempt === undefined ? 'common.headers.request' : 'common.headers.response')}
    >
      <LoadedHeaderFacts {...props} />
    </Fold>
  );
}

function LoadedHeaderFacts({ role, accountID, requestID, attempt }: HeaderFactsProps) {
  const query = useQuery<HeaderSnapshot | ResponseHeaders>({
    queryKey: [...numberedLogKeys.root(role), 'headers', accountID, requestID, attempt ?? 0],
    queryFn: ({ signal }) =>
      attempt === undefined
        ? getRequestHeaders(role, requestID, signal)
        : getResponseHeaders(role, requestID, attempt, signal),
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
  });
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  if (!query.data) return <LoadingState />;
  return <HeaderContent data={query.data} />;
}

function HeaderContent({ data }: { data: HeaderSnapshot | ResponseHeaders }) {
  const { t } = useTranslation();
  const formatDateTime = useDateTimeFormatter();
  const responses = 'responses' in data;
  const snapshots = useMemo(
    () => ('responses' in data ? data.responses : [{ ...data, response_index: 0 }]),
    [data],
  );
  const entries = useMemo(
    () =>
      snapshots.flatMap((snapshot) =>
        snapshot.headers.map((header, index) => ({
          header,
          response: snapshot.response_index,
          key: `${snapshot.response_index}:${index}`,
        })),
      ),
    [snapshots],
  );
  if (data.save_state !== 'saved') return <Note>{t(STATE_COPY[data.save_state])}</Note>;
  return (
    <section
      className="log-headers"
      aria-label={t(responses ? 'common.headers.response' : 'common.headers.request')}
    >
      <Note>
        <p>{t('common.headers.credentials')}</p>
        {snapshots.some((snapshot) => snapshot.ipv4_redacted) ? (
          <p>{t('common.headers.ipv4')}</p>
        ) : null}
      </Note>
      {entries.some(
        ({ header }) => header.name_encoding === 'base64' || header.value_encoding === 'base64',
      ) ? (
        <p>{t('common.headers.binary')}</p>
      ) : null}
      <ol className="log-headers__snapshots">
        {snapshots.map((snapshot) => (
          <li key={snapshot.response_index}>
            {responses ? (
              <h4>{t('common.headers.physicalResponse', { index: snapshot.response_index })}</h4>
            ) : null}
            <dl className="nb-facts nb-facts--inline">
              <div>
                <dt>{t('common.headers.status')}</dt>
                <dd>HTTP {snapshot.http_status ?? '—'}</dd>
              </div>
              <div>
                <dt>{t('common.headers.time')}</dt>
                <dd>
                  {snapshot.captured_at === null ? '—' : formatDateTime(snapshot.captured_at)}
                </dd>
              </div>
            </dl>
            {snapshot.save_state !== 'saved' ? (
              <Note>{t(STATE_COPY[snapshot.save_state])}</Note>
            ) : snapshot.headers.length === 0 ? (
              <p>{t('common.headers.empty')}</p>
            ) : null}
          </li>
        ))}
      </ol>
      <HeaderList entries={entries} responses={responses} />
    </section>
  );
}

function HeaderText({
  value,
  encoding,
}: {
  value: string;
  encoding: HeaderEntry['value_encoding'];
}) {
  const { t } = useTranslation();
  return (
    <div className="log-headers__text mono">
      {encoding === 'base64' ? <span className="status-badge">Base64</span> : null}
      <span>{value === '' ? t('common.headers.emptyValue') : value}</span>
    </div>
  );
}

interface HeaderRow {
  header: HeaderEntry;
  response: number;
  key: string;
}
function HeaderList({ entries, responses }: { entries: HeaderRow[]; responses: boolean }) {
  const { t } = useTranslation();
  const [search, setSearch] = useState('');
  const [page, setPage] = useState('1');
  const [size, setSize] = useState<PageSize>(20);
  const filtered = useMemo(() => {
    const value = search.toLocaleLowerCase();
    return entries.filter(
      ({ header }) =>
        header.name.toLocaleLowerCase().includes(value) ||
        header.value.toLocaleLowerCase().includes(value),
    );
  }, [entries, search]);
  const last = Math.max(1, Math.ceil(filtered.length / size));
  const current = Math.min(Number(page), last);
  const visible = filtered.slice((current - 1) * size, current * size);
  const columns = [
    ...(responses
      ? [
          {
            key: 'response',
            header: t('common.headers.responseColumn'),
            cell: 'meta' as const,
            mobileLabel: t('common.headers.responseColumn'),
            render: (row: HeaderRow) =>
              t('common.headers.physicalResponse', { index: row.response }),
          },
        ]
      : []),
    {
      key: 'name',
      header: t('common.headers.name'),
      cell: 'title' as const,
      mobileLabel: t('common.headers.name'),
      render: (row: HeaderRow) => (
        <HeaderText value={row.header.name} encoding={row.header.name_encoding} />
      ),
    },
    {
      key: 'value',
      header: t('common.headers.value'),
      cell: 'meta' as const,
      mobileLabel: t('common.headers.value'),
      render: (row: HeaderRow) => (
        <HeaderText value={row.header.value} encoding={row.header.value_encoding} />
      ),
    },
  ];
  if (!entries.length) return null;
  return (
    <>
      <FilterBar
        activeCount={search ? 1 : 0}
        onSubmit={() => setPage('1')}
        onReset={() => {
          setSearch('');
          setPage('1');
        }}
        search={
          <Field label={t('common.headers.search')}>
            {(props) => (
              <input
                {...props}
                type="search"
                placeholder={t('common.headers.searchPlaceholder')}
                value={search}
                onChange={(event) => {
                  setSearch(event.target.value);
                  setPage('1');
                }}
              />
            )}
          </Field>
        }
      />
      {visible.length ? (
        <DataTable
          caption={t('common.headers.list')}
          rows={visible}
          rowKey={(row) => row.key}
          columns={columns}
        />
      ) : (
        <EmptyState title={t('common.headers.noMatch')} body={t('common.headers.noMatchBody')} />
      )}
      <PagePagination
        metadata={{
          page: String(current),
          page_size: size,
          total_items: String(filtered.length),
          total_pages: String(last),
        }}
        alwaysShowJump
        onPageChange={setPage}
        onPageSizeChange={(next) => {
          setSize(next);
          setPage('1');
        }}
      />
    </>
  );
}
