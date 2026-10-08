import { SimplePager } from '@shared/operations/SimplePager';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { useActivityText } from '@shared/limitedactivities/copy';
import { FatFishCanvas, FatFishPlayer } from '@shared/fatfish/FatFishPlayer';
import {
  fatFishApi,
  formatScoreUnits,
  userChallengeTransport,
  type FatFishChallenge,
  type FatFishConditionHint,
  type FatFishNode,
  type FatFishPeriod,
} from '@shared/fatfish/api';
import {
  createFatFishSessionController,
  type FatFishSessionController,
} from '@shared/fatfish/session';
import { cleanupLocalFishSessions, freshKey, localPlaySupportError } from '@shared/fatfish/storage';
import { PublicGameIdentity } from '../../games/common/PublicGameIdentity';
import { UserPageGate } from '../../components/UserPageGate';
import { useUserSession } from '../../data';
import './fatfish.css';
const root = ['user', 'fat-fish'] as const;
const terminal = (state: string) =>
  ['settled_pass', 'settled_fail', 'abandoned', 'expired', 'cancelled_refunded'].includes(state);
function Condition({ hint }: { hint?: FatFishConditionHint }) {
  const text = useActivityText();
  if (!hint) return null;
  if (hint.kind === 'hidden') return <span>{text('common.completeAHiddenPrerequisite')}</span>;
  const conditionText =
    hint.kind === 'none'
      ? text('common.noPrerequisite')
      : hint.kind === 'passed'
        ? text('common.passNode')
        : hint.kind === 'stars'
          ? text('common.reachStars')
          : hint.kind === 'all'
            ? text('common.meetAll')
            : text('common.meetAny');
  return (
    <div className={hint.met ? 'fatfish-condition--met' : ''}>
      {conditionText}
      {hint.node_id ? ` ${hint.node_id}` : ''}
      {hint.min ? ` ${hint.min}★` : ''}
      {hint.children?.length ? (
        <ul>
          {hint.children.map((child, index) => (
            <li key={index}>
              <Condition hint={child} />
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
function nodeLabel(node: FatFishNode, text: ReturnType<typeof useActivityText>): string {
  return node.hidden && !node.title
    ? text('common.hiddenNode')
    : (node.title ?? text('common.node'));
}
function canEnter(period: FatFishPeriod): boolean {
  const now = Date.now();
  return (
    period.state === 'open' &&
    !period.paused &&
    now >= period.starts_at * 1000 &&
    now < period.ends_at * 1000
  );
}
function Content({ account }: { account: string }) {
  const text = useActivityText(),
    client = useQueryClient();
  const [confirmation, setConfirmation] = useState<{
    body: string;
    label: string;
    danger: boolean;
  } | null>(null);
  const confirmationResult = useRef<((accepted: boolean) => void) | null>(null);
  useEffect(
    () => () => {
      confirmationResult.current?.(false);
      confirmationResult.current = null;
    },
    [],
  );
  const confirm = (body: string, label: string, danger = false) =>
    new Promise<boolean>((resolve) => {
      confirmationResult.current?.(false);
      confirmationResult.current = resolve;
      setConfirmation({ body, label, danger });
    });
  const finishConfirmation = (accepted: boolean) => {
    confirmationResult.current?.(accepted);
    confirmationResult.current = null;
    setConfirmation(null);
  };
  const textRef = useRef(text);
  useEffect(() => {
    textRef.current = text;
  }, [text]);
  const keys = [...root, account] as const;
  const playSupportError = localPlaySupportError();
  const [params, setParams] = useSearchParams();
  const periodID = params.get('period') ?? '',
    nodeID = params.get('node') ?? '';
  const [page, setPage] = useState(1),
    [historyPage, setHistoryPage] = useState(1);
  const [boardPage, setBoardPage] = useState(1),
    [boardNode, setBoardNode] = useState('');
  const [mapZoom, setMapZoom] = useState(1);
  const [controller, setController] = useState<FatFishSessionController | null>(null);
  const controllerRef = useRef<FatFishSessionController | null>(null);
  const attemptedRecovery = useRef('');
  const unlockKey = useRef<{
    periodID: string;
    nodeID: string;
    revision: string;
    key: string;
  } | null>(null);
  const [working, setWorking] = useState(false),
    [notice, setNotice] = useState('');
  const [error, setError] = useState<unknown>(null);
  const periods = useQuery({
    queryKey: [...keys, 'periods', page],
    queryFn: () => fatFishApi.periods(page),
    enabled: !!account,
  });
  const period = useQuery({
    queryKey: [...keys, 'period', periodID],
    queryFn: () => fatFishApi.period(periodID),
    enabled: !!account && !!periodID,
  });
  const node = useQuery({
    queryKey: [...keys, 'node', periodID, nodeID],
    queryFn: () => fatFishApi.node(periodID, nodeID),
    enabled: !!account && !!periodID && !!nodeID,
  });
  const current = useQuery({
    queryKey: [...keys, 'current'],
    queryFn: () => fatFishApi.current(),
    enabled: !!account,
    refetchInterval: (query) =>
      controller?.snapshot().challenge?.id ===
      (query.state.data as FatFishChallenge | null | undefined)?.id
        ? false
        : 5000,
  });
  const history = useQuery({
    queryKey: [...keys, 'history', historyPage],
    queryFn: () => fatFishApi.history(historyPage),
    enabled: !!account,
  });
  const board = useQuery({
    queryKey: [...keys, 'board', periodID, boardNode, boardPage],
    queryFn: () => fatFishApi.leaderboard(periodID, boardNode, boardPage),
    enabled: !!account && !!periodID,
  });
  useEffect(
    () => () => {
      controllerRef.current?.dispose();
      controllerRef.current = null;
    },
    [],
  );
  useEffect(() => {
    if (playSupportError) return;
    void cleanupLocalFishSessions().catch((failure: unknown) => setError(failure));
  }, [playSupportError]);
  const recoveryDescriptor =
    current.data && !terminal(current.data.state)
      ? [
          current.data.id,
          current.data.period_id ?? '',
          current.data.node_id ?? '',
          current.data.node_revision ?? '',
        ].join('|')
      : '';
  useEffect(() => {
    if (!recoveryDescriptor || controllerRef.current) return;
    const [id, period, nodeID, revision] = recoveryDescriptor.split('|');
    if (attemptedRecovery.current === id) return;
    attemptedRecovery.current = id;
    let live = true;
    const session = createFatFishSessionController(
      userChallengeTransport(period, nodeID, revision),
    );
    void session
      .recover(id)
      .then(() => {
        if (!live) {
          session.dispose();
          return;
        }
        controllerRef.current = session;
        setController(session);
        setNotice(textRef.current('common.challengeResumedInThisTab'));
      })
      .catch(() => {
        session.dispose();
        if (live) setNotice(textRef.current('common.thisTabCannotResumeTheChallengeYou'));
      });
    return () => {
      live = false;
      session.dispose();
      if (controllerRef.current === session) controllerRef.current = null;
      attemptedRecovery.current = '';
    };
  }, [recoveryDescriptor, account]);
  const refresh = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: root }),
      client.invalidateQueries({ queryKey: ['user', 'credits'] }),
    ]);
  };
  const doUnlock = async () => {
    const target = node.data;
    if (!target?.revision || !target.amounts || working) return;
    if (
      !(await confirm(
        text('common.unlockForGeneralCredits', { unlock_cost: target.amounts.unlock_cost }),
        text('common.unlockNode'),
      ))
    )
      return;
    setWorking(true);
    setError(null);
    try {
      const retained = unlockKey.current;
      const key =
        retained?.periodID === periodID &&
        retained.nodeID === target.id &&
        retained.revision === target.revision
          ? retained.key
          : freshKey();
      unlockKey.current = { periodID, nodeID: target.id, revision: target.revision, key };
      await fatFishApi.unlock(periodID, target.id, target.revision, key);
      unlockKey.current = null;
      await refresh();
      setNotice(text('common.nodeUnlocked'));
    } catch (failure) {
      await refresh().catch(() => undefined);
      if ((await fatFishApi.node(periodID, target.id).catch(() => null))?.progress.unlocked)
        unlockKey.current = null;
      else setError(failure);
    } finally {
      setWorking(false);
    }
  };
  const doPrepare = async () => {
    if (!node.data?.revision || !node.data.amounts || working) return;
    setWorking(true);
    setError(null);
    let session: FatFishSessionController | null = null;
    try {
      session = createFatFishSessionController(
        userChallengeTransport(periodID, nodeID, node.data.revision),
      );
      const prepared = await session.prepare();
      controllerRef.current = session;
      setController(session);
      client.setQueryData([...keys, 'current'], prepared);
      setNotice(text('common.readyConfirmTheTicketChargeToStart'));
    } catch (failure) {
      session?.dispose();
      setError(failure);
    } finally {
      setWorking(false);
    }
  };
  const doStart = async () => {
    const challenge = controller?.snapshot().challenge;
    if (!challenge || working) return;
    if (
      !(await confirm(
        text('common.startingChargesGeneralCreditsPreparationIsFree', {
          ticket_price: challenge.ticket_price,
        }),
        text('common.confirmTicketAndStart'),
      ))
    )
      return;
    setWorking(true);
    setError(null);
    try {
      client.setQueryData([...keys, 'current'], await controller.start());
      setNotice('');
      await refresh();
    } catch (failure) {
      setError(failure);
    } finally {
      setWorking(false);
    }
  };
  const finishSession = () => {
    controllerRef.current?.dispose();
    controllerRef.current = null;
    setController(null);
    setNotice('');
    void refresh();
  };
  const abandonReadOnly = async (challenge: FatFishChallenge) => {
    if (
      !(await confirm(
        text('common.abandonThisChallengeAChargedTicketIs'),
        text('common.abandonChallenge'),
        true,
      ))
    )
      return;
    setWorking(true);
    setError(null);
    try {
      await userChallengeTransport('', '', '').abandon?.(challenge.id, '', freshKey());
      await refresh();
    } catch (failure) {
      setError(failure);
    } finally {
      setWorking(false);
    }
  };
  const nodes = period.data?.nodes ?? [];
  const minX = Math.min(0, ...nodes.map((item) => item.map_x)),
    maxX = Math.max(1, ...nodes.map((item) => item.map_x));
  const minY = Math.min(0, ...nodes.map((item) => item.map_y)),
    maxY = Math.max(1, ...nodes.map((item) => item.map_y));
  const position = (item: FatFishNode) => ({
    x: 8 + ((item.map_x - minX) / (maxX - minX)) * 84,
    y: 10 + ((item.map_y - minY) / (maxY - minY)) * 78,
  });
  const visibleByID = new Map(nodes.filter((item) => !!item.title).map((item) => [item.id, item]));
  const edges: {
    from: FatFishNode;
    to: FatFishNode;
  }[] = [];
  for (const item of nodes) {
    if (!item.title) continue;
    const hint =
      item.id === nodeID ? (node.data?.condition_hint ?? item.condition_hint) : item.condition_hint;
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
  return (
    <div className="page fatfish-page">
      <ConfirmDialog
        open={confirmation !== null}
        title={confirmation?.label ?? ''}
        description={confirmation?.body}
        confirmLabel={confirmation?.label ?? ''}
        danger={confirmation?.danger}
        onConfirm={() => finishConfirmation(true)}
        onCancel={() => finishConfirmation(false)}
      />
      <PageHeader
        eyebrow={text('common.limitedTimeActivity')}
        title={text('common.raiseABigFish')}
        description={text('common.placeToolsAndGuideTheFishTo')}
        icon="activities"
        back={<Link to="/activities">{text('common.backToActivities')}</Link>}
      />
      {notice ? <p role="status">{notice}</p> : null}
      {error ? <ErrorState error={error} /> : null}
      {active ? (
        <Card>
          <h2>{text('common.currentChallenge')}</h2>
          <p>
            {text('common.status')}: {active.state} · {text('common.ticket')}: {active.ticket_price}
          </p>
          {activeController ? (
            <>
              {activeController.snapshot().phase === 'prepared' ? (
                <button
                  type="button"
                  className="nb-btn nb-btn--primary"
                  disabled={working}
                  onClick={() => void doStart()}
                >
                  {text('common.confirmTicketAndStart')}
                </button>
              ) : null}
              <FatFishPlayer controller={activeController} onTerminal={finishSession} />
            </>
          ) : (
            <>
              <p>{text('common.thisTabCannotContinuePlayingWaitFor')}</p>
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                disabled={working}
                onClick={() => void abandonReadOnly(active)}
              >
                {text('common.abandonChallenge')}
              </button>
            </>
          )}
        </Card>
      ) : null}
      <section className="fatfish-catalog">
        <h2>{text('common.periods')}</h2>
        {periods.isPending ? (
          <LoadingState />
        ) : periods.error ? (
          <ErrorState error={periods.error} onRetry={() => void periods.refetch()} />
        ) : null}
        {periods.data && periods.data.items.length === 0 && page === 1 ? (
          <EmptyState title={text('common.noOpenPeriodsTitle')} body={text('common.noOpenPeriodsBody')} />
        ) : null}
        <div className="fatfish-periods">
          {periods.data?.items.map((item) => (
            <button
              key={item.id}
              type="button"
              className={periodID === item.id ? 'is-selected' : ''}
              onClick={() => {
                setParams({ period: item.id });
                setBoardNode('');
                setBoardPage(1);
              }}
            >
              <strong>{item.title}</strong>
              <span>
                {item.state === 'open' && !item.paused
                  ? text('common.open2')
                  : text('common.endedOrPaused')}
              </span>
            </button>
          ))}
        </div>
        <SimplePager
          page={page}
          hasMore={Boolean(periods.data?.has_more)}
          onPrev={() => setPage(page - 1)}
          onNext={() => setPage(page + 1)}
          labels={{ previous: text('common.previous'), next: text('common.next') }}
        />
      </section>
      {periodID ? (
        <section>
          {period.isPending ? (
            <LoadingState />
          ) : period.error ? (
            <ErrorState error={period.error} onRetry={() => void period.refetch()} />
          ) : null}
          {period.data ? (
            <>
              <h2>{period.data.title}</h2>
              <p>{period.data.description}</p>
              {period.data.paused ? <p>{text('common.newChallengesArePaused')}</p> : null}
              <div className="fatfish-map-controls">
                <span>
                  {text('common.mapZoom')}: {Math.round(mapZoom * 100)}%
                </span>
                <button
                  type="button"
                  disabled={mapZoom <= 1}
                  onClick={() => setMapZoom(Math.max(1, mapZoom - 0.5))}
                >
                  {text('common.zoomOut')}
                </button>
                <button
                  type="button"
                  disabled={mapZoom >= 3}
                  onClick={() => setMapZoom(Math.min(3, mapZoom + 0.5))}
                >
                  {text('common.zoomIn')}
                </button>
              </div>
              <div className="fatfish-map-viewport">
                <div
                  className="fatfish-map"
                  aria-label={text('common.nodeMap')}
                  style={{ width: `${40 * mapZoom}rem`, height: `${17 * mapZoom}rem` }}
                >
                  <svg
                    className="fatfish-map__edges"
                    viewBox="0 0 100 100"
                    preserveAspectRatio="none"
                    aria-hidden="true"
                  >
                    {edges.map((edge) => {
                      const from = position(edge.from),
                        to = position(edge.to);
                      return (
                        <line
                          key={`${edge.from.id}:${edge.to.id}`}
                          x1={from.x}
                          y1={from.y}
                          x2={to.x}
                          y2={to.y}
                        />
                      );
                    })}
                  </svg>
                  {nodes.map((item) => {
                    const point = position(item);
                    return (
                      <button
                        key={item.id}
                        type="button"
                        className={item.id === nodeID ? 'is-selected' : ''}
                        style={{ left: `${point.x}%`, top: `${point.y}%` }}
                        title={nodeLabel(item, text)}
                        onClick={() => setParams({ period: periodID, node: item.id })}
                        aria-label={nodeLabel(item, text)}
                      >
                        {item.progress.best_stars ? `${item.progress.best_stars}★` : item.order + 1}
                      </button>
                    );
                  })}
                </div>
              </div>
              <ol className="fatfish-node-list">
                {nodes.map((item) => (
                  <li key={item.id}>
                    <button
                      type="button"
                      onClick={() => setParams({ period: periodID, node: item.id })}
                    >
                      {nodeLabel(item, text)}
                    </button>
                    <span>
                      {item.progress.unlocked
                        ? text('common.unlocked')
                        : item.eligible
                          ? text('common.eligible')
                          : text('common.locked')}
                    </span>
                  </li>
                ))}
              </ol>
            </>
          ) : null}
          {nodeID && node.data ? (
            <Card>
              <h3>{nodeLabel(node.data, text)}</h3>
              {node.data.description ? <p>{node.data.description}</p> : null}
              {node.data.condition_hint ? (
                <div className="fatfish-condition">
                  <Condition hint={node.data.condition_hint} />
                </div>
              ) : null}
              {node.data.amounts ? (
                <dl className="fatfish-facts">
                  <dt>{text('common.unlockCost')}</dt>
                  <dd>{node.data.amounts.unlock_cost}</dd>
                  <dt>{text('common.ticketPerChallenge')}</dt>
                  <dd>{node.data.amounts.ticket_price}</dd>
                  <dt>{text('common.firstClearReward')}</dt>
                  <dd>{node.data.amounts.first_clear_reward}</dd>
                  <dt>{text('common.starRewards')}</dt>
                  <dd>{node.data.amounts.star_rewards.join(' / ')}</dd>
                </dl>
              ) : null}
              {node.data.level ? (
                <>
                  <h4>{text('common.initialLayout')}</h4>
                  <FatFishCanvas level={node.data.level} />
                </>
              ) : null}
              <p>
                {text('common.personalBest')}:{' '}
                {formatScoreUnits(node.data.progress.best_score_units)} ·{' '}
                {node.data.progress.best_stars}★
              </p>
              {!active &&
              period.data &&
              canEnter(period.data) &&
              node.data.eligible &&
              !node.data.progress.unlocked &&
              node.data.revision ? (
                <button
                  type="button"
                  className="nb-btn nb-btn--primary"
                  disabled={working}
                  onClick={() => void doUnlock()}
                >
                  {text('common.unlockNode')}
                </button>
              ) : null}
              {!active && period.data && canEnter(period.data) && node.data.progress.unlocked ? (
                <button
                  type="button"
                  className="nb-btn nb-btn--primary"
                  disabled={working || !!playSupportError}
                  onClick={() => void doPrepare()}
                >
                  {text('common.prepareChallengeFree')}
                </button>
              ) : null}
              {playSupportError ? (
                <p role="status">{text('common.thisBrowserCannotSafelySaveAndExclusively')}</p>
              ) : null}
            </Card>
          ) : nodeID && node.isPending ? (
            <LoadingState />
          ) : nodeID && node.error ? (
            <ErrorState error={node.error} onRetry={() => void node.refetch()} />
          ) : null}
          <section>
            <h2>{text('common.leaderboard')}</h2>
            <label>
              {text('common.scope')}{' '}
              <select
                value={boardNode}
                onChange={(event) => {
                  setBoardNode(event.target.value);
                  setBoardPage(1);
                }}
              >
                <option value="">{text('common.periodTotal')}</option>
                {nodes
                  .filter((item) => item.title)
                  .map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.title}
                    </option>
                  ))}
              </select>
            </label>
            {board.isPending ? (
              <LoadingState />
            ) : board.error ? (
              <ErrorState error={board.error} onRetry={() => void board.refetch()} />
            ) : null}
            <ol className="fatfish-board">
              {board.data?.rows.map((row) => (
                <li key={`${row.rank}-${row.achieved_at_ms}-${row.score_units}`}>
                  <span>#{row.rank}</span>
                  <PublicGameIdentity
                    identity={{
                      kind: row.identity.kind === 'public' ? 'public' : 'anonymous',
                      displayName: row.identity.display_name ?? '',
                      avatarURL: row.identity.avatar_url ?? null,
                    }}
                    anonymousLabel={text('common.anonymousPlayer')}
                    isMe={row.is_me}
                    meLabel={text('common.me')}
                  />
                  <strong>{formatScoreUnits(row.score_units)}</strong>
                </li>
              ))}
            </ol>
            <SimplePager
              page={boardPage}
              hasMore={Boolean(board.data && boardPage * board.data.page_size < board.data.total)}
              onPrev={() => setBoardPage(boardPage - 1)}
              onNext={() => setBoardPage(boardPage + 1)}
              labels={{ previous: text('common.previous'), next: text('common.next') }}
            />
          </section>
        </section>
      ) : null}
      <section>
        <h2>{text('common.challengeHistory')}</h2>
        {history.isPending ? (
          <LoadingState />
        ) : history.error ? (
          <ErrorState error={history.error} onRetry={() => void history.refetch()} />
        ) : null}
        {history.data && history.data.items.length === 0 && historyPage === 1 ? (
          <EmptyState title={text('common.noChallengeHistoryTitle')} body={text('common.noChallengeHistoryBody')} />
        ) : null}
        <ol className="fatfish-history">
          {history.data?.items.map((item) => (
            <li key={item.id}>
              <code>{item.id}</code>
              <strong>
                {item.result?.passed ? text('common.passed') : text('common.notPassed')}
              </strong>
              <span>
                {item.result?.stars ?? 0}★ · {formatScoreUnits(item.result?.score_units ?? '0')}
              </span>
              <span>
                {text('common.ticket')}: {item.result?.ticket_charge ?? item.ticket_price} ·{' '}
                {text('common.refund')}: {item.result?.ticket_refund ?? '0'} ·{' '}
                {text('common.rewards')}: {item.result?.rewards ?? '0'}
              </span>
            </li>
          ))}
        </ol>
        <SimplePager
          page={historyPage}
          hasMore={Boolean(history.data?.has_more)}
          onPrev={() => setHistoryPage(historyPage - 1)}
          onNext={() => setHistoryPage(historyPage + 1)}
          labels={{ previous: text('common.previous'), next: text('common.next') }}
        />
      </section>
    </div>
  );
}
export function FatFishActivityPage() {
  const account = useUserSession().data?.user.id ?? '';
  return (
    <UserPageGate>
      <Content key={account} account={account} />
    </UserPageGate>
  );
}
