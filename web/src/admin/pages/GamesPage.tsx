import { LakeFields, lakeWire } from '../features/lakenotes/LakeFields';
import { useRef, useState } from 'react';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { Card, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { useOptionalToast } from '@shared/components/Toast';
import {
  adminEconomyKeys,
  gamesConfigPatch,
  getActiveCounts,
  getGamesConfig,
  patchGamesConfig,
  type GamesConfig,
} from '../features/operations/economy';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import { DuelConfiguration } from '../features/games/Configuration';
import {
  CatchFields,
  FishingFields,
  LinklinkFields,
  RPSFields,
} from '../features/games/GameFields';
import { ExpandablePanel } from '@shared/components/ui/ExpandablePanel';
import { Note, Panel, PanelBody, SaveBar, Toggle } from '@shared/components/ui';
import '../features/games/configuration.css';
import {
  BlackjackConfiguration,
  validateBlackjackConfiguration,
} from '../features/games/BlackjackConfiguration';
import { validateDuelConfigurations } from '../features/games/config';
import { fishingChanceValid } from '../features/games/fishing';
import { gameLabel, modeLabel, useGameAdminText } from '../features/games/copy';
import '@shared/operations/operations.css';

type GameName = 'fishing' | 'linklink' | 'rps';
type RPSMode = 'quick' | 'standard' | 'deathmatch';
type RPSSeconds = 'queue_seconds' | 'gesture_seconds' | 'dealer_seconds' | 'follower_seconds';

const MAX_AMOUNT_MILLI = 9_000_000_000_000_000n;
const RPS_MODES: readonly RPSMode[] = ['quick', 'standard', 'deathmatch'];
const GAME_LABEL_KEYS: Record<GameName, string> = {
  fishing: 'admin.games.sections.fishing',
  linklink: 'admin.games.sections.linklink',
  rps: 'admin.games.sections.rps',
};
const BAIT_LABEL_KEYS = {
  worm: 'admin.games.worm',
  lure: 'admin.games.lure',
  premium: 'admin.games.premium',
} as const;
const FISHING_RTP_LABEL_KEYS = {
  standard: 'admin.games.standardRTP',
  premium: 'admin.games.premiumRTP',
} as const;
const TREASURE_LABEL_KEYS = {
  bottle: 'admin.games.bottle',
  clover: 'admin.games.clover',
  shell: 'admin.games.shell',
} as const;
const LINKLINK_SPEC_LABEL_KEYS: Record<string, string> = {
  '6x8': 'admin.games.linklink.specs.6x8',
  '8x8': 'admin.games.linklink.specs.8x8',
  '10x10': 'admin.games.linklink.specs.10x10',
};
const RPS_MODE_LABEL_KEYS: Record<RPSMode, string> = {
  quick: 'admin.games.rps.modes.quick',
  standard: 'admin.games.rps.modes.standard',
  deathmatch: 'admin.games.rps.modes.deathmatch',
};
const RPS_PUMP_LABEL_KEYS = {
  platform: 'admin.games.rps.pumps.platform',
  welfare: 'admin.games.rps.pumps.welfare',
  thursday: 'admin.games.rps.pumps.thursday',
} as const;
const RPS_DEADLINE_LABEL_KEYS: Record<RPSSeconds, string> = {
  queue_seconds: 'admin.games.rps.deadlines.queue',
  gesture_seconds: 'admin.games.rps.deadlines.gesture',
  dealer_seconds: 'admin.games.rps.deadlines.dealer',
  follower_seconds: 'admin.games.rps.deadlines.follower',
};
const RPS_PHASE_LABEL_KEYS: Record<string, string> = {
  gesture: 'admin.games.rps.phases.gesture',
  dealer_raise: 'admin.games.rps.phases.dealerRaise',
  followers: 'admin.games.rps.phases.followers',
  paid_pool_gesture: 'admin.games.rps.phases.paidPoolGesture',
  free_pool_gesture: 'admin.games.rps.phases.freePoolGesture',
  ultimate_gesture: 'admin.games.rps.phases.ultimateGesture',
  terminal_processing: 'admin.games.rps.phases.terminalProcessing',
};

const enumLabel = (t: TFunction, labels: Readonly<Record<string, string>>, value: string) =>
  t(labels[value] ?? 'admin.games.enums.unknown', { value });

function amountMilli(value: string): bigint | null {
  const match = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,3}))?$/.exec(value.trim());
  if (!match) return null;
  try {
    const result = BigInt(match[1]) * 1_000n + BigInt((match[2] ?? '').padEnd(3, '0') || '0');
    return result <= MAX_AMOUNT_MILLI ? result : null;
  } catch {
    return null;
  }
}

const validInteger = (value: number, minimum: number, maximum: number) =>
  Number.isSafeInteger(value) && value >= minimum && value <= maximum;

type ConfigGame =
  GameName | 'bidding' | 'likes' | 'blackjack' | 'gwent' | 'steadycatch' | 'lakenotes';
type GameProblem = { game: ConfigGame | null; message: string; field?: string };

function validateGamesDraft(draft: GamesConfig, t: TFunction): GameProblem | null {
  let game: ConfigGame | null = null;
  let field: string | undefined;
  const invalid = (message: string): GameProblem => ({ game, message, field });
  if (
    !draft.master_enabled &&
    (draft.fishing.enabled || draft.linklink.enabled || draft.rps.enabled)
  )
    return invalid(t('admin.games.validation.masterRequired'));
  game = 'fishing';
  for (const bait of ['worm', 'lure', 'premium'] as const) {
    field = `fishing.bait_prices.${bait}`;
    const value = amountMilli(draft.fishing.bait_prices[bait]);
    if (value === null || value < 1n)
      return invalid(
        t('admin.games.validation.amountMinimum', {
          field: t(BAIT_LABEL_KEYS[bait]),
          minimum: '0.001',
        }),
      );
  }
  for (const mode of ['standard', 'premium'] as const) {
    field = `fishing.rtp_percent.${mode}`;
    if (!validInteger(draft.fishing.rtp_percent[mode], 0, 100))
      return invalid(
        t('admin.games.validation.integerRange', {
          field: t(FISHING_RTP_LABEL_KEYS[mode]),
          minimum: 0,
          maximum: 100,
        }),
      );
  }
  for (const pump of ['platform', 'welfare', 'thursday'] as const) {
    field = `fishing.rake_bp.${pump}`;
    if (!validInteger(draft.fishing.rake_bp[pump], 0, 9_999))
      return invalid(
        t('admin.games.validation.feeRange', {
          field: t('admin.games.fishingRake', { pump: t(RPS_PUMP_LABEL_KEYS[pump]) }),
        }),
      );
  }
  field = 'fishing.rake_bp.platform';
  if (
    draft.fishing.rake_bp.platform +
      draft.fishing.rake_bp.welfare +
      draft.fishing.rake_bp.thursday >=
    10_000
  )
    return invalid(
      t('admin.games.validation.totalCuts', {
        mode: t('admin.games.sections.fishing'),
        maximum: 100,
      }),
    );
  for (const treasure of ['bottle', 'clover', 'shell'] as const) {
    field = `fishing.treasure_multipliers.${treasure}`;
    if (!validInteger(draft.fishing.treasure_multipliers[treasure], 0, 1_000_000)) {
      return invalid(
        t('admin.games.validation.integerRange', {
          field: t(TREASURE_LABEL_KEYS[treasure]),
          minimum: 0,
          maximum: 1_000_000,
        }),
      );
    }
  }
  game = 'linklink';
  for (const spec of ['6x8', '8x8', '10x10'] as const) {
    field = `linklink.${spec}.price`;
    if (
      draft.linklink.specs[spec].enabled &&
      (!draft.linklink.enabled || amountMilli(draft.linklink.specs[spec].price) === 0n)
    )
      return invalid(t('admin.games.validation.specRequiresPrice', { spec }));
    if (amountMilli(draft.linklink.specs[spec].price) === null)
      return invalid(
        t('admin.games.validation.amountNonNegative', {
          field: enumLabel(t, LINKLINK_SPEC_LABEL_KEYS, spec),
        }),
      );
  }
  game = 'rps';
  for (const mode of RPS_MODES) {
    field = `rps.${mode}.base`;
    const value = draft.rps.modes[mode];
    if (value.enabled && (!draft.rps.enabled || amountMilli(value.base) === 0n))
      return invalid(
        t('admin.games.validation.modeRequiresStake', { mode: t(RPS_MODE_LABEL_KEYS[mode]) }),
      );
    if (amountMilli(value.base) === null)
      return invalid(
        t('admin.games.validation.amountNonNegative', {
          field: t('admin.games.rps.base', { mode: t(RPS_MODE_LABEL_KEYS[mode]) }),
        }),
      );
    for (const pump of ['platform', 'welfare', 'thursday'] as const) {
      field = `rps.${mode}.pumps_bp.${pump}`;
      if (!validInteger(value.pumps_bp[pump], 0, 9_999))
        return invalid(
          t('admin.games.validation.feeRange', {
            field: t('admin.games.rps.cut', {
              mode: t(RPS_MODE_LABEL_KEYS[mode]),
              pump: t(RPS_PUMP_LABEL_KEYS[pump]),
            }),
          }),
        );
    }
    field = `rps.${mode}.pumps_bp.platform`;
    if (value.pumps_bp.platform + value.pumps_bp.welfare + value.pumps_bp.thursday >= 10_000) {
      return invalid(
        t('admin.games.validation.totalCuts', {
          mode: t(RPS_MODE_LABEL_KEYS[mode]),
          maximum: 100,
        }),
      );
    }
    field = `rps.${mode}.queue_seconds`;
    if (!validInteger(value.queue_seconds, 30, 120))
      return invalid(
        t('admin.games.validation.integerRange', {
          field: t('admin.games.rps.deadline', {
            mode: t(RPS_MODE_LABEL_KEYS[mode]),
            deadline: t(RPS_DEADLINE_LABEL_KEYS.queue_seconds),
          }),
          minimum: 30,
          maximum: 120,
        }),
      );
    field = `rps.${mode}.gesture_seconds`;
    if (!validInteger(value.gesture_seconds, 5, 20))
      return invalid(
        t('admin.games.validation.integerRange', {
          field: t('admin.games.rps.deadline', {
            mode: t(RPS_MODE_LABEL_KEYS[mode]),
            deadline: t(RPS_DEADLINE_LABEL_KEYS.gesture_seconds),
          }),
          minimum: 5,
          maximum: 20,
        }),
      );
    field = `rps.${mode}.dealer_seconds`;
    if (!validInteger(value.dealer_seconds, 5, 15))
      return invalid(
        t('admin.games.validation.integerRange', {
          field: t('admin.games.rps.deadline', {
            mode: t(RPS_MODE_LABEL_KEYS[mode]),
            deadline: t(RPS_DEADLINE_LABEL_KEYS.dealer_seconds),
          }),
          minimum: 5,
          maximum: 15,
        }),
      );
    field = `rps.${mode}.follower_seconds`;
    if (!validInteger(value.follower_seconds, 5, 15))
      return invalid(
        t('admin.games.validation.integerRange', {
          field: t('admin.games.rps.deadline', {
            mode: t(RPS_MODE_LABEL_KEYS[mode]),
            deadline: t(RPS_DEADLINE_LABEL_KEYS.follower_seconds),
          }),
          minimum: 5,
          maximum: 15,
        }),
      );
  }
  return null;
}

function canonicalGamesDraft(draft: GamesConfig): GamesConfig {
  const result = structuredClone(draft);
  const canonical = (value: string) => {
    const milli = amountMilli(value);
    if (milli === null) return value;
    return `${milli / 1000n}${milli % 1000n ? '.' + (milli % 1000n).toString().padStart(3, '0').replace(/0+$/, '') : ''}`;
  };
  result.steadycatch.price = canonical(result.steadycatch.price);
  result.steadycatch.first_clear_reward = canonical(result.steadycatch.first_clear_reward);
  for (const bait of ['worm', 'lure', 'premium'] as const)
    result.fishing.bait_prices[bait] = canonical(result.fishing.bait_prices[bait]);
  for (const spec of ['6x8', '8x8', '10x10'] as const)
    result.linklink.specs[spec].price = canonical(result.linklink.specs[spec].price);
  for (const mode of RPS_MODES)
    result.rps.modes[mode].base = canonical(result.rps.modes[mode].base);
  for (const game of ['bidding', 'likes', 'gwent'] as const) {
    const config = result[game];
    if (config)
      for (const mode of Object.values(config.modes)) mode.ticket = canonical(mode.ticket);
  }
  for (const field of ['min_stake', 'max_stake', 'stake_step', 'default_stake'] as const)
    result.blackjack[field] = canonical(result.blackjack[field]);
  return result;
}
function GamesEditor({
  authority,
  refresh,
}: {
  authority: GamesConfig;
  refresh: () => Promise<unknown>;
}) {
  const { t } = useTranslation();
  const text = useGameAdminText();
  const toast = useOptionalToast();
  const [draft, setDraft] = useState(authority);
  const [active, setActive] = useState<ConfigGame | null>(null);
  const opening = useRef<GamesConfig | null>(null);
  const save = useRetainedOperation<GamesConfig, GamesConfig>(
    (input, key) => patchGamesConfig(gamesConfigPatch(canonicalGamesDraft(input)), key),
    refresh,
  );
  const edit = (updater: (current: GamesConfig) => GamesConfig) => {
    save.reset();
    setDraft(updater);
  };
  const label = (game: ConfigGame) =>
    game === 'bidding' ||
    game === 'likes' ||
    game === 'blackjack' ||
    game === 'gwent' ||
    game === 'steadycatch' ||
    game === 'lakenotes'
      ? gameLabel(game, text)
      : t(GAME_LABEL_KEYS[game]);
  const problem = (): GameProblem | null => {
    if (draft.lakenotes.enabled && !draft.master_enabled)
      return {
        game: 'lakenotes',
        message: text('请先开启小游戏总开关。', 'Enable the games master switch first.'),
      };
    try {
      lakeWire(draft.lakenotes);
    } catch {
      return {
        game: 'lakenotes',
        message: text(
          '请填写完整兑换比例，金币为正整数，积分最多三位小数。',
          'Enter both exchange amounts. Use positive whole coins and credits with up to three decimals.',
        ),
      };
    }
    if (draft.steadycatch.enabled && !draft.master_enabled)
      return {
        game: 'steadycatch',
        message: text('请先开启小游戏总开关。', 'Enable the games master switch first.'),
      };
    if (
      amountMilli(draft.steadycatch.price) === null ||
      amountMilli(draft.steadycatch.first_clear_reward) === null
    )
      return {
        game: 'steadycatch',
        message: text(
          '金额应为非负数，最多三位小数。',
          'Use nonnegative amounts with up to three decimal places.',
        ),
      };
    const basic = validateGamesDraft(draft, t);
    if (basic) return basic;
    for (const game of ['bidding', 'likes', 'gwent'] as const) {
      const message = validateDuelConfigurations(
        { master_enabled: draft.master_enabled, [game]: draft[game] },
        text,
      );
      if (message) return { game, message };
    }
    const blackjack = validateBlackjackConfiguration(draft, text);
    if (blackjack) return { game: 'blackjack', message: blackjack };
    return fishingChanceValid(draft.fishing.blue_fish_chance_bps)
      ? null
      : {
          game: 'fishing',
          field: 'fishing.blue_fish_chance_bps',
          message: text(
            '蓝色大肥鱼概率必须为0%至100%，最多两位小数。',
            'Blue-fish probability must be between 0% and 100%, with at most two decimal places.',
          ),
        };
  };
  const formError = problem();
  const changed = JSON.stringify(canonicalGamesDraft(draft)) !== JSON.stringify(authority);
  const stale = draft.revision !== authority.revision;
  const dirtyCount = (Object.keys(draft) as (keyof GamesConfig)[]).filter(
    (key) => key !== 'revision' && JSON.stringify(draft[key]) !== JSON.stringify(authority[key]),
  ).length;
  const submit = () => {
    if (formError || !changed || stale || save.isPending) return;
    void save
      .mutateAsync(draft)
      .then((result) => {
        setDraft(result);
        setActive(null);
        toast?.push({ message: t('admin.games.saved'), tone: 'success' });
      })
      .catch(() => undefined);
  };
  const restore = () => {
    save.reset();
    setDraft(authority);
  };
  const close = () => {
    if (save.isPending) return;
    if (active && opening.current) {
      const original = opening.current[active];
      edit((current) => ({ ...current, [active]: original }));
    }
    setActive(null);
  };
  const open = (game: ConfigGame) => {
    opening.current = structuredClone(draft);
    setActive(game);
    if (formError?.game === game)
      requestAnimationFrame(() => {
        const drawer = document.querySelector('.nb-expandable-panel');
        const field =
          (formError.field
            ? drawer?.querySelector<HTMLElement>(`[name="${CSS.escape(formError.field)}"]`)
            : null) ??
          drawer?.querySelector<HTMLElement>(':invalid') ??
          drawer?.querySelector<HTMLElement>('input:not([type="checkbox"])');
        let disclosure = field?.closest('details');
        while (disclosure) {
          disclosure.open = true;
          disclosure = disclosure.parentElement?.closest('details') ?? null;
        }
        field?.scrollIntoView({ block: 'center' });
        field?.focus();
      });
  };
  const enabledSummary = (values: { enabled: boolean }[]) =>
    text(
      `${values.length} 种模式 · ${values.filter((value) => value.enabled).length} 种开放`,
      `${values.length} modes · ${values.filter((value) => value.enabled).length} enabled`,
    );
  const summary = (game: ConfigGame) => {
    if (game === 'lakenotes')
      return text('免费常驻 · 四向兑换', 'Free play · four exchange directions');
    if (game === 'steadycatch')
      return text(
        '90 秒接物挑战 · 仅首通奖励',
        '90-second catch challenge · first-clear reward only',
      );
    if (game === 'fishing')
      return `${Object.values(draft.fishing.bait_prices).join(' / ')} ${text('积分', 'credits')} · ${text('抽成', 'fees')} ${Object.values(draft.fishing.rake_bp).reduce((a, b) => a + b, 0) / 100}%`;
    if (game === 'linklink') return enabledSummary(Object.values(draft.linklink.specs));
    if (game === 'rps') return enabledSummary(Object.values(draft.rps.modes));
    if (game === 'blackjack')
      return text('单桌 9 席 · 每 30 秒一局', '9 seats · a round every 30 seconds');
    return enabledSummary(Object.values(draft[game]?.modes ?? {}));
  };
  return (
    <form
      className="nb-stack"
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <Panel>
        <PanelBody>
          <Toggle
            label={t('admin.games.masterEnabled')}
            description={t('admin.games.controls.revisionTerms')}
            checked={draft.master_enabled}
            disabled={save.isPending}
            onChange={(master_enabled) => edit((current) => ({ ...current, master_enabled }))}
          />
          <div className="admin-game-list">
            {(
              [
                'fishing',
                'linklink',
                'rps',
                'bidding',
                'likes',
                'blackjack',
                'gwent',
                'steadycatch',
                'lakenotes',
              ] as const
            )
              .filter((game) => draft[game])
              .map((game) => (
                <div className="admin-game-row" key={game}>
                  <div>
                    <h2>{label(game)}</h2>
                    <p>{summary(game)}</p>
                    {formError?.game === game ? (
                      <span className="nb-badge nb-badge--bad">
                        {text('请检查设置', 'Check settings')}
                      </span>
                    ) : null}
                  </div>
                  <Toggle
                    label={
                      <span className="nb-sr">
                        {text('启用', 'Enable ')}
                        {label(game)}
                      </span>
                    }
                    checked={draft[game]!.enabled}
                    disabled={save.isPending}
                    onChange={(enabled) =>
                      edit((current) => ({ ...current, [game]: { ...current[game], enabled } }))
                    }
                  />
                  <button
                    className="nb-btn nb-btn--secondary"
                    type="button"
                    disabled={save.isPending}
                    aria-label={`${label(game)} ${t('admin.games.controls.settings')}`}
                    onClick={() => open(game)}
                  >
                    {t('admin.games.controls.settings')}
                  </button>
                </div>
              ))}
          </div>
        </PanelBody>
      </Panel>
      {formError ? <Note tone="bad">{formError.message}</Note> : null}
      {stale ? <Note tone="bad">{t('admin.games.validation.changedElsewhere')}</Note> : null}
      {save.error && !active ? <ErrorState error={save.error} /> : null}
      <SaveBar
        dirtyCount={changed ? Math.max(1, dirtyCount) : 0}
        busy={save.isPending}
        saveDisabled={!!formError || stale}
        onSave={submit}
        onDiscard={restore}
        saveLabel={t('admin.games.save')}
        discardLabel={t('admin.games.restoreAuthorityValues')}
        dirtyLabel={(count) => text(`${count} 项未保存`, `${count} unsaved changes`)}
      />
      <ExpandablePanel
        open={active !== null}
        title={active ? label(active) : ''}
        closeLabel={t('common.close')}
        onClose={close}
        busy={save.isPending}
        footer={
          <>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={save.isPending}
              onClick={close}
            >
              {t('common.cancel')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--primary"
              disabled={save.isPending || !changed || !!formError || stale}
              onClick={submit}
            >
              {text('保存', 'Save ')}
              {active ? label(active) : ''}
              {text('设置', ' settings')}
            </button>
          </>
        }
      >
        {formError ? <Note tone="bad">{formError.message}</Note> : null}
        {stale ? <Note tone="bad">{t('admin.games.validation.changedElsewhere')}</Note> : null}
        {save.error ? <ErrorState error={save.error} /> : null}
        {active === 'lakenotes' && (
          <LakeFields draft={draft} edit={edit} disabled={save.isPending} />
        )}
        {active === 'steadycatch' && (
          <CatchFields draft={draft} edit={edit} disabled={save.isPending} />
        )}
        {active === 'fishing' ? (
          <FishingFields draft={draft} edit={edit} disabled={save.isPending} />
        ) : null}
        {active === 'linklink' ? (
          <LinklinkFields draft={draft} edit={edit} disabled={save.isPending} />
        ) : null}
        {active === 'rps' ? (
          <RPSFields draft={draft} edit={edit} disabled={save.isPending} />
        ) : null}
        {(active === 'bidding' || active === 'likes' || active === 'gwent') && draft[active] ? (
          <DuelConfiguration
            game={active}
            value={draft[active]}
            disabled={save.isPending}
            onChange={(value) => edit((current) => ({ ...current, [active]: value }))}
          />
        ) : null}
        {active === 'blackjack' ? (
          <BlackjackConfiguration
            value={draft.blackjack}
            disabled={save.isPending}
            onChange={(value) => edit((current) => ({ ...current, blackjack: value }))}
          />
        ) : null}
      </ExpandablePanel>
    </form>
  );
}
export function GamesPage() {
  const { t } = useTranslation();
  const duelText = useGameAdminText();
  const config = useQuery({
    queryKey: adminEconomyKeys.games,
    queryFn: getGamesConfig,
    retry: false,
  });
  const counts = useQuery({
    queryKey: adminEconomyKeys.counts,
    queryFn: getActiveCounts,
    retry: false,
    refetchInterval: 15_000,
  });
  const initialConfigFailure = !config.data && config.error;

  return (
    <div className="page ops-page">
      <PageHeader
        title={t('admin.games.title')}
        description={duelText(
          '开关和规则保存后对新对局生效；进行中的对局按开始时的规则结束。',
          'Saved settings apply to new matches; active matches finish with their starting rules.',
        )}
        actions={
          <>
            <Link className="nb-btn nb-btn--secondary" to="/games/ai">
              {duelText('AI 玩家与策略', 'AI players and strategies')}
            </Link>
            {config.data?.bidding && config.data.likes ? (
              <Link className="nb-btn nb-btn--secondary" to="/games/history">
                {duelText('对战历史与导出', 'Match history and exports')}
              </Link>
            ) : null}
            <Link className="nb-btn nb-btn--secondary" to="/games/blackjack/history">
              {duelText('二十一点历史', 'Blackjack history')}
            </Link>
          </>
        }
      />
      {counts.data &&
      !counts.error &&
      counts.data.games.length === 0 &&
      counts.data.queues.length === 0 ? (
        <Note>{t('admin.games.counts.empty')}</Note>
      ) : (
        <Card>
          <h2>{t('admin.games.counts.title')}</h2>
          {counts.isPending ? (
            <LoadingState />
          ) : counts.error ? (
            <ErrorState error={counts.error} onRetry={() => void counts.refetch()} />
          ) : counts.data.games.length === 0 && counts.data.queues.length === 0 ? (
            <Note>{t('admin.games.counts.empty')}</Note>
          ) : (
            <div className="ops-grid">
              <section>
                <h3>{t('admin.games.counts.games')}</h3>
                {counts.data.games.map((row, index) => {
                  const dimensions =
                    [
                      row.mode
                        ? t('admin.games.counts.mode', {
                            value:
                              row.game === 'bidding' ||
                              row.game === 'likes' ||
                              row.game === 'blackjack' ||
                              row.game === 'gwent' ||
                              row.game === 'steadycatch' ||
                              row.game === 'lakenotes'
                                ? modeLabel(row.mode, duelText)
                                : enumLabel(t, RPS_MODE_LABEL_KEYS, row.mode),
                          })
                        : null,
                      row.spec
                        ? t('admin.games.counts.spec', {
                            value: enumLabel(t, LINKLINK_SPEC_LABEL_KEYS, row.spec),
                          })
                        : null,
                      row.phase
                        ? t('admin.games.counts.phase', {
                            value:
                              row.game === 'bidding' ||
                              row.game === 'likes' ||
                              row.game === 'blackjack' ||
                              row.game === 'gwent' ||
                              row.game === 'steadycatch' ||
                              row.game === 'lakenotes'
                                ? ({
                                    plan: duelText('选招', 'Choosing skills'),
                                    settlement: duelText('结算展示', 'Settlement presentation'),
                                    joker: duelText('王的决定', 'Joker choice'),
                                    bid: duelText('竞标', 'Bidding'),
                                    seating: duelText('落座', 'Seating'),
                                    decision: duelText('决策', 'Decisions'),
                                    mulligan: duelText('换牌', 'Mulligan'),
                                    turn: duelText('出牌', 'Playing cards'),
                                    choice: duelText('选择目标', 'Choosing a target'),
                                    playing: duelText('游玩中', 'Playing'),
                                    paused: duelText('已暂停', 'Paused'),
                                  }[row.phase] ?? row.phase)
                                : enumLabel(t, RPS_PHASE_LABEL_KEYS, row.phase),
                          })
                        : null,
                    ]
                      .filter(Boolean)
                      .join(' · ') || t('admin.games.counts.active');
                  return (
                    <p key={`${row.game}:${row.mode}:${row.spec}:${row.phase}:${index}`}>
                      <StatusBadge
                        active
                        label={
                          row.game === 'bidding' ||
                          row.game === 'likes' ||
                          row.game === 'blackjack' ||
                          row.game === 'gwent' ||
                          row.game === 'steadycatch' ||
                          row.game === 'lakenotes'
                            ? gameLabel(row.game, duelText)
                            : enumLabel(t, GAME_LABEL_KEYS, row.game)
                        }
                      />{' '}
                      {dimensions} · {row.count}
                    </p>
                  );
                })}
              </section>
              <section>
                <h3>{duelText('匹配队列', 'Matchmaking queues')}</h3>
                {counts.data.queues.map((row) => (
                  <p key={`${row.game}:${row.mode}`}>
                    {row.game === 'rps'
                      ? `${t('admin.games.sections.rps')} · ${enumLabel(t, RPS_MODE_LABEL_KEYS, row.mode)}`
                      : `${gameLabel(row.game, duelText)} · ${modeLabel(row.mode, duelText)}`}
                    : {row.count}
                  </p>
                ))}
              </section>
            </div>
          )}
        </Card>
      )}
      {config.isPending ? (
        <LoadingState />
      ) : initialConfigFailure ? (
        <ErrorState error={initialConfigFailure} onRetry={() => void config.refetch()} />
      ) : config.data ? (
        <>
          {config.error ? (
            <ErrorState error={config.error} onRetry={() => void config.refetch()} />
          ) : null}
          <GamesEditor
            authority={config.data}
            refresh={async () => {
              await Promise.all([config.refetch(), counts.refetch()]);
            }}
          />
        </>
      ) : null}
    </div>
  );
}
