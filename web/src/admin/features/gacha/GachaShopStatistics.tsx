import { useMemo, useState } from 'react';
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
import { listShopCardStatistics, shopStatisticsExportURL } from './api';
import { GachaStatisticsChart } from './GachaStatisticsChart';
import { StatisticsCount as Count } from './StatisticsControls';
import type { CardSort, CardStatistics } from './types';
import './gacha-statistics.css';

export function GachaShopStatistics({ accountKey }: { accountKey: string }) {
  const { t, i18n } = useTranslation();
  const [params, setParams] = useSearchState();
  const q = params.get('stats_shop_q') ?? '';
  const [draft, setDraft] = useState(q);
  const [seeded, setSeeded] = useState(q);
  if (seeded !== q) {
    setSeeded(q);
    setDraft(q);
  }
  const rawSort = params.get('stats_shop_sort');
  const sort: CardSort = rawSort === 'rarity' || rawSort === 'name' ? rawSort : 'count';
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'gacha-shop-card-statistics',
    scopeKey: accountKey,
    resetKey: q + ':' + sort,
    pageParam: 'stats_shop_page',
    pageSizeParam: 'stats_shop_size',
  });
  const query = useQuery({
    queryKey: [
      'admin',
      'gacha',
      'statistics',
      accountKey,
      'shop',
      q,
      sort,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      listShopCardStatistics({ page: pager.page, pageSize: pager.pageSize }, q, sort, signal),
    enabled: Boolean(accountKey),
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === accountKey &&
      previousQuery.queryKey[5] === q &&
      previousQuery.queryKey[6] === sort
        ? previous
        : undefined,
  });
  const change = (key: string, value: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (value) next.set(key, value);
      else next.delete(key);
      return next;
    });
  const cardName = (row: CardStatistics) =>
    (i18n.language.startsWith('en') ? row.name_en || row.name : row.name || row.name_en) ||
    row.card_id;
  const rarityRows = useMemo(
    () =>
      (query.data?.rarity_totals ?? []).map((row) => ({
        label: row.rarity ?? t('admin.gachaStats.unspecifiedRarity'),
        count: row.count,
      })),
    [query.data?.rarity_totals, t],
  );
  const topRows = useMemo(
    () =>
      (query.data?.top_cards ?? []).map((row) => ({
        label:
          (i18n.language.startsWith('en') ? row.name_en || row.name : row.name || row.name_en) ||
          row.card_id,
        count: row.count,
        details: [
          t('admin.gachaShopStats.participants') +
            ': ' +
            formatCount(row.participant_count).display,
        ],
      })),
    [query.data?.top_cards, i18n.language, t],
  );
  return (
    <div className="gacha-shop-statistics nb-stack" aria-busy={query.isFetching}>
      <Panel aria-label={t('admin.gachaShopStats.title')}>
        <PanelHead
          title={t('admin.gachaShopStats.title')}
          description={t('admin.gachaShopStats.description')}
          actions={
            <div className="nb-actions">
              <Button onClick={() => void query.refetch()} disabled={query.isFetching}>
                {t('admin.gachaStats.refresh')}
              </Button>
              <a
                className="nb-btn nb-btn--secondary"
                href={shopStatisticsExportURL('csv')}
                download
              >
                {t('admin.gachaStats.downloadCSV')}
              </a>
              <a
                className="nb-btn nb-btn--secondary"
                href={shopStatisticsExportURL('json')}
                download
              >
                {t('admin.gachaStats.downloadJSON')}
              </a>
            </div>
          }
        />
        <PanelBody>
          {query.error ? (
            <ErrorState error={query.error} onRetry={() => void query.refetch()} />
          ) : null}
          {query.isPending ? (
            <LoadingState />
          ) : query.data ? (
            <>
              <dl className="gacha-stats-summary gacha-stats-summary--pair">
                <div>
                  <dt>{t('admin.gachaShopStats.totalRedemptions')}</dt>
                  <dd>
                    <Count value={query.data.summary.total_redemptions} />
                  </dd>
                </div>
                <div>
                  <dt>{t('admin.gachaShopStats.participants')}</dt>
                  <dd>
                    <Count value={query.data.summary.participant_count} />
                  </dd>
                </div>
              </dl>
              <Note>{t('admin.gachaShopStats.metricHelp')}</Note>
              {query.data.summary.total_redemptions === '0' ? (
                <p className="nb-small nb-muted">{t('admin.gachaShopStats.noRedemptions')}</p>
              ) : null}
              <p className="nb-small nb-muted">{t('admin.gachaShopStats.fullShopData')}</p>
              <div className="gacha-stats-charts">
                <GachaStatisticsChart
                  title={t('admin.gachaShopStats.rarityChart')}
                  poolName={t('admin.gachaShopStats.title')}
                  metric={t('admin.gachaShopStats.redemptions')}
                  rows={rarityRows}
                  filename="gacha-shop-rarities"
                />
                <GachaStatisticsChart
                  title={t('admin.gachaShopStats.topCardsChart')}
                  poolName={t('admin.gachaShopStats.title')}
                  metric={t('admin.gachaShopStats.redemptions')}
                  rows={topRows}
                  filename="gacha-shop-cards"
                />
              </div>
              <h3>{t('admin.gachaShopStats.cardsTitle')}</h3>
              <FilterBar
                ariaLabel={t('admin.gachaStats.cardFilters')}
                activeCount={q || sort !== 'count' ? 1 : 0}
                secondaryAlwaysVisible
                onSubmit={() => change('stats_shop_q', draft.trim())}
                onReset={() => {
                  change('stats_shop_q', '');
                  change('stats_shop_sort', '');
                }}
                search={
                  <Field label={t('admin.gachaStats.searchCards')}>
                    {(props) => (
                      <input
                        {...props}
                        type="search"
                        placeholder={t('admin.gachaStats.searchCardsPlaceholder')}
                        value={draft}
                        onChange={(event) => setDraft(event.target.value)}
                      />
                    )}
                  </Field>
                }
                secondary={
                  <Field label={t('admin.gachaStats.cardOrder')}>
                    {(props) => (
                      <select
                        {...props}
                        value={sort}
                        onChange={(event) => change('stats_shop_sort', event.target.value)}
                      >
                        <option value="count">{t('admin.gachaShopStats.mostRedeemed')}</option>
                        <option value="rarity">{t('admin.gachaStats.rarityOrder')}</option>
                        <option value="name">{t('admin.gachaStats.nameOrder')}</option>
                      </select>
                    )}
                  </Field>
                }
              />
              {query.data.items.length ? (
                <DataTable
                  caption={t('admin.gachaShopStats.cardsTitle')}
                  rows={query.data.items}
                  rowKey={(row) => row.card_id}
                  columns={[
                    {
                      key: 'name',
                      header: t('admin.gachaStats.card'),
                      cell: 'title',
                      render: (row) => (
                        <div className="nb-stack nb-stack--sm">
                          <strong className="gacha-stats-name" title={cardName(row)}>
                            {cardName(row)}
                          </strong>
                          <small className="nb-muted mono gacha-stats-name">{row.card_id}</small>
                        </div>
                      ),
                    },
                    {
                      key: 'rarity',
                      header: t('admin.gachaStats.rarity'),
                      cell: 'status',
                      render: (row) => (
                        <span className="nb-badge gacha-stats-rarity" data-rarity={row.rarity}>
                          {row.rarity ?? t('admin.gachaStats.unspecifiedRarity')}
                        </span>
                      ),
                    },
                    {
                      key: 'count',
                      header: t('admin.gachaShopStats.redemptions'),
                      cell: 'meta',
                      mobileLabel: t('admin.gachaShopStats.redemptions'),
                      align: 'num',
                      render: (row) => <Count value={row.count} />,
                    },
                    {
                      key: 'participants',
                      header: t('admin.gachaShopStats.participants'),
                      cell: 'meta',
                      mobileLabel: t('admin.gachaShopStats.participants'),
                      align: 'num',
                      render: (row) => <Count value={row.participant_count} />,
                    },
                  ]}
                />
              ) : (
                <EmptyState
                  title={t('admin.gachaShopStats.noCardsTitle')}
                  body={t(q ? 'admin.gachaStats.noMatches' : 'admin.gachaShopStats.noCardsBody')}
                />
              )}
              <PagePagination
                metadata={query.data.pagination}
                requestedPage={pager.page}
                busy={query.isFetching}
                onPageChange={pager.setPage}
                onPageSizeChange={pager.setPageSize}
              />
            </>
          ) : null}
        </PanelBody>
      </Panel>
    </div>
  );
}
