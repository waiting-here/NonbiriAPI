import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ErrorState } from '@shared/components/States';
import { useActivityText } from '@shared/limitedactivities/copy';
import { idempotentOptions } from '@shared/operations/api';
import { apiFetch } from '@shared/query/http';
import { FatFishPlayer } from '@shared/fatfish/FatFishPlayer';
import type { FatFishChallenge, FatFishChallengeTransport, FatFishSubmission } from '@shared/fatfish/api';
import { createFatFishSessionController, type FatFishSessionController, type FatFishPlayerSnapshot } from '@shared/fatfish/session';
import { localPlaySupportError } from '@shared/fatfish/storage';

const base = '/admin/api/limited-activities/fat-fish';
const segment = (value: string) => encodeURIComponent(value);
const finished = (state: FatFishChallenge['state']) =>
  ['settled_pass', 'settled_fail', 'abandoned', 'expired', 'cancelled_refunded'].includes(state);
const rememberedID = (versionID: string) => `nonbiri-fatfish-admin-playtest:${versionID}`;
function readRememberedID(key: string): string | null {
  try { return sessionStorage.getItem(key); }
  catch { return null; }
}

export function adminPlaytestTransport(versionID: string): FatFishChallengeTransport {
  const path = (id: string) => `${base}/playtests/${segment(id)}`;
  return {
    prepareScope: `playtest:${versionID}`,
    prepare: (tabCapabilityHash: string, key: string) => apiFetch<FatFishChallenge>(`${base}/playtests`, idempotentOptions(key, {
      method: 'POST', json: { version_id: versionID, tab_capability_hash: tabCapabilityHash },
    })),
    start: (id: string, tabCapability: string, key: string) => apiFetch<FatFishChallenge>(`${path(id)}/start`, idempotentOptions(key, {
      method: 'POST', json: { tab_capability: tabCapability },
    })),
    read: (id: string, tabCapability: string) => apiFetch<FatFishChallenge>(path(id), {
      headers: { 'X-FatFish-Tab-Capability': tabCapability },
    }),
    submit: (id: string, payload: FatFishSubmission, key: string) => apiFetch<FatFishChallenge>(`${path(id)}/submit`, idempotentOptions(key, {
      method: 'POST', json: payload,
    })),
  };
}

export function PlaytestPane({ versionID, contentHash }: { versionID: string; contentHash: string }) {
  const t = useActivityText(), client = useQueryClient();
  const supportError = localPlaySupportError();
  const key = rememberedID(versionID);
  const [controller, setController] = useState<FatFishSessionController | null>(null);
  const controllerRef = useRef<FatFishSessionController | null>(null);
  const [recoveryAttempt, setRecoveryAttempt] = useState(0);
  const [snapshot, setSnapshot] = useState<FatFishPlayerSnapshot | null>(null);
  const [busy, setBusy] = useState(() => !!readRememberedID(key)), [notice, setNotice] = useState('');
  const [error, setError] = useState<unknown>(null);
  useEffect(() => () => { controllerRef.current?.dispose(); controllerRef.current = null; }, []);
  useEffect(() => controller?.subscribe(() => setSnapshot(controller.snapshot())), [controller]);
  const verified = (view: FatFishChallenge) => {
    if (!finished(view.state)) return;
    sessionStorage.removeItem(key);
    void client.invalidateQueries({ queryKey: ['fatfish', 'playtests', versionID] });
  };
  useEffect(() => {
    if (supportError || controllerRef.current) return;
    const id = readRememberedID(key);
    if (!id) return;
    let live = true;
    let session: FatFishSessionController | null = null;
    const timer = window.setTimeout(() => {
      if (!live) return;
      const candidate = createFatFishSessionController(adminPlaytestTransport(versionID));
      session = candidate;
      void candidate.recover(id).then((view: FatFishChallenge) => {
        if (!live) { candidate.dispose(); return; }
        if (view.version_id !== versionID || view.content_hash !== contentHash || view.ticket_price !== '0')
          throw new Error('Recovered playtest does not match the selected version or zero-charge policy.');
        controllerRef.current = candidate; setSnapshot(candidate.snapshot()); setController(candidate);
        verified(view);
        setNotice(t('已恢复本标签页的试玩。', 'Playtest resumed in this tab.'));
      }).catch((cause: unknown) => {
        candidate.dispose();
        if (live) setError(cause);
      }).finally(() => { if (live) setBusy(false); });
    }, 0);
    return () => { live = false; clearTimeout(timer); if (session && controllerRef.current !== session) session.dispose(); };
    // Recovery repeats only after an explicit retry; the controller owns the retained capability.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [versionID, supportError, recoveryAttempt]);
  const prepare = async () => {
    if (busy || supportError || controllerRef.current) return;
    setBusy(true); setError(null); setNotice('');
    const session = createFatFishSessionController(adminPlaytestTransport(versionID));
    try {
      const view = await session.prepare();
      sessionStorage.setItem(key, view.id);
      if (view.content_hash !== contentHash || view.version_id !== versionID || view.ticket_price !== '0')
        throw new Error('Prepared playtest does not match the selected version or zero-charge policy.');
      controllerRef.current = session; setSnapshot(session.snapshot()); setController(session);
      setNotice(t('准备完成。试玩无门票费用和奖励；确认后开始计时。', 'Ready. This playtest has no ticket charge or reward; start when ready.'));
    } catch (cause) { session.dispose(); setError(cause); }
    finally { setBusy(false); }
  };
  const start = async () => {
    if (!controller || busy || snapshot?.phase === 'read_only' || snapshot?.challenge?.state !== 'prepared' || snapshot.challenge.ticket_price !== '0') return;
    setBusy(true); setError(null);
    try { await controller.start(); setNotice(t('试玩已经开始。', 'Playtest started.')); }
    catch (cause) { setError(cause); }
    finally { setBusy(false); }
  };
  const newPlaytest = () => {
    if (!snapshot?.challenge || !finished(snapshot.challenge.state)) return;
    controllerRef.current?.dispose(); controllerRef.current = null;
    setController(null); setSnapshot(null); setError(null); setNotice('');
  };
  const storedID = supportError ? null : readRememberedID(key);
  return <section className="fatfish-playtest" aria-label={t('管理员试玩', 'Administrator playtest')}>
    <h4>{t('无消费试玩', 'No-charge playtest')}</h4>
    <p>{t('试玩只核验不可变版本，不扣通用积分，不发奖励，也不开放正式期次。',
      'Playtesting verifies the immutable version. It charges no credits, grants no rewards, and never opens a formal period.')}</p>
    {supportError ? <p role="alert">{supportError}</p> : null}
    {!controller ? <button type="button" disabled={busy || !!supportError} onClick={() => {
      if (storedID) { setError(null); setBusy(true); setRecoveryAttempt((attempt) => attempt + 1); }
      else void prepare();
    }}>
      {busy ? t('恢复试玩…', 'Resuming playtest…') : storedID ? t('重试恢复本标签页试玩', 'Retry this tab’s playtest recovery') : t('准备试玩', 'Prepare playtest')}
    </button> : null}
    {controller && snapshot?.challenge?.state === 'prepared' ? <button type="button" disabled={busy || snapshot.phase === 'read_only'} onClick={() => void start()}>
      {t('开始试玩', 'Start playtest')}
    </button> : null}
    {controller && snapshot?.challenge ? <>
      <p>{t('核验版本哈希', 'Verified version hash')}: <code>{snapshot.challenge.content_hash}</code></p>
      <FatFishPlayer controller={controller} mode="playtest" onTerminal={verified} />
      {snapshot.phase === 'verifying' ? <p role="status">{t('服务端正在复算试玩输入。', 'The server is replaying the playtest inputs.')}</p> : null}
      {finished(snapshot.challenge.state) ? <>
        <p role="status">{snapshot.challenge.result?.passed && snapshot.challenge.result.stars >= 1
          ? t('至少一星的服务端核验证据已生成。', 'Server-verified one-star proof is available.')
          : t('本次没有产生一星通过证据。', 'This playtest did not produce a one-star pass.')}</p>
        <button type="button" onClick={newPlaytest}>{t('再次试玩', 'Play another playtest')}</button>
      </> : null}
    </> : null}
    {notice ? <p role="status">{notice}</p> : null}
    {error ? <ErrorState error={error} /> : null}
  </section>;
}
