import { Fragment, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Button,
  DataTable,
  Field,
  FilterBar,
  Note,
  Panel,
  PanelBody,
  PanelHead,
} from '@shared/components/ui';
import { EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { formatCount } from '@shared/utils/formatNumber';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { listPoolCardStatistics, listPoolStatistics, poolStatisticsExportURL } from './api';
import { GachaStatisticsChart } from './GachaStatisticsChart';
import type { CardSort, PoolSort, PoolStatistics } from './types';
import './gacha-statistics.css';

export function GachaPoolStatistics({ accountKey }: { accountKey: string }) {
  const { t, i18n } = useTranslation();
  const dateTime = useDateTimeFormatter();
  const [params, setParams] = useSearchState();
  const detailRef = useRef<HTMLDivElement>(null);
  const poolQuery = params.get('stats_pool_q') ?? '';
  const cardQuery = params.get('stats_card_q') ?? '';
  const poolSort: PoolSort = params.get('stats_pool_sort') === 'draws' ? 'draws' : 'recent';
  const rawCardSort = params.get('stats_card_sort');
  const cardSort: CardSort =
    rawCardSort === 'rarity' || rawCardSort === 'name' ? rawCardSort : 'count';
  const poolPager = useUrlPagePager({
    station: 'admin',
    listType: 'gacha-pool-statistics',
    scopeKey: accountKey,
    resetKey: poolQuery + ':' + poolSort,
    pageParam: 'stats_pools_page',
    pageSizeParam: 'stats_pools_size',
  });
  const pools = useQuery({
    queryKey: [
      'admin',
      'gacha',
      'statistics',
      accountKey,
      'pools',
      poolQuery,
      poolSort,
      poolPager.page,
      poolPager.pageSize,
    ],
    queryFn: ({ signal }) =>
      listPoolStatistics(
        { page: poolPager.page, pageSize: poolPager.pageSize },
        poolQuery,
        poolSort,
        signal,
      ),
    enabled: Boolean(accountKey),
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === accountKey &&
      previousQuery.queryKey[5] === poolQuery &&
      previousQuery.queryKey[6] === poolSort
        ? previous
        : undefined,
  });
  const selectedId = params.get('stats_pool') ?? pools.data?.items[0]?.id;
  const cardPager = useUrlPagePager({
    station: 'admin',
    listType: 'gacha-pool-card-statistics',
    scopeKey: accountKey + ':' + (selectedId ?? ''),
    scopeReady: Boolean(selectedId),
    resetKey: cardQuery + ':' + cardSort,
    pageParam: 'stats_cards_page',
    pageSizeParam: 'stats_cards_size',
  });
  const cards = useQuery({
    queryKey: [
      'admin',
      'gacha',
      'statistics',
      accountKey,
      'cards',
      selectedId,
      cardQuery,
      cardSort,
      cardPager.page,
      cardPager.pageSize,
    ],
    queryFn: ({ signal }) =>
      listPoolCardStatistics(
        selectedId!,
        { page: cardPager.page, pageSize: cardPager.pageSize },
        cardQuery,
        cardSort,
        signal,
      ),
    enabled: Boolean(accountKey && selectedId),
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === accountKey &&
      previousQuery.queryKey[5] === selectedId &&
      previousQuery.queryKey[6] === cardQuery &&
      previousQuery.queryKey[7] === cardSort
        ? previous
        : undefined,
  });
  const pool = cards.data?.pool;
  const name = (row: { name: string; name_en: string }, fallback: string) =>
    (i18n.language.startsWith('en') ? row.name_en || row.name : row.name || row.name_en) ||
    fallback;
  const poolName = (row: PoolStatistics) => name(row, t('admin.gachaStats.unnamedPool'));
  const period = (row: PoolStatistics) =>
    row.kind === 'permanent'
      ? t('admin.gachaStats.permanent')
      : (row.starts_at ? dateTime(row.starts_at) : '—') +
        ' → ' +
        (row.ends_at ? dateTime(row.ends_at) : '—');
  const change = (key: string, value: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (value) next.set(key, value);
      else next.delete(key);
      return next;
    });
  const rarityRows = useMemo(
    () =>
      (cards.data?.rarity_totals ?? []).map((row) => ({
        label: row.rarity ?? t('admin.gachaStats.unspecifiedRarity'),
        count: row.count,
        details: [
          t('admin.gachaStats.firstAcquisitions') +
            ': ' +
            formatCount(row.first_acquisition_count).display,
        ],
      })),
    [cards.data?.rarity_totals, t],
  );
  const topRows = useMemo(
    () =>
      (cards.data?.top_cards ?? []).map((row) => ({
        label:
          (i18n.language.startsWith('en') ? row.name_en || row.name : row.name || row.name_en) ||
          row.card_id,
        count: row.count,
        details: [
          t('admin.gachaStats.cardParticipants') +
            ': ' +
            formatCount(row.participant_count).display,
          t('admin.gachaStats.firstOwners') +
            ': ' +
            formatCount(row.first_acquisition_count).display,
        ],
      })),
    [cards.data?.top_cards, i18n.language, t],
  );
  const firstAcquisitions = (cards.data?.rarity_totals ?? []).reduce(
    (sum, row) => sum + BigInt(row.first_acquisition_count),
    0n,
  );
  return (
    <div
      className="gacha-pool-statistics nb-stack"
      aria-busy={pools.isFetching || cards.isFetching}
    >
      <Panel aria-label={t('admin.gachaStats.poolsTitle')}>
        <PanelHead
          title={t('admin.gachaStats.poolsTitle')}
          description={t('admin.gachaStats.poolsDescription')}
          actions={
            <Button
              onClick={() => {
                void pools.refetch();
                if (selectedId) void cards.refetch();
              }}
              disabled={pools.isFetching || cards.isFetching}
            >
              {t('admin.gachaStats.refresh')}
            </Button>
          }
        />
        <PanelBody>
          <FilterBar
            ariaLabel={t('admin.gachaStats.poolFilters')}
            activeCount={poolQuery || poolSort !== 'recent' ? 1 : 0}
            secondaryLabel={t('admin.gachaStats.sort')}
            secondaryAlwaysVisible
            onSubmit={() => undefined}
            search={
              <SearchField
                key={poolQuery}
                label={t('admin.gachaStats.searchPools')}
                placeholder={t('admin.gachaStats.searchPoolsPlaceholder')}
                value={poolQuery}
                onSearch={(value) => change('stats_pool_q', value)}
              />
            }
            secondary={
              <Field label={t('admin.gachaStats.poolOrder')}>
                {(props) => (
                  <select
                    {...props}
                    value={poolSort}
                    onChange={(event) => change('stats_pool_sort', event.target.value)}
                  >
                    <option value="recent">{t('admin.gachaStats.recentFirst')}</option>
                    <option value="draws">{t('admin.gachaStats.mostDraws')}</option>
                  </select>
                )}
              </Field>
            }
          />
          {pools.error ? (
            <ErrorState error={pools.error} onRetry={() => void pools.refetch()} />
          ) : null}
          {pools.isPending ? (
            <LoadingState />
          ) : pools.data ? (
            <>
              {pools.data.items.length ? (
                <DataTable
                  caption={t('admin.gachaStats.poolsTitle')}
                  rows={pools.data.items}
                  rowKey={(row) => row.id}
                  selectedKey={selectedId}
                  columns={[
                    {
                      key: 'name',
                      header: t('admin.gachaStats.pool'),
                      cell: 'title',
                      render: (row) => (
                        <div className="nb-stack nb-stack--sm">
                          <strong className="gacha-stats-name" title={poolName(row)}>
                            {poolName(row)}
                          </strong>
                          <small className="nb-muted mono gacha-stats-name">{row.id}</small>
                          {!row.available ? (
                            <span className="nb-badge">
                              {t('admin.gachaStats.configurationRemoved')}
                            </span>
                          ) : null}
                        </div>
                      ),
                    },
                    {
                      key: 'period',
                      header: t('admin.gachaStats.period'),
                      cell: 'meta',
                      mobileLabel: t('admin.gachaStats.period'),
                      render: (row) => <span className="gacha-stats-period">{period(row)}</span>,
                    },
                    {
                      key: 'draws',
                      header: t('admin.gachaStats.totalDraws'),
                      cell: 'meta',
                      mobileLabel: t('admin.gachaStats.totalDraws'),
                      align: 'num',
                      render: (row) => <Count value={row.total_draws} />,
                    },
                    {
                      key: 'participants',
                      header: t('admin.gachaStats.participants'),
                      cell: 'meta',
                      mobileLabel: t('admin.gachaStats.participants'),
                      align: 'num',
                      render: (row) => <Count value={row.participant_count} />,
                    },
                    {
                      key: 'select',
                      header: t('admin.gachaStats.actions'),
                      cell: 'action',
                      render: (row) => (
                        <Button
                          aria-pressed={row.id === selectedId}
                          onClick={() => {
                            requestAnimationFrame(() =>
                              detailRef.current?.scrollIntoView?.({ block: 'start' }),
                            );
                            setParams((previous) => {
                              const next = new URLSearchParams(previous);
                              next.set('stats_pool', row.id);
                              next.delete('stats_cards_page');
                              return next;
                            });
                          }}
                        >
                          {row.id === selectedId
                            ? t('admin.gachaStats.selected')
                            : t('admin.gachaStats.viewCards')}
                        </Button>
                      ),
                    },
                  ]}
                />
              ) : (
                <EmptyState
                  title={t('admin.gachaStats.noPoolsTitle')}
                  body={t(
                    poolQuery ? 'admin.gachaStats.noMatches' : 'admin.gachaStats.noPoolsBody',
                  )}
                />
              )}
              <PagePagination
                metadata={pools.data.pagination}
                requestedPage={poolPager.page}
                busy={pools.isFetching}
                onPageChange={poolPager.setPage}
                onPageSizeChange={poolPager.setPageSize}
              />
            </>
          ) : null}
        </PanelBody>
      </Panel>
      {selectedId ? (
        <div ref={detailRef} className="gacha-stats-detail">
          <Panel aria-label={t('admin.gachaStats.selectedPoolStatistics')}>
            <PanelHead
              title={pool ? poolName(pool) : t('admin.gachaStats.cardsTitle')}
              description={pool ? period(pool) : undefined}
              actions={
                pool ? (
                  <div className="nb-actions">
                    <a
                      className="nb-btn nb-btn--secondary"
                      href={poolStatisticsExportURL(pool.id, 'csv')}
                      download
                    >
                      {t('admin.gachaStats.downloadCSV')}
                    </a>
                    <a
                      className="nb-btn nb-btn--secondary"
                      href={poolStatisticsExportURL(pool.id, 'json')}
                      download
                    >
                      {t('admin.gachaStats.downloadJSON')}
                    </a>
                  </div>
                ) : undefined
              }
            />
            <PanelBody>
              {cards.error ? (
                <ErrorState error={cards.error} onRetry={() => void cards.refetch()} />
              ) : null}
              {cards.isPending ? (
                <LoadingState />
              ) : cards.data ? (
                <>
                  {!cards.data.pool.available ? (
                    <p className="nb-small nb-muted">
                      {t('admin.gachaStats.configurationRemoved')}
                    </p>
                  ) : null}
                  <dl className="gacha-stats-summary">
                    <div>
                      <dt>{t('admin.gachaStats.totalDraws')}</dt>
                      <dd>
                        <Count value={cards.data.pool.total_draws} />
                      </dd>
                    </div>
                    <div>
                      <dt>{t('admin.gachaStats.participants')}</dt>
                      <dd>
                        <Count value={cards.data.pool.participant_count} />
                      </dd>
                    </div>
                    <div>
                      <dt>{t('admin.gachaStats.firstAcquisitions')}</dt>
                      <dd>
                        <Count value={firstAcquisitions.toString()} />
                      </dd>
                    </div>
                  </dl>
                  <Note>{t('admin.gachaStats.metricHelp')}</Note>
                  {cards.data.pool.total_draws === '0' ? (
                    <p className="nb-small nb-muted">{t('admin.gachaStats.noDraws')}</p>
                  ) : null}
                  <p className="nb-small nb-muted">{t('admin.gachaStats.fullPoolData')}</p>
                  <div className="gacha-stats-charts">
                    <GachaStatisticsChart
                      title={t('admin.gachaStats.rarityChart')}
                      poolName={poolName(cards.data.pool)}
                      metric={t('admin.gachaStats.acquisitions')}
                      rows={rarityRows}
                      filename={'gacha-' + encodeURIComponent(selectedId) + '-rarities'}
                    />
                    <GachaStatisticsChart
                      title={t('admin.gachaStats.topCardsChart')}
                      poolName={poolName(cards.data.pool)}
                      metric={t('admin.gachaStats.acquisitions')}
                      rows={topRows}
                      filename={'gacha-' + encodeURIComponent(selectedId) + '-cards'}
                    />
                  </div>
                  <h3>{t('admin.gachaStats.cardsTitle')}</h3>
                  <FilterBar
                    ariaLabel={t('admin.gachaStats.cardFilters')}
                    activeCount={cardQuery || cardSort !== 'count' ? 1 : 0}
                    secondaryLabel={t('admin.gachaStats.sort')}
                    secondaryAlwaysVisible
                    onSubmit={() => undefined}
                    search={
                      <SearchField
                        key={selectedId + ':' + cardQuery}
                        label={t('admin.gachaStats.searchCards')}
                        placeholder={t('admin.gachaStats.searchCardsPlaceholder')}
                        value={cardQuery}
                        onSearch={(value) => change('stats_card_q', value)}
                      />
                    }
                    secondary={
                      <Field label={t('admin.gachaStats.cardOrder')}>
                        {(props) => (
                          <select
                            {...props}
                            value={cardSort}
                            onChange={(event) => change('stats_card_sort', event.target.value)}
                          >
                            <option value="count">{t('admin.gachaStats.mostAcquired')}</option>
                            <option value="rarity">{t('admin.gachaStats.rarityOrder')}</option>
                            <option value="name">{t('admin.gachaStats.nameOrder')}</option>
                          </select>
                        )}
                      </Field>
                    }
                  />
                  {cards.data.items.length ? (
                    <DataTable
                      caption={t('admin.gachaStats.cardsTitle')}
                      rows={cards.data.items}
                      rowKey={(row) => row.card_id}
                      columns={[
                        {
                          key: 'name',
                          header: t('admin.gachaStats.card'),
                          cell: 'title',
                          render: (row) => (
                            <div className="nb-stack nb-stack--sm">
                              <strong className="gacha-stats-name" title={name(row, row.card_id)}>
                                {name(row, row.card_id)}
                              </strong>
                              <small className="nb-muted mono gacha-stats-name">
                                {row.card_id}
                              </small>
                            </div>
                          ),
                        },
                        {
                          key: 'rarity',
                          header: t('admin.gachaStats.rarity'),
                          cell: 'status',
                          render: (row) => (
                            <span className="nb-badge">
                              {row.rarity ?? t('admin.gachaStats.unspecifiedRarity')}
                            </span>
                          ),
                        },
                        {
                          key: 'count',
                          header: t('admin.gachaStats.acquisitions'),
                          cell: 'meta',
                          mobileLabel: t('admin.gachaStats.acquisitions'),
                          align: 'num',
                          render: (row) => <Count value={row.count} />,
                        },
                        {
                          key: 'participants',
                          header: t('admin.gachaStats.cardParticipants'),
                          cell: 'meta',
                          mobileLabel: t('admin.gachaStats.cardParticipants'),
                          align: 'num',
                          render: (row) => <Count value={row.participant_count} />,
                        },
                        {
                          key: 'first',
                          header: t('admin.gachaStats.firstOwners'),
                          cell: 'meta',
                          mobileLabel: t('admin.gachaStats.firstOwners'),
                          align: 'num',
                          render: (row) => <Count value={row.first_acquisition_count} />,
                        },
                      ]}
                    />
                  ) : (
                    <EmptyState
                      title={t('admin.gachaStats.noCardsTitle')}
                      body={t(
                        cardQuery ? 'admin.gachaStats.noMatches' : 'admin.gachaStats.noCardsBody',
                      )}
                    />
                  )}
                  <PagePagination
                    metadata={cards.data.pagination}
                    requestedPage={cardPager.page}
                    busy={cards.isFetching}
                    onPageChange={cardPager.setPage}
                    onPageSizeChange={cardPager.setPageSize}
                  />
                </>
              ) : null}
            </PanelBody>
          </Panel>
        </div>
      ) : null}
    </div>
  );
}
function Count({ value }: { value: string }) {
  const exact = formatCount(value).display;
  const groups = exact.split(',');
  return (
    <span className="gacha-stats-count" aria-label={exact} title={exact}>
      {groups.map((group, index) => (
        <Fragment key={index}>
          <span className="gacha-stats-count-group">
            {group}
            {index < groups.length - 1 ? ',' : ''}
          </span>
          {index < groups.length - 1 ? <wbr /> : null}
        </Fragment>
      ))}
    </span>
  );
}
function SearchField({
  label,
  placeholder,
  value,
  onSearch,
}: {
  label: string;
  placeholder: string;
  value: string;
  onSearch: (value: string) => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(value);
  return (
    <>
      <Field label={label}>
        {(props) => (
          <input
            {...props}
            type="search"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder={placeholder}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                onSearch(draft.trim());
              }
            }}
          />
        )}
      </Field>
      <Button type="button" onClick={() => onSearch(draft.trim())}>
        {t('admin.gachaStats.search')}
      </Button>
    </>
  );
}
