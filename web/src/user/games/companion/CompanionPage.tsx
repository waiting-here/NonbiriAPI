import { lazy, Suspense, useState } from 'react';
import { Link } from 'react-router';
import { useCompanionCopy, type CompanionCopyKey } from '@shared/companionCopy';
import { ErrorState, LoadingState } from '@shared/components/States';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Field, Fields, Fold, Toggle } from '@shared/components/ui';
import { PagePagination } from '@shared/operations/PagePagination';
import type { PageSize } from '@shared/operations/pageNumbers';
import { formatDateTime } from '@shared/utils/datetime';
import type { CompanionGame, CompanionPrograms } from '@shared/companion';
import { GameBackLink } from '../common/GameBackLink';
import { DuelDialog } from '../common/duel/Dialog';
import type { DuelLobbyContext } from '../common/duel/types';
import { FACTIONS } from '../gwent/types';
import { factionName } from '../gwent/copy';
import {
  useCompanionRead,
  useCompanionWrite,
  type CompanionHome,
  type Preferences,
  type Match,
  type MatchSummary,
  type StartOptions,
} from './api';
import { ProgramLibrary } from './ProgramLibrary';
import '../common/duel/duel.css';
import './companion.css';

const Bidding = lazy(async () => ({ default: (await import('./BiddingBoard')).BiddingBoard }));
const Likes = lazy(async () => ({ default: (await import('./LikesBoard')).LikesBoard }));
const Gwent = lazy(async () => ({ default: (await import('../gwent/GwentGame')).GwentGame }));

function outcome(m: MatchSummary): CompanionCopyKey {
  if (m.status !== 'completed')
    return m.status === 'technical'
      ? ((
          {
            timeout: 'timeout',
            invalid_action: 'invalidAction',
            program_error: 'programError',
            invalid_output: 'invalidOutput',
          } as Record<string, CompanionCopyKey>
        )[m.reason] ?? 'engineError')
      : m.status;
  return m.winner == null ? 'draw' : m.winner === m.seat ? 'win' : 'loss';
}
function Board({
  match,
  context,
  busy = false,
  onAction,
  onAbandon,
}: {
  match: Match;
  context: DuelLobbyContext;
  busy?: boolean;
  onAction?: (action: unknown) => void;
  onAbandon?: () => void;
}) {
  const text = useCompanionCopy();
  if (match.phase === 'setup' && (!match.decision_id || !onAction)) return <p>{text('setup')}</p>;
  if (match.game === 'bidding')
    return 'hand_remaining' in match.view ? (
      <Bidding match={match} busy={busy} onAction={onAction} />
    ) : null;
  if (match.game === 'likes')
    return <Likes match={match} mode={match.mode} busy={busy} onAction={onAction} />;
  if (!('self' in match.view) && !(onAction && match.decision_id)) return null;
  return (
    <Gwent
      {...context}
      companion={{
        match,
        busy,
        readOnly: !onAction,
        opponent: match.program === 'official' ? text('official') : match.program,
        onStart: onAction,
        onAction,
        onAbandon,
      }}
    />
  );
}
function History({
  game,
  context,
  close,
}: {
  game: CompanionGame;
  context: DuelLobbyContext;
  close: () => void;
}) {
  const text = useCompanionCopy();
  const [page, setPage] = useState('1'),
    [size, setSize] = useState<PageSize>(10);
  const [id, setID] = useState(''),
    [step, setStep] = useState(0);
  const list = useCompanionRead<{ items: MatchSummary[]; total: number }>(
    game,
    '/history?page=' + page + '&page_size=' + size,
  );
  const replay = useCompanionRead<{ step: number; steps: number; match: Match }>(
    game,
    '/matches/' + encodeURIComponent(id) + '/replay?step=' + step,
    { enabled: !!id },
  );
  return (
    <DuelDialog title={text('history')} onClose={close}>
      {id ? (
        <div className="nb-stack">
          <button className="nb-btn nb-btn--secondary" type="button" onClick={() => setID('')}>
            {text('backHistory')}
          </button>
          {replay.error ? (
            <ErrorState error={replay.error} onRetry={() => void replay.refetch()} />
          ) : !replay.data ? (
            <LoadingState />
          ) : (
            <>
              <Field label={text('replayStep', { step: step + 1, total: replay.data.steps })}>
                {(field) => (
                  <input
                    {...field}
                    type="range"
                    min={0}
                    max={Math.max(0, replay.data.steps - 1)}
                    value={step}
                    onChange={(e) => setStep(Number(e.target.value))}
                  />
                )}
              </Field>
              <div className="duel-actions">
                <button
                  className="nb-btn nb-btn--secondary"
                  disabled={!step}
                  onClick={() => setStep(step - 1)}
                >
                  {text('previous')}
                </button>
                <button
                  className="nb-btn nb-btn--secondary"
                  disabled={step >= replay.data.steps - 1}
                  onClick={() => setStep(step + 1)}
                >
                  {text('next')}
                </button>
              </div>
              <Board match={replay.data.match} context={context} />
            </>
          )}
        </div>
      ) : list.error ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : !list.data ? (
        <LoadingState />
      ) : (
        <div className="nb-stack">
          {list.data.items.length === 0 && <p>{text('emptyHistory')}</p>}
          {list.data.items.map((m) => (
            <div className="companion-history-row" key={m.id}>
              <div>
                <strong>{text(outcome(m))}</strong>
                <br />
                {m.program === 'official' ? text('official') : m.program} · v{m.version} ·{' '}
                {formatDateTime(m.created_at * 1000)}
              </div>
              <button
                className="nb-btn nb-btn--secondary"
                onClick={() => {
                  setStep(0);
                  setID(m.id);
                }}
              >
                {text('replay')}
              </button>
            </div>
          ))}
          <PagePagination
            metadata={{
              page,
              page_size: size,
              total_items: String(list.data.total),
              total_pages: String(Math.max(1, Math.ceil(list.data.total / size))),
            }}
            onPageChange={setPage}
            onPageSizeChange={(value) => {
              setPage('1');
              setSize(value);
            }}
          />
        </div>
      )}
    </DuelDialog>
  );
}
export function CompanionPage({
  game,
  context,
}: {
  game: CompanionGame;
  context: DuelLobbyContext;
}) {
  const text = useCompanionCopy();
  const home = useCompanionRead<CompanionHome>(game, '', { poll: (state) => !!state?.active });
  const programs = useCompanionRead<CompanionPrograms>(game, '/programs');
  const [program, setProgram] = useState('official'),
    [difficulty, setDifficulty] = useState(1);
  const [mode, setMode] = useState<Match['mode']>(game === 'likes' ? 'quick' : 'standard');
  const [faction, setFaction] = useState('');
  const [history, setHistory] = useState(false),
    [abandon, setAbandon] = useState(false);
  const [dismissed, setDismissed] = useState('');
  const mutation = useCompanionWrite<Match>(game, home.refetch);
  const pref = useCompanionWrite<Preferences>(game, home.refetch);
  const active = home.data?.active;
  const latest = home.data?.latest;
  const target =
    program === 'official' ||
    programs.data?.programs.some((p) => p.name === program && p.version != null)
      ? program
      : 'official';
  const changePref = (update: Partial<Preferences>) =>
    pref.mutate({
      method: 'PUT',
      path: '/preferences',
      body: { ...home.data?.preferences, ...update },
    });
  const busy = mutation.isPending || mutation.outcome === 'unknown';
  const blocked =
    busy ||
    !programs.data?.enabled ||
    !context.accepting ||
    pref.isPending ||
    pref.outcome === 'unknown';
  const start = (setup?: unknown, constraints: Record<string, unknown> = {}) => {
    const body: StartOptions = {
      mode,
      program: target,
      difficulty: target === 'official' ? difficulty : 1,
      constraints,
      ...(setup ? { setup } : {}),
    };
    mutation.mutate({ path: '/matches', body });
  };
  const act = (action: unknown) => {
    if (active?.decision_id)
      mutation.mutate({
        path: '/matches/' + encodeURIComponent(active.id) + '/actions',
        body: { decision_id: active.decision_id, action },
      });
  };
  return (
    <div className="companion-page nb-stack">
      <header className="companion-heading">
        <div>
          <GameBackLink />
          <h1>
            {text(game)} · {text('title')}
          </h1>
        </div>
        <div className="duel-actions">
          <Link className="nb-btn nb-btn--secondary" to={'/games/' + game}>
            {text('human')}
          </Link>
          <button className="nb-btn nb-btn--secondary" onClick={() => setHistory(true)}>
            {text('history')}
          </button>
        </div>
      </header>
      {home.error && <ErrorState error={home.error} onRetry={() => void home.refetch()} />}
      {programs.error && (
        <ErrorState error={programs.error} onRetry={() => void programs.refetch()} />
      )}
      {!home.data && !home.error && <LoadingState />}
      {mutation.error && (
        <ErrorState
          error={mutation.error}
          onRetry={mutation.variables ? () => mutation.mutate(mutation.variables!) : undefined}
        />
      )}
      {pref.error && (
        <ErrorState
          error={pref.error}
          onRetry={pref.variables ? () => pref.mutate(pref.variables!) : undefined}
        />
      )}
      <Suspense fallback={<LoadingState />}>
        {active ? (
          <>
            <div className="companion-match-head">
              <div>
                <strong>
                  {active.program === 'official' ? text('official') : active.program} · v
                  {active.version}
                </strong>
                <p role="status">
                  {active.ai_state === 'idle'
                    ? text(active.decision_id ? 'yourMove' : 'waiting')
                    : text(active.ai_state)}
                  {active.decision_id &&
                  (active.ai_state === 'queued' || active.ai_state === 'thinking')
                    ? ' · ' + text('yourMove')
                    : ''}
                </p>
              </div>
              <button
                className="nb-btn nb-btn--danger-outline"
                disabled={busy}
                onClick={() => setAbandon(true)}
              >
                {text('abandon')}
              </button>
            </div>
            <Board
              match={active}
              context={context}
              busy={busy}
              onAction={act}
              onAbandon={() =>
                mutation.mutate({
                  path: '/matches/' + encodeURIComponent(active.id) + '/abandon',
                  body: {},
                })
              }
            />
          </>
        ) : (
          <>
            {latest && latest.id !== dismissed && (
              <div className="companion-match-head">
                <div>
                  <strong>{text(outcome(latest))}</strong>
                  <p>
                    {latest.program === 'official' ? text('official') : latest.program} · v
                    {latest.version}{' '}
                    {latest.status === 'completed' &&
                      ' · ' +
                        text('score', {
                          you: latest.scores[latest.seat],
                          other: latest.scores[1 - latest.seat],
                        })}
                  </p>
                </div>
                <button
                  className="nb-btn nb-btn--secondary"
                  onClick={() => setDismissed(latest.id)}
                >
                  {text('newMatch')}
                </button>
              </div>
            )}
            {programs.data && home.data && (
              <section className="companion-panel nb-stack">
                <h2>{text('choose')}</h2>
                <p>{text('free')}</p>
                <Fields columns={2}>
                  {game === 'likes' && (
                    <Field label={text('mode')}>
                      {(field) => (
                        <select
                          {...field}
                          value={mode}
                          onChange={(e) => setMode(e.target.value as Match['mode'])}
                        >
                          <option value="quick">{text('quick')}</option>
                          <option value="standard">{text('standard')}</option>
                        </select>
                      )}
                    </Field>
                  )}
                  {target === 'official' && (
                    <Field label={text('difficulty', { level: difficulty })}>
                      {(field) => (
                        <input
                          {...field}
                          type="range"
                          min={0}
                          max={Math.max(0, programs.data!.official.difficulties.length - 1)}
                          value={Math.max(
                            0,
                            programs.data!.official.difficulties.indexOf(difficulty),
                          )}
                          disabled={programs.data!.official.difficulties.length <= 1}
                          onChange={(e) =>
                            setDifficulty(
                              programs.data!.official.difficulties[Number(e.target.value)],
                            )
                          }
                        />
                      )}
                    </Field>
                  )}
                  <Field label={text('opponent')}>
                    {(field) => (
                      <select
                        {...field}
                        value={target}
                        onChange={(e) => setProgram(e.target.value)}
                      >
                        <option value="official">
                          {text('official')} · v{programs.data!.official.version}
                        </option>
                        {programs
                          .data!.programs.filter((p) =>
                            p.versions.some((v) => v.status === 'ready'),
                          )
                          .map((p) => (
                            <option value={p.name} key={p.name}>
                              {p.name} · v{p.version}
                            </option>
                          ))}
                      </select>
                    )}
                  </Field>
                  {game === 'gwent' && (
                    <Field label={text('aiFaction')}>
                      {(field) => (
                        <select
                          {...field}
                          value={faction}
                          onChange={(e) => setFaction(e.target.value)}
                        >
                          <option value="">{text('aiChoice')}</option>
                          {FACTIONS.map((f) => (
                            <option key={f} value={f}>
                              {factionName[f]}
                            </option>
                          ))}
                        </select>
                      )}
                    </Field>
                  )}
                </Fields>
                {!programs.data.enabled || !context.accepting ? (
                  <p role="status">{text('closed')}</p>
                ) : null}
                {game === 'bidding' ? (
                  <button
                    className="nb-btn nb-btn--primary"
                    type="button"
                    disabled={blocked}
                    onClick={() => start()}
                  >
                    {text('start')}
                  </button>
                ) : game === 'likes' ? (
                  <Likes mode={mode} busy={blocked} onStart={start} />
                ) : (
                  <Gwent
                    {...context}
                    companion={{
                      busy: blocked,
                      opponent: text('official'),
                      onStart: (deck) => start(deck, faction ? { faction } : {}),
                      onRefresh: () => void programs.refetch(),
                    }}
                  />
                )}
              </section>
            )}
          </>
        )}
      </Suspense>
      {home.data && (
        <Fold
          title={text('preferences')}
          summary={text(
            home.data.preferences.memory === 'opponent' ? 'separate' : home.data.preferences.memory,
          )}
          meta={home.data.preferences.contribute ? text('contribute') : undefined}
        >
          <Fields columns={1}>
            <Field label={text('memory')} help={text('memoryHelp')}>
              {(field) => (
                <select
                  {...field}
                  disabled={pref.isPending || pref.outcome === 'unknown'}
                  value={home.data!.preferences.memory}
                  onChange={(e) => changePref({ memory: e.target.value as Preferences['memory'] })}
                >
                  {(['none', 'opponent', 'shared'] as const).map((m) => (
                    <option key={m} value={m}>
                      {text(m === 'opponent' ? 'separate' : m)}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Toggle
              label={text('contribute')}
              description={text('contributeHelp')}
              checked={home.data.preferences.contribute}
              disabled={pref.isPending || pref.outcome === 'unknown'}
              onChange={(contribute) => changePref({ contribute })}
            />
          </Fields>
        </Fold>
      )}
      {programs.data && (
        <Fold title={text('library')} lazy>
          <ProgramLibrary game={game} programs={programs.data} refresh={programs.refetch} />
        </Fold>
      )}
      {history && (
        <Suspense fallback={<LoadingState />}>
          <History game={game} context={context} close={() => setHistory(false)} />
        </Suspense>
      )}
      <ConfirmDialog
        open={!!active && abandon}
        title={text('abandonTitle')}
        description={text('abandonHelp')}
        confirmLabel={text('abandon')}
        cancelLabel={text('continue')}
        danger
        busy={busy}
        onCancel={() => setAbandon(false)}
        onConfirm={() => {
          if (active)
            mutation.mutate({
              path: '/matches/' + encodeURIComponent(active.id) + '/abandon',
              body: {},
            });
          setAbandon(false);
        }}
      />
    </div>
  );
}
