import { useCallback, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { GameWallets } from '../common/GameWallets';
import { RandomnessProof } from '../common/RandomnessProof';
import { GamePayment } from '../common/GamePayment';
import { gameRequest } from '../common/request';
import { useAuthoritativeCountdown } from '../common/countdown';
import { useDuel } from '../common/duel/api';
import { DuelDialog } from '../common/duel/Dialog';
import { DuelFeedback } from '../common/duel/Feedback';
import { DuelFinance, DuelTerms } from '../common/duel/Finance';
import { DuelHistory, DuelRoundLog } from '../common/duel/History';
import type { DuelLobbyContext, DuelResult, Seat } from '../common/duel/types';
import { useDuelText } from '../common/duel/copy';
import { creditsToMilli } from '../common/strict';
import { likesCatalog, type ModeCatalog } from './catalog';
import { assertArtCoverage, characterSlot } from './art';
import { LikesArt } from './LikesArt';
import { Arena, CompactScores } from './Arena';
import { Glossary } from './Glossary';
import { LoadoutEditor } from './Loadout';
import { initialSelection, selectionProblem } from './selection';
import { LikesRoundLog } from './Log';
import { useReducedMotion, useServerClock } from './motion';
import { likesCodec } from './normalize';
import { PlanEditor } from './PlanEditor';
import { skillName } from './labels';
import type { LikesView, Presentation, Selection } from './types';
import '../games.css';
import '../common/duel/duel.css';
import './likes.css';
import './effects.css';

function Rules({
  catalog,
  onClose,
}: {
  readonly catalog: ModeCatalog;
  readonly onClose: () => void;
}) {
  const t = useDuelText(),
    p = catalog.parameters;
  return (
    <DuelDialog
      title={t('点赞大战 · 对战规则', 'Likes Battle · Rules')}
      onClose={onClose}
      className="likes-glossary"
    >
      <h3>{t('同时出招，争取点赞', 'Choose together. Compete for likes.')}</h3>
      <p>
        {t('每轮双方有', 'Both players have')} {p.TURN_SECONDS}{' '}
        {t(
          '秒选择购物、主技能和允许的额外技能。必须选择主招；只有购物后仍眩晕才可跳过。确认后不可更改，双方锁定或超时后共同揭示；未锁定的一方自动跳过出招。',
          'seconds to choose purchases, a main skill and any permitted extra skill. A main skill is required unless you remain stunned after shopping. Confirming locks your plan. Plans are revealed when both lock or time expires; an unlocked player automatically skips casting.',
        )}
      </p>
      <p>
        {t('先达到', 'Reach')} {p.TARGET_LIKES}{' '}
        {t('赞争取胜利，最多', 'likes to compete for victory, with at most')} {p.MAX_ROUNDS}{' '}
        {t(
          '轮。双方同轮达标按实际总赞数比较；达到轮数上限也比较总赞数，相同为平局。',
          'rounds. If both reach the target together, their final totals decide the winner. The round limit also compares totals; equal totals draw.',
        )}
      </p>
      <h3>{t('五秒结算', 'Five-second settlement')}</h3>
      <p>
        {t(
          '方案揭示 → 购物充电 → 费用与过载 → 净化与Buff → 得赞 → 追加效果 → 轮末变化。双方同步展示，结束后开始新的完整20秒。资源补充与结果均以服务端记录为准。',
          'Plans → shopping and charge → payment and overload → cleansing and buffs → likes → follow-ups → round end. Both sides display together, followed by a fresh twenty seconds. Resource changes and results follow server records.',
        )}
      </p>
      <h3>{t('共享电能与过载', 'Shared energy and overload')}</h3>
      <p>
        {t(
          '共同购物结束后比较双方冻结方案的总耗电与电池余量。总需求超过余量时，仅耗电大于零的一方获得过载；另一方若零耗电，仍正常执行。双方都耗电则双方过载。恰好耗尽不算过载。Flash连答独立检查，不因失败额外施加过载。',
          'After both players shop, their frozen energy quotes are compared with the battery. When demand exceeds the battery, only players quoting positive energy overload. A zero-energy player still acts normally. If both quote positive energy, both overload. Using exactly the remaining energy is allowed. Flash follow-ups are checked separately; a failed follow-up does not add overload.',
        )}
      </p>
      <h3>{t('角色与配装', 'Characters and loadouts')}</h3>
      <p>
        {t(
          '每位角色拥有自身技能与公共技能。基础四槽，Harness可改变额外槽与固定被动；空槽可保留，至少携带一项可持续得赞的稳定技能。对手未使用的技能在对局中隐藏，终局完整公开。角色、模型、API、token与资源均为游戏设定。',
          'Each character has their own skills and public skills. Start with four slots; harnesses add slots or fixed passives. Empty slots are allowed, but include a sustainable stable scoring skill. Unused opponent skills stay concealed until the game ends. Characters, models, APIs, tokens and resources here are game mechanics.',
        )}
      </p>
      <h3>{t('订阅、图像与API', 'Subscriptions, images and API reserve')}</h3>
      <p>
        {t(
          '订阅瞬发和总量使用各自重置时钟，图像额度与订阅总量同步补充；API余量不自动重置。升级订阅可以扩充额度。每轮开始符合条件的补充会直接显示，阅读词条与日志不暂停计时。',
          'Subscription burst and total quota have separate reset clocks; image quota replenishes with the total quota. API reserve does not reset automatically. Subscription upgrades expand quotas. Eligible replenishment appears at round start. Reading the guide or log never pauses the clock.',
        )}
      </p>
    </DuelDialog>
  );
}
function Outcomes({ result }: { readonly result: DuelResult<LikesView, Presentation> }) {
  const t = useDuelText();
  return (
    <section className="likes-outcome">
      <div className="likes-outcome-art">
        {result.view?.players.map((player, seat) => {
          const pose =
            result.outcome === 'system_cancelled'
              ? 'portrait'
              : result.outcome === 'draw'
                ? 'draw'
                : (result.outcome === 'win') === (seat === result.you)
                  ? 'win'
                  : 'loss';
          return (
            <div key={seat}>
              <LikesArt
                slot={characterSlot(player.role, pose)}
                label={`${player.role} ${pose === 'win' ? t('胜利', 'Victory') : pose === 'loss' ? t('失败', 'Defeat') : pose === 'draw' ? t('平局', 'Draw') : ''}`}
              />
              <strong>{player.role}</strong>
            </div>
          );
        })}
      </div>
      <DuelFinance result={result} />
    </section>
  );
}
function Lobby({
  catalog,
  context,
  blocked,
  onQueue,
  onInspect,
}: {
  readonly catalog: ModeCatalog;
  readonly context: DuelLobbyContext;
  readonly blocked: boolean;
  readonly onQueue: (selection: Selection) => void;
  readonly onInspect: (id: string) => void;
}) {
  const t = useDuelText();
  const [selection, setSelection] = useState(() => initialSelection(catalog));
  const mode = context.config.modes[catalog.mode],
    enough =
      creditsToMilli(context.wallets.balance) + creditsToMilli(context.wallets.gameBalance) >=
      creditsToMilli(mode.ticket);
  const unavailable = !context.accepting || !context.config.available || !mode.available;
  return (
    <>
      <LoadoutEditor
        catalog={catalog}
        value={selection}
        onChange={setSelection}
        disabled={blocked}
        onInspect={onInspect}
      />
      <DuelTerms mode={mode} />
      <div className="likes-enqueue">
        <span>
          {catalog.mode === 'quick' ? t('快速模式', 'Quick mode') : t('标准模式', 'Standard mode')}{' '}
          · {catalog.parameters.TARGET_LIKES} ♥ · {catalog.parameters.MAX_ROUNDS}{' '}
          {t('轮上限', 'round limit')}
        </span>
        <button
          type="button"
          className="likes-primary"
          disabled={blocked || unavailable || !enough || !!selectionProblem(catalog, selection)}
          onClick={() => onQueue(selection)}
        >
          {unavailable
            ? t('暂时无法入场', 'Entry unavailable')
            : !enough
              ? t('可用积分不足', 'Insufficient credits')
              : t('支付票价并匹配', 'Pay entry and find a match')}
        </button>
      </div>
    </>
  );
}
export function LikesGame(context: DuelLobbyContext) {
  const t = useDuelText();
  const duel = useDuel(likesCodec, context.refreshWallets);
  const catalogQuery = useQuery({
    queryKey: ['user', 'games', 'likes', 'catalog'],
    queryFn: async ({ signal }) => {
      const c = likesCatalog(
        (
          await gameRequest<unknown>('/api/games/likes/catalog', {
            signal,
            maxResponseBytes: 1024 * 1024,
            expectedStatuses: [200],
          })
        ).data,
      );
      assertArtCoverage(c.modes.quick);
      assertArtCoverage(c.modes.standard);
      return c;
    },
    retry: false,
    staleTime: Infinity,
  });
  const [mode, setMode] = useState<'quick' | 'standard'>('quick');
  const [rules, setRules] = useState(false),
    [guide, setGuide] = useState<string | null>(null),
    [history, setHistory] = useState(false),
    [log, setLog] = useState(false),
    [surrender, setSurrender] = useState(false);
  const closeRules = useCallback(() => setRules(false), []),
    closeGuide = useCallback(() => setGuide(null), []),
    closeHistory = useCallback(() => setHistory(false), []),
    closeLog = useCallback(() => setLog(false), []);
  const home = duel.query.data,
    current = home?.current,
    queue = home?.queue,
    result = home?.latestResult;
  const reduced = useReducedMotion();
  const now = useServerClock(
    home?.serverNow ?? 0,
    reduced,
    !!current ||
      !!queue ||
      (!!result?.resolution && (home?.serverNow ?? 0) < result.resolution.endsAt),
  );
  const remaining = useAuthoritativeCountdown(
    current ? `${current.id}:${current.phaseSeq}` : (queue?.id ?? 'idle'),
    current?.deadline ?? queue?.deadline ?? null,
    home?.serverNow ?? 0,
    duel.refresh,
  );
  const activeMode = current?.mode ?? queue?.mode ?? mode;
  const c = catalogQuery.data?.modes[activeMode === 'standard' ? 'standard' : 'quick'];
  const compatible = !current || c?.contentHash === current.contentHash;
  const terminalPresentation =
    !current && !queue && result?.resolution && now < result.resolution.endsAt && result.view;
  const logSession = current?.id ?? result?.id,
    logSeat = current?.you ?? result?.you ?? 0;
  return (
    <div className="likes-game">
      <header className="likes-heading">
        <div>
          <span className="likes-eyebrow">LIKES // DUEL</span>
          <h1>{t('点赞大战', 'Likes Battle')}</h1>
          <p>
            {t(
              '共享电池，独立选择，同时爆发。',
              'One battery. Independent choices. A simultaneous reveal.',
            )}
          </p>
        </div>
        <div className="duel-actions">
          <button type="button" disabled={!c} onClick={() => setRules(true)}>
            {t('规则', 'Rules')}
          </button>
          <button type="button" disabled={!c} onClick={() => setGuide('')}>
            {t('词条手册', 'Field guide')}
          </button>
          <button type="button" disabled={!c} onClick={() => setHistory(true)}>
            {t('对局记录', 'Game history')}
          </button>
        </div>
      </header>
      <GameWallets wallets={context.wallets} />
      <RandomnessProof
        game="likes"
        id={current?.id ?? result?.id}
        terminal={!current && !!result}
      />
      <DuelFeedback
        error={duel.error ?? duel.query.error ?? catalogQuery.error}
        uncertain={duel.uncertain}
        pending={duel.pending || duel.query.isPending || catalogQuery.isPending}
        onRetry={
          duel.uncertain
            ? duel.retry
            : () => {
                duel.refresh();
                void catalogQuery.refetch();
              }
        }
      />
      {!compatible && (
        <p role="alert">
          {t(
            '规则内容尚未同步。请刷新后继续；对局计时仍在进行。',
            'The rules have not synced. Refresh to continue; the game clock is still running.',
          )}
        </p>
      )}
      {c && compatible && (
        <>
          {current ? (
            <>
              <div className="likes-phase">
                <strong>
                  {t('第', 'Round')} {current.round}/{c.parameters.MAX_ROUNDS} {t('轮', '')}
                </strong>
                <span>
                  {current.phase === 'settlement'
                    ? t('共同结算', 'Settlement')
                    : t('共同选招', 'Choose skills')}
                </span>
                <strong className={(remaining ?? 0) <= 5 ? 'is-urgent' : ''}>
                  {remaining ?? '—'}s
                </strong>
                <button type="button" onClick={() => setLog(true)}>
                  {t('结算日志', 'Round log')}
                </button>
                <CompactScores
                  view={current.view}
                  resolution={current.resolution}
                  roundStart={current.roundStart}
                  now={now}
                  reduced={reduced}
                  you={current.you}
                />
              </div>
              {current.round === 1 && current.phase === 'plan' && (
                <div className="likes-matched">
                  {current.view.players.map((p, seat) => (
                    <LikesArt key={seat} slot={characterSlot(p.role, 'portrait')} label={p.role} />
                  ))}
                  <span>
                    {t('匹配成功', 'MATCH FOUND')}
                    <small>
                      {current.view.players[0].role} × {current.view.players[1].role}
                    </small>
                  </span>
                </div>
              )}
              <Arena
                catalog={c}
                view={current.view}
                profiles={current.profiles}
                you={current.you}
                round={current.round}
                locked={current.locked}
                resolution={current.resolution}
                roundStart={current.roundStart}
                now={now}
                reduced={reduced}
                onInspect={setGuide}
              />
              {current.phase === 'plan' && (
                <PlanEditor
                  key={`${current.id}:${current.phaseSeq}`}
                  catalog={c}
                  state={current}
                  blocked={duel.blocked || remaining === 0}
                  onInspect={setGuide}
                  onLock={(plan) =>
                    duel.run({
                      kind: 'action',
                      id: current.id,
                      phaseSeq: current.phaseSeq,
                      action: { kind: 'plan', plan },
                    })
                  }
                />
              )}
              <div className="duel-actions">
                <button type="button" disabled={duel.blocked} onClick={() => setSurrender(true)}>
                  {t('认输', 'Surrender')}
                </button>
                <span>
                  {t('本局投入', 'Your entry')}: <GamePayment payment={current.payment} />
                </span>
              </div>
            </>
          ) : queue ? (
            <section className="likes-queue">
              <h2>{t('正在匹配对手', 'Finding your opponent')}</h2>
              {queue.loadout && (
                <>
                  <LikesArt
                    slot={characterSlot(queue.loadout.role, 'portrait')}
                    label={queue.loadout.role}
                  />
                  <strong>{queue.loadout.role}</strong>
                  <p>{queue.loadout.skills.map((id) => skillName(c, id)).join(' · ')}</p>
                </>
              )}
              <p>
                {t('排队剩余', 'Queue time left')}: {remaining ?? '—'}s
              </p>
              <GamePayment payment={queue.payment} />
              <p>
                {t(
                  '排队期间配装已冻结；取消后可修改。',
                  'Your loadout is frozen while queued. Cancel to edit it.',
                )}
              </p>
              <button
                type="button"
                disabled={duel.blocked}
                onClick={() => duel.run({ kind: 'cancel', id: queue.id, revision: queue.revision })}
              >
                {t('取消排队并退款', 'Cancel queue and refund')}
              </button>
            </section>
          ) : terminalPresentation && result?.view && result.resolution ? (
            <>
              <div className="likes-phase">
                <strong>{t('最后一轮结算', 'Final settlement')}</strong>
                <span>{Math.max(0, Math.ceil(result.resolution.endsAt - now))}s</span>
              </div>
              <Arena
                catalog={
                  catalogQuery.data!.modes[result.mode === 'standard' ? 'standard' : 'quick']
                }
                view={result.view}
                profiles={result.profiles}
                you={result.you}
                round={result.resolution.round}
                locked={[true, true]}
                resolution={result.resolution}
                roundStart={null}
                now={now}
                reduced={reduced}
                onInspect={setGuide}
              />
            </>
          ) : (
            <>
              {result && (
                <>
                  <Outcomes result={result} />
                  <button type="button" onClick={() => setLog(true)}>
                    {t('查看结算日志', 'View round log')}
                  </button>
                </>
              )}
              <div className="likes-modes" role="group" aria-label={t('模式', 'Mode')}>
                {(['quick', 'standard'] as const).map((m) => (
                  <button
                    key={m}
                    type="button"
                    aria-pressed={mode === m}
                    disabled={duel.pending || duel.uncertain}
                    onClick={() => setMode(m)}
                  >
                    <strong>{m === 'quick' ? t('快速', 'Quick') : t('标准', 'Standard')}</strong>
                    <span>
                      {catalogQuery.data!.modes[m].parameters.TARGET_LIKES} ♥ ·{' '}
                      {catalogQuery.data!.modes[m].parameters.MAX_ROUNDS} {t('轮', 'rounds')}
                    </span>
                  </button>
                ))}
              </div>
              <Lobby
                key={mode}
                catalog={c}
                context={context}
                blocked={duel.blocked}
                onInspect={setGuide}
                onQueue={(selection) =>
                  duel.run({
                    kind: 'queue',
                    mode,
                    termsHash: context.config.modes[mode].termsHash,
                    loadout: selection,
                  })
                }
              />
            </>
          )}
        </>
      )}
      {rules && c && <Rules catalog={c} onClose={closeRules} />}
      {guide !== null && c && (
        <Glossary key={guide} catalog={c} initial={guide || undefined} onClose={closeGuide} />
      )}
      {history && catalogQuery.data && (
        <DuelHistory
          codec={likesCodec}
          onClose={closeHistory}
          renderRound={(round, you, meta) => {
            const catalog =
              catalogQuery.data.modes[meta.mode === 'standard' ? 'standard' : 'quick'];
            return catalog.contentHash === meta.contentHash ? (
              <LikesRoundLog round={round} you={you} catalog={catalog} />
            ) : (
              <p>
                {t('无法取得匹配版本的规则词条。', 'The matching rule catalog is unavailable.')}
              </p>
            );
          }}
          renderDetail={(detail) =>
            catalogQuery.data.modes[detail.result.mode === 'standard' ? 'standard' : 'quick']
              .contentHash !== detail.contentHash ? (
              <p>
                {t('无法取得匹配版本的规则词条。', 'The matching rule catalog is unavailable.')}
              </p>
            ) : (
              <div className="likes-history-loadouts">
                {detail.result.view?.players.map((player, seat) => (
                  <section key={seat}>
                    <h3>{player.role}</h3>
                    <p>
                      {player.loadout
                        ?.map((id) =>
                          skillName(
                            catalogQuery.data.modes[
                              detail.result.mode === 'standard' ? 'standard' : 'quick'
                            ],
                            id,
                          ),
                        )
                        .join(' · ')}
                    </p>
                  </section>
                ))}
              </div>
            )
          }
        />
      )}
      {log && logSession && c && (
        <DuelDialog
          title={t('结算日志', 'Round log')}
          onClose={closeLog}
          className="likes-glossary"
        >
          <div className="likes-log-return">
            <span>
              {current ? t('对局继续计时', 'The game clock continues') : t('已结束', 'Completed')}{' '}
              {current ? `${remaining ?? '—'}s` : ''}
            </span>
            <button type="button" onClick={closeLog}>
              {t('返回对战', 'Return to battle')}
            </button>
          </div>
          <DuelRoundLog
            key={logSession}
            codec={likesCodec}
            id={logSession}
            active={!!current}
            you={logSeat as Seat}
            renderRound={(round, you) => (
              <LikesRoundLog
                round={round}
                you={you}
                catalog={
                  current
                    ? c
                    : catalogQuery.data!.modes[result?.mode === 'standard' ? 'standard' : 'quick']
                }
              />
            )}
          />
        </DuelDialog>
      )}
      <ConfirmDialog
        open={surrender && !!current}
        title={t('确认认输', 'Confirm surrender')}
        description={t(
          '认输将立即判负，入场积分不退还。',
          'Surrender immediately concedes the game without an entry refund.',
        )}
        confirmLabel={t('确认认输', 'Surrender')}
        cancelLabel={t('继续对战', 'Keep playing')}
        danger
        busy={duel.pending}
        onCancel={() => setSurrender(false)}
        onConfirm={() => {
          if (current) duel.run({ kind: 'surrender', id: current.id, phaseSeq: current.phaseSeq });
          setSurrender(false);
        }}
      />
    </div>
  );
}
