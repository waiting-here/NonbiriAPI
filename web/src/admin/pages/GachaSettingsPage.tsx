import { cloneElement, useState, type ReactElement, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import {
  Button,
  Field as SharedField,
  Fold,
  Note,
  Panel,
  PanelBody,
  PanelHead,
  Tabs,
  Toggle,
} from '@shared/components/ui';
import { ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { TimeInput } from '@shared/components/TimeInput';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { operationKey } from '@shared/operations/api';
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
const covers = [
  ['permanent', '永续智能', 'Perpetual intelligence'],
  ['h', '万象化身', 'Myriad incarnations'],
  ['i', '夏夜花火', 'Summer fireworks'],
  ['j', '童话剧场', 'Fairytale theatre'],
  ['entry', '星光收藏册', 'Book of starlight'],
];
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
  return (
    <fieldset className="gacha-admin-fieldset" disabled={disabled}>
      <div className="gacha-admin-grid">
        {rarities.map((rarity, i) => (
          <Field key={rarity} label={rarity + t(' 基础概率（%）', ' base rate (%)')}>
            <input
              type="number"
              min="0"
              max="100"
              step="0.01"
              value={value.weights[i] / 100}
              onChange={(event) => {
                const weights = [...value.weights] as Rules['weights'];
                weights[i] = Math.round(Number(event.target.value) * 100);
                onChange({ ...value, weights });
              }}
            />
          </Field>
        ))}
      </div>
      <p
        className={
          value.weights.reduce((a, b) => a + b, 0) === 10000
            ? 'gacha-admin-help'
            : 'gacha-admin-invalid'
        }
      >
        {t('基础概率合计：', 'Base rate total: ')}
        {value.weights.reduce((a, b) => a + b, 0) / 100}%
      </p>
      <div className="gacha-admin-grid">
        <Field
          label={t('SR 或以上硬保底（抽）', 'SR or better hard pity (pulls)')}
          help={t('1–100 抽', '1–100 pulls')}
        >
          <input
            type="number"
            min="1"
            max="100"
            value={value.sr_pity}
            onChange={(e) => onChange({ ...value, sr_pity: Number(e.target.value) })}
          />
        </Field>
        <Field
          label={t('SSR 硬保底（抽）', 'SSR hard pity (pulls)')}
          help={t('不小于 SR 保底，最多 1000 抽', 'At least the SR limit; up to 1000 pulls')}
        >
          <input
            type="number"
            min={value.sr_pity}
            max="1000"
            value={value.ssr_pity}
            onChange={(e) => onChange({ ...value, ssr_pity: Number(e.target.value) })}
          />
        </Field>
      </div>
      {limited && (
        <div className="gacha-admin-grid">
          {(['sr', 'ssr'] as const).map((tier) => (
            <div key={tier}>
              <Field label={tier.toUpperCase() + t(' 档限定占比（%）', ' featured share (%)')}>
                <input
                  type="number"
                  min="0"
                  max="100"
                  step="0.01"
                  value={value[tier === 'sr' ? 'sr_featured' : 'ssr_featured'] / 100}
                  onChange={(e) =>
                    onChange({
                      ...value,
                      [tier === 'sr' ? 'sr_featured' : 'ssr_featured']: Math.round(
                        Number(e.target.value) * 100,
                      ),
                    })
                  }
                />
              </Field>
              <Toggle
                label={
                  tier.toUpperCase() +
                  t(
                    ' 抽到常驻后，下次同档必为限定',
                    ' permanent hit guarantees the next is featured',
                  )
                }
                checked={value[tier === 'sr' ? 'sr_guarantee' : 'ssr_guarantee']}
                onChange={(checked) =>
                  onChange({
                    ...value,
                    [tier === 'sr' ? 'sr_guarantee' : 'ssr_guarantee']: checked,
                  })
                }
              />
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
  const [rules, setRules] = useState(config.rules),
    [shop, setShop] = useState(config.shop);
  const fields: [keyof ShopSettings, string, string][] = [
    ['n_ticket', 'N 碎片 → 1 常驻券', 'N shards → 1 standard ticket'],
    ['r_ticket', 'R 碎片 → 1 常驻券', 'R shards → 1 standard ticket'],
    ['sr_ticket', 'SR 碎片 → 1 限定券', 'SR shards → 1 limited ticket'],
    ['ssr_tickets', 'SSR 碎片 → 2 限定券', 'SSR shards → 2 limited tickets'],
    ['permanent_cap', '每月常驻券合计上限', 'Monthly standard ticket limit'],
    ['limited_cap', '每月限定券合计上限', 'Monthly limited ticket limit'],
    ['sr_card', '兑换限定 SR 所需 SR 碎片', 'SR shards per limited SR card'],
    ['ssr_card', '兑换限定 SSR 所需 SSR 碎片', 'SSR shards per limited SSR card'],
    ['chibi_n', 'Q 版卡所需 N 碎片', 'N shards per chibi card'],
    ['chibi_r', 'Q 版卡所需 R 碎片', 'R shards per chibi card'],
  ];
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        save({ kind: 'settings', expected_revision: config.revision, rules, shop });
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
            <div className="gacha-admin-grid">
              {(['permanent', 'limited'] as const).map((ticket) => (
                <Field
                  key={ticket}
                  label={
                    ticket === 'permanent'
                      ? t('每张常驻券所需积分', 'Credits per standard ticket')
                      : t('每张限定券所需通用积分', 'General credits per limited ticket')
                  }
                >
                  <input
                    required
                    type="number"
                    min="0.001"
                    step="0.001"
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
              {fields.map(([key, zh, en]) => (
                <Field key={key} label={t(zh, en)}>
                  <input
                    required
                    type="number"
                    min={key.endsWith('_cap') ? '0' : '1'}
                    step="1"
                    value={shop[key]}
                    onChange={(e) => setShop({ ...shop, [key]: Number(e.target.value) })}
                  />
                </Field>
              ))}
            </div>
          </fieldset>
          <p>
            {t(
              'Q 版卡同时消耗 N、R 两种碎片。两种券的月额度分别按产出的票券张数累计。',
              'A chibi card costs both N and R shards. Monthly limits count the number of tickets received.',
            )}
          </p>
          <Button variant="primary" type="submit" disabled={busy || !validRules(rules)}>
            {t('保存规则与商店设置', 'Save rules and shop settings')}
          </Button>
        </PanelBody>
      </Panel>
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
  const valid =
    validRules(pool.rules) &&
    !!pool.name.trim() &&
    pool.featured.length > 0 &&
    (!pool.published || (startValue != null && endValue != null && endValue > startValue));
  const chooseTheme = (theme: string) => {
    const ids = cards.filter((c) => c.theme === theme).map((c) => c.id);
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
      onClose={() => !busy && close()}
      className="gacha-admin-dialog"
    >
      {feedback}
      <form
        onSubmit={(e) => {
          e.preventDefault();
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
                onClick={() => setPool({ ...pool, cover: id })}
              >
                <img src={'/assets/gacha/covers/' + id + '.webp'} alt="" />
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
                disabled={locked}
                onChange={(published) => setPool({ ...pool, published })}
              />
              <Toggle
                label={t('暂停奖池和限定兑换', 'Pause drawing and limited exchanges')}
                checked={pool.paused}
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
          <Fold title={t('概率与保底', 'Rates and guarantees')} defaultOpen>
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
          <p>
            {t(
              '所有奖池自动包含全部常驻卡牌。下方仅选择本期限定卡。',
              'Every pool includes all permanent cards. Select the limited cards for this period below.',
            )}
          </p>
          <fieldset disabled={locked || busy} className="gacha-admin-fieldset">
            {themes
              .filter((theme) => theme.source === 'limited')
              .map((theme) => (
                <section key={theme.id} className="gacha-admin-roster">
                  <header>
                    <h4>
                      {t(theme.name, theme.name_en)} · {theme.rarity}
                    </h4>
                    <Button size="sm" onClick={() => chooseTheme(theme.id)}>
                      {t('全选／取消本组', 'Select / clear theme')}
                    </Button>
                  </header>
                  <div>
                    {cards
                      .filter((card) => card.theme === theme.id)
                      .map((card) => (
                        <label key={card.id}>
                          <img src={card.thumb} alt="" />
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
  const formatDateTime = useDateTimeFormatter();
  const { i18n } = useTranslation();
  const t: Copy = (zh, en) => (i18n.language.startsWith('en') ? en : zh);
  const client = useQueryClient(),
    session = useAdminSession();
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => getGachaConfiguration(signal),
    enabled: !!session.data,
    retry: false,
  });
  const [section, setSection] = useState('pools'),
    [draft, setDraft] = useState<Draft | null>(null);
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
  const save = (input: AdminInput) => mutation.mutate(input, { onSuccess: () => setDraft(null) });
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
        description={t(
          '管理常驻规则、限定奖池和神秘商店。',
          'Manage permanent rules, limited pools and the mystery shop.',
        )}
        icon="spark"
        back={
          <Link className="nb-btn nb-btn--ghost" to="/activities">
            {t('返回活动管理', 'Back to activities')}
          </Link>
        }
      />
      {session.error && <ErrorState error={session.error} />}
      {query.isPending && <LoadingState />}
      {query.error && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
      {!draft && feedback}
      {query.data && (
        <>
          <TimeContextNotice station="admin" />
          <Tabs
            label={t('抽卡管理', 'Gacha management')}
            value={section}
            onChange={setSection}
            tabs={[
              { value: 'pools', label: t('限定奖池', 'Limited pools') },
              { value: 'settings', label: t('规则与商店', 'Rules and shop') },
              { value: 'presets', label: t('配置模板', 'Templates') },
            ]}
          />
          {section === 'settings' && (
            <SettingsForm
              key={query.data.revision}
              config={query.data}
              busy={busy}
              save={save}
              t={t}
            />
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
                {!query.data.pools.some((p) => p.kind === 'limited') && (
                  <p>
                    {t(
                      '尚未发布限定奖池。可先从配置模板读取并微调。',
                      'No limited pool yet. Start by loading a template and adjusting it.',
                    )}
                  </p>
                )}
                <div className="gacha-admin-pools">
                  {query.data.pools
                    .filter((p) => p.kind === 'limited')
                    .map((pool) => (
                      <article key={pool.id}>
                        <img src={'/assets/gacha/covers/' + pool.cover + '.webp'} alt="" />
                        <div>
                          <h3>{pool.name}</h3>
                          <p>
                            {!pool.published
                              ? t('草稿', 'Draft')
                              : pool.paused
                                ? t('已暂停', 'Paused')
                                : poolOpen(pool, query.data!.server_time)
                                  ? t('开放中', 'Open')
                                  : pool.ends_at && pool.ends_at <= query.data!.server_time
                                    ? t('已结束', 'Ended')
                                    : t('即将开放', 'Scheduled')}
                          </p>
                          <p>
                            {pool.starts_at ? formatDateTime(pool.starts_at) : '—'} →{' '}
                            {pool.ends_at ? formatDateTime(pool.ends_at) : '—'}
                          </p>
                          <div className="gacha-admin-actions">
                            <Button
                              disabled={busy}
                              onClick={() => {
                                mutation.reset();
                                setDraft({ pool });
                              }}
                            >
                              {t('编辑', 'Edit')}
                            </Button>
                            <Button disabled={busy} onClick={() => copyPool(pool)}>
                              {t('复制为新一期', 'Create similar period')}
                            </Button>
                          </div>
                        </div>
                      </article>
                    ))}
                </div>
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
                      <img src={'/assets/gacha/covers/' + preset.pool.cover + '.webp'} alt="" />
                      <div>
                        <h3>{preset.name}</h3>
                        <p>
                          {preset.pool.featured.length} {t('张限定卡', 'limited cards')}
                        </p>
                        <div className="gacha-admin-actions">
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
        </>
      )}
    </div>
  );
}
