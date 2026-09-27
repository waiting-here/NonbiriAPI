import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { useActivityText } from '@shared/limitedactivities/copy';
import { FatFishCanvas, FatFishPlayer } from '@shared/fatfish/FatFishPlayer';
import { fatFishApi, formatScoreUnits, userChallengeTransport, type FatFishChallenge, type FatFishConditionHint,
  type FatFishNode, type FatFishPeriod } from '@shared/fatfish/api';
import { createFatFishSessionController, type FatFishSessionController } from '@shared/fatfish/session';
import { cleanupLocalFishSessions, freshKey, localPlaySupportError } from '@shared/fatfish/storage';
import { PublicGameIdentity } from '../../games/common/PublicGameIdentity';
import { UserPageGate } from '../../components/UserPageGate';
import { useUserSession } from '../../data';
import './fatfish.css';

const root = ['user', 'fat-fish'] as const;
const terminal = (state: string) => ['settled_pass', 'settled_fail', 'abandoned', 'expired', 'cancelled_refunded'].includes(state);
function Condition({ hint }: { hint?: FatFishConditionHint }) {
  const t = useActivityText();
  if (!hint) return null;
  if (hint.kind === 'hidden') return <span>{t('需先完成隐藏条件', 'Complete a hidden prerequisite')}</span>;
  const text = hint.kind === 'none' ? t('无前置条件', 'No prerequisite') : hint.kind === 'passed' ?
    t('通过关卡', 'Pass node') : hint.kind === 'stars' ? t('达到星级', 'Reach stars') :
      hint.kind === 'all' ? t('全部满足', 'Meet all') : t('满足其一', 'Meet any');
  return <div className={hint.met ? 'fatfish-condition--met' : ''}>{text}
    {hint.node_id ? ` ${hint.node_id}` : ''}{hint.min ? ` ${hint.min}★` : ''}
    {hint.children?.length ? <ul>{hint.children.map((child, index) => <li key={index}><Condition hint={child} /></li>)}</ul> : null}
  </div>;
}
function nodeLabel(node: FatFishNode, t: ReturnType<typeof useActivityText>): string {
  return node.hidden && !node.title ? t('隐藏关卡', 'Hidden node') : node.title ?? t('关卡', 'Node');
}
function canEnter(period: FatFishPeriod): boolean {
  const now = Date.now();
  return period.state === 'open' && !period.paused && now >= period.starts_at * 1000 && now < period.ends_at * 1000;
}

function Content({ account }: { account: string }) {
  const t = useActivityText(), client = useQueryClient();
  const tRef = useRef(t);
  useEffect(() => { tRef.current = t; }, [t]);
  const keys = [...root, account] as const;
  const playSupportError = localPlaySupportError();
  const [params, setParams] = useSearchParams();
  const periodID = params.get('period') ?? '', nodeID = params.get('node') ?? '';
  const [page, setPage] = useState(1), [historyPage, setHistoryPage] = useState(1);
  const [boardPage, setBoardPage] = useState(1), [boardNode, setBoardNode] = useState('');
  const [mapZoom, setMapZoom] = useState(1);
  const [controller, setController] = useState<FatFishSessionController | null>(null);
  const controllerRef = useRef<FatFishSessionController | null>(null);
  const attemptedRecovery = useRef('');
  const unlockKey = useRef<{ periodID: string; nodeID: string; revision: string; key: string } | null>(null);
  const [working, setWorking] = useState(false), [notice, setNotice] = useState('');
  const [error, setError] = useState<unknown>(null);
  const periods = useQuery({ queryKey: [...keys, 'periods', page], queryFn: () => fatFishApi.periods(page), enabled: !!account });
  const period = useQuery({ queryKey: [...keys, 'period', periodID], queryFn: () => fatFishApi.period(periodID), enabled: !!account && !!periodID });
  const node = useQuery({ queryKey: [...keys, 'node', periodID, nodeID], queryFn: () => fatFishApi.node(periodID, nodeID), enabled: !!account && !!periodID && !!nodeID });
  const current = useQuery({ queryKey: [...keys, 'current'], queryFn: () => fatFishApi.current(), enabled: !!account,
    refetchInterval: (query) => controller?.snapshot().challenge?.id ===
      (query.state.data as FatFishChallenge | null | undefined)?.id ? false : 5000 });
  const history = useQuery({ queryKey: [...keys, 'history', historyPage], queryFn: () => fatFishApi.history(historyPage), enabled: !!account });
  const board = useQuery({ queryKey: [...keys, 'board', periodID, boardNode, boardPage],
    queryFn: () => fatFishApi.leaderboard(periodID, boardNode, boardPage), enabled: !!account && !!periodID });
  useEffect(() => () => { controllerRef.current?.dispose(); controllerRef.current = null; }, []);
  useEffect(() => {
    if (playSupportError) return;
    void cleanupLocalFishSessions().catch((failure: unknown) => setError(failure));
  }, [playSupportError]);
  const recoveryDescriptor = current.data && !terminal(current.data.state) ?
    [current.data.id, current.data.period_id ?? '', current.data.node_id ?? '', current.data.node_revision ?? ''].join('|') : '';
  useEffect(() => {
    if (!recoveryDescriptor || controllerRef.current) return;
    const [id, period, nodeID, revision] = recoveryDescriptor.split('|');
    if (attemptedRecovery.current === id) return;
    attemptedRecovery.current = id;
    let live = true;
    const session = createFatFishSessionController(userChallengeTransport(period, nodeID, revision));
    void session.recover(id).then(() => {
      if (!live) { session.dispose(); return; }
      controllerRef.current = session; setController(session);
      setNotice(tRef.current('已恢复原标签页的挑战。', 'Challenge resumed in this tab.'));
    }).catch(() => {
      session.dispose();
      if (live) setNotice(tRef.current('本页没有可用的原标签页记录；只能查看或放弃当前挑战。',
        'This tab cannot resume the challenge. You can view or abandon it.'));
    });
    return () => {
      live = false;
      session.dispose();
      if (controllerRef.current === session) controllerRef.current = null;
      attemptedRecovery.current = '';
    };
  }, [recoveryDescriptor, account]);
  const refresh = async () => { await Promise.all([
    client.invalidateQueries({ queryKey: root }), client.invalidateQueries({ queryKey: ['user', 'credits'] }),
  ]); };
  const doUnlock = async () => {
    const target = node.data;
    if (!target?.revision || !target.amounts || working) return;
    if (!window.confirm(t(`解锁需要 ${target.amounts.unlock_cost} 通用积分，确定继续？`,
      `Unlock for ${target.amounts.unlock_cost} general credits?`))) return;
    setWorking(true); setError(null);
    try {
      const retained = unlockKey.current;
      const key = retained?.periodID === periodID && retained.nodeID === target.id && retained.revision === target.revision ?
        retained.key : freshKey();
      unlockKey.current = { periodID, nodeID: target.id, revision: target.revision, key };
      await fatFishApi.unlock(periodID, target.id, target.revision, key);
      unlockKey.current = null; await refresh();
      setNotice(t('关卡已解锁。', 'Node unlocked.'));
    } catch (failure) {
      await refresh().catch(() => undefined);
      if ((await fatFishApi.node(periodID, target.id).catch(() => null))?.progress.unlocked) unlockKey.current = null;
      else setError(failure);
    } finally { setWorking(false); }
  };
  const doPrepare = async () => {
    if (!node.data?.revision || !node.data.amounts || working) return;
    setWorking(true); setError(null);
    let session: FatFishSessionController | null = null;
    try {
      session = createFatFishSessionController(userChallengeTransport(periodID, nodeID, node.data.revision));
      const prepared = await session.prepare();
      controllerRef.current = session; setController(session);
      client.setQueryData([...keys, 'current'], prepared);
      setNotice(t('准备完成。确认门票费用后正式开局。', 'Ready. Confirm the ticket charge to start.'));
    } catch (failure) { session?.dispose(); setError(failure); }
    finally { setWorking(false); }
  };
  const doStart = async () => {
    const challenge = controller?.snapshot().challenge;
    if (!challenge || working) return;
    if (!window.confirm(t(`正式开局将扣除 ${challenge.ticket_price} 通用积分；准备阶段不收费。确定开始？`,
      `Starting charges ${challenge.ticket_price} general credits. Preparation is free. Start now?`))) return;
    setWorking(true); setError(null);
    try {
      client.setQueryData([...keys, 'current'], await controller.start());
      setNotice('');
      await refresh();
    }
    catch (failure) { setError(failure); }
    finally { setWorking(false); }
  };
  const finishSession = () => {
    controllerRef.current?.dispose();
    controllerRef.current = null;
    setController(null);
    setNotice('');
    void refresh();
  };
  const abandonReadOnly = async (challenge: FatFishChallenge) => {
    if (!window.confirm(t('放弃当前挑战？已扣门票不会退还。', 'Abandon this challenge? A charged ticket is not refunded.'))) return;
    setWorking(true); setError(null);
    try { await userChallengeTransport('', '', '').abandon?.(challenge.id, '', freshKey()); await refresh(); }
    catch (failure) { setError(failure); }
    finally { setWorking(false); }
  };
  const nodes = period.data?.nodes ?? [];
  const minX = Math.min(0, ...nodes.map((item) => item.map_x)), maxX = Math.max(1, ...nodes.map((item) => item.map_x));
  const minY = Math.min(0, ...nodes.map((item) => item.map_y)), maxY = Math.max(1, ...nodes.map((item) => item.map_y));
  const position = (item: FatFishNode) => ({
    x: 8 + (item.map_x - minX) / (maxX - minX) * 84,
    y: 10 + (item.map_y - minY) / (maxY - minY) * 78,
  });
  const visibleByID = new Map(nodes.filter((item) => !!item.title).map((item) => [item.id, item]));
  const edges: { from: FatFishNode; to: FatFishNode }[] = [];
  for (const item of nodes) {
    if (!item.title) continue;
    const hint = item.id === nodeID ? node.data?.condition_hint ?? item.condition_hint : item.condition_hint;
    const visit = (condition?: FatFishConditionHint) => {
      if (!condition) return;
      if (condition.node_id) {
        const from = visibleByID.get(condition.node_id);
        if (from && !edges.some((edge) => edge.from.id === from.id && edge.to.id === item.id))
          edges.push({ from, to: item });
      }
      condition.children?.forEach(visit);
    };
    visit(hint);
  }
  const active = current.data && !terminal(current.data.state) ? current.data : null;
  const activeController = controller?.snapshot().challenge?.id === active?.id ? controller : null;
  return <div className="page fatfish-page">
    <PageHeader eyebrow={t('限时活动', 'Limited-time activity')} title={t('饲养大肥鱼', 'Raise a big fish')}
      description={t('摆放工具，引导鱼儿安全进食。成绩与奖励由服务端核验。',
        'Place tools and guide the fish to food. The server verifies scores and rewards.')}
      icon="activities" back={<Link to="/activities">{t('返回活动', 'Back to activities')}</Link>} />
    {notice ? <p role="status">{notice}</p> : null}{error ? <ErrorState error={error} /> : null}
    {active ? <Card><h2>{t('当前挑战', 'Current challenge')}</h2>
      <p>{t('状态', 'Status')}: {active.state} · {t('门票', 'Ticket')}: {active.ticket_price}</p>
      {activeController ? <>{activeController.snapshot().phase === 'prepared' ? <button type="button" className="btn btn-primary"
        disabled={working} onClick={() => void doStart()}>{t('确认门票并开始', 'Confirm ticket and start')}</button> : null}
        <FatFishPlayer controller={activeController} onTerminal={finishSession} /></> : <>
        <p>{t('本标签页无法继续游玩；可以等待到期或放弃。',
          'This tab cannot continue playing. Wait for expiry or abandon the challenge.')}</p>
        <button type="button" className="btn btn-secondary" disabled={working}
          onClick={() => void abandonReadOnly(active)}>{t('放弃挑战', 'Abandon challenge')}</button></>}
    </Card> : null}
    <section className="fatfish-catalog"><h2>{t('期次', 'Periods')}</h2>
      {periods.isPending ? <LoadingState /> : periods.error ? <ErrorState error={periods.error} onRetry={() => void periods.refetch()} /> : null}
      <div className="fatfish-periods">{periods.data?.items.map((item) => <button key={item.id} type="button"
        className={periodID === item.id ? 'is-selected' : ''} onClick={() => { setParams({ period: item.id }); setBoardNode(''); setBoardPage(1); }}>
        <strong>{item.title}</strong><span>{item.state === 'open' && !item.paused ? t('进行中', 'Open') : t('已结束或暂停', 'Ended or paused')}</span>
      </button>)}</div>
      <div className="fatfish-pager"><button type="button" disabled={page <= 1} onClick={() => setPage(page - 1)}>{t('上一页', 'Previous')}</button>
        <span>{page}</span><button type="button" disabled={!periods.data?.has_more} onClick={() => setPage(page + 1)}>{t('下一页', 'Next')}</button></div>
    </section>
    {periodID ? <section>
      {period.isPending ? <LoadingState /> : period.error ? <ErrorState error={period.error} onRetry={() => void period.refetch()} /> : null}
      {period.data ? <><h2>{period.data.title}</h2><p>{period.data.description}</p>
        {period.data.paused ? <p>{t('本期已暂停新挑战。', 'New challenges are paused.')}</p> : null}
        <div className="fatfish-map-controls">
          <span>{t('地图缩放', 'Map zoom')}: {Math.round(mapZoom * 100)}%</span>
          <button type="button" disabled={mapZoom <= 1} onClick={() => setMapZoom(Math.max(1, mapZoom - .5))}>
            {t('缩小', 'Zoom out')}</button>
          <button type="button" disabled={mapZoom >= 3} onClick={() => setMapZoom(Math.min(3, mapZoom + .5))}>
            {t('放大', 'Zoom in')}</button>
        </div>
        <div className="fatfish-map-viewport">
          <div className="fatfish-map" aria-label={t('关卡地图', 'Node map')}
            style={{ width: `${40 * mapZoom}rem`, height: `${17 * mapZoom}rem` }}>
            <svg className="fatfish-map__edges" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
              {edges.map((edge) => {
                const from = position(edge.from), to = position(edge.to);
                return <line key={`${edge.from.id}:${edge.to.id}`} x1={from.x} y1={from.y} x2={to.x} y2={to.y} />;
              })}
            </svg>
            {nodes.map((item) => {
              const point = position(item);
              return <button key={item.id} type="button" className={item.id === nodeID ? 'is-selected' : ''}
                style={{ left: `${point.x}%`, top: `${point.y}%` }} title={nodeLabel(item, t)}
                onClick={() => setParams({ period: periodID, node: item.id })} aria-label={nodeLabel(item, t)}>
                {item.progress.best_stars ? `${item.progress.best_stars}★` : item.order + 1}</button>;
            })}
          </div>
        </div>
        <ol className="fatfish-node-list">{nodes.map((item) => <li key={item.id}>
          <button type="button" onClick={() => setParams({ period: periodID, node: item.id })}>{nodeLabel(item, t)}</button>
          <span>{item.progress.unlocked ? t('已解锁', 'Unlocked') : item.eligible ? t('可解锁', 'Eligible') : t('未达到条件', 'Locked')}</span>
        </li>)}</ol></> : null}
      {nodeID && node.data ? <Card><h3>{nodeLabel(node.data, t)}</h3>
        {node.data.description ? <p>{node.data.description}</p> : null}
        {node.data.condition_hint ? <div className="fatfish-condition"><Condition hint={node.data.condition_hint} /></div> : null}
        {node.data.amounts ? <dl className="fatfish-facts">
          <dt>{t('解锁费用', 'Unlock cost')}</dt><dd>{node.data.amounts.unlock_cost}</dd>
          <dt>{t('每局门票', 'Ticket per challenge')}</dt><dd>{node.data.amounts.ticket_price}</dd>
          <dt>{t('首次通关奖励', 'First clear reward')}</dt><dd>{node.data.amounts.first_clear_reward}</dd>
          <dt>{t('星级奖励', 'Star rewards')}</dt><dd>{node.data.amounts.star_rewards.join(' / ')}</dd>
        </dl> : null}
        {node.data.level ? <><h4>{t('初始布局', 'Initial layout')}</h4><FatFishCanvas level={node.data.level} /></> : null}
        <p>{t('个人最佳', 'Personal best')}: {formatScoreUnits(node.data.progress.best_score_units)} · {node.data.progress.best_stars}★</p>
        {!active && period.data && canEnter(period.data) && node.data.eligible && !node.data.progress.unlocked && node.data.revision ?
          <button type="button" className="btn btn-primary" disabled={working} onClick={() => void doUnlock()}>{t('解锁关卡', 'Unlock node')}</button> : null}
        {!active && period.data && canEnter(period.data) && node.data.progress.unlocked ?
          <button type="button" className="btn btn-primary" disabled={working || !!playSupportError} onClick={() => void doPrepare()}>{t('准备挑战（不收费）', 'Prepare challenge (free)')}</button> : null}
        {playSupportError ? <p role="status">{t('此浏览器不能安全保存并独占恢复挑战，因此无法开始付费局。',
          'This browser cannot safely save and exclusively resume a challenge, so paid play is unavailable.')}</p> : null}
      </Card> : nodeID && node.isPending ? <LoadingState /> : nodeID && node.error ? <ErrorState error={node.error} onRetry={() => void node.refetch()} /> : null}
      <section><h2>{t('排行榜', 'Leaderboard')}</h2>
        <label>{t('范围', 'Scope')} <select value={boardNode} onChange={(event) => { setBoardNode(event.target.value); setBoardPage(1); }}>
          <option value="">{t('本期总榜', 'Period total')}</option>
          {nodes.filter((item) => item.title).map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}
        </select></label>
        {board.isPending ? <LoadingState /> : board.error ? <ErrorState error={board.error} onRetry={() => void board.refetch()} /> : null}
        <ol className="fatfish-board">{board.data?.rows.map((row) => <li key={`${row.rank}-${row.achieved_at_ms}-${row.score_units}`}>
          <span>#{row.rank}</span><PublicGameIdentity identity={{ kind: row.identity.kind === 'public' ? 'public' : 'anonymous',
            displayName: row.identity.display_name ?? '', avatarURL: row.identity.avatar_url ?? null }}
            anonymousLabel={t('匿名玩家', 'Anonymous player')} isMe={row.is_me} meLabel={t('我', 'Me')} />
          <strong>{formatScoreUnits(row.score_units)}</strong></li>)}</ol>
        <div className="fatfish-pager"><button type="button" disabled={boardPage <= 1} onClick={() => setBoardPage(boardPage - 1)}>{t('上一页', 'Previous')}</button>
          <span>{boardPage}</span><button type="button" disabled={!board.data || boardPage * board.data.page_size >= board.data.total}
            onClick={() => setBoardPage(boardPage + 1)}>{t('下一页', 'Next')}</button></div>
      </section>
    </section> : null}
    <section><h2>{t('挑战历史', 'Challenge history')}</h2>
      {history.isPending ? <LoadingState /> : history.error ? <ErrorState error={history.error} onRetry={() => void history.refetch()} /> : null}
      <ol className="fatfish-history">{history.data?.items.map((item) => <li key={item.id}>
        <code>{item.id}</code>
        <strong>{item.result?.passed ? t('通过', 'Passed') : t('未通过', 'Not passed')}</strong>
        <span>{item.result?.stars ?? 0}★ · {formatScoreUnits(item.result?.score_units ?? '0')}</span>
        <span>{t('门票', 'Ticket')}: {item.result?.ticket_charge ?? item.ticket_price} · {t('退款', 'Refund')}: {item.result?.ticket_refund ?? '0'} · {t('奖励', 'Rewards')}: {item.result?.rewards ?? '0'}</span>
      </li>)}</ol>
      <div className="fatfish-pager"><button type="button" disabled={historyPage <= 1} onClick={() => setHistoryPage(historyPage - 1)}>{t('上一页', 'Previous')}</button>
        <span>{historyPage}</span><button type="button" disabled={!history.data?.has_more} onClick={() => setHistoryPage(historyPage + 1)}>{t('下一页', 'Next')}</button></div>
    </section>
  </div>;
}
export function FatFishActivityPage() {
  const account = useUserSession().data?.user.id ?? '';
  return <UserPageGate><Content key={account} account={account} /></UserPageGate>;
}
