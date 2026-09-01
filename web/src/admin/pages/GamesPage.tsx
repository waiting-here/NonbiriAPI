import { useState, type FormEvent } from 'react';
import { useQuery } from '@tanstack/react-query';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import {
  adminEconomyKeys,
  gamesConfigPatch,
  getActiveCounts,
  getGamesConfig,
  patchGamesConfig,
  type GamesConfig,
  type RPSModeConfig,
} from '../features/operations/economy';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
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

const enumLabel = (
  t: TFunction,
  labels: Readonly<Record<string, string>>,
  value: string,
) => t(labels[value] ?? 'admin.games.enums.unknown', { value });

function amountMilli(value: string): bigint | null {
  const match = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,3}))?$/.exec(value);
  if (!match || match[2]?.endsWith('0')) return null;
  try {
    const result = BigInt(match[1]) * 1_000n + BigInt((match[2] ?? '').padEnd(3, '0') || '0');
    return result <= MAX_AMOUNT_MILLI ? result : null;
  } catch {
    return null;
  }
}

const validInteger = (value: number, minimum: number, maximum: number) =>
  Number.isSafeInteger(value) && value >= minimum && value <= maximum;

function validateGamesDraft(draft: GamesConfig, t: TFunction): string | null {
  for (const bait of ['worm', 'lure', 'premium'] as const) {
    const value = amountMilli(draft.fishing.bait_prices[bait]);
    if (value === null || value < 1n)
      return t('admin.games.validation.amountMinimum', {
        field: t(BAIT_LABEL_KEYS[bait]),
        minimum: '0.001',
      });
  }
  for (const mode of ['standard', 'premium'] as const) {
    if (!validInteger(draft.fishing.rtp_percent[mode], 0, 100))
      return t('admin.games.validation.integerRange', {
        field: t(FISHING_RTP_LABEL_KEYS[mode]),
        minimum: 0,
        maximum: 100,
      });
  }
  for (const treasure of ['bottle', 'clover', 'shell'] as const) {
    if (!validInteger(draft.fishing.treasure_multipliers[treasure], 0, 1_000_000)) {
      return t('admin.games.validation.integerRange', {
        field: t(TREASURE_LABEL_KEYS[treasure]),
        minimum: 0,
        maximum: 1_000_000,
      });
    }
  }
  for (const spec of ['6x8', '8x8', '10x10'] as const) {
    if (amountMilli(draft.linklink.specs[spec].price) === null)
      return t('admin.games.validation.amountNonNegative', {
        field: enumLabel(t, LINKLINK_SPEC_LABEL_KEYS, spec),
      });
  }
  for (const mode of RPS_MODES) {
    const value = draft.rps.modes[mode];
    if (amountMilli(value.base) === null)
      return t('admin.games.validation.amountNonNegative', {
        field: t('admin.games.rps.base', { mode: t(RPS_MODE_LABEL_KEYS[mode]) }),
      });
    for (const pump of ['platform', 'welfare', 'thursday'] as const) {
      if (!validInteger(value.pumps_bp[pump], 0, 9_999))
        return t('admin.games.validation.integerRange', {
          field: t('admin.games.rps.cut', {
            mode: t(RPS_MODE_LABEL_KEYS[mode]),
            pump: t(RPS_PUMP_LABEL_KEYS[pump]),
          }),
          minimum: 0,
          maximum: 9_999,
        });
    }
    if (value.pumps_bp.platform + value.pumps_bp.welfare + value.pumps_bp.thursday >= 10_000) {
      return t('admin.games.validation.totalCuts', {
        mode: t(RPS_MODE_LABEL_KEYS[mode]),
        maximum: 10_000,
      });
    }
    if (!validInteger(value.queue_seconds, 30, 120))
      return t('admin.games.validation.integerRange', {
        field: t('admin.games.rps.deadline', {
          mode: t(RPS_MODE_LABEL_KEYS[mode]),
          deadline: t(RPS_DEADLINE_LABEL_KEYS.queue_seconds),
        }),
        minimum: 30,
        maximum: 120,
      });
    if (!validInteger(value.gesture_seconds, 5, 20))
      return t('admin.games.validation.integerRange', {
        field: t('admin.games.rps.deadline', {
          mode: t(RPS_MODE_LABEL_KEYS[mode]),
          deadline: t(RPS_DEADLINE_LABEL_KEYS.gesture_seconds),
        }),
        minimum: 5,
        maximum: 20,
      });
    if (!validInteger(value.dealer_seconds, 5, 15))
      return t('admin.games.validation.integerRange', {
        field: t('admin.games.rps.deadline', {
          mode: t(RPS_MODE_LABEL_KEYS[mode]),
          deadline: t(RPS_DEADLINE_LABEL_KEYS.dealer_seconds),
        }),
        minimum: 5,
        maximum: 15,
      });
    if (!validInteger(value.follower_seconds, 5, 15))
      return t('admin.games.validation.integerRange', {
        field: t('admin.games.rps.deadline', {
          mode: t(RPS_MODE_LABEL_KEYS[mode]),
          deadline: t(RPS_DEADLINE_LABEL_KEYS.follower_seconds),
        }),
        minimum: 5,
        maximum: 15,
      });
  }
  return null;
}

const numberInput = (value: number): string | number => (Number.isNaN(value) ? '' : value);
const numberFromInput = (value: string): number => (value === '' ? Number.NaN : Number(value));
function GamesEditor({
  authority,
  refresh,
}: {
  authority: GamesConfig;
  refresh: () => Promise<unknown>;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(authority);
  const [validation, setValidation] = useState<string | null>(null);
  const save = useRetainedOperation<GamesConfig, GamesConfig>(
    (input, key) => patchGamesConfig(gamesConfigPatch(input), key),
    refresh,
  );
  const edit = (updater: (current: GamesConfig) => GamesConfig) => {
    save.reset();
    setValidation(null);
    setDraft(updater);
  };
  const setGameEnabled = (game: GameName, enabled: boolean) => {
    edit((current) => ({ ...current, [game]: { ...current[game], enabled } }));
  };
  const setRPSMode = (mode: RPSMode, patch: Partial<RPSModeConfig>) => {
    edit((current) => ({
      ...current,
      rps: {
        ...current.rps,
        modes: { ...current.rps.modes, [mode]: { ...current.rps.modes[mode], ...patch } },
      },
    }));
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    const error = validateGamesDraft(draft, t);
    setValidation(error);
    if (error === null) save.mutate(draft);
  };
  const restore = () => {
    save.reset();
    setValidation(null);
    setDraft(authority);
  };

  return (
    <form className="ops-stack" noValidate onSubmit={submit}>
      <Card>
        <h2>{t('admin.games.controls.globalGate')}</h2>
        <p>{t('admin.games.controls.revisionTerms', { revision: authority.revision })}</p>
        <label>
          <input
            type="checkbox"
            checked={draft.master_enabled}
            disabled={save.isPending}
            onChange={(event) =>
              edit((current) => ({ ...current, master_enabled: event.target.checked }))
            }
          />
          {t('admin.games.masterEnabled')}
        </label>
      </Card>
      <Card>
        <h2>{t('admin.games.sections.fishing')}</h2>
        <label>
          <input
            type="checkbox"
            checked={draft.fishing.enabled}
            disabled={save.isPending}
            onChange={(event) => setGameEnabled('fishing', event.target.checked)}
          />
          {t('admin.games.fishingEnabled')}
        </label>
        <div className="ops-field-grid">
          {(['worm', 'lure', 'premium'] as const).map((bait) => (
            <label key={bait}>
              <span>{t('admin.games.controls.priceCredits', { field: t(BAIT_LABEL_KEYS[bait]) })}</span>
              <input
                type="number"
                inputMode="decimal"
                min="0.001"
                max="9000000000000"
                step="0.001"
                value={draft.fishing.bait_prices[bait]}
                disabled={save.isPending}
                onChange={(event) =>
                  edit((current) => ({
                    ...current,
                    fishing: {
                      ...current.fishing,
                      bait_prices: { ...current.fishing.bait_prices, [bait]: event.target.value },
                    },
                  }))
                }
              />
            </label>
          ))}
          {(['standard', 'premium'] as const).map((mode) => (
            <label key={mode}>
              <span>{t(FISHING_RTP_LABEL_KEYS[mode])}</span>
              <input
                type="number"
                min="0"
                max="100"
                step="1"
                value={numberInput(draft.fishing.rtp_percent[mode])}
                disabled={save.isPending}
                onChange={(event) =>
                  edit((current) => ({
                    ...current,
                    fishing: {
                      ...current.fishing,
                      rtp_percent: {
                        ...current.fishing.rtp_percent,
                        [mode]: numberFromInput(event.target.value),
                      },
                    },
                  }))
                }
              />
            </label>
          ))}
          {(['bottle', 'clover', 'shell'] as const).map((treasure) => (
            <label key={treasure}>
              <span>{t(TREASURE_LABEL_KEYS[treasure])}</span>
              <input
                type="number"
                min="0"
                max="1000000"
                step="1"
                value={numberInput(draft.fishing.treasure_multipliers[treasure])}
                disabled={save.isPending}
                onChange={(event) =>
                  edit((current) => ({
                    ...current,
                    fishing: {
                      ...current.fishing,
                      treasure_multipliers: {
                        ...current.fishing.treasure_multipliers,
                        [treasure]: numberFromInput(event.target.value),
                      },
                    },
                  }))
                }
              />
            </label>
          ))}
        </div>
      </Card>
      <Card>
        <h2>{t('admin.games.sections.linklink')}</h2>
        <label>
          <input
            type="checkbox"
            checked={draft.linklink.enabled}
            disabled={save.isPending}
            onChange={(event) => setGameEnabled('linklink', event.target.checked)}
          />
          {t('admin.games.linklink.enabled')}
        </label>
        <div className="ops-field-grid">
          {(['6x8', '8x8', '10x10'] as const).map((spec) => (
            <div key={spec} className="ops-subcard">
              <label>
                <input
                  type="checkbox"
                  checked={draft.linklink.specs[spec].enabled}
                  disabled={save.isPending}
                  onChange={(event) =>
                    edit((current) => ({
                      ...current,
                      linklink: {
                        ...current.linklink,
                        specs: {
                          ...current.linklink.specs,
                          [spec]: {
                            ...current.linklink.specs[spec],
                            enabled: event.target.checked,
                          },
                        },
                      },
                    }))
                  }
                />
                {t('admin.games.linklink.specEnabled', { spec: enumLabel(t, LINKLINK_SPEC_LABEL_KEYS, spec) })}
              </label>
              <label>
                <span>{t('admin.games.linklink.entryPrice', { spec: enumLabel(t, LINKLINK_SPEC_LABEL_KEYS, spec) })}</span>
                <input
                  type="number"
                  inputMode="decimal"
                  min="0"
                  max="9000000000000"
                  step="0.001"
                  value={draft.linklink.specs[spec].price}
                  disabled={save.isPending}
                  onChange={(event) =>
                    edit((current) => ({
                      ...current,
                      linklink: {
                        ...current.linklink,
                        specs: {
                          ...current.linklink.specs,
                          [spec]: { ...current.linklink.specs[spec], price: event.target.value },
                        },
                      },
                    }))
                  }
                />
              </label>
            </div>
          ))}
        </div>
      </Card>
      <Card>
        <h2>{t('admin.games.sections.rps')}</h2>
        <label>
          <input
            type="checkbox"
            checked={draft.rps.enabled}
            disabled={save.isPending}
            onChange={(event) => setGameEnabled('rps', event.target.checked)}
          />
          {t('admin.games.rps.enabled')}
        </label>
        <div className="ops-stack">
          {RPS_MODES.map((mode) => {
            const value = draft.rps.modes[mode];
            return (
              <section key={mode} className="ops-subcard">
                <h3>{t(RPS_MODE_LABEL_KEYS[mode])}</h3>
                <label>
                  <input
                    type="checkbox"
                    checked={value.enabled}
                    disabled={save.isPending}
                    onChange={(event) => setRPSMode(mode, { enabled: event.target.checked })}
                  />
                  {t('admin.games.rps.modeEnabled', { mode: t(RPS_MODE_LABEL_KEYS[mode]) })}
                </label>
                <div className="ops-field-grid">
                  <label>
                    <span>{t('admin.games.rps.base', { mode: t(RPS_MODE_LABEL_KEYS[mode]) })}</span>
                    <input
                      type="number"
                      inputMode="decimal"
                      min="0"
                      max="9000000000000"
                      step="0.001"
                      value={value.base}
                      disabled={save.isPending}
                      onChange={(event) => setRPSMode(mode, { base: event.target.value })}
                    />
                  </label>
                  {(['platform', 'welfare', 'thursday'] as const).map((pump) => (
                    <label key={pump}>
                      <span>
                        {t('admin.games.rps.cut', { mode: t(RPS_MODE_LABEL_KEYS[mode]), pump: t(RPS_PUMP_LABEL_KEYS[pump]) })}
                      </span>
                      <input
                        type="number"
                        min="0"
                        max="9999"
                        step="1"
                        value={numberInput(value.pumps_bp[pump])}
                        disabled={save.isPending}
                        onChange={(event) =>
                          setRPSMode(mode, {
                            pumps_bp: {
                              ...value.pumps_bp,
                              [pump]: numberFromInput(event.target.value),
                            },
                          })
                        }
                      />
                    </label>
                  ))}
                  {(
                    [
                      'queue_seconds',
                      'gesture_seconds',
                      'dealer_seconds',
                      'follower_seconds',
                    ] as const
                  ).map((field: RPSSeconds) => {
                    const bounds =
                      field === 'queue_seconds'
                        ? [30, 120]
                        : field === 'gesture_seconds'
                          ? [5, 20]
                          : [5, 15];
                    return (
                      <label key={field}>
                        <span>
                          {t('admin.games.rps.deadline', { mode: t(RPS_MODE_LABEL_KEYS[mode]), deadline: t(RPS_DEADLINE_LABEL_KEYS[field]) })}
                        </span>
                        <input
                          type="number"
                          min={bounds[0]}
                          max={bounds[1]}
                          step="1"
                          value={numberInput(value[field])}
                          disabled={save.isPending}
                          onChange={(event) =>
                            setRPSMode(mode, { [field]: numberFromInput(event.target.value) })
                          }
                        />
                      </label>
                    );
                  })}
                  <div>
                    <span>{t('admin.games.rps.queueCapacity', { mode: t(RPS_MODE_LABEL_KEYS[mode]) })}</span>
                    <strong>{value.queue_capacity}</strong>
                    <small> {t('admin.games.rps.queueCapacityHint')}</small>
                  </div>
                </div>
              </section>
            );
          })}
        </div>
      </Card>
      {validation ? (
        <p className="field-error" role="alert">
          {validation}
        </p>
      ) : null}
      {save.error ? <ErrorState error={save.error} /> : null}
      <div className="ops-actions">
        <button className="btn btn-primary" type="submit" disabled={save.isPending}>
          {save.isPending ? t('common.working') : t('admin.games.save')}
        </button>
        <button
          className="btn btn-secondary"
          type="button"
          disabled={save.isPending}
          onClick={restore}
        >
          {t('admin.games.restoreAuthorityValues')}
        </button>
      </div>
    </form>
  );
}
export function GamesPage() {
  const { t } = useTranslation();
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
        description={t('admin.games.operationsDescription')}
      />
      <Card>
        <h2>{t('admin.games.counts.title')}</h2>
        {counts.isPending ? (
          <LoadingState />
        ) : counts.error ? (
          <ErrorState error={counts.error} onRetry={() => void counts.refetch()} />
        ) : counts.data.games.length === 0 && counts.data.queues.length === 0 ? (
          <EmptyState
            title={t('admin.games.counts.empty')}
            body={t('admin.games.counts.emptyBody')}
          />
        ) : (
          <div className="ops-grid">
            <section>
              <h3>{t('admin.games.counts.games')}</h3>
              {counts.data.games.map((row, index) => {
                const dimensions =
                  [
                    row.mode ? t('admin.games.counts.mode', { value: enumLabel(t, RPS_MODE_LABEL_KEYS, row.mode) }) : null,
                    row.spec ? t('admin.games.counts.spec', { value: enumLabel(t, LINKLINK_SPEC_LABEL_KEYS, row.spec) }) : null,
                    row.phase ? t('admin.games.counts.phase', { value: enumLabel(t, RPS_PHASE_LABEL_KEYS, row.phase) }) : null,
                  ]
                    .filter(Boolean)
                    .join(' · ') || t('admin.games.counts.active');
                return (
                  <p key={`${row.game}:${row.mode}:${row.spec}:${row.phase}:${index}`}>
                    <StatusBadge active label={enumLabel(t, GAME_LABEL_KEYS, row.game)} /> {dimensions} · {row.count}
                  </p>
                );
              })}
            </section>
            <section>
              <h3>{t('admin.games.counts.rpsQueues')}</h3>
              {counts.data.queues.map((row) => (
                <p key={row.mode}>
                  {t(RPS_MODE_LABEL_KEYS[row.mode])}: {row.count}
                </p>
              ))}
            </section>
          </div>
        )}
      </Card>
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
            key={config.data.revision}
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
