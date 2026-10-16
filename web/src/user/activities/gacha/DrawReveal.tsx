import { artSourceSet } from './art-resolution';
import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';
import { cardById, isRare, themeById, tierOf } from './catalog';
import { Artwork } from './Artwork';
import { GachaDialog } from './GachaDialog';
import { SceneClock } from './SceneClock';
import { EYEBROW, PRELUDE, WHISPER, summaryLine, voiceLines } from './voice';
import type { GachaAudio } from './audio';
import type { DrawReceipt, Preferences, Rarity } from './types';
import './reveal.css';

const rank: Record<Rarity, number> = { N: 0, R: 1, SR: 2, SSR: 3, SP: 4 };
const colors: Record<Rarity, string> = {
  N: '#d6e6d4',
  R: '#b4ddff',
  SR: '#e3c9ff',
  SSR: '#ffe2a0',
  SP: '#bdf5e9',
};
type Phase = 'gather' | 'deal' | 'flip' | 'charge' | 'prelude' | 'closeup' | 'settle' | 'done';
type Scene = { phase: Phase; index: number };
const firstPhase = (receipt: DrawReceipt, index: number): Phase =>
  isRare(cardById[receipt.items[index].card_id].rarity) ? 'charge' : 'flip';

export function DrawReveal({
  receipt,
  preferences,
  audio,
  english,
  onClose,
  onAgain,
  replay = false,
}: {
  receipt: DrawReceipt;
  preferences: Preferences;
  audio: GachaAudio;
  english: boolean;
  onClose(): void;
  onAgain?: () => void;
  replay?: boolean;
}) {
  const t = (zh: string, en: string) => (english ? en : zh);
  const required = receipt.items.flatMap((item, i) =>
    !replay && !item.duplicate && isRare(cardById[item.card_id].rarity) ? [i] : [],
  );
  const staticScene = (i: number): Scene => ({
    index: i,
    phase:
      cardById[receipt.items[i].card_id].rarity === 'SP' && !receipt.items[i].duplicate
        ? 'prelude'
        : 'closeup',
  });
  const [seen, setSeen] = useState<number[]>([]);
  const [scene, setScene] = useState<Scene>(() => {
    if (!preferences.reduced) return { phase: 'gather', index: 0 };
    if (replay || required.length) return staticScene(required[0] ?? 0);
    return { phase: 'done', index: 0 };
  });
  const [paused, setPaused] = useState(false),
    [hidden, setHidden] = useState(document.hidden);
  const [tinted, setTinted] = useState(false),
    [instant, setInstant] = useState(preferences.reduced);
  const [inspect, setInspect] = useState<number | null>(null);
  const [inspectMotion, setInspectMotion] = useState(true);
  const cues = useRef(new Set<string>());
  const { phase, index } = scene;
  const current = receipt.items[index],
    card = cardById[current.card_id];
  const highest = receipt.items.reduce<Rarity>(
    (best, item) =>
      rank[cardById[item.card_id].rarity] > rank[best] ? cardById[item.card_id].rarity : best,
    'N',
  );
  const tell = preferences.cardBackTell !== false;
  const complete = phase === 'done',
    settled = complete || phase === 'settle';
  const waiting = phase === 'closeup' || (phase === 'flip' && card.rarity === 'SR');
  const suspended = paused || hidden;
  const pendingFirst = required.some((i) => !seen.includes(i));
  const readingFirst = phase === 'closeup' && required.includes(index) && !seen.includes(index);
  const canContinue = waiting && !suspended && !readingFirst;
  const revealed = phase === 'flip' || phase === 'prelude' || phase === 'closeup';
  const cueKey =
    phase === 'gather'
      ? 'gather'
      : phase === 'settle'
        ? 'settle'
        : revealed
          ? 'card-' + index
          : null;
  const skip = () => {
    if (pendingFirst) return;
    navigator.vibrate?.(0);
    audio.stopEffects();
    setInstant(true);
    setScene({ phase: 'done', index });
    setPaused(false);
  };
  const advance = () => {
    if (suspended || readingFirst) return;
    if (replay && waiting) {
      onClose();
      return;
    }
    setScene((previous) => {
      if (previous.phase !== phase || previous.index !== index) return previous;
      if (phase === 'gather') return { phase: 'deal', index: 0 };
      if (phase === 'deal') return { phase: firstPhase(receipt, 0), index: 0 };
      if (phase === 'charge')
        return { phase: card.rarity === 'SP' && !current.duplicate ? 'prelude' : 'closeup', index };
      if (phase === 'prelude') return { phase: 'closeup', index };
      if (phase === 'settle') return { phase: 'done', index };
      if (preferences.reduced) {
        const next = required.find((i) => i > index);
        return next === undefined ? { phase: 'done', index } : staticScene(next);
      }
      if (index + 1 < receipt.items.length)
        return { phase: firstPhase(receipt, index + 1), index: index + 1 };
      return { phase: 'settle', index };
    });
  };
  const milliseconds =
    phase === 'gather'
      ? 2200
      : phase === 'deal'
        ? receipt.items.length === 1
          ? 620
          : 70 * receipt.items.length + 620
        : phase === 'charge'
          ? 900
          : phase === 'prelude'
            ? 1800
            : phase === 'settle'
              ? 1200
              : phase === 'flip' && card.rarity === 'N'
                ? 330
                : phase === 'flip' && card.rarity === 'R'
                  ? 420
                  : 0;
  useEffect(() => {
    const visibility = () => {
      setHidden(document.hidden);
      if (document.hidden) {
        navigator.vibrate?.(0);
        audio.stopEffects();
      }
    };
    document.addEventListener('visibilitychange', visibility);
    for (const item of receipt.items) {
      const image = new Image();
      image.src = cardById[item.card_id].art;
    }
    return () => {
      document.removeEventListener('visibilitychange', visibility);
      navigator.vibrate?.(0);
      audio.stopEffects();
    };
  }, [receipt, audio]);
  useEffect(() => {
    if (!cueKey || preferences.reduced || hidden || paused || cues.current.has(cueKey)) return;
    const timer = window.setTimeout(() => {
      cues.current.add(cueKey);
      audio.cue(
        cueKey === 'gather' ? 'gather' : cueKey === 'settle' ? 'spread' : card.rarity,
        preferences.sound,
      );
      if (cueKey.startsWith('card-') && preferences.vibration !== false)
        navigator.vibrate?.(
          card.rarity === 'SP'
            ? [65, 75, 110, 100, 160]
            : card.rarity === 'SSR'
              ? [55, 75, 110]
              : card.rarity === 'SR'
                ? [30, 55, 45]
                : 15,
        );
    }, 0);
    return () => window.clearTimeout(timer);
  }, [audio, card.rarity, cueKey, hidden, paused, preferences]);
  const lines = voiceLines(card.id, english, current.duplicate);
  const newCount = receipt.items.filter((item) => !item.duplicate).length;
  const color =
    phase === 'closeup' || phase === 'prelude'
      ? card.rarity
      : phase === 'gather' && tinted && tell
        ? highest
        : 'N';
  return (
    <GachaDialog
      title={
        complete
          ? t('此次相遇', 'These encounters')
          : t('星光回应了你的期待', 'The stars have answered')
      }
      onClose={complete || replay ? onClose : skip}
      closeDisabled={!replay && pendingFirst}
      onSurfaceClick={canContinue ? advance : undefined}
      closeLabel={
        complete || replay ? t('关闭结果', 'Close results') : t('跳过演出', 'Skip animation')
      }
      className={
        'gacha-cinema gacha-cinema--' +
        color +
        (preferences.reduced ? ' gacha-static' : '') +
        (suspended ? ' is-paused' : '')
      }
    >
      <div className="gacha-cinema__sky" aria-hidden="true">
        <div className="gacha-cinema__nebula" />
        <div className="gacha-cinema__stars" />
        <div className="gacha-cinema__horizon" />
      </div>
      <div className="gacha-draw-stage" data-phase={phase} data-instant={instant || undefined}>
        {readingFirst && (
          <SceneClock
            key={'read-' + index}
            milliseconds={card.rarity === 'SP' ? 4800 : 1200}
            paused={paused}
            advance={() => setSeen((previous) => [...previous, index])}
          />
        )}
        {milliseconds > 0 && (
          <SceneClock
            key={phase + index}
            milliseconds={milliseconds}
            paused={paused}
            advance={advance}
          />
        )}
        {phase === 'gather' && (
          <>
            <SceneClock milliseconds={1500} paused={paused} advance={() => setTinted(true)} />
            <div
              className="gacha-cinema__summon"
              aria-label={t('星光汇聚中', 'Gathering starlight')}
            >
              <div className="gacha-cinema__portal">
                <i />
                <i />
                <i />
                <Icon name="spark" />
              </div>
              <div className="gacha-draw-motes" aria-hidden="true">
                {Array.from({ length: 28 }, (_, i) => (
                  <i
                    key={i}
                    style={
                      {
                        '--x': Math.cos((i / 28) * Math.PI * 2) * 46 + 'vw',
                        '--y': Math.sin((i / 28) * Math.PI * 2) * 40 + 'vh',
                        '--d': (i % 7) * 90 + 'ms',
                      } as CSSProperties
                    }
                  />
                ))}
              </div>
              <p>
                {WHISPER[tell ? (highest === 'R' ? 'N' : highest) : 'neutral'][english ? 1 : 0]}
              </p>
            </div>
          </>
        )}
        {phase !== 'gather' && (
          <div
            className={'gacha-draw-board' + (receipt.items.length === 1 ? ' is-single' : '')}
            aria-hidden={phase === 'prelude' || phase === 'closeup'}
            role={settled ? 'region' : undefined}
            aria-label={
              settled
                ? t('全部抽卡结果', 'All draw results')
                : t('等待翻开的卡阵', 'Cards awaiting their reveal')
            }
          >
            {receipt.items.map((item, i) => {
              const c = cardById[item.card_id],
                open = settled || i < index || (i === index && revealed);
              const dupIndex = receipt.items.slice(0, i).filter((value) => value.duplicate).length;
              return (
                <button
                  type="button"
                  key={i}
                  tabIndex={complete ? 0 : -1}
                  className={
                    'gacha-draw-slot' +
                    (open ? ' is-open' : '') +
                    (item.duplicate ? ' is-dup' : '') +
                    (phase === 'charge' && i === index ? ' is-charging' : '') +
                    (waiting && phase === 'flip' && i === index ? ' is-focus' : '')
                  }
                  data-card-id={c.id}
                  data-rarity={c.rarity}
                  data-open={open}
                  data-tell={tell && rank[c.rarity] >= 2 ? c.rarity : undefined}
                  style={
                    {
                      '--i': i,
                      '--k': dupIndex,
                      '--c': colors[c.rarity],
                      '--fx': (2 - (i % 5)) * 18 + 'vw',
                      '--fy': (i < 5 ? 30 : 12) + 'vh',
                      '--glow': [0, 4, 10, 18, 22][rank[c.rarity]],
                    } as CSSProperties
                  }
                  aria-label={
                    open ? t(c.name, c.name_en) + ' · ' + c.rarity : t('卡牌 ', 'Card ') + (i + 1)
                  }
                  onClick={() => {
                    if (complete) {
                      setInspect(i);
                      setInspectMotion(true);
                    } else if (canContinue) advance();
                  }}
                >
                  <span className="gacha-draw-burst" aria-hidden="true" />
                  <span className="gacha-draw-slot__inner">
                    <span className="gacha-draw-face gacha-draw-back" aria-hidden="true" />
                    <span className="gacha-draw-face gacha-draw-front" aria-hidden={!open}>
                      <Artwork card={c} animated={false} />
                      <span className="gacha-draw-rarity">{c.rarity}</span>
                      {!item.duplicate && <span className="gacha-draw-new">NEW</span>}
                      <span className="gacha-draw-name">
                        <small>{t(themeById[c.theme].name, themeById[c.theme].name_en)}</small>
                        {c.character}
                      </span>
                    </span>
                  </span>
                  {item.duplicate && settled && (
                    <span
                      className="gacha-draw-shard"
                      aria-label={t(
                        '转化为 1 枚 ' + tierOf(c.rarity) + ' 碎片',
                        'Converted to 1 ' + tierOf(c.rarity) + ' shard',
                      )}
                    >
                      {tierOf(c.rarity)} {t('碎片 +1', 'shard +1')}
                    </span>
                  )}
                </button>
              );
            })}
          </div>
        )}
        {phase === 'prelude' && (
          <div className="gacha-draw-prelude" data-card-id={card.id}>
            <div className="gacha-draw-drift" aria-hidden="true" />
            <p>{PRELUDE[card.id][english ? 1 : 0]}</p>
          </div>
        )}
        {phase === 'closeup' && (
          <div
            key={index}
            className={
              'gacha-cinema__encounter is-rare' + (card.rarity === 'SP' ? ' is-special' : '')
            }
            data-card-id={card.id}
            data-rarity={card.rarity}
          >
            <img
              className="gacha-cinema__scenery"
              src={themeById[card.theme].art}
              srcSet={artSourceSet(
                themeById[card.theme].art,
                ['l', 'm'].includes(card.theme) ? 1600 : 1672,
              )}
              sizes="100vw"
              alt=""
            />
            <div className="gacha-cinema__halo" aria-hidden="true">
              <i />
              <i />
              <i />
            </div>
            <div className="gacha-cinema__rays" aria-hidden="true" />
            {card.rarity === 'SP' && <div className="gacha-draw-drift" aria-hidden="true" />}
            <div className="gacha-cinema__silhouette" aria-hidden="true">
              {card.rarity}
            </div>
            <div className="gacha-cinema__art">
              <Artwork card={card} full animated={!preferences.reduced} paused={suspended} />
            </div>
            <div className="gacha-cinema__caption">
              <span className="gacha-cinema__eyebrow">{EYEBROW[card.rarity][english ? 1 : 0]}</span>
              <strong className="gacha-cinema__rarity">{card.rarity}</strong>
              <h3>{t(themeById[card.theme].name, themeById[card.theme].name_en)}</h3>
              <p className="gacha-cinema__character">{card.character}</p>
              <p className="gacha-cinema__quote">
                {lines[0]}
                {lines[1] && <span className="gacha-voice-after">{lines[1]}</span>}
              </p>
              <span className="gacha-cinema__status">
                <Icon name={current.duplicate ? 'models' : 'spark'} />
                {replay
                  ? current.duplicate
                    ? t('重逢回忆', 'Reunion memory')
                    : t('初遇回忆', 'First encounter memory')
                  : current.duplicate
                    ? t(
                        '重逢 · ' + tierOf(card.rarity) + ' 碎片 +1',
                        'Together again · +1 ' + tierOf(card.rarity) + ' shard',
                      )
                    : t('初次相遇 · 已收入图鉴', 'First encounter · collected')}
              </span>
              {card.appearance && !current.duplicate && !replay && (
                <span className="gacha-cinema__unlock">
                  <Icon name="check" />
                  {t('同名主题装扮已解锁', 'Matching appearance unlocked')}
                </span>
              )}
            </div>
            <div className="gacha-cinema__glint" aria-hidden="true" />
          </div>
        )}
        {phase === 'flip' && card.rarity === 'SR' && (
          <p className="gacha-draw-sr-caption" key={index}>
            <strong>SR · {t(card.name, card.name_en)}</strong>
            <span>{lines[0]}</span>
          </p>
        )}
        {complete && (
          <section className="gacha-draw-summary">
            <h3>{summaryLine(receipt, english)}</h3>
            <p>
              {t(
                '新收入图鉴 ' +
                  newCount +
                  ' 张 · 重逢化为碎片 ' +
                  (receipt.items.length - newCount) +
                  ' 枚',
                newCount + ' new cards · ' + (receipt.items.length - newCount) + ' reunion shards',
              )}
            </p>
            <div>
              {onAgain && <Button onClick={onAgain}>{t('再来一次', 'Draw again')}</Button>}
              <Button variant="primary" onClick={onClose}>
                {t('收下这份星光', 'Keep these memories')}
              </Button>
            </div>
          </section>
        )}
        {!complete && (
          <footer className="gacha-cinema__controls">
            <span aria-live="polite">
              {phase === 'gather'
                ? t('期待正在靠近', 'An encounter draws near')
                : phase === 'deal'
                  ? t('星光正在落位', 'Dealing starlight')
                  : phase === 'settle'
                    ? t('重逢化作星光碎片', 'Reunions become shards')
                    : index +
                      1 +
                      ' / ' +
                      receipt.items.length +
                      ' · ' +
                      (waiting
                        ? readingFirst
                          ? t('初次相遇，听她说完', 'Your first encounter · let her finish')
                          : t('点击任意位置继续', 'Click anywhere to continue')
                        : t('自动播放', 'Playing automatically'))}
            </span>
            <div>
              {waiting && !paused && (
                <Button variant="ghost" onClick={advance} disabled={!canContinue}>
                  {t('继续', 'Continue')}
                </Button>
              )}
              <Button
                variant="ghost"
                onClick={() => {
                  audio.stopEffects();
                  navigator.vibrate?.(0);
                  setPaused(!paused);
                }}
              >
                <Icon name={paused ? 'play' : 'pause'} />
                {paused ? t('继续播放', 'Resume') : t('暂停', 'Pause')}
              </Button>
              <Button
                variant="ghost"
                onClick={replay ? onClose : skip}
                disabled={!replay && pendingFirst}
              >
                {replay ? t('关闭回看', 'Close replay') : t('跳过 · 查看全部', 'Skip · view all')}
              </Button>
            </div>
          </footer>
        )}
      </div>
      {inspect !== null && (
        <GachaDialog
          title={t(
            cardById[receipt.items[inspect].card_id].name,
            cardById[receipt.items[inspect].card_id].name_en,
          )}
          onClose={() => setInspect(null)}
          className="gacha-inspect gacha-spotlight"
          closeLabel={t('关闭大图', 'Close artwork')}
        >
          <div className="gacha-spotlight__stage">
            <Artwork
              card={cardById[receipt.items[inspect].card_id]}
              full
              animated={!preferences.reduced}
              paused={hidden || !inspectMotion}
            />
          </div>
          <p className="gacha-owned-voice">
            {voiceLines(receipt.items[inspect].card_id, english).map((line, i) => (
              <span key={i}>{line}</span>
            ))}
          </p>
          {isRare(cardById[receipt.items[inspect].card_id].rarity) && !preferences.reduced && (
            <Button onClick={() => setInspectMotion(!inspectMotion)}>
              <Icon name={inspectMotion ? 'pause' : 'play'} />
              {inspectMotion ? t('暂停动态', 'Pause animation') : t('播放动态', 'Play animation')}
            </Button>
          )}
        </GachaDialog>
      )}
    </GachaDialog>
  );
}
