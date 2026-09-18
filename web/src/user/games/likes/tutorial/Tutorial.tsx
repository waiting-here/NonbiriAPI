import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { DuelDialog } from '../../common/duel/Dialog';
import type { DuelState } from '../../common/duel/types';
import { useDuelText } from '../../common/duel/copy';
import { ArcadeAudioControls } from '../../common/audio/ArcadeAudioControls';
import type { ArcadeSoundControl } from '../../common/audio/useArcadeAudio';
import type { EffectCue } from '../../common/audio/assets';
import type { ModeCatalog } from '../catalog';
import type { LikesEvent, LikesView, Plan, Presentation, Selection } from '../types';
import { Arena } from '../Arena';
import { LoadoutEditor } from '../Loadout';
import { PlanEditor } from '../PlanEditor';
import { LikesArt } from '../LikesArt';
import { characterSlot } from '../art';
import { BattleAtmosphere } from '../BattleAtmosphere';
import { likesAudioFacts, likesMusicScene, type LikesHome, type MusicScene } from '../audioFacts';
import { useReducedMotion } from '../motion';
import { tutorialData } from './data';
import { tutorialSteps } from './steps';
import { GuidedSurface } from './GuidedSurface';
import { saveTutorialStatus } from './storage';

const profiles = [
  { kind: 'public', displayName: 'You' },
  { kind: 'public', displayName: '教学机器人 · Tutorial bot' },
] as const;
function stateFor(
  round: number,
  view: LikesView,
): DuelState<LikesView, Presentation, LikesEvent[]> {
  return {
    id: 'tutorial',
    game: 'likes',
    mode: 'quick',
    contentHash: tutorialData.contentHash,
    revision: String(round),
    phaseSeq: String(round),
    phase: 'plan',
    round,
    deadline: 1020,
    serverNow: 1000,
    you: 0,
    locked: [false, false],
    ticket: '0',
    rates: { platform: 0, welfare: 0, thursday: 0 },
    payment: { general: '0', game: '0' },
    profiles,
    view,
    resolution: null,
    roundStart: null,
  };
}
export function sameTeachingPlan(actual: Plan, expected: Plan) {
  const canonical = (p: Plan) => ({
    purchases: p.purchases,
    main: p.main && {
      skillId: p.main.skillId,
      pay: p.main.pay ?? 'auto',
      cleanseMode: p.main.cleanseMode ?? 'self',
      targets: p.main.targets ?? [],
    },
    extra: p.extra,
  });
  return JSON.stringify(canonical(actual)) === JSON.stringify(canonical(expected));
}

function TutorialResolution({
  round,
  catalog,
  onDone,
  onCue,
  onScene,
}: {
  readonly round: number;
  readonly catalog: ModeCatalog;
  readonly onDone: () => void;
  readonly onCue: (cue: EffectCue) => void;
  readonly onScene: (scene: MusicScene) => void;
}) {
  const data = tutorialData.rounds[round - 1],
    reduced = useReducedMotion();
  const [elapsed, setElapsed] = useState(0);
  const heard = useRef(new Set<string>());
  const origin = useRef<number | null>(null);
  useEffect(() => {
    const start = origin.current ?? performance.now();
    origin.current = start;
    const timer = setInterval(
      () => setElapsed((performance.now() - start) / 1000),
      reduced ? 150 : 33,
    );
    return () => clearInterval(timer);
  }, [reduced]);
  const resolution = { round, startedAt: 1000, endsAt: 1000 + data.seconds, summary: data.summary };
  const home: LikesHome = useMemo(
    () => ({
      serverNow: 1000 + elapsed,
      queue: null,
      latestResult: null,
      current: {
        ...stateFor(round, data.after),
        phase: 'settlement',
        resolution: { round, startedAt: 1000, endsAt: 1000 + data.seconds, summary: data.summary },
      },
    }),
    [data, elapsed, round],
  );
  const scene = likesMusicScene(home, home.serverNow);
  useEffect(() => onScene(scene), [scene, onScene]);
  useEffect(() => {
    for (const fact of likesAudioFacts(home)) {
      if (fact.at > home.serverNow * 1000 || heard.current.has(fact.key)) continue;
      heard.current.add(fact.key);
      if (home.serverNow * 1000 - fact.at < 1500) onCue(fact.cue as EffectCue);
    }
  }, [home, onCue]);
  useEffect(() => {
    if (elapsed >= data.seconds) onDone();
  }, [elapsed, data.seconds, onDone]);
  return (
    <>
      {scene === 'accelerated' && <BattleAtmosphere reduced={reduced} mode="accelerated" />}
      <Arena
        catalog={catalog}
        view={data.after}
        profiles={profiles}
        you={0}
        round={round}
        locked={[true, true]}
        resolution={resolution}
        roundStart={null}
        now={home.serverNow}
        reduced={reduced}
        onInspect={() => undefined}
      />
    </>
  );
}

export default function Tutorial({
  catalog,
  sound,
  music,
  unavailable,
  onScene,
  onExit,
}: {
  readonly catalog: ModeCatalog;
  readonly sound: ArcadeSoundControl;
  readonly music: { readonly enabled: boolean; readonly toggle: () => void };
  readonly unavailable: boolean;
  readonly onScene: (scene: MusicScene) => void;
  readonly onExit: (status: 'completed' | 'skipped', selection?: Selection) => void;
}) {
  const t = useDuelText(),
    reduced = useReducedMotion();
  const steps = useMemo(() => tutorialSteps(t), [t]);
  const [index, setIndex] = useState(0);
  const [selection, setSelection] = useState<Selection>({
    role: 'ChatGPT',
    harness: null,
    skills: [],
  });
  const [planError, setPlanError] = useState(false);
  const acceptedSelection = useRef(-1);
  const step = steps[index],
    data = step.round ? tutorialData.rounds[step.round - 1] : null;
  const finished = step.kind === 'finish';
  const advance = useCallback(
    (expected: number, target: string) => {
      setIndex((current) =>
        current === expected && steps[current].target === target
          ? Math.min(current + 1, steps.length - 1)
          : current,
      );
    },
    [steps],
  );
  const resolutionDone = useCallback(() => advance(index, 'resolution'), [advance, index]);
  const play = sound.play;
  useEffect(() => {
    if (finished) {
      saveTutorialStatus('completed');
      play('common_win');
    }
  }, [finished, play]);
  const normalScene: MusicScene = finished
    ? 'win'
    : data
      ? likesMusicScene({
          serverNow: 1000,
          queue: null,
          latestResult: null,
          current: stateFor(step.round, step.kind === 'plan' ? data.before : data.after),
        })
      : 'lobby';
  useEffect(() => {
    if (step.kind !== 'resolution') onScene(normalScene);
  }, [step.kind, normalScene, onScene]);
  const changeSelection = (next: Selection) => {
    if (step.target === 'role:ChatGPT' && next.role === 'ChatGPT') {
      setSelection({ role: 'ChatGPT', harness: null, skills: [] });
    } else if (step.target === 'harness:H01' && next.harness === 'H01') {
      setSelection({ ...selection, harness: 'H01' });
    } else if (
      step.target.startsWith('equip:') &&
      JSON.stringify(next.skills) === JSON.stringify([...selection.skills, step.target.slice(6)])
    ) {
      setSelection(next);
    } else return;
    acceptedSelection.current = index;
    sound.play('common_select');
  };
  const action = (expected: number, target: string) => {
    if (target === 'lock' || (step.kind === 'loadout' && acceptedSelection.current !== expected))
      return;
    advance(expected, target);
  };
  const next = () => {
    if (step.kind === 'matching') sound.play('common_match');
    advance(index, 'continue');
  };
  const close = () => onExit(finished ? 'completed' : 'skipped');
  return (
    <DuelDialog
      title={t('新手引导 · 本地练习', 'Tutorial · Local practice')}
      onClose={close}
      className="likes-tutorial likes-game"
    >
      <div className="likes-tutorial-toolbar">
        <span>{t('不会扣除或获得站点积分', 'No site credits spent or earned')}</span>
        <ArcadeAudioControls sound={sound} music={music} unavailable={unavailable} />
        <button type="button" onClick={close}>
          {t('跳过教学', 'Skip tutorial')}
        </button>
      </div>
      {catalog.contentHash !== tutorialData.contentHash ? (
        <p role="alert">
          {t(
            '教学与当前规则不一致，请刷新后重试。',
            'This tutorial does not match the current rules. Refresh to try again.',
          )}
        </p>
      ) : (
        <>
          <section
            className="likes-tutorial-tip"
            id="likes-tutorial-tip"
            role="status"
            aria-live="polite"
          >
            <small>
              {step.round
                ? t(`第 ${step.round} / 10 轮`, `Round ${step.round} / 10`)
                : t('准备阶段', 'Preparation')}{' '}
              · {index + 1}/{steps.length}
            </small>
            <h3>{step.title}</h3>
            <p>{step.body}</p>
            {step.target === 'continue' && !finished && (
              <button type="button" className="likes-primary" data-tutorial-next onClick={next}>
                {t('继续', 'Continue')}
              </button>
            )}
            {finished && (
              <div className="duel-actions">
                <button
                  type="button"
                  className="likes-primary"
                  onClick={() => onExit('completed', tutorialData.selection)}
                >
                  {t('使用教学配装', 'Use teaching loadout')}
                </button>
                <button type="button" onClick={close}>
                  {t('返回大厅', 'Return to lobby')}
                </button>
              </div>
            )}
          </section>
          {normalScene === 'accelerated' && step.kind !== 'resolution' && (
            <BattleAtmosphere reduced={reduced} mode="accelerated" />
          )}
          <GuidedSurface target={step.target} value={step.value} step={index} onAction={action}>
            {step.kind === 'intro' && (
              <div className="likes-tutorial-welcome">
                <LikesArt slot={characterSlot('ChatGPT', 'portrait')} label="ChatGPT" />
                <LikesArt slot={characterSlot('Claude', 'portrait')} label="Claude" />
              </div>
            )}
            {step.kind === 'loadout' && (
              <LoadoutEditor
                catalog={catalog}
                value={selection}
                onChange={changeSelection}
                disabled={false}
                onInspect={() => undefined}
              />
            )}
            {step.kind === 'matching' && (
              <div className="likes-matched">
                <LikesArt slot={characterSlot('ChatGPT', 'portrait')} label="ChatGPT" />
                <span>
                  {t('教学匹配成功', 'TEACHING MATCH FOUND')}
                  <small>ChatGPT × Claude</small>
                </span>
                <LikesArt slot={characterSlot('Claude', 'portrait')} label="Claude" />
              </div>
            )}
            {data && step.kind !== 'resolution' && (
              <>
                <div className="likes-phase">
                  <strong>{t('快速模式 · 教学', 'QUICK · TUTORIAL')}</strong>
                  <span>
                    {step.kind === 'plan'
                      ? t('等待你的操作，无倒计时', 'Waiting for you · no countdown')
                      : t('本轮结算完成', 'Round resolved')}
                  </span>
                </div>
                <Arena
                  catalog={catalog}
                  view={step.kind === 'plan' ? data.before : data.after}
                  profiles={profiles}
                  you={0}
                  round={step.round}
                  locked={[false, false]}
                  resolution={null}
                  roundStart={null}
                  now={1000}
                  reduced={reduced}
                  onInspect={() => undefined}
                />
                {step.kind === 'plan' && (
                  <PlanEditor
                    key={step.round}
                    catalog={catalog}
                    state={stateFor(step.round, data.before)}
                    blocked={false}
                    onInspect={() => undefined}
                    onSelect={() => sound.play('common_select')}
                    onLock={(plan) => {
                      if (step.target !== 'lock' || !sameTeachingPlan(plan, data.plan)) {
                        setPlanError(true);
                        return;
                      }
                      setPlanError(false);
                      sound.play('common_lock');
                      advance(index, 'lock');
                    }}
                  />
                )}
              </>
            )}
            {data && step.kind === 'resolution' && (
              <TutorialResolution
                key={step.round}
                round={step.round}
                catalog={catalog}
                onDone={resolutionDone}
                onCue={sound.play}
                onScene={onScene}
              />
            )}
          </GuidedSurface>
          {planError && (
            <p role="alert">
              {t(
                '请按高亮步骤完成本轮教学方案。',
                'Complete the highlighted steps for this teaching plan.',
              )}
            </p>
          )}
        </>
      )}
    </DuelDialog>
  );
}
