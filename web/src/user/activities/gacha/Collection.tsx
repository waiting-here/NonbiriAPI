import { useState } from 'react';
import { Button } from '@shared/components/ui/Button';
import { Field } from '@shared/components/ui/Field';
import { FilterBar } from '@shared/components/ui/FilterBar';
import { Tabs } from '@shared/components/ui/Tabs';
import { Icon } from '@shared/components/Icon';
import { CardTile } from './CardTile';
import { cards, characters, rarities, themes } from './catalog';
import type { Action, Card, Snapshot, Theme } from './types';

export function Collection({
  state,
  english,
  busy,
  act,
  inspect,
  viewGroup,
}: {
  state: Snapshot;
  english: boolean;
  busy: boolean;
  act: (action: Action, message: string) => void;
  inspect: (card: Card) => void;
  viewGroup: (theme: Theme) => void;
}) {
  const t = (zh: string, en: string) => (english ? en : zh);
  const [view, setView] = useState<'cards' | 'themes'>('cards');
  const [search, setSearch] = useState(''),
    [character, setCharacter] = useState(''),
    [rarity, setRarity] = useState(''),
    [owned, setOwned] = useState('');
  const filtered = cards.filter(
    (c) =>
      (c.name + c.name_en).toLowerCase().includes(search.toLowerCase()) &&
      (!character || c.character === character) &&
      (!rarity || c.rarity === rarity) &&
      (!owned || !!state.owned[c.id] === (owned === 'yes')),
  );
  return (
    <div className="gacha-collection">
      <div className="gacha-section-heading">
        <div>
          <span className="gacha-eyebrow">THE MEMORY ATLAS</span>
          <h2>{t('每一次相遇，都值得珍藏', 'Every encounter is worth keeping')}</h2>
        </div>
        <p>
          <strong>{Object.keys(state.owned).length}</strong> / {cards.length}{' '}
          {t('已收集', 'collected')}
        </p>
      </div>
      <Tabs
        label={t('图鉴视图', 'Collection view')}
        value={view}
        onChange={setView}
        tabs={[
          { value: 'cards', label: t('卡牌图鉴', 'Cards') },
          { value: 'themes', label: t('主题合照', 'Theme memories') },
        ]}
      />
      {view === 'cards' ? (
        <>
          <FilterBar
            ariaLabel={t('查找卡牌', 'Find cards')}
            search={
              <Field label={t('搜索', 'Search')}>
                {(props) => (
                  <input
                    {...props}
                    value={search}
                    placeholder={t('搜索主题或角色', 'Search a theme or character')}
                    onChange={(e) => setSearch(e.target.value)}
                  />
                )}
              </Field>
            }
            secondaryLabel={t('筛选', 'Filters')}
            activeCount={[character, rarity, owned].filter(Boolean).length}
            onSubmit={() => {}}
            secondary={
              <>
                <Field label={t('角色', 'Character')}>
                  {(props) => (
                    <select
                      {...props}
                      value={character}
                      onChange={(e) => setCharacter(e.target.value)}
                    >
                      <option value="">{t('全部角色', 'All characters')}</option>
                      {characters.map((c) => (
                        <option key={c}>{c}</option>
                      ))}
                    </select>
                  )}
                </Field>
                <Field label={t('稀有度', 'Rarity')}>
                  {(props) => (
                    <select {...props} value={rarity} onChange={(e) => setRarity(e.target.value)}>
                      <option value="">{t('全部稀有度', 'All rarities')}</option>
                      {rarities.map((r) => (
                        <option key={r}>{r}</option>
                      ))}
                    </select>
                  )}
                </Field>
                <Field label={t('收集状态', 'Collection status')}>
                  {(props) => (
                    <select {...props} value={owned} onChange={(e) => setOwned(e.target.value)}>
                      <option value="">{t('全部', 'All')}</option>
                      <option value="yes">{t('已拥有', 'Owned')}</option>
                      <option value="no">{t('未收集', 'Not collected')}</option>
                    </select>
                  )}
                </Field>
              </>
            }
          />
          <div className="gacha-catalog">
            {filtered.map((card) => (
              <CardTile
                key={card.id}
                card={card}
                english={english}
                owned={!!state.owned[card.id]}
                note={
                  state.owned[card.id] ? t('已珍藏', 'Collected') : t('未收集', 'Not collected')
                }
                onClick={() => inspect(card)}
              />
            ))}
          </div>
          {!filtered.length && (
            <div className="gacha-empty">
              <Icon name="search" />
              <h3>{t('没有找到这张卡', 'No cards found')}</h3>
              <p>{t('试试其他角色或主题名称。', 'Try another character or theme.')}</p>
              <Button
                onClick={() => {
                  setSearch('');
                  setCharacter('');
                  setRarity('');
                  setOwned('');
                }}
              >
                {t('清除筛选', 'Clear filters')}
              </Button>
            </div>
          )}
        </>
      ) : (
        <div className="gacha-themes">
          {themes.map((theme) => {
            const members = cards.filter((c) => c.theme === theme.id),
              collected = members.filter((c) => state.owned[c.id]).length;
            const complete = collected === members.length,
              claimed = state.claimed.includes(theme.id);
            return (
              <article className={'gacha-theme' + (complete ? ' is-complete' : '')} key={theme.id}>
                <button
                  className="gacha-theme__image"
                  disabled={!complete}
                  onClick={() => viewGroup(theme)}
                  aria-label={t('欣赏 ' + theme.name, 'View ' + theme.name_en)}
                >
                  <img
                    src={theme.art}
                    alt=""
                    loading="lazy"
                    className={complete ? '' : 'is-locked'}
                  />
                  {!complete && (
                    <span>
                      <Icon name="shield" />
                      {t('集齐后解锁合照', 'Complete the theme to reveal')}
                      <strong>{collected} / 5</strong>
                    </span>
                  )}
                  {complete && (
                    <span className="gacha-theme__unlocked">
                      <Icon name="spark" />
                      {t('欣赏合照', 'View memory')}
                    </span>
                  )}
                </button>
                <div className="gacha-theme__body">
                  <span className="gacha-eyebrow">
                    {theme.rarity} ·{' '}
                    {theme.source === 'permanent'
                      ? t('常驻主题', 'Permanent theme')
                      : theme.source === 'limited'
                        ? t('限定主题', 'Limited theme')
                        : t('商店专属', 'Shop exclusive')}
                  </span>
                  <h3>{t(theme.name, theme.name_en)}</h3>
                  <p>{t(theme.tagline, theme.tagline_en)}</p>
                  <div className="gacha-theme__members">
                    {members.map((c) => (
                      <button
                        key={c.id}
                        title={c.character}
                        aria-label={t(c.name, c.name_en)}
                        className={state.owned[c.id] ? 'is-owned' : ''}
                        onClick={() => inspect(c)}
                      >
                        <img src={c.thumb} alt={c.character} loading="lazy" />
                        {state.owned[c.id] && <Icon name="check" />}
                      </button>
                    ))}
                  </div>
                  <div className="gacha-theme__reward">
                    <span>
                      {t('集齐奖励', 'Completion reward')}
                      <strong>
                        1{' '}
                        {theme.source === 'permanent'
                          ? t('常驻券', 'standard ticket')
                          : t('限定券', 'limited ticket')}{' '}
                        + 10 {t('捐赠回馈', 'donation credit')}
                      </strong>
                    </span>
                    <Button
                      size="sm"
                      disabled={busy || !complete || claimed}
                      onClick={() =>
                        act(
                          { action: 'claim', theme: theme.id },
                          t('主题奖励已领取', 'Theme reward claimed'),
                        )
                      }
                    >
                      {claimed
                        ? t('已领取', 'Claimed')
                        : complete
                          ? t('领取奖励', 'Claim reward')
                          : t('待集齐', 'Collect all five')}
                    </Button>
                  </div>
                </div>
              </article>
            );
          })}
        </div>
      )}
    </div>
  );
}
