import { PercentInput } from '../features/games/PercentInput';
import { DraftLeaveGuard } from '@shared/components/charity/DraftLeaveScope';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { cloneElement, useState, type ReactElement, type ReactNode } from 'react';
import { ArtworkImage, ArtworkBadge, ArtworkUnavailable } from '@shared/artwork/ArtworkImage';
import { ConfiguredArtworkPolicy } from '@shared/artwork/policy';
import { useArtworkPolicy } from '@shared/artwork/context';
import { artworkCovers } from '@shared/artwork/catalog';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import {
  Affix,
  Button,
  Field as SharedField,
  Fields,
  Fold,
  Note,
  Panel,
  PanelBody,
  PanelHead,
  SaveBar,
  StatusBadge,
  Tabs,
  Toggle,
} from '@shared/components/ui';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { formatCount } from '@shared/utils/formatNumber';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { TimeInput } from '@shared/components/TimeInput';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { operationKey } from '@shared/operations/api';
import { scaledAmount } from '@shared/inactivity/amounts';
import { useSearchState } from '@shared/operations/useSearchState';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useAdminSession } from '../data';
import { getGachaConfiguration, saveGachaConfiguration } from '../../user/activities/gacha/api';
import { GachaDialog } from '../../user/activities/gacha/GachaDialog';
import { cards, themes, rarities } from '../../user/activities/gacha/catalog';
import { consolidatedRates, defaultRules, poolOpen } from '../../user/activities/gacha/rules';
import type {
  AdminInput,
  Configuration,
  Pool,
  Preset,
  Rules,
  ShopSettings,
} from '../../user/activities/gacha/types';
import '../../user/activities/gacha/gacha.css';
import './gacha-settings.css';
import { GachaPoolStatistics } from '../features/gacha/GachaPoolStatistics';
import { GachaShopStatistics } from '../features/gacha/GachaShopStatistics';

const queryKey = ['admin', 'gacha'] as const;
type Copy = (zh: string, en: string) => string;
function Field({
  label,
  help,
  children,
}: {
  label: ReactNode;
  help?: ReactNode;
  children: ReactElement<Record<string, unknown>>;
}) {
  return (
    <SharedField label={label} help={help}>
      {(props) => cloneElement(children, props)}
    </SharedField>
  );
}
type Draft = { pool: Pool; preset?: Preset };
const covers = artworkCovers.map(({ id, name, name_en }) => [id, name, name_en]);
function emptyPool(at: number): Pool {
  return {
    id: 'gp_' + operationKey(),
    name: '',
    kind: 'limited',
    cover: 'i',
    group: '',
    rules: structuredClone(defaultRules),
    featured: [],
    starts_at: at,
    ends_at: at + 30 * 86400,
    paused: false,
    published: false,
    revision: 0,
    rates: consolidatedRates(defaultRules),
  };
}
function RulesFields({
  value,
  onChange,
  limited = false,
  disabled = false,
  t,
}: {
  value: Rules;
  onChange: (rules: Rules) => void;
  limited?: boolean;
  disabled?: boolean;
  t: Copy;
}) {
  const rates = consolidatedRates(value);
  const weightTotal = value.weights.reduce((sum, part) => sum + part, 0);
  return (
    <fieldset className="gacha-admin-fieldset" disabled={disabled}>
      <div className="gacha-admin-grid">
        {rarities.map((rarity, i) => (
          <Field key={rarity} label={rarity + t(' 基础概率（%）', ' base rate (%)')}>
            <PercentInput
              unit="%"
              max="100"
              basisPoints={value.weights[i]}
              onBasisChange={(points) => {
                const weights = [...value.weights] as Rules['weights'];
                weights[i] = points;
                onChange({ ...value, weights });
              }}
            />
          </Field>
        ))}
      </div>
      <p className={weightTotal === 10000 ? 'gacha-admin-help' : 'gacha-admin-invalid'}>
        {t('基础概率合计：', 'Base rate total: ')}
        {Number.isFinite(weightTotal) ? `${weightTotal / 100}%` : '—'}
      </p>
      <div className="gacha-admin-grid">
        <Field
          label={t('SR 或以上硬保底（抽）', 'SR or better hard pity (pulls)')}
          help={t('1–100 抽', '1–100 pulls')}
        >
          <Affix
            unit={t('抽', 'pulls')}
            type="number"
            min="1"
            max="100"
            value={Number.isNaN(value.sr_pity) ? '' : value.sr_pity}
            onChange={(e) => onChange({ ...value, sr_pity: e.currentTarget.valueAsNumber })}
          />
        </Field>
        <Field
          label={t('SSR 硬保底（抽）', 'SSR hard pity (pulls)')}
          help={t('不小于 SR 保底，最多 1000 抽', 'At least the SR limit; up to 1000 pulls')}
        >
          <Affix
            unit={t('抽', 'pulls')}
            type="number"
            min={Number.isFinite(value.sr_pity) ? value.sr_pity : 1}
            max="1000"
            value={Number.isNaN(value.ssr_pity) ? '' : value.ssr_pity}
            onChange={(e) => onChange({ ...value, ssr_pity: e.currentTarget.valueAsNumber })}
          />
        </Field>
      </div>
      {limited && (
        <div className="gacha-admin-grid">
          {(['sr', 'ssr'] as const).map((tier) => (
            <div key={tier} className="nb-stack nb-stack--sm">
              <Field label={tier.toUpperCase() + t(' 档限定占比（%）', ' featured share (%)')}>
                <PercentInput
                  unit="%"
                  max="100"
                  basisPoints={value[tier === 'sr' ? 'sr_featured' : 'ssr_featured']}
                  onBasisChange={(points) =>
                    onChange({
                      ...value,
                      [tier === 'sr' ? 'sr_featured' : 'ssr_featured']: points,
                    })
                  }
                />
              </Field>
              <Toggle
                disabled={disabled}
                label={
                  tier.toUpperCase() +
                  t(' 连续未中后的限定保证', ' featured guarantee after consecutive misses')
                }
                checked={value[tier === 'sr' ? 'sr_guarantee' : 'ssr_guarantee']}
                onChange={(checked) =>
                  onChange({
                    ...value,
                    [tier === 'sr' ? 'sr_guarantee' : 'ssr_guarantee']: checked,
                  })
                }
              />
              {tier === 'ssr' && (
                <Field label={t('触发保证前最多未中次数', 'Misses before guaranteed featured hit')}>
                  <select
                    disabled={disabled || !value.ssr_guarantee}
                    value={Math.max(1, value.ssr_guarantee_misses ?? 1)}
                    onChange={(e) =>
                      onChange({
                        ...value,
                        ssr_guarantee_misses: Number(e.target.value) === 1 ? 0 : 2,
                      })
                    }
                  >
                    <option value="1">
                      {t('一次：下次 SSR 必中', 'One: next SSR is featured')}
                    </option>
                    <option value="2">
                      {t('两次：第三次 SSR 必中', 'Two: third SSR is featured')}
                    </option>
                  </select>
                </Field>
              )}
            </div>
          ))}
        </div>
      )}
      <Note>
        {t('含硬保底的长期综合概率：', 'Long-run rates including hard pity: ')}
        {rarities.map((rarity, i) => rarity + ' ' + (rates[i] * 100).toFixed(4) + '%').join(' · ')}
      </Note>
    </fieldset>
  );
}
function validRules(r: Rules) {
  return (
    [r.sr_featured, r.ssr_featured].every((n) => Number.isInteger(n) && n >= 0 && n <= 10000) &&
    r.weights.every((n) => Number.isInteger(n) && n >= 0 && n <= 10000) &&
    r.weights.reduce((a, b) => a + b, 0) === 10000 &&
    Number.isInteger(r.sr_pity) &&
    r.sr_pity >= 1 &&
    r.sr_pity <= 100 &&
    Number.isInteger(r.ssr_pity) &&
    r.ssr_pity >= r.sr_pity &&
    r.ssr_pity <= 1000
  );
}
function SettingsForm({
  config,
  busy,
  save,
  t,
}: {
  config: Configuration;
  busy: boolean;
  save: (input: AdminInput) => void;
  t: Copy;
}) {
  const [base, setBase] = useState(config);
  const [entryEnabled, setEntryEnabled] = useState(config.entry_enabled);
  const [rules, setRules] = useState(config.rules),
    [shop, setShop] = useState(config.shop);
  const dirty =
    entryEnabled !== base.entry_enabled ||
    JSON.stringify(rules) !== JSON.stringify(base.rules) ||
    JSON.stringify(shop) !== JSON.stringify(base.shop);
  const stale = base.revision !== config.revision;
  if (
    stale &&
    (!dirty ||
      (entryEnabled === config.entry_enabled &&
        JSON.stringify(rules) === JSON.stringify(config.rules) &&
        JSON.stringify(shop) === JSON.stringify(config.shop)))
  ) {
    setBase(config);
    setEntryEnabled(config.entry_enabled);
    setRules(config.rules);
    setShop(config.shop);
  }
  const shopValid = Object.entries(shop).every(([key, value]) => {
    if (typeof value === 'string') {
      const amount = scaledAmount(value, 3);
      return amount !== null && BigInt(amount) > 0n && BigInt(amount) <= 9_000_000_000_000_000n;
    }
    return (
      key === 'revision' || (Number.isSafeInteger(value) && value >= (key.endsWith('_cap') ? 0 : 1))
    );
  });
  const settingsValid = validRules(rules) && shopValid;
  const commitSettings = () =>
    !stale &&
    settingsValid &&
    save({
      kind: 'settings',
      expected_revision: base.revision,
      rules,
      shop,
      entry_enabled: entryEnabled,
    });
  const discardSettings = () => {
    setBase(config);
    setEntryEnabled(config.entry_enabled);
    setRules(config.rules);
    setShop(config.shop);
  };
  const fields: [keyof ShopSettings, string, string][] = [
    ['n_ticket', 'N 碎片 → 1 常驻券', 'N shards → 1 standard ticket'],
    ['r_ticket', 'R 碎片 → 1 常驻券', 'R shards → 1 standard ticket'],
    ['sr_ticket', 'SR 碎片 → 1 限定券', 'SR shards → 1 limited ticket'],
    ['ssr_tickets', 'SSR 碎片 → 2 限定券', 'SSR shards → 2 limited tickets'],
    ['permanent_cap', '每月常驻券合计上限', 'Monthly standard ticket limit'],
    ['limited_cap', '每月限定券合计上限', 'Monthly limited ticket limit'],
    ['sr_card', '兑换限定 SR 所需 SR 碎片', 'SR shards per limited SR card'],
    ['ssr_card', '兑换限定 SSR 所需 SSR 碎片', 'SSR shards per limited SSR card'],
    ['sp_card', '兑换限定 SP 所需 SSR 碎片', 'SSR shards per limited SP card'],
    ['chibi_n', 'Q 版卡所需 N 碎片', 'N shards per chibi card'],
    ['chibi_r', 'Q 版卡所需 R 碎片', 'R shards per chibi card'],
  ];
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        commitSettings();
      }}
    >
      <Panel>
        <PanelHead
          title={t('永续智能', 'Perpetual intelligence')}
          description={t(
            '调整基础概率立即生效；当前保底取已保护的位置与新位置中更近的一处。',
            'Base rates apply immediately. Current pity retains whichever protected or new limit is closer.',
          )}
        />
        <PanelBody>
          <Toggle
            disabled={busy}
            checked={entryEnabled}
            onChange={setEntryEnabled}
            label={t('在用户站活动页显示入口', 'Show the entry on the user activity page')}
            description={t(
              '默认隐藏。此开关控制入口展示，不改变已发布奖池的开放时间。',
              'Hidden by default. This controls the entry; published pool schedules stay in effect.',
            )}
          />
          <RulesFields value={rules} onChange={setRules} disabled={busy} t={t} />
        </PanelBody>
      </Panel>
      <Panel>
        <PanelHead
          title={t('票券兑换与神秘商店', 'Tickets and mystery shop')}
          description={t(
            '只影响后续兑换；本月已兑换数量和每卡仅兑一次的记录继续保留。',
            'Changes affect future exchanges. Monthly usage and once-per-card records remain.',
          )}
        />
        <PanelBody>
          <fieldset disabled={busy} className="gacha-admin-fieldset">
            <Fields columns={2} legend={t('票券积分价格', 'Ticket prices')}>
              {(['permanent', 'limited'] as const).map((ticket) => (
                <Field
                  key={ticket}
                  label={
                    ticket === 'permanent'
                      ? t('每张常驻券所需通用积分', 'General credits per standard ticket')
                      : t('每张限定券所需通用积分', 'General credits per limited ticket')
                  }
                >
                  <Affix
                    unit={t('通用积分', 'general credits')}
                    required
                    type="text"
                    inputMode="decimal"
                    maxLength={20}
                    value={shop[ticket === 'permanent' ? 'permanent_price' : 'limited_price']}
                    onChange={(e) =>
                      setShop({
                        ...shop,
                        [ticket === 'permanent' ? 'permanent_price' : 'limited_price']:
                          e.target.value,
                      })
                    }
                  />
                </Field>
              ))}
            </Fields>
            {[
              {
                title: t('每月兑换上限', 'Monthly exchange limits'),
                keys: ['permanent_cap', 'limited_cap'],
              },
              {
                title: t('碎片兑换票券', 'Shards for tickets'),
                keys: ['n_ticket', 'r_ticket', 'sr_ticket', 'ssr_tickets'],
              },
              {
                title: t('限定卡兑换成本', 'Limited card costs'),
                keys: ['sr_card', 'ssr_card', 'sp_card'],
              },
              { title: t('Q版卡兑换成本', 'Chibi card costs'), keys: ['chibi_n', 'chibi_r'] },
            ].map((group) => (
              <Fields
                key={group.title}
                columns={group.keys.length === 2 || group.keys.length === 4 ? 2 : undefined}
                legend={group.title}
              >
                {fields
                  .filter(([key]) => group.keys.includes(key))
                  .map(([key, zh, en]) => (
                    <Field key={key} label={t(zh, en)}>
                      <input
                        required
                        type="number"
                        min={key.endsWith('_cap') ? '0' : '1'}
                        step="1"
                        value={Number.isNaN(shop[key]) ? '' : shop[key]}
                        onChange={(e) => setShop({ ...shop, [key]: e.currentTarget.valueAsNumber })}
                      />
                    </Field>
                  ))}
              </Fields>
            ))}
          </fieldset>
          <p className="nb-small nb-muted">
            {t(
              'Q 版卡同时消耗 N、R 两种碎片。两种券的月额度分别按产出的票券张数累计。',
              'A chibi card costs both N and R shards. Monthly limits count the number of tickets received.',
            )}
          </p>
        </PanelBody>
      </Panel>
      <DraftLeaveGuard dirty={dirty} />
      {dirty && !settingsValid && (
        <Note tone="warn">
          {t(
            '请检查概率合计、保底、票价与商店数量。',
            'Check the total probability, pity limits, ticket prices and shop quantities.',
          )}
        </Note>
      )}
      {stale && (
        <Note tone="warn">
          {t(
            '服务器配置已更新。请放弃草稿以载入最新设置。',
            'The server configuration changed. Discard this draft to load the latest settings.',
          )}
        </Note>
      )}
      <SaveBar
        dirtyCount={dirty ? 1 : 0}
        scope={t('规则与商店', 'Rules and shop')}
        busy={busy}
        saveDisabled={stale || !settingsValid}
        onSave={commitSettings}
        onDiscard={discardSettings}
        saveLabel={t('保存规则与商店设置', 'Save rules and shop settings')}
        discardLabel={t('放弃改动', 'Discard')}
        dirtyLabel={() => t('规则与商店设置有未保存改动', 'Rules and shop have unsaved changes')}
      />
    </form>
  );
}
function PoolEditor({
  draft,
  config,
  busy,
  save,
  close,
  feedback,
  t,
}: {
  draft: Draft;
  config: Configuration;
  busy: boolean;
  save: (input: AdminInput) => void;
  close: () => void;
  feedback: ReactNode;
  t: Copy;
}) {
  const [pool, setPool] = useState(draft.pool);
  const [cardSearch, setCardSearch] = useState('');
  const [discarding, setDiscarding] = useState(false);
  const artworkPolicy = useArtworkPolicy();
  const [presetName, setPresetName] = useState(draft.preset?.name ?? draft.pool.name);
  const [start, setStart] = useState(() => createTimeDraft(draft.pool.starts_at));
  const [end, setEnd] = useState(() => createTimeDraft(draft.pool.ends_at));
  const locked =
    !draft.preset &&
    draft.pool.published &&
    draft.pool.starts_at !== null &&
    draft.pool.starts_at <= config.server_time;
  const startValue = timeDraftValue(start),
    endValue = timeDraftValue(end);
  const sameGroup = config.pools.filter(
    (p) =>
      p.kind === 'limited' &&
      p.id !== pool.id &&
      JSON.stringify(p.rules) === JSON.stringify(pool.rules),
  );
  const withTime = { ...pool, starts_at: startValue ?? null, ends_at: endValue ?? null };
  const dirty =
    JSON.stringify(pool) !== JSON.stringify(draft.pool) ||
    presetName !== (draft.preset?.name ?? draft.pool.name) ||
    startValue !== draft.pool.starts_at ||
    endValue !== draft.pool.ends_at;
  const currentRevision = draft.preset
    ? config.presets.find((item) => item.id === draft.preset?.id)?.revision
    : config.pools.find((item) => item.id === draft.pool.id)?.revision;
  const stale =
    (draft.preset?.revision ?? draft.pool.revision) > 0 &&
    currentRevision !== (draft.preset?.revision ?? draft.pool.revision);
  const term = cardSearch.toLocaleLowerCase().trim();
  const matches = cards.filter(
    (card) =>
      card.source === 'limited' &&
      [
        card.id,
        card.character,
        card.theme,
        ...themes
          .filter((theme) => theme.id === card.theme)
          .flatMap((theme) => [theme.name, theme.name_en]),
      ].some((value) => value.toLocaleLowerCase().includes(term)),
  );
  const valid =
    !stale &&
    (!draft.preset || Boolean(presetName.trim())) &&
    startValue !== undefined &&
    endValue !== undefined &&
    (startValue == null || endValue == null || endValue > startValue) &&
    validRules(pool.rules) &&
    (artworkPolicy.allowed('cover:' + pool.cover) ||
      (pool.cover === draft.pool.cover &&
        (draft.pool.revision > 0 || (draft.preset?.revision ?? 0) > 0))) &&
    !!pool.name.trim() &&
    pool.featured.length > 0 &&
    (!pool.published || (startValue != null && endValue != null && endValue > startValue));
  const chooseTheme = (theme: string) => {
    const ids = matches.filter((c) => c.theme === theme).map((c) => c.id);
    const all = ids.every((id) => pool.featured.includes(id));

    setPool({
      ...pool,
      featured: all
        ? pool.featured.filter((id) => !ids.includes(id))
        : [...new Set([...pool.featured, ...ids])],
    });
  };
  return (
    <GachaDialog
      title={
        draft.preset
          ? t('编辑配置模板', 'Edit configuration template')
          : t('限定奖池', 'Limited pool')
      }
      closeLabel={t('关闭', 'Close')}
      onClose={() => {
        if (!busy) {
          if (dirty) setDiscarding(true);
          else close();
        }
      }}
      className="gacha-admin-dialog"
    >
      <DraftLeaveGuard dirty={dirty} />
      {discarding && (
        <ConfirmDialog
          open
          danger
          title={t('放弃本次编辑？', 'Discard these edits?')}
          description={t(
            '未保存的选卡和配置将被丢弃。',
            'Unsaved card selections and settings will be discarded.',
          )}
          confirmLabel={t('放弃改动', 'Discard')}
          onCancel={() => setDiscarding(false)}
          onConfirm={close}
        />
      )}
      {feedback}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!valid || busy) return;
          if (draft.preset)
            save({
              kind: 'preset',
              expected_revision: draft.preset.revision,
              preset: { ...draft.preset, name: presetName, pool: withTime },
            });
          else save({ kind: 'pool', expected_revision: pool.revision, pool: withTime });
        }}
      >
        <fieldset disabled={busy} className="gacha-admin-fieldset">
          {draft.preset && (
            <Field label={t('模板名称', 'Template name')}>
              <input
                required
                maxLength={80}
                value={presetName}
                onChange={(e) => setPresetName(e.target.value)}
              />
            </Field>
          )}
          <div className="gacha-admin-grid">
            <Field label={t('奖池名称', 'Pool name')}>
              <input
                required
                maxLength={80}
                value={pool.name}
                onChange={(e) => setPool({ ...pool, name: e.target.value })}
              />
            </Field>
            <Field label={t('英文名称（可留空）', 'English name (optional)')}>
              <input
                maxLength={80}
                value={pool.name_en ?? ''}
                onChange={(e) => setPool({ ...pool, name_en: e.target.value })}
              />
            </Field>
          </div>
          <h3>{t('内置封面', 'Built-in cover')}</h3>
          <div className="gacha-admin-covers">
            {covers.map(([id, zh, en]) => (
              <button
                type="button"
                key={id}
                aria-pressed={pool.cover === id}
                disabled={!artworkPolicy.allowed('cover:' + id)}
                onClick={() => setPool({ ...pool, cover: id })}
              >
                <ArtworkImage
                  artworkKey={'cover:' + id}
                  src={'/assets/gacha/covers/' + id + '.webp'}
                  alt=""
                />
                <ArtworkBadge artworkKey={'cover:' + id} />
                <ArtworkUnavailable artworkKey={'cover:' + id} />
                <span>{t(zh, en)}</span>
              </button>
            ))}
          </div>
          {!draft.preset && (
            <>
              <div className="gacha-admin-grid">
                <Field label={t('开放时间', 'Starts at')}>
                  <TimeInput station="admin" draft={start} onChange={setStart} disabled={locked} />
                </Field>
                <Field label={t('结束时间', 'Ends at')}>
                  <TimeInput station="admin" draft={end} onChange={setEnd} />
                </Field>
              </div>
              <Toggle
                label={t('发布奖池，按配置时间开放', 'Publish and open on schedule')}
                checked={pool.published}
                disabled={locked || busy}
                onChange={(published) => setPool({ ...pool, published })}
              />
              <Toggle
                label={t('暂停奖池和限定兑换', 'Pause drawing and limited exchanges')}
                checked={pool.paused}
                disabled={busy}
                onChange={(paused) => setPool({ ...pool, paused })}
              />
            </>
          )}
          {locked && (
            <Note>
              {t(
                '此奖池已开放，规则、卡牌、开始时间已锁定。可以改名、换封面、暂停或延长结束时间。',
                'This pool has opened. Rules, cards and start time are locked. You can rename it, change its cover, pause it or extend its end time.',
              )}
            </Note>
          )}
          <Fold plain title={t('概率与保底', 'Rates and guarantees')} defaultOpen>
            <RulesFields
              value={pool.rules}
              onChange={(rules) => setPool({ ...pool, rules })}
              limited
              disabled={locked || busy}
              t={t}
            />
            <p>
              {sameGroup.length
                ? t('与以下奖池共享并继承保底：', 'Shares and inherits pity with: ') +
                  sameGroup.map((p) => p.name).join('、')
                : t(
                    '当前没有其他奖池使用这一组规则；以后相同规则的奖池会共享此进度。',
                    'No other pool currently uses these rules. Future pools with the same rules will share this progress.',
                  )}
            </p>
          </Fold>
          <h3>{t('本期限定卡牌', 'Featured cards')}</h3>
          <Field label={t('搜索卡牌或主题', 'Search cards or themes')}>
            <input
              type="search"
              value={cardSearch}
              onChange={(event) => setCardSearch(event.target.value)}
            />
          </Field>
          <p role="status">
            {t('已选', 'Selected')} {pool.featured.length} · {t('匹配', 'Matching')}{' '}
            {matches.length}
          </p>
          <p>
            {t(
              '所有奖池自动包含全部常驻卡牌。下方仅选择本期限定卡。',
              'Every pool includes all permanent cards. Select the limited cards for this period below.',
            )}
          </p>
          <fieldset disabled={locked || busy} className="gacha-admin-fieldset">
            {themes
              .filter((theme) => matches.some((card) => card.theme === theme.id))
              .map((theme) => (
                <section key={theme.id} className="gacha-admin-roster">
                  <header>
                    <h4>
                      {t(theme.name, theme.name_en)} · {theme.rarity}
                    </h4>
                    <Button type="button" size="sm" onClick={() => chooseTheme(theme.id)}>
                      {t('全选／取消本组', 'Select / clear theme')}
                    </Button>
                  </header>
                  <div>
                    {matches
                      .filter((card) => card.theme === theme.id)
                      .map((card) => (
                        <label key={card.id}>
                          <ArtworkImage artworkKey={'card:' + card.id} src={card.thumb} alt="" />
                          <ArtworkBadge artworkKey={'card:' + card.id} />
                          <ArtworkUnavailable artworkKey={'card:' + card.id} />
                          <span>
                            <input
                              type="checkbox"
                              checked={pool.featured.includes(card.id)}
                              onChange={(event) =>
                                setPool({
                                  ...pool,
                                  featured: event.target.checked
                                    ? [...pool.featured, card.id]
                                    : pool.featured.filter((id) => id !== card.id),
                                })
                              }
                            />
                            {card.character}
                          </span>
                        </label>
                      ))}
                  </div>
                </section>
              ))}
          </fieldset>
          {!valid && (
            <Note tone="warn">
              {stale
                ? t(
                    '版本已变化，请重新打开最新配置。',
                    'The revision changed. Reopen the latest configuration.',
                  )
                : !pool.name.trim() || (draft.preset && !presetName.trim())
                  ? t('请输入名称。', 'Enter a name.')
                  : !pool.featured.length
                    ? t('至少选择一张限定卡。', 'Select at least one featured card.')
                    : !validRules(pool.rules)
                      ? t(
                          '请检查概率合计、限定占比与保底。',
                          'Check the rate total, featured shares and pity limits.',
                        )
                      : t('请检查封面和开放时间。', 'Check the cover and opening times.')}
            </Note>
          )}
          <div className="gacha-admin-actions">
            <Button variant="primary" type="submit" disabled={busy || !valid}>
              {draft.preset
                ? t('保存模板', 'Save template')
                : pool.published
                  ? t('保存并按计划开放', 'Save and open on schedule')
                  : t('保存草稿', 'Save draft')}
            </Button>
            {!draft.preset && (
              <Button
                type="button"
                disabled={
                  busy || !validRules(pool.rules) || !pool.name.trim() || pool.featured.length === 0
                }
                onClick={() =>
                  save({
                    kind: 'preset',
                    expected_revision: 0,
                    preset: {
                      id: 'gt_' + operationKey(),
                      name: pool.name,
                      revision: 0,
                      pool: withTime,
                    },
                  })
                }
              >
                {t('另存为配置模板', 'Save as a template')}
              </Button>
            )}
          </div>
        </fieldset>
      </form>
    </GachaDialog>
  );
}
export function GachaSettingsPage() {
  return (
    <ConfiguredArtworkPolicy path="/admin/api/config">
      <GachaSettingsContent />
    </ConfiguredArtworkPolicy>
  );
}
function GachaSettingsContent() {
  const formatDateTime = useDateTimeFormatter();
  const { i18n, t: ui } = useTranslation();
  const t: Copy = (zh, en) => (i18n.language.startsWith('en') ? en : zh);
  const client = useQueryClient(),
    session = useAdminSession();
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => getGachaConfiguration(signal),
    enabled: !!session.data,
    retry: false,
  });
  const [params, setParams] = useSearchState();
  const requestedSection = params.get('gacha_view');
  const section =
    requestedSection &&
    ['settings', 'presets', 'statistics', 'shop-statistics'].includes(requestedSection)
      ? requestedSection
      : 'pools';
  const setSection = (value: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (value === 'pools') next.delete('gacha_view');
      else next.set('gacha_view', value);
      return next;
    });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [remove, setRemove] = useState<{
    kind: 'delete_pool' | 'delete_preset';
    name: string;
    id: string;
    revision: number;
  } | null>(null);
  const mutation = useRetainedOperation<AdminInput, Configuration>(
    async (input, key, context) => {
      const result = await saveGachaConfiguration(input, key, context.signal);
      context.commit(() => client.setQueryData(queryKey, result));
      return result;
    },
    (_input, _error, context) => {
      context.assertCurrent();
      return client.invalidateQueries({ queryKey });
    },
    ['admin', 'gacha'],
  );
  const busy = mutation.isPending || mutation.outcome === 'unknown';
  const save = (input: AdminInput) =>
    mutation.mutate(input, {
      onSuccess: () => {
        setRemove(null);
        if (!(input.kind === 'preset' && draft && !draft.preset)) setDraft(null);
      },
    });
  const copyPool = (pool?: Pool) => {
    const blank = emptyPool(query.data?.server_time ?? Math.floor(Date.now() / 1000));
    setDraft({
      pool: pool
        ? {
            ...structuredClone(pool),
            id: blank.id,
            revision: 0,
            published: false,
            paused: false,
            starts_at: blank.starts_at,
            ends_at: blank.ends_at,
          }
        : blank,
    });
    mutation.reset();
  };
  const feedback = (
    <>
      {mutation.error && <ErrorState error={mutation.error} />}
      {mutation.outcome === 'unknown' && (
        <Note tone="warn">
          {t(
            '保存结果尚未确认，请重试原操作。',
            'The save result is unknown. Retry the same operation.',
          )}
          <Button onClick={() => mutation.variables && save(mutation.variables)}>
            {t('重试保存', 'Retry save')}
          </Button>
        </Note>
      )}
      {mutation.isSuccess && <Note tone="ok">{t('设置已保存。', 'Settings saved.')}</Note>}
    </>
  );
  return (
    <div className="page gacha-admin-page">
      <PageHeader
        title={t('真·抽卡模拟器', 'The Gacha Simulator')}
        description={ui('admin.gachaStats.pageDescription')}
        back={<Link to="/activities">{t('返回活动与共享池', 'Back to activities')}</Link>}
      />
      {session.error && <ErrorState error={session.error} />}
      {query.isPending && <LoadingState />}
      {query.error && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
      {!draft && feedback}
      {query.data && (
        <>
          <Tabs
            label={t('抽卡管理', 'Gacha management')}
            value={section}
            onChange={setSection}
            tabs={[
              { value: 'pools', label: t('限定奖池', 'Limited pools') },
              { value: 'settings', label: t('规则与商店', 'Rules and shop') },
              { value: 'presets', label: t('配置模板', 'Templates') },
              { value: 'statistics', label: ui('admin.gachaStats.tab') },
              { value: 'shop-statistics', label: ui('admin.gachaShopStats.tab') },
            ]}
          >
            {section === 'statistics' && session.data && !session.error ? (
              <GachaPoolStatistics
                key={session.data.admin.username}
                accountKey={session.data.admin.username}
              />
            ) : null}
            {section === 'shop-statistics' && session.data && !session.error ? (
              <GachaShopStatistics
                key={session.data.admin.username}
                accountKey={session.data.admin.username}
              />
            ) : null}
            {section === 'settings' && (
              <SettingsForm config={query.data} busy={busy} save={save} t={t} />
            )}
            {section === 'pools' && (
              <Panel>
                <PanelHead
                  title={t('限定奖池', 'Limited pools')}
                  description={t(
                    '可以同时开放任意数量的限定奖池。',
                    'Multiple limited pools can be open at once.',
                  )}
                  actions={
                    <Button variant="primary" disabled={busy} onClick={() => copyPool()}>
                      {t('新建奖池', 'New pool')}
                    </Button>
                  }
                />
                <PanelBody>
                  <TimeContextNotice station="admin" />
                  {!query.data.pools.some((p) => p.kind === 'limited') ? (
                    <EmptyState
                      title={t('暂无限定奖池', 'No limited pools yet')}
                      body={t(
                        '尚未发布限定奖池。可先从配置模板读取并微调。',
                        'No limited pool yet. Start by loading a template and adjusting it.',
                      )}
                      action={
                        <Button type="button" onClick={() => setSection('presets')}>
                          {t('查看配置模板', 'Browse templates')}
                        </Button>
                      }
                    />
                  ) : (
                    <div className="gacha-admin-pools">
                      {query.data.pools
                        .filter((p) => p.kind === 'limited')
                        .map((pool) => {
                          const isOpen = poolOpen(pool, query.data!.server_time);
                          const isEnded = Boolean(
                            pool.ends_at && pool.ends_at <= query.data!.server_time,
                          );
                          return (
                            <article key={pool.id}>
                              <ArtworkImage
                                artworkKey={'cover:' + pool.cover}
                                src={'/assets/gacha/covers/' + pool.cover + '.webp'}
                                alt={pool.name}
                              />
                              <div className="nb-stack nb-stack--sm">
                                <div className="gacha-admin-actions">
                                  <h3>{pool.name}</h3>
                                  <StatusBadge
                                    tone={
                                      !pool.published
                                        ? 'neutral'
                                        : pool.paused
                                          ? 'warn'
                                          : isOpen
                                            ? 'ok'
                                            : isEnded
                                              ? 'neutral'
                                              : 'info'
                                    }
                                  >
                                    {!pool.published
                                      ? t('草稿', 'Draft')
                                      : pool.paused
                                        ? t('已暂停', 'Paused')
                                        : isOpen
                                          ? t('开放中', 'Open')
                                          : isEnded
                                            ? t('已结束', 'Ended')
                                            : t('即将开放', 'Scheduled')}
                                  </StatusBadge>
                                </div>
                                <ArtworkBadge artworkKey={'cover:' + pool.cover} />
                                <ArtworkUnavailable artworkKey={'cover:' + pool.cover} />
                                <p className="nb-small nb-muted">
                                  {pool.starts_at ? formatDateTime(pool.starts_at) : '—'} →{' '}
                                  {pool.ends_at ? formatDateTime(pool.ends_at) : '—'}
                                </p>
                                <div className="gacha-admin-actions">
                                  <Button
                                    size="sm"
                                    disabled={busy}
                                    onClick={() => {
                                      mutation.reset();
                                      setDraft({ pool });
                                    }}
                                  >
                                    {t('编辑', 'Edit')}
                                  </Button>
                                  {!pool.published && (
                                    <Button
                                      type="button"
                                      size="sm"
                                      disabled={busy}
                                      onClick={() =>
                                        setRemove({
                                          kind: 'delete_pool',
                                          name: pool.name,
                                          id: pool.id,
                                          revision: pool.revision,
                                        })
                                      }
                                    >
                                      {t('删除草稿', 'Delete draft')}
                                    </Button>
                                  )}
                                  <Button size="sm" disabled={busy} onClick={() => copyPool(pool)}>
                                    {t('复制为新一期', 'Create similar period')}
                                  </Button>
                                </div>
                              </div>
                            </article>
                          );
                        })}
                    </div>
                  )}
                </PanelBody>
              </Panel>
            )}
            {section === 'presets' && (
              <Panel>
                <PanelHead
                  title={t('保存好的配置组合', 'Saved configurations')}
                  description={t(
                    '模板保留封面、卡牌、概率和保底规则。读取后重新填写开放时间。',
                    'Templates keep the cover, cards, rates and pity rules. Set a new schedule when using one.',
                  )}
                />
                <PanelBody>
                  <div className="gacha-admin-pools">
                    {query.data.presets.map((preset) => (
                      <article key={preset.id}>
                        <ArtworkImage
                          artworkKey={'cover:' + preset.pool.cover}
                          src={'/assets/gacha/covers/' + preset.pool.cover + '.webp'}
                          alt={preset.name}
                        />
                        <div className="nb-stack nb-stack--sm">
                          <h3>{preset.name}</h3>
                          <ArtworkBadge artworkKey={'cover:' + preset.pool.cover} />
                          <ArtworkUnavailable artworkKey={'cover:' + preset.pool.cover} />
                          <p className="nb-small nb-muted">
                            {formatCount(preset.pool.featured.length).display}{' '}
                            {t('张限定卡', 'limited cards')}
                          </p>
                          <div className="gacha-admin-actions">
                            {/^gt_[A-Za-z0-9_-]{22}$/.test(preset.id) && (
                              <Button
                                type="button"
                                disabled={busy}
                                onClick={() =>
                                  setRemove({
                                    kind: 'delete_preset',
                                    name: preset.name,
                                    id: preset.id,
                                    revision: preset.revision,
                                  })
                                }
                              >
                                {t('删除自建模板', 'Delete custom template')}
                              </Button>
                            )}
                            <Button disabled={busy} onClick={() => copyPool(preset.pool)}>
                              {t('读取并微调', 'Load and adjust')}
                            </Button>
                            <Button
                              disabled={busy}
                              onClick={() => {
                                mutation.reset();
                                setDraft({ pool: preset.pool, preset });
                              }}
                            >
                              {t('编辑模板', 'Edit template')}
                            </Button>
                          </div>
                        </div>
                      </article>
                    ))}
                  </div>
                </PanelBody>
              </Panel>
            )}
            {remove && (
              <ConfirmDialog
                open
                danger
                busy={mutation.isPending}
                title={t('确认删除', 'Confirm deletion') + ' · ' + remove.name}
                description={
                  remove.kind === 'delete_pool'
                    ? t(
                        '只可删除从未发布、且没有抽取记录的草稿。',
                        'Only never-published drafts without draw records can be removed.',
                      )
                    : t(
                        '删除自建模板后，已按此模板创建的奖池继续使用保存的配置。',
                        'Pools created from this template keep their saved configuration.',
                      )
                }
                confirmLabel={t('删除', 'Delete')}
                onCancel={() => setRemove(null)}
                onConfirm={() =>
                  save({
                    kind: remove.kind,
                    delete_id: remove.id,
                    expected_revision: remove.revision,
                  })
                }
              >
                {feedback}
              </ConfirmDialog>
            )}
            {draft && (
              <>
                <PoolEditor
                  key={draft.preset?.id ?? draft.pool.id}
                  draft={draft}
                  config={query.data}
                  busy={busy}
                  save={save}
                  close={() => setDraft(null)}
                  feedback={feedback}
                  t={t}
                />
              </>
            )}
          </Tabs>
        </>
      )}
    </div>
  );
}
