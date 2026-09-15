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
  const t = useDuelText(),
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
    staleTime: 30_000,
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
        {t('随机性核验', 'Verify randomness')}{' '}
        <span>
          {p?.seed
            ? t('种子已公开', 'Seed disclosed')
            : p
              ? t('开局承诺已锁定', 'Opening commitment locked')
              : t('凭证', 'Proof')}
        </span>
      </summary>
      {query.isPending ? (
        <p>{t('正在读取凭证…', 'Loading proof…')}</p>
      ) : query.isError ? (
        <p role="status">
          {t('暂时无法读取凭证。', 'The proof is temporarily unavailable.')}{' '}
          <button type="button" className="btn btn-secondary" onClick={() => void refetch()}>
            {t('重试', 'Retry')}
          </button>
        </p>
      ) : p === null ? (
        <p>
          {t(
            '这局开始时尚未启用随机凭证，无法追溯核验。',
            'This game predates random proofs and cannot be verified retrospectively.',
          )}
        </p>
      ) : p ? (
        <>
          <p>
            {p.seed
              ? t(
                  '服务器已结束这局。可在本机核对种子承诺与全部已记录抽样，或下载凭证独立重放。',
                  'This game has ended. Check the seed commitment and all recorded draws locally, or download the proof for independent replay.',
                )
              : t(
                  '随机种子仍由服务器保密，整局结束后才公开。现在可保存承诺，结束后对照。',
                  'The server keeps the seed secret until the whole game ends. Save this commitment to compare it with the final proof.',
                )}
          </p>
          <dl>
            <dt>{t('开局承诺 · SHA-256', 'Opening commitment · SHA-256')}</dt>
            <dd>
              <code>{p.commitment}</code>
            </dd>
            {p.seed && (
              <>
                <dt>{t('公开种子', 'Disclosed seed')}</dt>
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
                className="btn btn-primary"
                disabled={verification === 'busy'}
                onClick={() => void verify()}
              >
                {verification === 'busy'
                  ? t('核验中…', 'Verifying…')
                  : t('本机核验', 'Verify locally')}
              </button>
            )}
            <button type="button" className="btn btn-secondary" onClick={() => save(p)}>
              {p.seed ? t('下载随机凭证', 'Download proof') : t('保存开局承诺', 'Save commitment')}
            </button>
          </div>
          <p role="status" className={`random-proof-status is-${verification}`}>
            {verification === 'passed'
              ? t(
                  `核验通过：承诺一致，${count} 次记录抽样均可复现。`,
                  `Verified: commitment matches; all ${count} recorded draws reproduce.`,
                )
              : verification === 'failed'
                ? t(
                    '核验失败：凭证不一致，或当前浏览器不支持安全核验。请下载后独立核对。',
                    'Verification failed: the proof is inconsistent or secure verification is unavailable in this browser. Download it for an independent check.',
                  )
                : null}
          </p>
          <p className="random-proof-note">
            {t(
              '凭证验证随机抽样的一致性；完整结果还需结合桌规与公开操作记录复演。',
              'This verifies the consistency of random draws. Replaying a complete result also requires the game rules and public action log.',
            )}
          </p>
        </>
      ) : null}
    </details>
  );
}
