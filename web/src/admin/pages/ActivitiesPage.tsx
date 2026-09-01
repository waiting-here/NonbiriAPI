import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import { amount } from '@shared/operations/wire';
import { adjustPool, adminEconomyKeys, getActivitiesConfig, getAdminThursday, getPools, patchActivitiesConfig, putThursdayNext, resumeThursday, thursdayMutationRevision, type ActivitiesConfig, type Period } from '../features/operations/economy';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import '@shared/operations/operations.css';

function validPositiveAmount(value: string): boolean {
  try { return BigInt(amount(value, 'positive activity amount', false).replace('.', '')) > 0n; } catch { return false; }
}

interface PeriodDraft {
  period_key: string;
  opens_at: string;
  literature: string;
  entry: string;
  per_user_limit: string;
  platform: string;
  welfare: string;
  next_pool: string;
}

type PeriodMutation = PeriodDraft & { expected_revision: string };

const emptyPeriodDraft = (): PeriodDraft => ({ period_key: '', opens_at: '', literature: '', entry: '', per_user_limit: '1', platform: '0', welfare: '0', next_pool: '0' });

function localDateTime(seconds: number): string {
  const instant = new Date(seconds * 1_000);
  return new Date(instant.getTime() - instant.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

const draftForPeriod = (period: Period | null): PeriodDraft => period ? {
  period_key: period.period_key,
  opens_at: localDateTime(period.opens_at),
  literature: period.literature,
  entry: period.entry,
  per_user_limit: String(period.per_user_limit),
  platform: String(period.pumps_bp.platform),
  welfare: String(period.pumps_bp.welfare),
  next_pool: String(period.pumps_bp.next_pool),
} : emptyPeriodDraft();

export function ActivitiesPage() {
  const poolPager = useCursorPager();
  const config = useQuery({ queryKey: adminEconomyKeys.activities, queryFn: getActivitiesConfig, retry: false });
  const thursday = useQuery({ queryKey: adminEconomyKeys.thursday, queryFn: getAdminThursday, retry: false });
  const pools = useQuery({ queryKey: adminEconomyKeys.pools('', '', poolPager.cursor), queryFn: () => getPools('', '', poolPager.cursor), retry: false });
  const [configOverride, setConfigOverride] = useState<ActivitiesConfig | null>(null);
  const [periodOverride, setPeriodOverride] = useState<PeriodDraft | null>(null);
  const [adjustment, setAdjustment] = useState({ poolId: '', revision: '', authorityRevision: '', direction: 'increase' as 'increase' | 'decrease', amount: '', reason: '', confirmed: false });
  const period = thursday.data?.period ?? null;
  const authorityPeriodRevision = thursday.data?.period?.revision ?? (thursday.data ? 'none' : '');
  const configDraft = config.data
    ? configOverride ? { ...configOverride, revision: config.data.revision } : config.data
    : null;
  const periodDraft = periodOverride ?? draftForPeriod(period);
  const adjustmentConfirmed = adjustment.confirmed
    && adjustment.authorityRevision === authorityPeriodRevision;
  const reconcile = async () => { await Promise.all([config.refetch(), thursday.refetch(), pools.refetch()]); };
  const saveConfig = useRetainedOperation((input: ActivitiesConfig, key) => patchActivitiesConfig({ expected_revision: input.revision, master_enabled: input.master_enabled, welfare: input.welfare, thursday: input.thursday }, key), reconcile);
  const savePeriod = useRetainedOperation((input: PeriodMutation, key) => putThursdayNext({ expected_revision: input.expected_revision, period_key: input.period_key, opens_at: Math.floor(Date.parse(input.opens_at) / 1_000), literature: input.literature, entry: input.entry, per_user_limit: Number(input.per_user_limit), pumps_bp: { platform: Number(input.platform), welfare: Number(input.welfare), next_pool: Number(input.next_pool) } }, key), reconcile);
  const resume = useRetainedOperation((input: { id: string; revision: string }, key) => resumeThursday(input.id, input.revision, key), reconcile);
  const adjust = useRetainedOperation((input: typeof adjustment, key) => adjustPool(input.poolId, { direction: input.direction, amount: input.amount, reason: input.reason, expected_revision: input.revision, confirmation: input.direction === 'decrease' ? 'DECREASE' : '' }, key), async () => {
    setAdjustment((current) => ({ ...current, confirmed: false }));
    await reconcile();
  });
  const periodLocked = period?.state === 'open' || period?.state === 'settling';
  const periodNeedsConfigRevision = period?.state !== 'configured';
  const periodAuthorityReady = thursday.data !== undefined && !thursday.error && !thursday.isFetching
    && thursdayMutationRevision(config.data, period) !== null
    && (!periodNeedsConfigRevision || (!config.error && !config.isFetching));
  const poolAuthorityBlocked = Boolean(pools.error) || pools.isFetching;
  const currentPoolDecreaseBlocked = adjustment.direction === 'decrease'
    && period?.current_pool_id === adjustment.poolId
    && (period.state === 'open' || period.state === 'settling');
  const editPeriod = (patch: Partial<PeriodDraft>) => {
    setPeriodOverride((current) => ({ ...(current ?? periodDraft), ...patch }));
  };
  const editConfig = (next: ActivitiesConfig) => {
    setConfigOverride(next);
  };
  return (
    <div className="page ops-page">
      <PageHeader title="Activities and pools" description="Configuration, Thursday lifecycle, and balance adjustments remain separate authority surfaces." />
      <Card><h2>Activity configuration</h2>{config.error ? <ErrorState error={config.error} onRetry={() => void config.refetch()} /> : config.isPending || !configDraft ? <LoadingState /> : <><p>Revision {configDraft.revision}. Configuration changes compile atomically; a Thursday period already open keeps its frozen snapshot.</p><div className="ops-field-grid"><label><input type="checkbox" checked={configDraft.master_enabled} onChange={(event) => editConfig({ ...configDraft, master_enabled: event.target.checked })} /> Master enabled</label><label><input type="checkbox" checked={configDraft.welfare.enabled} onChange={(event) => editConfig({ ...configDraft, welfare: { ...configDraft.welfare, enabled: event.target.checked } })} /> Welfare enabled</label><label><span>Welfare threshold (credits)</span><input value={configDraft.welfare.threshold} onChange={(event) => editConfig({ ...configDraft, welfare: { ...configDraft.welfare, threshold: event.target.value } })} /></label><label><span>Welfare cap (credits)</span><input value={configDraft.welfare.cap} onChange={(event) => editConfig({ ...configDraft, welfare: { ...configDraft.welfare, cap: event.target.value } })} /></label><label><input type="checkbox" checked={configDraft.thursday.enabled} onChange={(event) => editConfig({ ...configDraft, thursday: { enabled: event.target.checked } })} /> Thursday enabled</label></div>{saveConfig.error ? <ErrorState error={saveConfig.error} /> : null}<button className="btn btn-primary" type="button" disabled={saveConfig.isPending} onClick={() => saveConfig.mutate(configDraft, { onSuccess: () => setConfigOverride(null) })}>Save atomic configuration</button></>}</Card>
      <Card><h2>Thursday authority</h2>{thursday.isPending ? <LoadingState /> : thursday.error ? <ErrorState error={thursday.error} onRetry={() => void thursday.refetch()} /> : period ? <><dl className="ops-kv"><dt>State</dt><dd><StatusBadge active={period.state === 'open'} danger={period.state === 'configuration_error'} label={period.state} /></dd><dt>Period / revision</dt><dd>{period.period_key} / {period.revision}</dd><dt>Window</dt><dd>{formatDateTime(period.opens_at)} — {formatDateTime(period.closes_at)}</dd><dt>Entry / limit</dt><dd>{period.entry} credits / {period.per_user_limit}</dd><dt>Literature</dt><dd>{period.literature}</dd>{period.settlement ? <><dt>Processed</dt><dd>{period.settlement.processed_count} / {period.settlement.contribution_count}</dd><dt>Payout / rollover</dt><dd>{period.settlement.payout_total} / {period.settlement.rollover}</dd></> : null}</dl>{period.state === 'settling' ? <button className="btn btn-danger" type="button" disabled={resume.isPending} onClick={() => resume.mutate({ id: period.id, revision: period.revision })}>Resume existing settlement checkpoint</button> : null}</> : <EmptyState title="No configured or recoverable period" body="Create the next 24-hour period from the current activities revision." />}</Card>
      <Card><h2>{period ? 'Update configured next period' : 'Create next period'}</h2><p>{periodLocked ? 'The period is open or settling; frozen/current configuration cannot be edited.' : 'A successful write, conflict, or unknown response is followed only by a fresh authoritative GET.'}</p><div className="ops-field-grid"><label><span>Period key</span><input value={periodDraft.period_key} onChange={(event) => editPeriod({ period_key: event.target.value })} /></label><label><span>Opens at</span><input type="datetime-local" value={periodDraft.opens_at} onChange={(event) => editPeriod({ opens_at: event.target.value })} /></label><label><span>Entry (credits)</span><input value={periodDraft.entry} onChange={(event) => editPeriod({ entry: event.target.value })} /></label><label><span>Per-user limit</span><input type="number" min="1" max="1000" value={periodDraft.per_user_limit} onChange={(event) => editPeriod({ per_user_limit: event.target.value })} /></label><label><span>Platform bp</span><input type="number" min="0" max="9999" value={periodDraft.platform} onChange={(event) => editPeriod({ platform: event.target.value })} /></label><label><span>Welfare bp</span><input type="number" min="0" max="9999" value={periodDraft.welfare} onChange={(event) => editPeriod({ welfare: event.target.value })} /></label><label><span>Next-pool bp</span><input type="number" min="0" max="9999" value={periodDraft.next_pool} onChange={(event) => editPeriod({ next_pool: event.target.value })} /></label></div><label className="ops-form-field"><span>Literature</span><textarea value={periodDraft.literature} onChange={(event) => editPeriod({ literature: event.target.value })} /></label>{savePeriod.error ? <ErrorState error={savePeriod.error} /> : null}<button className="btn btn-primary" type="button" disabled={!periodAuthorityReady || periodLocked || savePeriod.isPending || !periodDraft.period_key || !periodDraft.opens_at || !periodDraft.literature || !validPositiveAmount(periodDraft.entry)} onClick={() => { const revision = thursdayMutationRevision(config.data, period); if (revision !== null) savePeriod.mutate({ ...periodDraft, expected_revision: revision }, { onSuccess: () => setPeriodOverride(null) }); }}>Save next period</button></Card>
      <Card><h2>Shared pools</h2>{pools.isPending ? <LoadingState /> : pools.error ? <ErrorState error={pools.error} onRetry={() => void pools.refetch()} /> : pools.data.data.length === 0 ? <EmptyState title="No pools" body="The authority read returned no pools." /> : <><div className="ops-table-scroll"><table className="ops-table"><thead><tr><th>Type / state</th><th>Period</th><th>Balance</th><th>Revision</th><th>Adjust</th></tr></thead><tbody>{pools.data.data.map((pool) => <tr key={pool.id}><td>{pool.pool_type} / {pool.state}</td><td>{pool.period_id ?? 'singleton'}</td><td>{pool.balance} credits</td><td>{pool.revision}</td><td><button className="btn btn-secondary" type="button" disabled={pool.state !== 'open'} onClick={() => setAdjustment({ poolId: pool.id, revision: pool.revision, authorityRevision: authorityPeriodRevision, direction: 'increase', amount: '', reason: '', confirmed: false })}>Select</button></td></tr>)}</tbody></table></div><CursorPagination page={poolPager.page} nextCursor={pools.data.next_cursor} onPrevious={poolPager.previous} onNext={poolPager.next} /></>}</Card>
      {adjustment.poolId ? <Card className={adjustment.direction === 'decrease' ? 'ops-danger' : ''}><h2>Pool adjustment</h2><p>Selected {adjustment.poolId} at revision {adjustment.revision}. Increases are positive injection; decreases require the exact dangerous confirmation.</p>{currentPoolDecreaseBlocked ? <p className="inline-notice" role="status">The current Thursday pool cannot be forcibly decreased after its natural window opens.</p> : null}<div className="ops-field-grid"><label><span>Direction</span><select value={adjustment.direction} onChange={(event) => setAdjustment({ ...adjustment, authorityRevision: authorityPeriodRevision, direction: event.target.value as typeof adjustment.direction, confirmed: false })}><option value="increase">Increase</option><option value="decrease" disabled={period?.current_pool_id === adjustment.poolId && (period.state === 'open' || period.state === 'settling')}>Decrease</option></select></label><label><span>Amount (credits)</span><input value={adjustment.amount} onChange={(event) => setAdjustment({ ...adjustment, amount: event.target.value })} /></label><label><span>Reason</span><input value={adjustment.reason} maxLength={1024} onChange={(event) => setAdjustment({ ...adjustment, reason: event.target.value })} /></label></div>{adjustment.direction === 'decrease' ? <label><input type="checkbox" checked={adjustmentConfirmed} onChange={(event) => setAdjustment({ ...adjustment, authorityRevision: authorityPeriodRevision, confirmed: event.target.checked })} /> I understand this forcibly removes credits from the shared pool.</label> : null}{adjust.error ? <ErrorState error={adjust.error} /> : null}<div className="ops-actions"><button className={adjustment.direction === 'decrease' ? 'btn btn-danger' : 'btn btn-primary'} type="button" disabled={poolAuthorityBlocked || currentPoolDecreaseBlocked || adjust.isPending || !validPositiveAmount(adjustment.amount) || !adjustment.reason.trim() || (adjustment.direction === 'decrease' && !adjustmentConfirmed)} onClick={() => adjust.mutate(adjustment, { onSuccess: () => setAdjustment({ poolId: '', revision: '', authorityRevision: '', direction: 'increase', amount: '', reason: '', confirmed: false }) })}>Apply adjustment</button><button className="btn btn-link" type="button" onClick={() => setAdjustment({ poolId: '', revision: '', authorityRevision: '', direction: 'increase', amount: '', reason: '', confirmed: false })}>Cancel</button></div></Card> : null}
    </div>
  );
}
