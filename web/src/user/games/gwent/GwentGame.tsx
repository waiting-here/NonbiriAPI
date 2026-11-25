import { useEffect, useEffectEvent, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { ApiError } from '@shared/query/http';
import type { Match } from '../companion/api';
import { useUserSession } from '../../data';
import { useAuthoritativeCountdown } from '../common/countdown';
import { useDuel, readHistory, readDetail, readRounds, type DuelIntent } from '../common/duel/api';
import { DuelDialog } from '../common/duel/Dialog';
import type { DuelLobbyContext, Page, DuelRound } from '../common/duel/types';
import { useDuelText } from '../common/duel/copy';
import { spendableGameCredits } from '../common/spendable';
import { formatCredits } from '../common/strict';
import { RandomnessProof } from '../common/RandomnessProof';
import { GwentLeaderboard } from './Leaderboard';
import { gwentCodec, type Deck, type Action, type View, type Round } from './types';
import { useGwentText } from './copy';
import '../common/duel/duel.css';
import './gwent.css';

export interface GwentCompanion {
  match?: Match;
  busy: boolean;
  readOnly?: boolean;
  opponent: string;
  onStart?: (deck: Deck) => void;
  onAction?: (action: Action) => void;
  onAbandon?: () => void;
  onRefresh?: () => void;
}
const channel = 'nonbiri.gwent';
export function GwentGame({
  config,
  wallets,
  accepting,
  refreshWallets,
  companion,
}: DuelLobbyContext & { companion?: GwentCompanion }) {
  const t = useGwentText(),
    text = useDuelText(),
    navigate = useNavigate();
  const account = useUserSession(false);
  const duel = useDuel(gwentCodec, refreshWallets, !companion);
  const frame = useRef<HTMLIFrameElement>(null);
  const readSequence = useRef(0);
  const lastIntent = useRef<Extract<DuelIntent, { kind: 'queue' }> | null>(null);
  const [frameVersion, setFrameVersion] = useState(0);
  const [frameStatus, setFrameStatus] = useState<{
    accountID: string | null;
    status: 'mounted' | 'failed';
  } | null>(null);
  const [rematchFor, setRematchFor] = useState<{ accountID: string | null } | null>(null);
  const [height, setHeight] = useState(1),
    [rankings, setRankings] = useState(false),
    [bridgeError, setBridgeError] = useState<string | null>(null),
    [proof, setProof] = useState<{
      id: string;
      terminal: boolean;
      accountID: string | null;
    } | null>(null);
  const home = companion ? undefined : duel.query.data,
    current = home?.current,
    queue = home?.queue;
  const remaining = useAuthoritativeCountdown(
    current ? `${current.id}:${current.phaseSeq}` : (queue?.id ?? 'idle'),
    current?.deadline ?? queue?.deadline ?? null,
    home?.serverNow ?? 0,
    duel.refresh,
  );
  const error = companion ? null : (duel.error ?? duel.query.error);
  const feedback = duel.uncertain
    ? text('common.theResponseIsUnconfirmedRetryTheSame')
    : error instanceof ApiError && error.code === 'insufficient_credits'
      ? text('common.insufficientAvailableCreditsCheckYourWallets')
      : error
        ? text('common.aValidResponseCouldNotBeObtained')
        : null;
  const snapshot = {
    accountID: account.data?.user.id ?? null,
    config,
    home: companion
      ? {
          current:
            companion.match && 'self' in companion.match.view
              ? {
                  id: companion.match.id,
                  phase: companion.match.phase,
                  phaseSeq: companion.match.decision_id,
                  decisionID: companion.match.decision_id,
                  you: companion.match.seat,
                  profiles: [0, 1].map((seat) =>
                    seat === companion.match!.seat
                      ? { kind: 'anonymous' }
                      : { kind: 'ai', displayName: companion.opponent },
                  ),
                  view: {
                    ...companion.match.view,
                    ...(!companion.readOnly && companion.match.decision_id
                      ? {}
                      : { legal_actions: [], choice: null }),
                  },
                }
              : null,
          queue: null,
          latestResult: null,
        }
      : (home ?? null),
    companion: !!companion,
    readOnly: companion?.readOnly,
    accepting,
    blocked: companion?.busy ?? duel.blocked,
    uncertain: duel.uncertain,
    canRematch: rematchFor?.accountID === (account.data?.user.id ?? null),
    remaining,
    error: feedback ?? bridgeError,
    availableCredits: formatCredits(spendableGameCredits(wallets).total),
  };
  const send = (message: Record<string, unknown>) =>
    frame.current?.contentWindow?.postMessage({ channel, ...message }, location.origin);
  const viewport = () => {
    const rect = frame.current?.getBoundingClientRect();
    const headerBottom =
      document.querySelector('.nb-user-header')?.getBoundingClientRect().bottom ?? 0;
    const visibleTop = Math.max(0, headerBottom, rect?.top ?? 0);
    const visibleBottom = Math.min(innerHeight, rect?.bottom ?? innerHeight);
    return {
      top: Math.max(0, visibleTop - (rect?.top ?? 0)),
      height: Math.max(1, visibleBottom - visibleTop),
      screenHeight: innerHeight,
    };
  };
  const publish = useEffectEvent(() =>
    send({ type: 'snapshot', snapshot: { ...snapshot, viewport: viewport() } }),
  );
  useEffect(() => {
    publish();
  });
  const onMessage = useEffectEvent((event: MessageEvent, controller: AbortController) => {
    if (
      event.origin !== location.origin ||
      event.source !== frame.current?.contentWindow ||
      event.data?.channel !== channel
    )
      return;
    const message = event.data;
    if (message.type === 'failed') {
      setFrameStatus({ accountID: snapshot.accountID, status: 'failed' });
      return;
    }
    if (message.type === 'ready' || message.type === 'mounted') {
      if (message.type === 'mounted')
        setFrameStatus({ accountID: snapshot.accountID, status: 'mounted' });
      publish();
      return;
    }
    if (message.type === 'height') {
      if (Number.isFinite(message.height) && message.height > 0 && message.height < 100000)
        setHeight(message.height);
      return;
    }
    if (message.type === 'back') {
      navigate('/games');
      return;
    }
    if (message.type === 'cancel-read') {
      readSequence.current++;
      return;
    }
    if (message.type === 'proof') {
      setProof({ id: message.id, terminal: message.terminal, accountID: snapshot.accountID });
      return;
    }
    if (message.type === 'rankings') {
      setRankings(true);
      return;
    }
    if (message.type === 'companion-entry' && !companion) {
      navigate('?opponent=companion');
      return;
    }
    if (companion) {
      if (message.type === 'retry') companion.onRefresh?.();
      if (!companion.busy && !companion.readOnly) {
        if (message.type === 'companion-start') companion.onStart?.(message.deck);
        if (companion.match && message.id === companion.match.id) {
          if (
            message.type === 'action' &&
            companion.match.decision_id &&
            message.decisionID === companion.match.decision_id
          )
            companion.onAction?.(message.action);
          if (message.type === 'surrender') companion.onAbandon?.();
        }
      }
      return;
    }
    if (message.type === 'retry') {
      if (duel.uncertain) duel.retry();
      else duel.refresh();
      return;
    }
    if (message.type === 'queue') {
      if (message.mode !== 'standard') return;
      const displayedHash = config.modes.standard?.termsHash;
      if (!displayedHash || message.termsHash !== displayedHash) {
        setBridgeError(
          t(
            '对局条款已更新，请确认当前门票后重试。',
            'Match terms changed. Review the current ticket and try again.',
          ),
        );
        duel.refresh();
        return;
      }
      setBridgeError(null);
      setRematchFor({ accountID: snapshot.accountID });
      lastIntent.current = {
        kind: 'queue',
        mode: 'standard',
        termsHash: displayedHash,
        loadout: message.deck as Deck,
      };
      duel.run(lastIntent.current);
      return;
    }
    if (message.type === 'rematch' && lastIntent.current) {
      duel.run(lastIntent.current);
      return;
    }
    if (message.type === 'cancel' && queue) {
      duel.run({ kind: 'cancel', id: queue.id, revision: queue.revision });
      return;
    }
    if (
      message.type === 'action' &&
      current &&
      message.id === current.id &&
      (current.decisionID
        ? message.decisionID === current.decisionID
        : !message.decisionID && message.phaseSeq === current.phaseSeq)
    ) {
      duel.run({
        kind: 'action',
        id: current.id,
        phaseSeq: current.phaseSeq,
        decisionID: current.decisionID,
        action: message.action as Action,
      });
      return;
    }
    if (
      message.type === 'surrender' &&
      current &&
      message.id === current.id &&
      message.phaseSeq === current.phaseSeq
    ) {
      duel.run({ kind: 'surrender', id: current.id, phaseSeq: current.phaseSeq });
      return;
    }
    if (message.type === 'history') {
      const readID = ++readSequence.current;
      void readHistory(gwentCodec, message.cursor ?? null, controller.signal)
        .then((page) => {
          if (!controller.signal.aborted && readID === readSequence.current)
            send({ type: 'history', page });
        })
        .catch(() => {
          if (!controller.signal.aborted && readID === readSequence.current)
            send({
              type: 'read-error',
              message: t('对局记录未能加载，请重试。', 'Could not load match history. Try again.'),
            });
        });
    }
    if (message.type === 'replay') {
      const readID = ++readSequence.current;
      void (async () => {
        try {
          const detail = await readDetail(gwentCodec, message.id, controller.signal);
          if (controller.signal.aborted || readID !== readSequence.current) return;
          const rounds: DuelRound<View, Round, never>[] = [];
          let cursor: string | null = null;
          do {
            const page: Page<DuelRound<View, Round, never>> = await readRounds(
              gwentCodec,
              message.id,
              false,
              cursor,
              controller.signal,
            );
            if (controller.signal.aborted || readID !== readSequence.current) return;
            rounds.push(...page.items);
            cursor = page.nextCursor;
          } while (cursor);
          if (controller.signal.aborted || readID !== readSequence.current) return;
          send({
            type: 'replay',
            replay: { result: detail.result, initial: detail.initial, rounds },
          });
        } catch {
          if (controller.signal.aborted || readID !== readSequence.current) return;
          send({
            type: 'read-error',
            message: t('回放未能加载，请重试。', 'Could not load the replay. Try again.'),
          });
        }
      })();
    }
  });
  useEffect(() => {
    lastIntent.current = null;
    const controller = new AbortController();
    const listener = (event: MessageEvent) => onMessage(event, controller);
    window.addEventListener('message', listener);
    return () => {
      controller.abort();
      window.removeEventListener('message', listener);
    };
  }, [snapshot.accountID]);
  useEffect(() => {
    const update = () => send({ type: 'viewport', viewport: viewport() });
    const visibility = () =>
      send({ type: 'visibility', visible: document.visibilityState === 'visible' });
    window.addEventListener('resize', update);
    window.addEventListener('scroll', update, { passive: true, capture: true });
    document.addEventListener('visibilitychange', visibility);
    return () => {
      window.removeEventListener('resize', update);
      window.removeEventListener('scroll', update, { capture: true });
      document.removeEventListener('visibilitychange', visibility);
    };
  }, []);
  return (
    <div className="gwent-original">
      {!(frameStatus?.accountID === snapshot.accountID && frameStatus.status === 'mounted') && (
        <div className="gwent-frame-fallback">
          <Link to="/games">← {t('返回游戏中心', 'Back to game center')}</Link>
          <p role="status">
            {frameStatus?.status === 'failed'
              ? t('卡牌界面未能加载。', 'Could not load the card interface.')
              : t('正在加载卡牌界面…', 'Loading the card interface…')}
          </p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => {
              setFrameStatus(null);
              setFrameVersion((value) => value + 1);
            }}
          >
            {t('重新加载', 'Reload')}
          </button>
        </div>
      )}
      <iframe
        key={`${snapshot.accountID ?? 'guest'}:${frameVersion}`}
        ref={frame}
        className="gwent-original-frame"
        src="/assets/gwent/interface/index.html"
        title={t('AI昆特牌竞技场', 'AI Gwent Arena')}
        style={{ height }}
        sandbox="allow-scripts allow-same-origin allow-downloads allow-popups allow-top-navigation-by-user-activation"
        onError={() => setFrameStatus({ accountID: snapshot.accountID, status: 'failed' })}
        onLoad={() => send({ type: 'snapshot', snapshot: { ...snapshot, viewport: viewport() } })}
      />
      {proof && proof.accountID === snapshot.accountID && (
        <DuelDialog
          className="gwent-host-dialog"
          title={text('common.verifyRandomness')}
          onClose={() => setProof(null)}
        >
          <RandomnessProof game="gwent" id={proof.id} terminal={proof.terminal} />
        </DuelDialog>
      )}
      {rankings && (
        <DuelDialog
          className="gwent-host-dialog"
          title={t('胜场榜', 'Wins leaderboard')}
          onClose={() => setRankings(false)}
        >
          <GwentLeaderboard />
        </DuelDialog>
      )}
    </div>
  );
}
