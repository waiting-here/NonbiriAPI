import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useDuelText } from './duel/copy';
import { gameRequest } from './request';
import { exactRecord, invalidResponse } from './strict';
import {
  RANDOM_MAX_BYTES,
  randomProof,
  verifyRandomProof,
  type RandomGame,
  type RandomProof,
} from './randomness';
import './randomness.css';
function save(proof: RandomProof) {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(proof, null, 2)], { type: 'application/json' }),
  );
  const a = document.createElement('a');
  a.href = url;
  a.download = `nonbiri-${proof.game}-${proof.resource_id}-${proof.seed ? 'proof' : 'commitment'}.json`;
  a.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}
export function RandomnessProof({
  game,
  id,
  terminal = false,
}: {
  readonly game: RandomGame;
  readonly id?: string;
  readonly terminal?: boolean;
}) {
  return id ? <ProofPanel key={`${game}:${id}`} game={game} id={id} terminal={terminal} /> : null;
}
function ProofPanel({
  game,
  id,
  terminal,
}: {
  readonly game: RandomGame;
  readonly id: string;
  readonly terminal: boolean;
}) {
  const text = useDuelText(),
    client = useQueryClient();
  const key = ['user', 'games', game, 'randomness', id] as const;
  const query = useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      const envelope = exactRecord(
        (
          await gameRequest<unknown>(`/api/games/${game}/randomness/${encodeURIComponent(id)}`, {
            signal,
            expectedStatuses: [200],
            maxResponseBytes: RANDOM_MAX_BYTES + 32,
          })
        ).data,
        ['proof'],
      );
      if (envelope.proof === null) return null;
      const p = randomProof(envelope.proof, game, id);
      const previous = client.getQueryData<RandomProof | null>(key);
      if (previous && previous.commitment !== p.commitment)
        invalidResponse('changed opening commitment');
      return p;
    },
    retry: false,
    staleTime: 30000,
    refetchInterval: (q) => (q.state.data && !q.state.data.seed ? 5000 : false),
    refetchOnWindowFocus: true,
  });
  const { refetch } = query;
  useEffect(() => {
    if (terminal) void refetch();
  }, [terminal, refetch]);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  const [verificationStatus, setVerification] = useState<'idle' | 'busy' | 'passed' | 'failed'>(
    'idle',
  );
  const [checked, setChecked] = useState<RandomProof | null>(null);
  const [count, setCount] = useState(0);
  const p = query.data;
  const verification = checked === p ? verificationStatus : 'idle';
  useEffect(() => {
    controller.current?.abort();
  }, [p]);
  async function verify() {
    if (!p?.seed || verification === 'busy') return;
    controller.current?.abort();
    const next = new AbortController();
    controller.current = next;
    setChecked(p);
    setVerification('busy');
    try {
      const samples = await verifyRandomProof(p, p.commitment, next.signal);
      if (!next.signal.aborted) {
        setCount(samples);
        setVerification('passed');
      }
    } catch {
      if (!next.signal.aborted) setVerification('failed');
    }
  }
  return (
    <details className="random-proof">
      <summary>
        {text('common.verifyRandomness')}{' '}
        <span>
          {p?.seed
            ? text('common.seedDisclosed')
            : p
              ? text('common.openingCommitmentLocked')
              : text('common.proof')}
        </span>
      </summary>
      {query.isPending ? (
        <p>{text('common.loadingProof')}</p>
      ) : query.isError ? (
        <p role="status">
          {text('common.theProofIsTemporarilyUnavailable')}{' '}
          <button type="button" className="nb-btn nb-btn--secondary" onClick={() => void refetch()}>
            {text('common.retry')}
          </button>
        </p>
      ) : p === null ? (
        <p>{text('common.thisGamePredatesRandomProofsAndCannot')}</p>
      ) : p ? (
        <>
          <p>
            {p.seed
              ? text('common.thisGameHasEndedCheckTheSeed')
              : text('common.theServerKeepsTheSeedSecretUntil')}
          </p>
          <dl>
            <dt>{text('common.openingCommitmentSHA256')}</dt>
            <dd>
              <code>{p.commitment}</code>
            </dd>
            {p.seed && (
              <>
                <dt>{text('common.disclosedSeed')}</dt>
                <dd>
                  <code>{p.seed}</code>
                </dd>
              </>
            )}
          </dl>
          <div className="random-proof-actions">
            {p.seed && (
              <button
                type="button"
                className="nb-btn nb-btn--primary"
                disabled={verification === 'busy'}
                onClick={() => void verify()}
              >
                {verification === 'busy' ? text('common.verifying') : text('common.verifyLocally')}
              </button>
            )}
            <button type="button" className="nb-btn nb-btn--secondary" onClick={() => save(p)}>
              {p.seed ? text('common.downloadProof') : text('common.saveCommitment')}
            </button>
          </div>
          <p role="status" className={`random-proof-status is-${verification}`}>
            {verification === 'passed'
              ? text('common.verifiedCommitmentMatchesAllRecordedDrawsReproduce', { count: count })
              : verification === 'failed'
                ? text('common.verificationFailedTheProofIsInconsistentOr')
                : null}
          </p>
          <p className="random-proof-note">
            {text('common.thisVerifiesTheConsistencyOfRandomDraws')}
          </p>
        </>
      ) : null}
    </details>
  );
}
