import { useEffect, useRef, useState, type ReactNode } from 'react';
import {
  catalog,
  caught,
  catchValue,
  contractProgress,
  contractReward,
  copies,
  ensureContractBoard,
  fitLoadout,
  levelFromXp,
  pendingSkillTier,
  skillOptions,
  applyAction,
  type Action,
  type Profile,
} from './rules';
import { catalogText, type LakeText } from './copy';
import { art, label, loadoutLabel, loadoutStats, fishConditions } from './presentation';

export type Menu = 'skills' | 'shop' | 'basket' | 'catalog' | 'locations' | 'contracts';
interface MenuProps {
  menu: Menu;
  profile: Profile;
  blocked: boolean;
  text: LakeText;
  onAction: (action: Action) => void;
  close: () => void;
}
function MenuDialog({
  title,
  close,
  children,
  text,
}: {
  title: string;
  close: () => void;
  children: ReactNode;
  text: LakeText;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current,
      previous = document.activeElement;
    dialog?.showModal();
    return () => {
      dialog?.close();
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className="lake-menu"
      onCancel={(event) => {
        event.preventDefault();
        close();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) close();
      }}
      aria-labelledby="lake-menu-title"
    >
      <div className="skill-card">
        <div className="skill-head">
          <h2 id="lake-menu-title">{title}</h2>
          <button
            type="button"
            autoFocus
            className="skill-close"
            onClick={close}
            aria-label={text('close')}
          >
            ×
          </button>
        </div>
        {children}
      </div>
    </dialog>
  );
}
export function LakeMenus({ menu, profile: p, blocked, text, onAction, close }: MenuProps) {
  const [catalogLocation, setCatalogLocation] = useState(p.location);
  const can = (action: Action) => {
    if (blocked) return false;
    try {
      applyAction(p, action);
      return true;
    } catch {
      return false;
    }
  };
  const actionButton = (text: string, action: Action, extraDisabled = false, key?: string) => (
    <button
      type="button"
      key={key}
      disabled={extraDisabled || !can(action)}
      onClick={() => onAction(action)}
    >
      {text}
    </button>
  );
  const name = (group: string, id?: string) => label(text, group, id);
  const titles = {
    skills: text('skills'),
    shop: text('shop'),
    basket: text('basket'),
    catalog: text('catalog'),
    locations: text('locations'),
    contracts: text('contracts'),
  };
  const unlocked = p.basket.filter((c) => !c.locked),
    basketValue = unlocked.reduce((n, c) => n + catchValue(c), 0);
  const debrisValue = Object.entries(catalog.DEBRIS).reduce(
    (n, [id, d]) => n + p.debrisStock[id] * d.value,
    0,
  );
  const board = structuredClone(p);
  ensureContractBoard(board);
  const tiers = [5, 10, 15, 20] as const,
    selected = [p.first, p.second, p.third, p.fourth];
  const pending = pendingSkillTier(p),
    level = levelFromXp(p.xp);
  const rod = catalog.GEAR[p.equipped.rod as keyof typeof catalog.GEAR];
  return (
    <MenuDialog title={titles[menu]} close={close} text={text}>
      {blocked ? <p className="skill-intro">{text('pausedMenus')}</p> : null}
      {menu === 'skills' ? (
        <>
          <p className="skill-intro">{text('skillIntro')}</p>
          <div className="skill-choices">
            {tiers.map((tier, index) => (
              <section key={tier}>
                <h3 className="skill-section-label">
                  {text('skillTier', { level: tier })} ·{' '}
                  {selected[index] ? text('selected') : level < tier ? text('locked') : ''}
                </h3>
                {tier === 10 && !p.first ? <p>{text('skillPrerequisite')}</p> : null}
                {skillOptions(p, tier).map((id) => (
                  <button
                    className={'skill-option' + (selected[index] === id ? ' selected' : '')}
                    aria-pressed={selected[index] === id}
                    key={id}
                    disabled={pending !== tier || !can({ action: 'choose_skill', id })}
                    onClick={() => onAction({ action: 'choose_skill', id })}
                  >
                    <strong>{name('skills', id)}</strong>
                    <span>{catalogText(text, 'skills', id, 'description')}</span>
                  </button>
                ))}
              </section>
            ))}
          </div>
          <p className="skill-summary">
            {selected
              .filter(Boolean)
              .map((id) => name('skills', id))
              .join(' → ') || text('none')}
          </p>
          <div className="utility-row">
            {actionButton(text('respec', { cost: 1000 + level * 50 }), { action: 'respec' })}
            <p>{text('respecHelp')}</p>
          </div>
        </>
      ) : null}
      {menu === 'shop' ? (
        <>
          <p className="skill-intro">{text('shopHelp')}</p>
          <p className="skill-summary">{loadoutStats(text, p, p.equipped)}</p>
          <div className="shop-list">
            <h3 className="shop-group">{text('rods')}</h3>
            {Object.entries(catalog.GEAR)
              .filter(([, g]) => g.slot === 'rod')
              .map(([id, g]) => {
                const owned = copies(p, id) > 0,
                  equipped = p.equipped.rod === id;
                return (
                  <article className={'shop-item' + (equipped ? ' equipped' : '')} key={id}>
                    <strong>
                      {name('gear', id)} ·{' '}
                      {owned
                        ? equipped
                          ? text('equipped')
                          : text('owned', { count: 1 })
                        : text('buy', { cost: g.cost })}
                    </strong>
                    <p>{catalogText(text, 'gear', id, 'description')}</p>
                    <p>{loadoutStats(text, p, fitLoadout({ ...p.equipped, rod: id }))}</p>
                    {actionButton(
                      owned ? text('equip') : text('buy', { cost: g.cost }),
                      owned
                        ? { action: 'equip_gear', id, slot: 'rod' }
                        : { action: 'buy_gear', id },
                      equipped,
                    )}
                  </article>
                );
              })}
            <h3 className="shop-group">{text('tackle')}</h3>
            {Object.entries(catalog.GEAR)
              .filter(([, g]) => g.slot === 'tackle')
              .map(([id, g]) => (
                <article className="shop-item" key={id}>
                  <strong>
                    {name('gear', id)} · {text('owned', { count: copies(p, id) })}
                  </strong>
                  <p>{catalogText(text, 'gear', id, 'description')}</p>
                  {'tackleSlots' in rod && rod.tackleSlots > 0 ? (
                    <p>{loadoutStats(text, p, { ...p.equipped, tackle1: id })}</p>
                  ) : null}
                  <div className="shop-actions">
                    {actionButton(g.cost ? text('buy', { cost: g.cost }) : text('claim'), {
                      action: 'buy_gear',
                      id,
                    })}
                    {(['tackle1', 'tackle2'] as const).map((slot, index) =>
                      actionButton(
                        p.equipped[slot] === id
                          ? text('unequip') + ' · ' + text('tackleSlot', { slot: index + 1 })
                          : text('equipSlot', { slot: index + 1 }),
                        { action: 'equip_gear', slot, id: p.equipped[slot] === id ? '' : id },
                        false,
                        slot,
                      ),
                    )}
                  </div>
                </article>
              ))}
            <h3 className="shop-group">{text('presets')}</h3>
            {p.savedLoadouts.map((saved, index) => (
              <article className="shop-item" key={index}>
                <strong>
                  {text('preset', { slot: index + 1 })} ·{' '}
                  {saved ? loadoutLabel(text, saved) : text('empty')}
                </strong>
                {saved ? <p>{name('baits', saved.bait)}</p> : null}
                <div className="shop-actions">
                  {actionButton(text('savePreset'), { action: 'save_gear_loadout', index })}
                  {actionButton(text('applyPreset'), { action: 'load_gear_loadout', index })}
                </div>
              </article>
            ))}
            <h3 className="shop-group">{text('baits')}</h3>
            {Object.entries(catalog.BAITS).map(([id, b]) => (
              <article
                className={'shop-item' + (p.selectedBait === id ? ' equipped' : '')}
                key={id}
              >
                <strong>
                  {name('baits', id)} · {text('stock', { count: p.baitStock[id] })}
                </strong>
                <p>{catalogText(text, 'baits', id, 'description')}</p>
                <div className="shop-actions">
                  {actionButton(text('buy', { cost: b.cost }), { action: 'buy_bait', id })}
                  {actionButton(p.selectedBait === id ? text('disableBait') : text('useBait'), {
                    action: 'select_bait',
                    id: p.selectedBait === id ? '' : id,
                  })}
                </div>
              </article>
            ))}
          </div>
        </>
      ) : null}
      {menu === 'basket' ? (
        <>
          <p className="skill-intro">{text('basketHelp', { count: p.basket.length })}</p>
          <div className="collection-tools">
            {actionButton(
              text('sellAll', { value: basketValue }),
              { action: 'sell_all_fish' },
              !unlocked.length,
            )}
          </div>
          <div className="shop-list">
            {!p.basket.length ? <p className="collection-empty">{text('basketEmpty')}</p> : null}
            {[...p.basket].reverse().map((c) => (
              <article className={'collection-item' + (c.locked ? ' locked' : '')} key={c.id}>
                <img className="fish-art" src={art(c.kind)} alt="" loading="lazy" />
                <div>
                  <strong>{name('fish', c.kind)}</strong>
                  <span>
                    {text('catchDetail', {
                      length: c.length,
                      quality: catalogText(text, 'quality', 'q' + c.quality, ''),
                      perfect: c.perfect ? text('perfect') : '',
                    })}
                  </span>
                </div>
                <div className="collection-actions">
                  {actionButton(c.locked ? text('unlockFish') : text('lockFish'), {
                    action: 'set_fish_lock',
                    fish_ids: [c.id],
                    locked: !c.locked,
                  })}
                  {actionButton(text('sell', { value: catchValue(c) }), {
                    action: 'sell_fish',
                    fish_ids: [c.id],
                  })}
                </div>
              </article>
            ))}
            <h3>{text('debris')}</h3>
            {Object.entries(catalog.DEBRIS).map(([id, d]) =>
              p.debrisStock[id] ? (
                <article className="collection-item" key={id}>
                  <img src={art(id, true)} className="fish-art" loading="lazy" alt="" />
                  <div>
                    <strong>
                      {name('debris', id)} ×{p.debrisStock[id]}
                    </strong>
                    {actionButton(text('recycle', { value: p.debrisStock[id] * d.value }), {
                      action: 'sell_debris',
                      id,
                    })}
                  </div>
                </article>
              ) : null,
            )}
          </div>
          <div className="utility-row">
            {actionButton(
              text('recycleAll', { value: debrisValue }),
              { action: 'sell_all_debris' },
              !debrisValue,
            )}
            <p>{text('debrisSummary', { trash: p.trashRecovered, treasure: p.treasureOpened })}</p>
          </div>
        </>
      ) : null}
      {menu === 'catalog' ? (
        <>
          <p className="skill-intro">
            {text('catalogSummary', {
              found: Object.keys(p.records).length,
              caught: String(caught(p)),
              perfect: String(
                Object.values(p.records).reduce((n, r) => n + BigInt(r.perfectCount), 0n),
              ),
            })}
          </p>
          <div className="catalog-tabs">
            {Object.keys(catalog.LOCATIONS).map((id) => (
              <button
                key={id}
                type="button"
                aria-pressed={catalogLocation === id}
                className={catalogLocation === id ? 'active' : ''}
                onClick={() => setCatalogLocation(id)}
              >
                {name('locations', id)} ·{' '}
                {catalog.FISH_TYPES.filter((f) => f.location === id && p.records[f.kind]).length}/18
              </button>
            ))}
          </div>
          <div className="shop-list">
            {catalog.FISH_TYPES.filter((f) => f.location === catalogLocation).map((f) => {
              const r = p.records[f.kind];
              return (
                <article className={'catalog-entry' + (r ? '' : ' unknown')} key={f.kind}>
                  {r ? (
                    <img
                      className="fish-art"
                      src={art(f.kind)}
                      alt={name('fish', f.kind)}
                      loading="lazy"
                    />
                  ) : (
                    <div className="fish-art unknown" aria-hidden="true">
                      ?
                    </div>
                  )}
                  <div>
                    <strong>
                      {r
                        ? name('fish', f.kind) + ' · ' + catalogText(text, 'rarities', f.rarity, '')
                        : text('undiscovered')}
                    </strong>
                    <span>{fishConditions(text, f)}</span>
                    {r ? (
                      <>
                        <span>{catalogText(text, 'fish', f.kind, 'description')}</span>
                        <span>
                          {text('record', {
                            count: r.caught,
                            length: r.maxLength,
                            quality: catalogText(text, 'quality', 'q' + r.bestQuality, ''),
                            perfect: r.perfectCount,
                          })}
                        </span>
                      </>
                    ) : null}
                  </div>
                </article>
              );
            })}
          </div>
        </>
      ) : null}
      {menu === 'locations' ? (
        <div className="location-grid">
          {Object.keys(catalog.LOCATIONS).map((id) => (
            <article className="location-option" key={id}>
              <span className={'location-preview ' + id} />
              <strong>
                {name('locations', id)}
                {p.location === id ? ' · ' + text('currentLocation') : ''}
              </strong>
              <p>{catalogText(text, 'locations', id, 'description')}</p>
              <p>
                {text('locationFound', {
                  count: catalog.FISH_TYPES.filter((f) => f.location === id && p.records[f.kind])
                    .length,
                })}
              </p>
              {actionButton(
                name('locations', id),
                { action: 'switch_location', id },
                p.location === id,
              )}
            </article>
          ))}
        </div>
      ) : null}
      {menu === 'contracts' ? (
        <>
          <p className="skill-intro">{text('contractsHelp')}</p>
          <p className="skill-summary">
            {text('contractSummary', {
              active: board.contracts.filter((q) => q.status === 'active').length,
              completed: board.completedContracts,
            })}
          </p>
          <div className="shop-list">
            {board.contracts.map((q) => {
              const reward = contractReward(q);
              const title =
                q.type === 'delivery'
                  ? text('deliveryContract', { target: q.target, fish: name('fish', q.kind) })
                  : q.type === 'catch'
                    ? text('catchContract', {
                        target: q.target,
                        location: name('locations', q.location),
                      })
                    : q.type === 'perfect'
                      ? text('perfectContract', { target: q.target })
                      : text('cleanupContract', { target: q.target });
              return (
                <article
                  className={'quest-card' + (q.status === 'active' ? ' active' : '')}
                  key={q.id}
                >
                  <h3>{title}</h3>
                  <p>
                    {q.status === 'completed'
                      ? text('completed')
                      : q.status === 'active'
                        ? text('active')
                        : text('available')}{' '}
                    · {contractProgress(board, q)}/{q.target}
                  </p>
                  {q.kind ? (
                    <p>
                      {fishConditions(
                        text,
                        catalog.FISH_TYPES.find((f) => f.kind === q.kind)!,
                      )}
                    </p>
                  ) : null}
                  <p>
                    {text('contractReward', {
                      coins: reward.coins,
                      bait: name('baits', reward.bait),
                      count: reward.count,
                    })}
                  </p>
                  <div className="quest-actions">
                    {q.status === 'available' ? (
                      actionButton(text('acceptContract'), { action: 'accept_contract', id: q.id })
                    ) : q.status === 'active' ? (
                      <>
                        {actionButton(text('claimContract'), {
                          action: 'claim_contract',
                          id: q.id,
                        })}
                        {actionButton(text('cancelContract'), {
                          action: 'cancel_contract',
                          id: q.id,
                        })}
                      </>
                    ) : null}
                  </div>
                </article>
              );
            })}
          </div>
        </>
      ) : null}
    </MenuDialog>
  );
}
