import { useEffect, useState } from 'react';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';
import { cardById } from './catalog';
import { CardTile } from './CardTile';
import { Artwork } from './Artwork';
import { GachaDialog } from './GachaDialog';
import type { GachaAudio } from './audio';
import type { DrawReceipt, Preferences } from './types';

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
  const [phase, setPhase] = useState<'gather' | 'cards'>(preferences.reduced ? 'cards' : 'gather');
  const [shown, setShown] = useState<number[]>([]);
  const [spotlight, setSpotlight] = useState<number | null>(null);
  const [motion, setMotion] = useState(!preferences.reduced);
  useEffect(() => {
    audio.cue('gather', preferences.sound);
    if (preferences.reduced) return;
    const timer = window.setTimeout(() => {
      setPhase('cards');
      audio.cue('spread', preferences.sound);
    }, 1400);
    return () => window.clearTimeout(timer);
  }, [audio, preferences.reduced, preferences.sound]);
  function reveal(index: number) {
    const item = receipt.items[index],
      card = cardById[item.card_id];
    if (!shown.includes(index)) {
      setShown((old) => [...old, index]);
      audio.cue(card.rarity, preferences.sound);
    }
    if (card.rarity === 'SSR') {
      setSpotlight(index);
      audio.cue('spotlight', preferences.sound);
    }
  }
  const all = shown.length === receipt.items.length;
  function revealAll() {
    setPhase('cards');
    setShown(receipt.items.map((_, i) => i));
    audio.cue('spread', preferences.sound);
  }
  const featured = spotlight === null ? null : cardById[receipt.items[spotlight].card_id];
  return (
    <GachaDialog
      title={t('星光回应了你的期待', 'The stars have answered')}
      onClose={onClose}
      className={'gacha-reveal' + (preferences.reduced ? ' gacha-static' : '')}
      closeLabel={t('关闭结果', 'Close results')}
    >
      {phase === 'gather' ? (
        <div className="gacha-summon" aria-label={t('星光汇聚中', 'Gathering starlight')}>
          <div className="gacha-summon__ring" />
          <div className="gacha-summon__ring second" />
          <Icon name="spark" />
          <p>{t('将期待，交给星光', 'Let the stars carry your wishes')}</p>
          <Button variant="ghost" onClick={revealAll}>
            {t('跳过演出', 'Skip animation')}
          </Button>
        </div>
      ) : (
        <>
          <p className="gacha-reveal__subtitle">
            {t('点击卡牌，揭开这一次的相遇', 'Tap a card to reveal your encounter')}{' '}
            <span>
              {shown.length} / {receipt.items.length}
            </span>
          </p>
          <div
            className={
              'gacha-results' + (receipt.items.length === 1 ? ' gacha-results--single' : '')
            }
          >
            {receipt.items.map((item, i) =>
              shown.includes(i) ? (
                <CardTile
                  key={i}
                  card={cardById[item.card_id]}
                  index={i}
                  english={english}
                  onClick={() => reveal(i)}
                  note={
                    item.duplicate
                      ? t(
                          '+1 ' + cardById[item.card_id].rarity + ' 碎片',
                          '+1 ' + cardById[item.card_id].rarity + ' shard',
                        )
                      : t('初次相遇', 'NEW')
                  }
                />
              ) : (
                <button
                  key={i}
                  className="gacha-cardback"
                  aria-label={t('揭晓第 ' + (i + 1) + ' 张', 'Reveal card ' + (i + 1))}
                  onClick={() => reveal(i)}
                >
                  <span className="gacha-cardback__ring">
                    <Icon name="spark" />
                  </span>
                  <span>NONBIRI</span>
                  <small>{i + 1}</small>
                </button>
              ),
            )}
          </div>
          <footer className="gacha-reveal__actions">
            {all ? (
              <>
                <p>
                  {t(
                    '新卡已收入图鉴，重复卡已转为碎片。',
                    'New cards are in your collection. Duplicates became shards.',
                  )}
                </p>
                <Button variant="primary" onClick={onClose}>
                  {t('收下这份星光', 'Keep these memories')}
                </Button>
              </>
            ) : (
              <>
                <Button onClick={revealAll}>{t('全部揭晓', 'Reveal all')}</Button>
                <Button
                  variant="primary"
                  onClick={() => reveal(receipt.items.findIndex((_, i) => !shown.includes(i)))}
                >
                  {t('揭晓下一张', 'Reveal next')}
                </Button>
              </>
            )}
          </footer>
        </>
      )}
      {featured && spotlight !== null && (
        <GachaDialog
          title={t(featured.name, featured.name_en)}
          onClose={() => setSpotlight(null)}
          className="gacha-spotlight"
          closeLabel={t('返回揭卡', 'Back to reveals')}
        >
          <div className="gacha-spotlight__stage">
            <Artwork key={featured.id} card={featured} full animated={motion} />
            <div className="gacha-spotlight__caption">
              <span>SSR</span>
              <h3>{t(featured.name, featured.name_en)}</h3>
              <p>
                {receipt.items[spotlight].duplicate
                  ? t('再次相遇 · SSR 卡牌碎片 +1', 'Together again · +1 SSR shard')
                  : t('初次相遇 · 已收入图鉴', 'A new encounter · added to collection')}
              </p>
            </div>
          </div>
          <footer>
            <Button onClick={() => setMotion(!motion)}>
              <Icon name={motion ? 'pause' : 'play'} />
              {motion ? t('暂停动态', 'Pause animation') : t('播放动态', 'Play animation')}
            </Button>
            <Button variant="primary" onClick={() => setSpotlight(null)}>
              {t('继续揭晓', 'Continue')}
            </Button>
          </footer>
        </GachaDialog>
      )}
    </GachaDialog>
  );
}
