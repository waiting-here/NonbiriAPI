import { lazy, Suspense, useCallback, useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { GameWallets } from '../common/GameWallets';
import { OnboardingCard } from '../common/OnboardingCard';
import { RandomnessProof } from '../common/RandomnessProof';
import { GamePayment } from '../common/GamePayment';
import { gameRequest } from '../common/request';
import { useAuthoritativeCountdown } from '../common/countdown';
import { useDuel } from '../common/duel/api';
import { DuelDialog } from '../common/duel/Dialog';
import { DuelFeedback } from '../common/duel/Feedback';
import { entryMessage, entryProblem } from '../common/duel/availability';
import { DuelFinance, DuelTerms } from '../common/duel/Finance';
import { DuelHistory, DuelRoundLog } from '../common/duel/History';
import type { DuelLobbyContext, DuelResult, Seat } from '../common/duel/types';
import { useDuelText } from '../common/duel/copy';
import { creditsToMilli } from '../common/strict';
import { spendableGameCredits } from '../common/spendable';
import { likesCatalog, matchingCatalog, type ModeCatalog } from './catalog';
import { CharacterPassive } from './CharacterPassive';
import { assertArtCoverage, characterSlot } from './art';
import { LikesArt } from './LikesArt';
import { Arena, CompactScores } from './Arena';
import { Glossary } from './Glossary';
import { LoadoutEditor } from './Loadout';
import { CustomPresets } from './CustomPresets';
import { initialSelection, selectionProblem } from './selection';
import { LikesRoundLog } from './Log';
import { useReducedMotion, useServerClock } from './motion';
import { likesCodec } from './normalize';
import { PlanEditor } from './PlanEditor';
import { useActionWarning } from './useActionWarning';
import { skillName } from './labels';
import { likesAudioFacts, likesMusicScene, type MusicScene } from './audioFacts';
import { tutorialStatus, saveTutorialStatus, tutorialStorageKey } from './tutorial/storage';
import { useArcadeAudio } from '../common/audio/useArcadeAudio';
import { useSnapshotAudioFacts } from '../common/audio/useSnapshotAudioFacts';
import { BattleAtmosphere } from './BattleAtmosphere';
import { ArcadeAudioControls } from '../common/audio/ArcadeAudioControls';
import type { LikesView, Presentation, Selection } from './types';
import '../games.css';
import '../common/duel/duel.css';
import './likes.css';
import './effects.css';
import './desktop.css';
import './guidance.css';
const Tutorial = lazy(() => import('./tutorial/Tutorial'));
function Rules({
  catalog,
  onClose,
}: {
  readonly catalog: ModeCatalog;
  readonly onClose: () => void;
}) {
  const text = useDuelText(),
    p = catalog.parameters;
  return (
    <DuelDialog
      title={text('likes.turnBasedBattleMinigameTestRules')}
      onClose={onClose}
      className="likes-glossary"
    >
      <h3>{text('likes.chooseTogetherCompeteForLikes')}</h3>
      <p>
        {text('likes.bothPlayersHave')} {p.TURN_SECONDS}{' '}
        {text('likes.secondsToChoosePurchasesAMainSkill')}
      </p>
      <p>
        {text('likes.reach')} {p.TARGET_LIKES} {text('likes.likesToCompeteForVictoryWithAt')}{' '}
        {p.MAX_ROUNDS} {text('likes.roundsIfBothReachTheTargetTogether')}
      </p>
      <h3>{text('likes.stepByStepResolution')}</h3>
      <p>{text('likes.plansShoppingAndChargePaymentAndOverload')}</p>
      <h3>{text('likes.sharedEnergyAndOverload')}</h3>
      <p>{text('likes.afterBothPlayersShopTheirFrozenEnergy')}</p>
      <h3>{text('likes.charactersAndLoadouts')}</h3>
      <p>{text('likes.eachCharacterHasTheirOwnSkillsAnd')}</p>
      {catalog.roles.some((role) => role.passive) && (
        <h3>{text('likes.alwaysActiveCharacterPassives')}</h3>
      )}
      {catalog.roles.map((role) => (
        <CharacterPassive key={role.id} role={role} />
      ))}
      {catalog.roles.some((role) => role.passive) && (
        <p>{text('likes.characterBonusesEnterTheBaseStageBefore')}</p>
      )}
      <h3>{text('likes.subscriptionsImagesAndAPIReserve')}</h3>
      <p>{text('likes.subscriptionBurstAndTotalQuotaHaveSeparate')}</p>
    </DuelDialog>
  );
}
function Outcomes({ result }: { readonly result: DuelResult<LikesView, Presentation> }) {
  const text = useDuelText();
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
                label={`${player.role} ${pose === 'win' ? text('common.victory') : pose === 'loss' ? text('likes.defeat') : pose === 'draw' ? text('common.draw') : ''}`}
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
  catalogs,
  context,
  blocked,
  selection,
  onSelectionChange,
  onPresetLoad,
  onQueue,
  onInspect,
  onEdit,
}: {
  readonly catalog: ModeCatalog;
  readonly catalogs: Readonly<Record<'quick' | 'standard', ModeCatalog>>;
  readonly context: DuelLobbyContext;
  readonly blocked: boolean;
  readonly selection: Selection;
  readonly onSelectionChange: (selection: Selection) => void;
  readonly onPresetLoad: (mode: 'quick' | 'standard', selection: Selection) => void;
  readonly onQueue: (selection: Selection) => void;
  readonly onInspect: (id: string) => void;
  readonly onEdit: () => void;
}) {
  const text = useDuelText();
  const mode = context.config.modes[catalog.mode],
    enough =
      creditsToMilli(spendableGameCredits(context.wallets).total) >=
      creditsToMilli(mode?.ticket ?? '0');
  const unavailable = entryProblem(context, catalog.mode);
  return (
    <>
      <LoadoutEditor
        catalog={catalog}
        value={selection}
        onChange={(next) => {
          onSelectionChange(next);
          onEdit();
        }}
        disabled={blocked}
        onInspect={onInspect}
      />
      <CustomPresets
        catalogs={catalogs}
        mode={catalog.mode}
        selection={selection}
        blocked={blocked}
        onLoad={onPresetLoad}
      />
      {mode && <DuelTerms mode={mode} />}
      <div className="likes-enqueue">
        <span>
          {catalog.mode === 'quick' ? text('likes.quickMode') : text('likes.standardMode')} ·{' '}
          {catalog.parameters.TARGET_LIKES} ♥ · {catalog.parameters.MAX_ROUNDS}{' '}
          {text('likes.roundLimit')}
        </span>
        <button
          type="button"
          className="likes-primary"
          disabled={blocked || !!unavailable || !enough || !!selectionProblem(catalog, selection)}
          onClick={() => onQueue(selection)}
        >
          {unavailable
            ? entryMessage(unavailable, text)
            : !enough
              ? text('bidding.insufficientCredits')
              : text('bidding.payEntryAndFindAMatch')}
        </button>
      </div>
    </>
  );
}
export function LikesGame(context: DuelLobbyContext) {
  const text = useDuelText();
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
  const [tutorial, setTutorial] = useState(false);
  const [tutorialSeen, setTutorialSeen] = useState(() => !!tutorialStatus());
  const [tutorialScene, setTutorialScene] = useState<MusicScene>('lobby');
  const [drafts, setDrafts] = useState<Partial<Record<'quick' | 'standard', Selection>>>({});
  const [lobbyForResult, setLobbyForResult] = useState<string | null>(null);
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
  const audioFacts = useSnapshotAudioFacts(home, likesAudioFacts);
  const musicScene = tutorial
    ? tutorialScene
    : !home || (!current && !queue && result?.id === lobbyForResult)
      ? 'lobby'
      : likesMusicScene(home, now);
  const audio = useArcadeAudio('likes', {
    scene: musicScene,
    facts: audioFacts,
    now: now * 1000,
    ready: !!home,
  });
  const editLobby = () => {
    setLobbyForResult(result?.id ?? null);
    audio.sound.play('common_select');
  };
  const remaining = useAuthoritativeCountdown(
    current ? `${current.id}:${current.phaseSeq}` : (queue?.id ?? 'idle'),
    current?.deadline ?? queue?.deadline ?? null,
    home?.serverNow ?? 0,
    duel.refresh,
  );
  const activeMode = current?.mode ?? queue?.mode ?? mode;
  const urgent = useActionWarning(
    current ? `${current.id}:${current.phaseSeq}` : 'idle',
    remaining,
    !!current &&
      current.phase === 'plan' &&
      !current.locked[current.you] &&
      !current.view.players[current.you].overloaded &&
      !duel.blocked,
    audio.sound.play,
    audio.sound.stop,
  );
  const displayed =
    current ??
    (!queue && result && (log || (!!result.resolution && now < result.resolution.endsAt))
      ? result
      : null);
  const c =
    catalogQuery.data && displayed?.contentHash
      ? matchingCatalog(catalogQuery.data, displayed.mode, displayed.contentHash)
      : catalogQuery.data?.modes[activeMode === 'standard' ? 'standard' : 'quick'];
  const compatible = !displayed?.contentHash || c?.contentHash === displayed.contentHash;
  const terminalPresentation =
    !current && !queue && result?.resolution && now < result.resolution.endsAt && result.view;
  const canTeach =
    !!catalogQuery.data && !duel.blocked && !current && !queue && !terminalPresentation;
  if (tutorial && (current || queue || duel.uncertain)) setTutorial(false);
  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key === tutorialStorageKey) setTutorialSeen(!!tutorialStatus());
    };
    window.addEventListener('storage', sync);
    return () => window.removeEventListener('storage', sync);
  }, []);
  const exitTutorial = (status: 'completed' | 'skipped', selection?: Selection) => {
    saveTutorialStatus(status);
    setTutorialSeen(true);
    setTutorial(false);
    if (selection && !current && !queue && !duel.blocked) {
      setMode('quick');
      setDrafts((previous) => ({ ...previous, quick: selection }));
      setLobbyForResult(result?.id ?? null);
    }
  };
  const logSession = current?.id ?? result?.id,
    logSeat = current?.you ?? result?.you ?? 0;
  return (
    <div className="likes-game">
      <header className="likes-heading">
        <div>
          <span className="likes-eyebrow">LIKES // DUEL</span>
          <h1>{text('likes.turnBasedBattleMinigameTest')}</h1>
          <BattleAtmosphere
            reduced={reduced}
            mode={
              (!!current || !!terminalPresentation) &&
              (musicScene === 'danger' || musicScene === 'accelerated')
                ? musicScene
                : null
            }
          />
          <p>{text('likes.oneBatteryIndependentChoicesASimultaneousReveal')}</p>
        </div>
        <div className="duel-actions">
          <ArcadeAudioControls
            sound={audio.sound}
            music={audio.music}
            unavailable={audio.unavailable}
          />
          <button type="button" disabled={!c} onClick={() => setRules(true)}>
            {text('likes.rules')}
          </button>
          <button type="button" disabled={!c} onClick={() => setGuide('')}>
            {text('likes.fieldGuide')}
          </button>
          <button type="button" disabled={!c} onClick={() => setHistory(true)}>
            {text('bidding.gameHistory')}
          </button>
          <button type="button" disabled={!canTeach} onClick={() => setTutorial(true)}>
            {text('likes.tutorial')}
          </button>
        </div>
      </header>
      <GameWallets wallets={context.wallets} />
      {context.onboarding && <OnboardingCard game="likes" progress={context.onboarding} />}
      {!tutorialSeen && canTeach && (
        <section className="likes-tutorial-invite">
          <h2>{text('likes.newHereTryAGuidedMatch')}</h2>
          <p>{text('likes.learnTheGameFromLoadoutToA')}</p>
          <div className="duel-actions">
            <button type="button" className="likes-primary" onClick={() => setTutorial(true)}>
              {text('likes.startTutorial')}
            </button>
            <button type="button" onClick={() => exitTutorial('skipped')}>
              {text('likes.skipForNow')}
            </button>
          </div>
        </section>
      )}
      <RandomnessProof
        game="likes"
        id={current?.id ?? result?.id}
        terminal={!current && !!result}
      />
      <DuelFeedback
        queueAttempt={duel.intentKind === 'queue'}
        entryProblem={entryProblem(context, mode)}
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
      {!compatible && <p role="alert">{text('likes.theRulesHaveNotSyncedRefreshTo')}</p>}
      {c && compatible && (
        <>
          {current ? (
            <>
              <div className={`likes-phase ${urgent ? 'likes-action-urgent' : ''}`}>
                <strong>
                  {text('bidding.round')} {current.round}/{c.parameters.MAX_ROUNDS}{' '}
                  {text('bidding.message')}
                </strong>
                <span>
                  {current.phase === 'settlement'
                    ? text('likes.settlement')
                    : text('likes.chooseSkills')}
                </span>
                <strong className={urgent ? 'is-urgent' : ''}>{remaining ?? '—'}s</strong>
                <button type="button" onClick={() => setLog(true)}>
                  {text('likes.roundLog')}
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
                    {text('likes.mATCHFOUND')}
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
                <div className={urgent ? 'likes-action-urgent' : ''}>
                  {urgent && (
                    <p className="likes-time-warning" role="status">
                      {text('likes.timeIsAlmostUpConfirmYourPlan')}
                    </p>
                  )}
                  <PlanEditor
                    key={`${current.id}:${current.phaseSeq}`}
                    catalog={c}
                    state={current}
                    blocked={duel.blocked || remaining === 0}
                    onInspect={setGuide}
                    onSelect={() => audio.sound.play('common_select')}
                    onLock={(plan) =>
                      duel.run({
                        kind: 'action',
                        id: current.id,
                        phaseSeq: current.phaseSeq,
                        action: { kind: 'plan', plan },
                      })
                    }
                  />
                </div>
              )}
              <div className="duel-actions">
                <button type="button" disabled={duel.blocked} onClick={() => setSurrender(true)}>
                  {text('bidding.surrender')}
                </button>
                <span>
                  {text('bidding.yourEntry')}: <GamePayment payment={current.payment} />
                </span>
              </div>
            </>
          ) : queue ? (
            <section className="likes-queue">
              <h2>{text('likes.findingYourOpponent')}</h2>
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
                {text('bidding.queueTimeLeft')}: {remaining ?? '—'}s
              </p>
              <GamePayment payment={queue.payment} />
              <p>{text('likes.yourLoadoutIsFrozenWhileQueuedCancel')}</p>
              <button
                type="button"
                disabled={duel.blocked}
                onClick={() => duel.run({ kind: 'cancel', id: queue.id, revision: queue.revision })}
              >
                {text('bidding.cancelQueueAndRefund')}
              </button>
            </section>
          ) : terminalPresentation && result?.view && result.resolution ? (
            <>
              <div className="likes-phase">
                <strong>{text('likes.finalSettlement')}</strong>
                <span>{Math.max(0, Math.ceil(result.resolution.endsAt - now))}s</span>
              </div>
              <Arena
                catalog={c}
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
                    {text('likes.viewRoundLog')}
                  </button>
                </>
              )}
              <div className="likes-modes" role="group" aria-label={text('likes.mode')}>
                {(['quick', 'standard'] as const).map((m) => (
                  <button
                    key={m}
                    type="button"
                    aria-pressed={mode === m}
                    disabled={duel.pending || duel.uncertain || !!entryProblem(context, m)}
                    onClick={() => {
                      setMode(m);
                      editLobby();
                    }}
                  >
                    <strong>{m === 'quick' ? text('likes.quick') : text('likes.standard')}</strong>
                    <span>
                      {catalogQuery.data!.modes[m].parameters.TARGET_LIKES} ♥ ·{' '}
                      {catalogQuery.data!.modes[m].parameters.MAX_ROUNDS} {text('likes.rounds')}
                    </span>
                  </button>
                ))}
              </div>
              <Lobby
                catalog={c}
                catalogs={catalogQuery.data!.modes}
                context={context}
                blocked={duel.blocked}
                selection={drafts[mode] ?? initialSelection(c)}
                onSelectionChange={(selection) =>
                  setDrafts((previous) => ({ ...previous, [mode]: selection }))
                }
                onPresetLoad={(savedMode, selection) => {
                  setDrafts((previous) => ({ ...previous, [savedMode]: selection }));
                  setMode(savedMode);
                  editLobby();
                }}
                onInspect={setGuide}
                onEdit={editLobby}
                onQueue={(selection) => {
                  setLobbyForResult(result?.id ?? null);
                  duel.run({
                    kind: 'queue',
                    mode,
                    termsHash: context.config.modes[mode].termsHash,
                    loadout: selection,
                  });
                }}
              />
            </>
          )}
        </>
      )}
      {rules && c && <Rules catalog={c} onClose={closeRules} />}
      {tutorial && !current && !queue && !duel.uncertain && catalogQuery.data && (
        <Suspense fallback={<p role="status">{text('likes.preparingTheTutorial')}</p>}>
          <Tutorial
            catalog={catalogQuery.data.modes.quick}
            sound={audio.sound}
            music={audio.music}
            unavailable={audio.unavailable}
            onScene={setTutorialScene}
            onExit={exitTutorial}
          />
        </Suspense>
      )}
      {guide !== null && c && (
        <Glossary key={guide} catalog={c} initial={guide || undefined} onClose={closeGuide} />
      )}
      {history && catalogQuery.data && (
        <DuelHistory
          codec={likesCodec}
          onClose={closeHistory}
          renderRound={(round, you, meta) => {
            const catalog = matchingCatalog(catalogQuery.data, meta.mode, meta.contentHash);
            return catalog ? (
              <LikesRoundLog round={round} you={you} catalog={catalog} />
            ) : (
              <p>{text('likes.theMatchingRuleCatalogIsUnavailable')}</p>
            );
          }}
          renderDetail={(detail) => {
            const catalog = matchingCatalog(
              catalogQuery.data,
              detail.result.mode,
              detail.contentHash,
            );
            return !catalog ? (
              <p>{text('likes.theMatchingRuleCatalogIsUnavailable')}</p>
            ) : (
              <div className="likes-history-loadouts">
                {detail.result.view?.players.map((player, seat) => (
                  <section key={seat}>
                    <h3>{player.role}</h3>
                    <CharacterPassive role={catalog.roles.find((r) => r.id === player.role)!} />
                    <p>{player.loadout?.map((id) => skillName(catalog, id)).join(' · ')}</p>
                  </section>
                ))}
              </div>
            );
          }}
        />
      )}
      {log && logSession && c && (
        <DuelDialog title={text('likes.roundLog')} onClose={closeLog} className="likes-glossary">
          <div className="likes-log-return">
            <span>
              {current ? text('likes.theGameClockContinues') : text('likes.completed')}{' '}
              {current ? `${remaining ?? '—'}s` : ''}
            </span>
            <button type="button" onClick={closeLog}>
              {text('likes.returnToBattle')}
            </button>
          </div>
          <DuelRoundLog
            key={logSession}
            codec={likesCodec}
            id={logSession}
            active={!!current}
            you={logSeat as Seat}
            renderRound={(round, you) => <LikesRoundLog round={round} you={you} catalog={c} />}
          />
        </DuelDialog>
      )}
      <ConfirmDialog
        open={surrender && !!current}
        title={text('bidding.confirmSurrender')}
        description={text('likes.surrenderImmediatelyConcedesTheGameWithoutAn')}
        confirmLabel={text('bidding.surrender2')}
        cancelLabel={text('likes.keepPlaying')}
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
