import { useState, type FormEvent } from 'react';
import { useQuery } from '@tanstack/react-query';
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

function validateGamesDraft(draft: GamesConfig): string | null {
  for (const bait of ['worm', 'lure', 'premium'] as const) {
    const value = amountMilli(draft.fishing.bait_prices[bait]);
    if (value === null || value < 1n)
      return `${bait} bait price must be a canonical credit amount of at least 0.001.`;
  }
  for (const mode of ['standard', 'premium'] as const) {
    if (!validInteger(draft.fishing.rtp_percent[mode], 0, 100))
      return `${mode} Fishing RTP must be an integer from 0 to 100.`;
  }
  for (const treasure of ['bottle', 'clover', 'shell'] as const) {
    if (!validInteger(draft.fishing.treasure_multipliers[treasure], 0, 1_000_000)) {
      return `${treasure} multiplier must be an integer from 0 to 1000000.`;
    }
  }
  for (const spec of ['6x8', '8x8', '10x10'] as const) {
    if (amountMilli(draft.linklink.specs[spec].price) === null)
      return `${spec} LinkLink price must be a canonical non-negative credit amount.`;
  }
  for (const mode of RPS_MODES) {
    const value = draft.rps.modes[mode];
    if (amountMilli(value.base) === null)
      return `${mode} RPS base must be a canonical non-negative credit amount.`;
    for (const pump of ['platform', 'welfare', 'thursday'] as const) {
      if (!validInteger(value.pumps_bp[pump], 0, 9_999))
        return `${mode} ${pump} cut must be an integer from 0 to 9999 basis points.`;
    }
    if (value.pumps_bp.platform + value.pumps_bp.welfare + value.pumps_bp.thursday >= 10_000) {
      return `${mode} RPS pool cuts must total less than 10000 basis points.`;
    }
    if (!validInteger(value.queue_seconds, 30, 120))
      return `${mode} queue deadline must be an integer from 30 to 120 seconds.`;
    if (!validInteger(value.gesture_seconds, 5, 20))
      return `${mode} gesture deadline must be an integer from 5 to 20 seconds.`;
    if (!validInteger(value.dealer_seconds, 5, 15))
      return `${mode} dealer deadline must be an integer from 5 to 15 seconds.`;
    if (!validInteger(value.follower_seconds, 5, 15))
      return `${mode} follower deadline must be an integer from 5 to 15 seconds.`;
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
    const error = validateGamesDraft(draft);
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
        <h2>Global gate</h2>
        <p>
          Revision {authority.revision}. Saved changes only govern newly accepted games; in-flight
          sessions keep their frozen terms.
        </p>
        <label>
          <input
            type="checkbox"
            checked={draft.master_enabled}
            disabled={save.isPending}
            onChange={(event) =>
              edit((current) => ({ ...current, master_enabled: event.target.checked }))
            }
          />
          Games master enabled
        </label>
      </Card>
      <Card>
        <h2>Fishing</h2>
        <label>
          <input
            type="checkbox"
            checked={draft.fishing.enabled}
            disabled={save.isPending}
            onChange={(event) => setGameEnabled('fishing', event.target.checked)}
          />
          Fishing enabled
        </label>
        <div className="ops-field-grid">
          {(['worm', 'lure', 'premium'] as const).map((bait) => (
            <label key={bait}>
              <span>{bait} bait price (credits)</span>
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
              <span>{mode} Fishing RTP percent</span>
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
              <span>{treasure} treasure multiplier</span>
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
        <h2>LinkLink</h2>
        <label>
          <input
            type="checkbox"
            checked={draft.linklink.enabled}
            disabled={save.isPending}
            onChange={(event) => setGameEnabled('linklink', event.target.checked)}
          />
          LinkLink enabled
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
                {spec} enabled
              </label>
              <label>
                <span>{spec} entry price (credits)</span>
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
        <h2>Three-player RPS</h2>
        <label>
          <input
            type="checkbox"
            checked={draft.rps.enabled}
            disabled={save.isPending}
            onChange={(event) => setGameEnabled('rps', event.target.checked)}
          />
          RPS enabled
        </label>
        <div className="ops-stack">
          {RPS_MODES.map((mode) => {
            const value = draft.rps.modes[mode];
            return (
              <section key={mode} className="ops-subcard">
                <h3>{mode}</h3>
                <label>
                  <input
                    type="checkbox"
                    checked={value.enabled}
                    disabled={save.isPending}
                    onChange={(event) => setRPSMode(mode, { enabled: event.target.checked })}
                  />
                  {mode} mode enabled
                </label>
                <div className="ops-field-grid">
                  <label>
                    <span>{mode} base (credits)</span>
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
                        {mode} {pump} cut (bp)
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
                          {mode} {field.replaceAll('_', ' ')}
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
                    <span>{mode} queue capacity</span>
                    <strong>{value.queue_capacity}</strong>
                    <small> Read-only runtime capacity; it is excluded from PATCH.</small>
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
          {save.isPending ? 'Saving…' : 'Save atomic game configuration'}
        </button>
        <button
          className="btn btn-secondary"
          type="button"
          disabled={save.isPending}
          onClick={restore}
        >
          Restore authority values
        </button>
      </div>
    </form>
  );
}
export function GamesPage() {
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
        title="Games"
        description="Configure future game acceptance while monitoring independent in-flight and queue counts."
      />
      <Card>
        <h2>Live authority counts</h2>
        {counts.isPending ? (
          <LoadingState />
        ) : counts.error ? (
          <ErrorState error={counts.error} onRetry={() => void counts.refetch()} />
        ) : counts.data.games.length === 0 && counts.data.queues.length === 0 ? (
          <EmptyState
            title="No in-flight games"
            body="No active session or waiting queue is currently reported."
          />
        ) : (
          <div className="ops-grid">
            <section>
              <h3>Games</h3>
              {counts.data.games.map((row, index) => {
                const dimensions =
                  [
                    row.mode ? `mode ${row.mode}` : null,
                    row.spec ? `spec ${row.spec}` : null,
                    row.phase ? `phase ${row.phase}` : null,
                  ]
                    .filter(Boolean)
                    .join(' · ') || 'active';
                return (
                  <p key={`${row.game}:${row.mode}:${row.spec}:${row.phase}:${index}`}>
                    <StatusBadge active label={row.game} /> {dimensions} · {row.count}
                  </p>
                );
              })}
            </section>
            <section>
              <h3>RPS queues</h3>
              {counts.data.queues.map((row) => (
                <p key={row.mode}>
                  {row.mode}: {row.count}
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
