import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';
import { cardById, isRare, themeById, tierOf } from './catalog';
import { CardTile } from './CardTile';
import { Artwork } from './Artwork';
import { GachaDialog } from './GachaDialog';
import type { GachaAudio } from './audio';
import type { DrawReceipt, Preferences, Rarity } from './types';
import './reveal.css';

const duration: Record<Rarity, number> = { N: 1000, R: 1150, SR: 1800, SSR: 4100, SP: 5400 };
const rank: Record<Rarity, number> = { N: 0, R: 1, SR: 2, SSR: 3, SP: 4 };

// Hidden tabs and an explicit pause preserve the remaining presentation time.
function useSceneClock(milliseconds: number, paused: boolean, advance: () => void) {
  const callback = useRef(advance);
  const remainingTime = useRef(milliseconds);
  useEffect(() => {
    callback.current = advance;
  });
  useEffect(() => {
    if (milliseconds <= 0) return;
    let remaining = remainingTime.current,
      started = 0,
      timer = 0;
    const stop = () => {
      if (timer) {
        window.clearTimeout(timer);
        remaining = Math.max(0, remaining - (performance.now() - started));
        remainingTime.current = remaining;
        timer = 0;
      }
    };
    const resume = () => {
      stop();
      if (paused || document.hidden) return;
      started = performance.now();
      timer = window.setTimeout(() => {
        timer = 0;
        callback.current();
      }, remaining);
    };
    resume();
    document.addEventListener('visibilitychange', resume);
    return () => {
      stop();
      document.removeEventListener('visibilitychange', resume);
    };
  }, [milliseconds, paused]);
}
export function DrawReveal({
  receipt,
  preferences,
  audio,
  english,
  onClose,
}: {
  receipt: DrawReceipt;
  preferences: Preferences;
  audio: GachaAudio;
  english: boolean;
  onClose: () => void;
}) {
  const t = (zh: string, en: string) => (english ? en : zh);
  const [phase, setPhase] = useState<'summon' | 'reveal' | 'results'>(
    preferences.reduced ? 'results' : 'summon',
  );
  const [index, setIndex] = useState(0);
  const [paused, setPaused] = useState(false);
  const [hidden, setHidden] = useState(document.hidden);
  const [inspect, setInspect] = useState<number | null>(null);
  const lastCue = useRef('');
  const current = receipt.items[index];
  const card = cardById[current.card_id];
  const highest = receipt.items.reduce<Rarity>(
    (best, item) =>
      rank[cardById[item.card_id].rarity] > rank[best] ? cardById[item.card_id].rarity : best,
    'N',
  );
  const complete = phase === 'results';
  const skip = () => {
    navigator.vibrate?.(0);
    audio.stopEffects();
    setPhase('results');
    setPaused(false);
  };
  const advance = () => {
    if (phase === 'summon') {
      setPhase('reveal');
      return;
    }
    if (index + 1 < receipt.items.length) setIndex(index + 1);
    else setPhase('results');
  };
  // A different duration key for every card restarts the clock even for equal rarities.
  const scene = phase === 'summon' ? -1 : phase === 'reveal' ? index : -2;
  useEffect(() => {
    const visibility = () => {
      setHidden(document.hidden);
      if (document.hidden) navigator.vibrate?.(0);
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
    if (preferences.reduced) return;
    const key = receipt.id + ':' + scene;
    if (lastCue.current === key) return;
    lastCue.current = key;
    if (phase === 'summon') audio.cue('gather', preferences.sound);
    else if (phase === 'reveal') {
      audio.cue(card.rarity, preferences.sound);
      if (preferences.vibration !== false && !document.hidden) {
        const pattern =
          card.rarity === 'SP'
            ? [65, 75, 110, 100, 160]
            : card.rarity === 'SSR'
              ? [55, 75, 110]
              : card.rarity === 'SR'
                ? [30, 55, 45]
                : 15;
        navigator.vibrate?.(pattern);
      }
    } else audio.cue('spread', preferences.sound);
  }, [audio, card.rarity, phase, preferences, receipt.id, scene]);
  return (
    <GachaDialog
      title={
        complete
          ? t('此次相遇', 'These encounters')
          : t('星光回应了你的期待', 'The stars have answered')
      }
      onClose={complete ? onClose : skip}
      closeLabel={complete ? t('关闭结果', 'Close results') : t('跳过演出', 'Skip animation')}
      className={
        'gacha-cinema gacha-cinema--' +
        (phase === 'reveal' ? card.rarity : highest) +
        (preferences.reduced ? ' gacha-static' : '') +
        (paused || hidden ? ' is-paused' : '')
      }
    >
      {!complete && (
        <SceneClock
          key={'clock-' + scene}
          milliseconds={phase === 'summon' ? 2550 : duration[card.rarity]}
          paused={paused}
          advance={advance}
        />
      )}
      <div className="gacha-cinema__sky" aria-hidden="true">
        <div className="gacha-cinema__nebula" />
        <div className="gacha-cinema__stars" />
        <div className="gacha-cinema__horizon" />
      </div>
      {phase === 'summon' && (
        <div className="gacha-cinema__summon" aria-label={t('星光汇聚中', 'Gathering starlight')}>
          <div className="gacha-cinema__portal">
            <i />
            <i />
            <i />
            <Icon name="spark" />
          </div>
          <div className="gacha-cinema__comets" aria-hidden="true">
            {Array.from({ length: receipt.items.length }, (_, i) => (
              <i key={i} style={{ '--i': i } as CSSProperties} />
            ))}
          </div>
          <p>{t('循着微光，与你相遇', 'Following the light, we meet')}</p>
        </div>
      )}
      {phase === 'reveal' && (
        <div
          key={'card-' + index}
          className={
            'gacha-cinema__encounter' +
            (isRare(card.rarity) ? ' is-rare' : '') +
            (card.rarity === 'SP' ? ' is-special' : '')
          }
          data-card-id={card.id}
          data-rarity={card.rarity}
          style={{ '--scene-time': duration[card.rarity] + 'ms' } as CSSProperties}
        >
          <img
            className="gacha-cinema__scenery"
            src={isRare(card.rarity) ? themeById[card.theme].art : card.art}
            alt=""
          />
          <div className="gacha-cinema__halo" aria-hidden="true">
            <i />
            <i />
            <i />
          </div>
          <div className="gacha-cinema__rays" aria-hidden="true" />
          <div className="gacha-cinema__silhouette" aria-hidden="true">
            {card.rarity}
          </div>
          <div className="gacha-cinema__art">
            <Artwork card={card} full animated={isRare(card.rarity)} paused={paused || hidden} />
          </div>
          <div className="gacha-cinema__caption">
            <span className="gacha-cinema__eyebrow">
              {t('命运的回响', 'AN ENCOUNTER WRITTEN IN LIGHT')}
            </span>
            <strong className="gacha-cinema__rarity">{card.rarity}</strong>
            <h3>{t(themeById[card.theme].name, themeById[card.theme].name_en)}</h3>
            <p className="gacha-cinema__character">{card.character}</p>
            <p className="gacha-cinema__quote">
              {t(themeById[card.theme].tagline, themeById[card.theme].tagline_en)}
            </p>
            <span className={'gacha-cinema__status' + (current.duplicate ? ' is-duplicate' : '')}>
              <Icon name={current.duplicate ? 'models' : 'spark'} />
              {current.duplicate
                ? t(
                    '重逢 · ' + tierOf(card.rarity) + ' 碎片 +1',
                    'Together again · +1 ' + tierOf(card.rarity) + ' shard',
                  )
                : t('初次相遇 · 已收入图鉴', 'First encounter · collected')}
            </span>
            {card.appearance && !current.duplicate && (
              <span className="gacha-cinema__unlock">
                <Icon name="check" />
                {t('同名主题装扮已解锁', 'Matching appearance unlocked')}
              </span>
            )}
          </div>
          <div className="gacha-cinema__glint" aria-hidden="true" />
        </div>
      )}
      {complete && (
        <section
          className="gacha-cinema__results"
          aria-label={t('全部抽卡结果', 'All draw results')}
        >
          <div className="gacha-cinema__result-heading">
            <span>{t('星光落定，回忆长存', 'THE STARS SETTLE. THE MEMORIES STAY.')}</span>
            <h3>{t('每一次相遇，都值得珍藏', 'Every encounter is worth keeping')}</h3>
          </div>
          <div className={'gacha-cinema__grid' + (receipt.items.length === 1 ? ' is-single' : '')}>
            {receipt.items.map((item, i) => {
              const result = cardById[item.card_id];
              return (
                <div
                  className={'gacha-cinema__result gacha-cinema__result--' + result.rarity}
                  key={i}
                  style={{ '--i': i } as CSSProperties}
                >
                  <CardTile
                    card={result}
                    english={english}
                    index={i}
                    onClick={() => setInspect(i)}
                    note={
                      item.duplicate
                        ? t(
                            tierOf(result.rarity) + ' 碎片 +1',
                            '+1 ' + tierOf(result.rarity) + ' shard',
                          )
                        : t('初次相遇', 'NEW')
                    }
                  />
                  {item.duplicate && (
                    <span
                      className="gacha-cinema__conversion"
                      aria-label={t(
                        '转化为 1 枚 ' + tierOf(result.rarity) + ' 碎片',
                        'Converted to 1 ' + tierOf(result.rarity) + ' shard',
                      )}
                    >
                      <Icon name="models" />
                      <strong>+1</strong>
                      <span>
                        {tierOf(result.rarity)} {t('碎片', 'shard')}
                      </span>
                      <i aria-hidden="true" />
                    </span>
                  )}
                  {result.appearance && !item.duplicate && (
                    <span className="gacha-cinema__outfit">
                      {t('装扮解锁', 'Appearance unlocked')}
                    </span>
                  )}
                </div>
              );
            })}
          </div>
          <p className="gacha-cinema__result-note">
            {t(
              '新卡已收入图鉴，重复卡已转为碎片。',
              'New cards are in your collection. Duplicates became shards.',
            )}
          </p>
          <Button variant="primary" onClick={onClose}>
            {t('收下这份星光', 'Keep these memories')}
          </Button>
        </section>
      )}
      {!complete && (
        <footer className="gacha-cinema__controls">
          <span aria-live="polite">
            {phase === 'summon'
              ? t('期待正在靠近', 'An encounter draws near')
              : index +
                1 +
                ' / ' +
                receipt.items.length +
                ' · ' +
                t('自动播放', 'Playing automatically')}
          </span>
          <div>
            <Button
              variant="ghost"
              onClick={() => {
                audio.stopEffects();
                setPaused(!paused);
                navigator.vibrate?.(0);
              }}
            >
              <Icon name={paused ? 'play' : 'pause'} />
              {paused ? t('继续播放', 'Resume') : t('暂停', 'Pause')}
            </Button>
            <Button variant="ghost" onClick={skip}>
              {t('跳过 · 查看全部', 'Skip · view all')}
            </Button>
          </div>
        </footer>
      )}
      {inspect !== null && (
        <GachaDialog
          title={t(
            cardById[receipt.items[inspect].card_id].name,
            cardById[receipt.items[inspect].card_id].name_en,
          )}
          onClose={() => setInspect(null)}
          className="gacha-inspect"
          closeLabel={t('关闭大图', 'Close artwork')}
        >
          <Artwork
            card={cardById[receipt.items[inspect].card_id]}
            full
            animated={!preferences.reduced && !hidden}
          />
        </GachaDialog>
      )}
    </GachaDialog>
  );
}
function SceneClock({
  milliseconds,
  paused,
  advance,
}: {
  milliseconds: number;
  paused: boolean;
  advance: () => void;
}) {
  useSceneClock(milliseconds, paused, advance);
  return null;
}
