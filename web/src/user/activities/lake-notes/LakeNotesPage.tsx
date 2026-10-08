import { useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  captureStationSession,
  stationSessionMatches,
  StationSessionChangedError,
} from '@shared/charityManagement';
import { directions, directionUnits, unitsToNatural, type Direction } from '@shared/lakenotes/api';
import { UserPageGate } from '../../components/UserPageGate';
import { useUserSession } from '../../data';
import { economySessionRequest } from '../../features/economy/queries';
import { type Action } from './rules';
import { LakeController } from './controller';
import { NativeLake } from './NativeLake';
import { nativeEnglish } from './native-language';
import { useLakeCopy, type LakeText } from './copy';
import {
  act,
  checkpoint,
  controlCast,
  controlInput,
  exchange,
  getCast,
  getProfile,
  getQuote,
  lakeKeys,
  startCast,
  terminalPhase,
  type CastResult,
  type ControlInput,
  type ProfileView,
  type Quote,
} from './api';

import './platform.css';

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
    settings = view.settings;
  const enabled = directions.filter((d) => settings.exchanges[d].enabled);
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
    if (!actual || locked || quoting) return;
    setQuote(null);
    setError(null);
    setSaved(false);
    setQuoting(true);
    try {
      if (!/^[1-9][0-9]{0,38}$/.test(quantity)) throw new Error(text('batchInvalid'));
      const result = await economySessionRequest(
        client,
        () => getQuote({ direction: actual, quantity }),
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
      direction: quote.direction,
      quantity: quote.quantity,
      expected_settings_revision: quote.settings_revision,
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
  const { t: text, language } = useLakeCopy(),
    client = useQueryClient();
  const query = useQuery({
    queryKey: lakeKeys(account),
    queryFn: ({ signal }) => economySessionRequest(client, () => getProfile({ signal }), account),
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
  });
  const controller = useMemo(() => {
    const subject = captureStationSession(client, 'steward').subject;
    const sessions = new WeakMap<CastResult, ReturnType<typeof captureStationSession>>();
    const request = async (call: () => Promise<CastResult>) => {
      // Session reads advance the request generation without changing accounts.
      // Bind each new checkpoint to the current session of this controller's owner.
      const station = captureStationSession(client, 'steward');
      if (station.subject !== subject || !stationSessionMatches(client, 'steward', station))
        throw new StationSessionChangedError();
      const result = await call();
      if (!stationSessionMatches(client, 'steward', station))
        throw new StationSessionChangedError();
      sessions.set(result, station);
      return result;
    };
    return new LakeController(
      {
        checkpoint: (id, input, key) => request(() => checkpoint(id, input, key)),
        pause: (id, input, key) => request(() => controlCast(id, 'pause', input, key)),
        read: (id) => request(() => getCast(id)),
      },
      (result) => {
        const savedSession = sessions.get(result);
        if (savedSession && stationSessionMatches(client, 'steward', savedSession))
          client.setQueryData(lakeKeys(account), result.profile);
      },
    );
  }, [account, client]);
  const state = useSyncExternalStore(controller.subscribe, controller.snapshot);
  useEffect(() => {
    controller.activate();
    return () => controller.dispose();
  }, [controller]);
  useEffect(() => {
    const view = query.data;
    const status = controller.snapshot().status;
    // Returning to a cached page can reveal a newer saved cast. Preserve active
    // input and uncertain requests, but refresh a controller waiting to resume.
    if (view?.cast && !['running', 'saving', 'unknown'].includes(status))
      controller.adopt({ profile: view, cast: view.cast });
  }, [query.data, controller]);
  const update = (view: ProfileView) => client.setQueryData(lakeKeys(account), view);
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
        document.querySelector<HTMLElement>('.lake-original .track')?.focus();
      });
      return result;
    },
    async (_input, error, context) => {
      if (error && !responseOutcomeUnknown(error)) await context.commit(() => query.refetch());
    },
    ['user', 'lake-notes'],
  );
  if (query.isPending || query.error || !query.data)
    return (
      <div className="page lake-page">
        <div className="lake-platform-toolbar">
          <Link className="lake-back" to="/games">
            {text('back')}
          </Link>
        </div>
        {query.isPending ? (
          <LoadingState />
        ) : (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        )}
      </div>
    );
  const view = query.data,
    unfinished = Boolean(view.cast && !terminalPhase(view.cast.phase));
  const locked =
    query.isFetching ||
    action.isPending ||
    action.outcome === 'unknown' ||
    control.isPending ||
    control.outcome === 'unknown';
  const blocked = view.readonly || unfinished || locked;
  const onAction = async (input: Action) => {
    if (blocked) return;
    return action.mutateAsync({ ...input, expected_profile_revision: view.revision });
  };
  const resume = () => {
    if (controller.snapshot().status === 'unknown') {
      void controller.retry().catch(() => undefined);
      return;
    }
    const cast = controller.snapshot().result?.cast ?? view.cast;
    if (cast && !locked && !view.readonly)
      control.mutate({ kind: 'resume', id: cast.id, input: controlInput(cast) });
  };
  return (
    <div className="page lake-page">
      <div className="lake-platform-toolbar">
        <Link className="lake-back" to="/games">
          {text('back')}
        </Link>
        <p>
          {Object.values(view.settings.exchanges).some((direction) => direction.enabled)
            ? text('exchangeAvailable')
            : text('exchangeUnavailable')}
        </p>
      </div>
      {view.readonly ? <p role="status">{text('closed')}</p> : null}
      <NativeLake
        bridge={{
          language,
          translate: nativeEnglish,
          profile: () => view.profile,
          revision: () => view.revision,
          snapshot: controller.snapshot,
          projection: controller.projection,
          held: (held) => controller.setHeld(held),
          tick: () => controller.tick(),
          act: onAction,
          start: () => {
            if (!blocked)
              control.mutate({ kind: 'start', expected_profile_revision: view.revision });
          },
          resume,
          pause: () => {
            void controller.pause().catch(() => undefined);
          },
          blocked: () => blocked,
          busy: () => locked,
          readonly: () => view.readonly,
          text,
        }}
      />
      <div className="lake-platform-controls">
        <p role="status">
          {state.status === 'unknown'
            ? text('reconnecting')
            : state.status === 'saving'
              ? text('saving')
              : text('saved')}
        </p>
        {unfinished && state.status !== 'saving' ? (
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => void controller.readCurrent().catch(() => undefined)}
          >
            {text('checkSaved')}
          </button>
        ) : null}
        {state.status === 'unknown' ? (
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => void controller.retry().catch(() => undefined)}
          >
            {text('retry')}
          </button>
        ) : null}
        {state.error || action.error || control.error ? (
          <ErrorState error={state.error ?? action.error ?? control.error} />
        ) : null}
        {action.outcome === 'unknown' || control.outcome === 'unknown' ? (
          <div role="status">
            <p>{text('saveUnknown')}</p>
            <button
              className="btn btn-secondary"
              type="button"
              onClick={() => {
                if (action.outcome === 'unknown' && action.variables)
                  action.mutate(action.variables);
                else if (control.variables) control.mutate(control.variables);
              }}
            >
              {text('retry')}
            </button>
          </div>
        ) : null}
      </div>
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
