import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useCompanionCopy } from '@shared/companionCopy';
import { ErrorState, LoadingState } from '@shared/components/States';
import { Field, Fields, Fold } from '@shared/components/ui';
import { likesCatalog, type ModeCatalog } from '../likes/catalog';
import { likesView } from '../likes/normalize';
import { initialSelection, selectionProblem } from '../likes/selection';
import type { Selection } from '../likes/types';
import { LoadoutEditor } from '../likes/Loadout';
import { Arena, CompactScores } from '../likes/Arena';
import { PlanEditor } from '../likes/PlanEditor';
import { Glossary } from '../likes/Glossary';
import { gameRequest } from '../common/request';
import { useReducedMotion } from '../likes/motion';
import type { Pair, Profile } from '../common/duel/types';
import type { Match } from './api';
import '../likes/likes.css';

export interface LikesBoardProps {
  match?: Match;
  mode: Match['mode'];
  busy: boolean;
  onAction?: (action: unknown) => void;
  onStart?: (setup: Selection, constraints: Record<string, unknown>) => void;
}
export function LikesBoard(props: LikesBoardProps) {
  const catalogs = useQuery({
    queryKey: ['user', 'games', 'likes', 'catalog'],
    queryFn: async ({ signal }) =>
      likesCatalog(
        (await gameRequest('/api/games/likes/catalog', { signal, maxResponseBytes: 1024 * 1024 }))
          .data,
      ),
    staleTime: Infinity,
  });
  if (!catalogs.data)
    return catalogs.error ? (
      <ErrorState error={catalogs.error} onRetry={() => void catalogs.refetch()} />
    ) : (
      <LoadingState />
    );
  return <LikesTable key={props.mode} {...props} catalog={catalogs.data.modes[props.mode]} />;
}
function LikesTable({
  catalog,
  match,
  busy,
  onStart,
  onAction,
}: LikesBoardProps & { catalog: ModeCatalog }) {
  const key = useCompanionCopy();
  const [selection, setSelection] = useState(() => initialSelection(catalog));
  const [guide, setGuide] = useState<string | null>(null);
  const [role, setRole] = useState(''),
    [harness, setHarness] = useState(''),
    [skills, setSkills] = useState<string[]>([]);
  const skillRole =
    role || catalog.skills.find((s) => skills.includes(s.id) && s.owner !== '全局公共')?.owner;
  const reduced = useReducedMotion();
  const view =
    match && match.phase !== 'setup' && 'players' in match.view ? likesView(match.view) : null;
  const locked: [boolean, boolean] = [false, false];
  const profiles: Pair<Profile> =
    match?.seat === 1
      ? [
          {
            kind: 'ai',
            displayName: match.program === 'official' ? key('official') : match.program,
          },
          { kind: 'anonymous' },
        ]
      : [
          { kind: 'anonymous' },
          {
            kind: 'ai',
            displayName: match?.program === 'official' ? key('official') : match?.program,
          },
        ];
  if (match) {
    locked[match.seat] = !match.decision_id;
    locked[1 - match.seat] = match.ai_state === 'locked';
  }
  return (
    <div className="likes-game">
      {view && match ? (
        <>
          <div className="likes-phase">
            <strong>{key('round', { round: match.round })}</strong>
            <CompactScores
              view={view}
              you={match.seat}
              now={0}
              reduced={reduced}
              resolution={null}
              roundStart={null}
            />
          </div>
          <Arena
            view={view}
            catalog={catalog}
            you={match.seat}
            round={match.round}
            now={0}
            profiles={profiles}
            locked={locked}
            resolution={null}
            roundStart={null}
            reduced={reduced}
            onInspect={setGuide}
          />
          {onAction && (
            <PlanEditor
              key={match.id + ':' + match.round}
              state={{
                view,
                you: match.seat,
                round: match.round,
                phase: match.phase === 'plan' ? 'plan' : 'settlement',
                locked,
              }}
              catalog={catalog}
              blocked={busy}
              onInspect={setGuide}
              onLock={(plan) => onAction({ kind: 'plan', plan })}
            />
          )}
        </>
      ) : !match || (match.decision_id && onAction) ? (
        <>
          <Fold title={key('yourLoadout')} summary={selection.role}>
            <LoadoutEditor
              catalog={catalog}
              value={selection}
              onChange={setSelection}
              disabled={busy}
              onInspect={setGuide}
            />
          </Fold>
          {!match && (
            <Fold title={key('aiLoadout')} summary={key('optional')}>
              <Fields columns={2}>
                <Field label={key('aiRole')}>
                  {(field) => (
                    <select
                      {...field}
                      value={role}
                      onChange={(e) => {
                        const next = e.target.value;
                        setRole(next);
                        if (next)
                          setSkills(
                            skills.filter((id) =>
                              catalog.skills.some(
                                (s) => s.id === id && (s.owner === next || s.owner === '全局公共'),
                              ),
                            ),
                          );
                      }}
                    >
                      <option value="">{key('aiChoice')}</option>
                      {catalog.roles.map((r) => (
                        <option value={r.id} key={r.id}>
                          {r.name}
                        </option>
                      ))}
                    </select>
                  )}
                </Field>
                <Field label={key('aiTool')}>
                  {(field) => (
                    <select {...field} value={harness} onChange={(e) => setHarness(e.target.value)}>
                      <option value="">{key('aiChoice')}</option>
                      <option value="none">{key('noTool')}</option>
                      {catalog.harnesses.map((h) => (
                        <option key={h.id} value={h.id}>
                          {h.name}
                        </option>
                      ))}
                    </select>
                  )}
                </Field>
                <Field label={key('requiredSkills')} help={key('skillHelp')}>
                  {(field) => (
                    <select
                      {...field}
                      value=""
                      onChange={(e) => {
                        if (e.target.value) setSkills([...skills, e.target.value]);
                      }}
                    >
                      <option value="">{key('addSkill')}</option>
                      {catalog.skills
                        .filter(
                          (s) =>
                            !skills.includes(s.id) &&
                            (!skillRole || s.owner === skillRole || s.owner === '全局公共'),
                        )
                        .map((s) => (
                          <option key={s.id} value={s.id}>
                            {s.name}
                          </option>
                        ))}
                    </select>
                  )}
                </Field>
              </Fields>
              <div className="duel-actions">
                {skills.map((id) => (
                  <button
                    type="button"
                    className="nb-btn nb-btn--secondary nb-btn--sm"
                    key={id}
                    onClick={() => setSkills(skills.filter((s) => s !== id))}
                  >
                    {key('remove')} {catalog.skills.find((s) => s.id === id)?.name}
                  </button>
                ))}
              </div>
            </Fold>
          )}
          <button
            className="nb-btn nb-btn--primary"
            type="button"
            disabled={busy || !!selectionProblem(catalog, selection)}
            onClick={() =>
              match
                ? onAction?.(selection)
                : onStart?.(selection, {
                    ...(role ? { role } : {}),
                    ...(harness ? { harness: harness === 'none' ? null : harness } : {}),
                    ...(skills.length ? { skills } : {}),
                  })
            }
          >
            {key(match ? 'lockSetup' : 'start')}
          </button>
        </>
      ) : null}
      {guide !== null && (
        <Glossary catalog={catalog} initial={guide} onClose={() => setGuide(null)} />
      )}
    </div>
  );
}
