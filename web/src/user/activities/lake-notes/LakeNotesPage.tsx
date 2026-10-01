import { useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  captureStationSession,
  stationSessionMatches,
  StationSessionChangedError,
} from '@shared/charityManagement';
import {
  getLakeDetail,
  directions,
  directionUnits,
  unitsToNatural,
  type Direction,
} from '@shared/lakenotes/api';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { UserPageGate } from '../../components/UserPageGate';
import { useUserSession } from '../../data';
import { economySessionRequest } from '../../features/economy/queries';
import { caught, levelFromXp, xpForLevel, type Action } from './rules';
import { LakeController } from './controller';
import { LakeScene } from './scene';
import { LakeMenus, type Menu } from './menus';
import { art, loadoutLabel } from './presentation';
import { catalogText, useLakeCopy, type LakeText } from './copy';
import {
  act,
  checkpoint,
  controlCast,
  controlInput,
  enter,
  exchange,
  getCast,
  getProfile,
  getQuote,
  lakeKeys,
  startCast,
  terminalPhase,
  type ControlInput,
  type ProfileView,
  type Quote,
} from './api';
import './original.css';
import './lake.css';

function LakeExchange({
  view,
  account,
  text,
}: {
  view: ProfileView;
  account: string;
  text: LakeText;
}) {
  const client = useQueryClient(),
    period = view.period;
  const enabled = period ? directions.filter((d) => period.exchanges[d]?.enabled) : [];
  const [direction, setDirection] = useState<Direction>(enabled[0] ?? 'general_to_coins'),
    [quantity, setQuantity] = useState('1'),
    [quote, setQuote] = useState<Quote | null>(null),
    [error, setError] = useState<unknown>(null),
    [quoting, setQuoting] = useState(false),
    [saved, setSaved] = useState(false);
  const operation = useRetainedOperation(
    async (input: Parameters<typeof exchange>[0], key, context) => {
      const result = await exchange(input, key, { signal: context.signal });
      context.commit(() => {
        client.setQueryData(lakeKeys(account), result.profile);
        setSaved(true);
        setQuote(null);
      });
      return result;
    },
    async (_input, error, context) => {
      if (!error || !responseOutcomeUnknown(error))
        await context.commit(async () => {
          if (error) setQuote(null);
          await client.invalidateQueries({ queryKey: lakeKeys(account) });
        });
    },
    ['user', 'lake-notes'],
  );
  const unknown = operation.outcome === 'unknown',
    locked = operation.isPending || unknown;
  const actual = enabled.includes(direction) ? direction : enabled[0];
  const preview = async () => {
    if (!period || !actual || locked || quoting) return;
    setQuote(null);
    setError(null);
    setSaved(false);
    setQuoting(true);
    try {
      if (!/^[1-9][0-9]{0,38}$/.test(quantity)) throw new Error(text('batchInvalid'));
      const result = await economySessionRequest(
        client,
        () => getQuote({ period_id: period.id, direction: actual, quantity }),
        account,
      );
      setQuote(result);
    } catch (e) {
      setError(e);
    } finally {
      setQuoting(false);
    }
  };
  const confirm = () => {
    if (unknown && operation.variables) {
      operation.mutate(operation.variables);
      return;
    }
    if (!quote || view.readonly) return;
    operation.mutate({
      period_id: quote.period_id,
      direction: quote.direction,
      quantity: quote.quantity,
      expected_period_revision: quote.period_revision,
      expected_profile_revision: quote.profile_revision,
    });
  };
  if (!enabled.length && !unknown && !saved) return null;
  const pair = quote ? directionUnits[quote.direction] : null;
  return (
    <Card>
      <h2>{text('exchangeHeading')}</h2>
      <p>{text('exchangeHelp')}</p>
      <form
        className="lake-form"
        onChange={() => {
          operation.reset();
          setSaved(false);
          setError(null);
        }}
        onSubmit={(e) => {
          e.preventDefault();
          void preview();
        }}
      >
        <fieldset disabled={locked || view.readonly || quoting}>
          <label>
            {text('direction')}
            <select
              aria-label={text('direction')}
              value={actual}
              onChange={(e) => {
                setDirection(e.target.value as Direction);
                setQuote(null);
              }}
            >
              {enabled.map((d) => (
                <option key={d} value={d}>
                  {text(d)}
                </option>
              ))}
            </select>
          </label>
          <label>
            {text('batches')}
            <input
              inputMode="numeric"
              pattern="[1-9][0-9]*"
              maxLength={39}
              value={quantity}
              onChange={(e) => {
                setQuantity(e.target.value);
                setQuote(null);
              }}
              required
            />
          </label>
          <button type="submit" className="btn btn-secondary">
            {text('quote')}
          </button>
        </fieldset>
      </form>
      {quote && pair ? (
        <p className="lake-quote">
          {text('quoteAmounts', {
            source: unitsToNatural(quote.source_amount, pair[0]),
            sourceUnit: text(pair[0]),
            target: unitsToNatural(quote.target_amount, pair[1]),
            targetUnit: text(pair[1]),
          })}
        </p>
      ) : null}
      {error || operation.error ? <ErrorState error={error ?? operation.error} /> : null}
      {operation.outcome === 'conflict' ? <p role="status">{text('exchangeConflict')}</p> : null}
      {unknown ? <p role="status">{text('exchangeUnknown')}</p> : null}
      {saved ? <p role="status">{text('exchangeSaved')}</p> : null}
      <button
        type="button"
        className="btn btn-primary"
        onClick={confirm}
        disabled={operation.isPending || (!unknown && (!quote || view.readonly))}
      >
        {unknown ? text('retry') : text('exchangeConfirm')}
      </button>
    </Card>
  );
}

function LakeContent({ account }: { account: string }) {
  const formatDateTime = useDateTimeFormatter();
  const { t: text } = useLakeCopy(),
    client = useQueryClient(),
    [menu, setMenu] = useState<Menu | null>(null);
  const query = useQuery({
    queryKey: lakeKeys(account),
    queryFn: ({ signal }) => economySessionRequest(client, () => getProfile({ signal }), account),
    refetchOnWindowFocus: false,
  });
  const detail = useQuery({
    queryKey: [...lakeKeys(account), 'detail'],
    queryFn: ({ signal }) =>
      economySessionRequest(client, () => getLakeDetail({ signal }), account),
    refetchOnWindowFocus: false,
  });
  const controller = useMemo(() => {
    const station = captureStationSession(client, 'steward');
    const request = async <T,>(call: () => Promise<T>) => {
      if (!stationSessionMatches(client, 'steward', station))
        throw new StationSessionChangedError();
      const result = await call();
      if (!stationSessionMatches(client, 'steward', station))
        throw new StationSessionChangedError();
      return result;
    };
    return new LakeController(
      {
        checkpoint: (id, input, key) => request(() => checkpoint(id, input, key)),
        pause: (id, input, key) => request(() => controlCast(id, 'pause', input, key)),
        read: (id) => request(() => getCast(id)),
      },
      (result) => {
        if (stationSessionMatches(client, 'steward', station))
          client.setQueryData(lakeKeys(account), result.profile);
      },
    );
  }, [account, client]);
  const state = useSyncExternalStore(controller.subscribe, controller.snapshot);
  useEffect(() => {
    const view = query.data;
    if (view?.cast && !controller.snapshot().result)
      controller.adopt({ profile: view, cast: view.cast });
  }, [query.data, controller]);
  useEffect(() => () => controller.dispose(), [controller]);
  const update = (view: ProfileView) => client.setQueryData(lakeKeys(account), view);
  const entry = useRetainedOperation(
    async (input: Parameters<typeof enter>[0], key, context) => {
      const result = await enter(input, key, { signal: context.signal });
      context.commit(() => update(result.profile));
      return result;
    },
    async (_input, error, context) => {
      if (!error || !responseOutcomeUnknown(error)) await context.commit(() => query.refetch());
    },
    ['user', 'lake-notes'],
  );
  const action = useRetainedOperation(
    async (input: Parameters<typeof act>[0], key, context) => {
      const result = await act(input, key, { signal: context.signal });
      context.commit(() => update(result.profile));
      return result;
    },
    async (_input, error, context) => {
      if (!error || !responseOutcomeUnknown(error)) await context.commit(() => query.refetch());
    },
    ['user', 'lake-notes'],
  );
  type StartControl =
    | { kind: 'start'; expected_profile_revision: string }
    | { kind: 'resume'; id: string; input: ControlInput };
  const control = useRetainedOperation(
    async (input: StartControl, key, context) => {
      const result = await (input.kind === 'start'
        ? startCast({ expected_profile_revision: input.expected_profile_revision }, key, {
            signal: context.signal,
          })
        : controlCast(input.id, 'resume', input.input, key, { signal: context.signal }));
      context.commit(() => {
        update(result.profile);
        controller.adopt(result, true);
        document.querySelector<HTMLElement>('.lake-game .track')?.focus();
      });
      return result;
    },
    async (_input, error, context) => {
      if (error && !responseOutcomeUnknown(error)) await context.commit(() => query.refetch());
    },
    ['user', 'lake-notes'],
  );
  if (query.isPending) return <LoadingState />;
  if (query.error || !query.data)
    return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  const view = query.data,
    p = view.profile,
    current = view.cast,
    unfinished = Boolean(current && !terminalPhase(current.phase));
  const locked =
    entry.isPending ||
    entry.outcome === 'unknown' ||
    action.isPending ||
    action.outcome === 'unknown' ||
    control.isPending ||
    control.outcome === 'unknown';
  const blocked = view.readonly || unfinished || locked,
    level = levelFromXp(p.xp),
    levelStart = xpForLevel(level),
    next = level === 20 ? levelStart : xpForLevel(level + 1);
  const xpPercent =
    level === 20
      ? 100
      : Number(((BigInt(p.xp) - BigInt(levelStart)) * 100n) / BigInt(next - levelStart));
  const onAction = (input: Action) => {
    if (!blocked) action.mutate({ ...input, expected_profile_revision: view.revision });
  };
  const terminal = state.status === 'terminal' ? state.result : null;
  return (
    <div className="page">
      <PageHeader
        title={text('title')}
        icon="activities"
        back={<Link to="/activities">{text('back')}</Link>}
      />
      <Card>
        <h2>{text('entryHeading')}</h2>
        {view.period ? (
          <>
            <h3>{view.period.name}</h3>
            <p>
              {formatDateTime(view.period.starts_at)} — {formatDateTime(view.period.ends_at)}
            </p>
            <p>
              {text('entryFee', {
                amount: unitsToNatural(view.period.entry_fee_milli ?? '0', 'general'),
              })}
            </p>
          </>
        ) : null}
        <p>{text('entryScope')}</p>
        {view.entitlement ? <p role="status">{text('paid')}</p> : null}
        {detail.isPending ? (
          <LoadingState />
        ) : detail.error ? (
          <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
        ) : detail.data?.status !== 'open' || !view.period ? (
          <p role="status">{text('closed')}</p>
        ) : null}
        {detail.data?.module_config.periods
          .filter((period) => period.id !== view.period?.id)
          .map((period) => (
            <p key={period.id}>
              {period.name} · {formatDateTime(period.starts_at)} — {formatDateTime(period.ends_at)}
            </p>
          ))}
        {entry.error ? <ErrorState error={entry.error} /> : null}
        {entry.outcome === 'unknown' ? <p role="status">{text('entryUnknown')}</p> : null}
        {(!view.entitlement && view.period) || entry.outcome === 'unknown' ? (
          <button
            className="btn btn-primary"
            type="button"
            disabled={
              entry.isPending || (entry.outcome !== 'unknown' && detail.data?.status !== 'open')
            }
            onClick={() => {
              if (entry.outcome === 'unknown' && entry.variables) entry.mutate(entry.variables);
              else if (view.period)
                entry.mutate({
                  period_id: view.period.id,
                  expected_period_revision: view.period.revision,
                });
            }}
          >
            {entry.outcome === 'unknown' ? text('retry') : text('enter')}
          </button>
        ) : null}
      </Card>
      <div className="lake-game">
        <section className="shell">
          <header className="topbar">
            <h2>{text('title')}</h2>
            <p className="top-note">{text('intro')}</p>
          </header>
          <LakeScene
            controller={controller}
            profile={p}
            controls={
              <>
                <div className="lake-save" role="status">
                  {state.status === 'unknown'
                    ? text('reconnecting')
                    : state.status === 'conflict' || (state.status === 'readonly' && unfinished)
                      ? text('controllerLost')
                      : state.status === 'saving'
                        ? text('saving')
                        : text('saved')}
                </div>
                {state.error ? <ErrorState error={state.error} /> : null}
                <div className="lake-actions">
                  {!unfinished ? (
                    <button
                      type="button"
                      className="primary"
                      disabled={view.readonly || locked}
                      onClick={() =>
                        control.mutate({ kind: 'start', expected_profile_revision: view.revision })
                      }
                    >
                      {terminal ? text('again') : text('start')}
                    </button>
                  ) : null}
                  {state.status === 'running' || state.status === 'saving' ? (
                    <button
                      type="button"
                      className="secondary"
                      onClick={() => void controller.pause().catch(() => undefined)}
                    >
                      {text('pause')}
                    </button>
                  ) : null}
                  {unfinished &&
                  state.status !== 'running' &&
                  state.status !== 'saving' &&
                  state.status !== 'unknown' ? (
                    <button
                      className="resume"
                      type="button"
                      disabled={view.readonly || locked}
                      onClick={() => {
                        const cast = state.result?.cast ?? current;
                        if (cast)
                          control.mutate({
                            kind: 'resume',
                            id: cast.id,
                            input: controlInput(cast),
                          });
                      }}
                    >
                      {current?.paused ? text('resume') : text('takeOver')}
                    </button>
                  ) : null}
                  {state.status === 'unknown' ? (
                    <button
                      type="button"
                      className="resume"
                      onClick={() => void controller.retry().catch(() => undefined)}
                    >
                      {text('retry')}
                    </button>
                  ) : null}
                  {unfinished && state.status !== 'saving' ? (
                    <button
                      type="button"
                      onClick={() => void controller.readCurrent().catch(() => undefined)}
                    >
                      {text('checkSaved')}
                    </button>
                  ) : null}
                </div>
                <div className="stats">
                  <span>
                    {text('coins')}
                    <b>{p.coins}</b>
                  </span>
                  <span>{text('caught', { count: String(caught(p)) })}</span>
                  <span>{text('streak', { count: p.streak })}</span>
                </div>
                <div className="xp-heading">
                  <strong>{text('level', { level })}</strong>
                  <span>
                    {level === 20
                      ? text('maxLevel', { xp: p.xp })
                      : text('xp', {
                          xp: String(BigInt(p.xp) - BigInt(levelStart)),
                          next: next - levelStart,
                        })}
                  </span>
                </div>
                <div
                  className="xp-track"
                  role="progressbar"
                  aria-label={text('level', { level })}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={xpPercent}
                >
                  <div className="xp-fill" style={{ width: xpPercent + '%' }} />
                </div>
                <p className="loadout">
                  {text('loadout', {
                    gear: loadoutLabel(text, p.equipped),
                    bait: p.selectedBait
                      ? catalogText(text, 'baits', p.selectedBait)
                      : text('none'),
                  })}
                </p>
                <div className="menu-actions">
                  {(['locations', 'skills', 'shop', 'basket', 'catalog', 'contracts'] as const).map(
                    (item) => (
                      <button
                        type="button"
                        className="skill-open"
                        key={item}
                        onClick={() => {
                          controller.setHeld(false);
                          setMenu(item);
                        }}
                      >
                        {text(item)}
                      </button>
                    ),
                  )}
                  <button
                    type="button"
                    className="skill-open"
                    disabled={blocked}
                    onClick={() => onAction({ action: 'rest' })}
                  >
                    {text('rest')}
                  </button>
                </div>
              </>
            }
          />
          {terminal ? (
            <section className="lake-result" role="status">
              <h2>{text(terminal.cast.phase === 'success' ? 'success' : 'failed')}</h2>
              {terminal.cast.state.result?.debris ? (
                <>
                  <img src={art(terminal.cast.state.result.debris, true)} alt="" loading="lazy" />
                  <p>
                    {text('debrisReward', {
                      item: catalogText(text, 'debris', terminal.cast.state.result.debris),
                    })}
                  </p>
                </>
              ) : terminal.cast.phase === 'success' && terminal.cast.state.plan.fishKind ? (
                <>
                  <img
                    src={art(terminal.cast.state.plan.fishKind)}
                    alt={catalogText(text, 'fish', terminal.cast.state.plan.fishKind)}
                    loading="lazy"
                  />
                  <p>
                    {text('rewardSummary', {
                      fish: catalogText(text, 'fish', terminal.cast.state.plan.fishKind),
                      length: terminal.cast.state.plan.length,
                      quality: catalogText(
                        text,
                        'quality',
                        'q' + terminal.cast.state.result?.quality,
                        '',
                      ),
                      xp: terminal.cast.state.result?.xp ?? 0,
                    })}
                  </p>
                </>
              ) : null}
              {terminal.cast.state.result?.overflow ? <p>{text('overflow')}</p> : null}
              {terminal.cast.state.reward ? (
                <p>
                  {text('treasureReward', {
                    coins: terminal.cast.state.reward.coins,
                    bait: catalogText(text, 'baits', terminal.cast.state.reward.bait),
                    count: terminal.cast.state.reward.count,
                  })}
                </p>
              ) : null}
            </section>
          ) : null}
          {menu ? (
            <LakeMenus
              key={menu}
              menu={menu}
              profile={p}
              blocked={blocked}
              text={text}
              onAction={onAction}
              close={() => setMenu(null)}
            />
          ) : null}
        </section>
      </div>
      {action.error || control.error ? <ErrorState error={action.error ?? control.error} /> : null}
      {action.outcome === 'unknown' || control.outcome === 'unknown' ? (
        <div role="status">
          <p>{text('saveUnknown')}</p>
          <button
            className="btn btn-secondary"
            type="button"
            onClick={() => {
              if (action.outcome === 'unknown' && action.variables) action.mutate(action.variables);
              else if (control.variables) control.mutate(control.variables);
            }}
          >
            {text('retry')}
          </button>
        </div>
      ) : null}
      {action.isSuccess ? <p role="status">{text('actionSaved')}</p> : null}
      <LakeExchange view={view} account={account} text={text} />
    </div>
  );
}
export function LakeNotesPage() {
  const session = useUserSession(),
    account = session.data?.user.id;
  return (
    <UserPageGate>{account ? <LakeContent key={account} account={account} /> : null}</UserPageGate>
  );
}
