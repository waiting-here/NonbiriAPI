import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { captureStationSession, stationSessionMatches } from '@shared/charityManagement';
import { ErrorState } from '@shared/components/States';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { useFatFishText } from './copy';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { FatFishPlayer } from '@shared/fatfish/FatFishPlayer';
import type { FatFishChallenge } from '@shared/fatfish/api';
import { createFatFishSessionController, type FatFishSessionController, type FatFishPlayerSnapshot } from '@shared/fatfish/session';
import { localPlaySupportError } from '@shared/fatfish/storage';
import { abandonPlaytest, adminPlaytestTransport, currentPlaytest, type CurrentPlaytest } from './playtestApi';
export { adminPlaytestTransport } from './playtestApi';

interface Target { id: string; content_hash: string }
export interface PlaytestWorkspace { prepare(version: Target): Promise<void>; reset(): void }
const finished = (state: FatFishChallenge['state']) =>
  ['settled_pass', 'settled_fail', 'abandoned', 'expired', 'cancelled_refunded'].includes(state);

export const PlaytestPane = forwardRef<PlaytestWorkspace, { versionID?: string; contentHash?: string }>(
function PlaytestPane({ versionID, contentHash }, ref) {
  const text = useFatFishText(), client = useQueryClient();
  const supportError = localPlaySupportError();
  const administrator = useQuery({ queryKey: ['admin', 'session'], enabled: false }).data;
  const session = useRef<FatFishSessionController | null>(null);
  const live = useRef(true), running = useRef(false);
  const [controller, setController] = useState<FatFishSessionController | null>(null);
  const [snapshot, setSnapshot] = useState<FatFishPlayerSnapshot | null>(null);
  const [current, setCurrent] = useState<CurrentPlaytest | null>(null);
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(null);
  const [confirmation, setConfirmation] = useState(false);
  const pending = useRef<{ target: Target; assertCurrent(): void; resolve(): void; reject(error: unknown): void } | null>(null);
  const reset = () => {
    session.current?.dispose(); session.current = null;
    pending.current?.reject(new Error('The playtest operation has closed.')); pending.current = null;
    setController(null); setSnapshot(null); setCurrent(null); setConfirmation(false);
  };
  const preparation = useRetainedOperation((target: Target, _key, context) => request(target, context.assertCurrent),
    () => undefined, ['admin', 'fatfish'], { clearSecrets: reset });
  const abandonment = useRetainedOperation((input: { id: string; revision: string }, key) =>
    abandonPlaytest(input.id, input.revision, key), () => undefined, ['admin', 'fatfish']);

  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false; session.current?.dispose(); session.current = null;
      pending.current?.reject(new Error('The playtest workspace has closed.')); pending.current = null;
    };
  }, []);
  useEffect(() => controller?.subscribe(() => {
    if (live.current) setSnapshot(controller.snapshot());
  }), [controller]);
  const attach = (candidate: FatFishSessionController) => {
    session.current = candidate; setController(candidate); setSnapshot(candidate.snapshot());
  };
  const verified = (view: FatFishChallenge) => {
    if (!finished(view.state)) return;
    setCurrent(null);
    void client.invalidateQueries({ queryKey: ['fatfish', 'playtests', view.version_id] });
  };
  const recover = async (metadata: CurrentPlaytest, assertCurrent: () => void) => {
    assertCurrent();
    if (session.current?.snapshot().challenge?.id === metadata.id) return;
    const candidate = createFatFishSessionController(adminPlaytestTransport(metadata.version_id));
    try {
      const view = await candidate.recover(metadata.id);
      assertCurrent();
      if (!live.current) { candidate.dispose(); return; }
      if (view.version_id !== metadata.version_id || view.content_hash !== metadata.content_hash || view.ticket_price !== '0')
        throw new Error('Recovered playtest does not match its immutable version.');
      session.current?.dispose(); attach(candidate); verified(view);
    } catch (cause) { candidate.dispose(); throw cause; }
  };
  useEffect(() => {
    let active = true;
    if (!administrator) return;
    const identity = captureStationSession(client, 'admin');
    const unsubscribe = client.getQueryCache().subscribe(() => {
      if (!stationSessionMatches(client, 'admin', identity)) { active = false; reset(); }
    });
    void currentPlaytest().then(async (metadata) => {
      if (!active || !live.current || running.current || session.current) return;
      setCurrent(metadata);
      if (metadata && !supportError) await recover(metadata, () => {
        if (!active || !stationSessionMatches(client, 'admin', identity)) throw new Error('The administrator session has changed.');
      });
    }).catch((cause: unknown) => { if (active && live.current) setError(cause); });
    return () => { active = false; unsubscribe(); };
    // The existing controller owns recovery and tab capability.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [supportError, administrator]);
  const prepareNew = async (target: Target, assertCurrent: () => void = () => {}) => {
    assertCurrent();
    session.current?.dispose(); session.current = null; setController(null); setSnapshot(null);
    const candidate = createFatFishSessionController(adminPlaytestTransport(target.id));
    try {
      const view = await candidate.prepare();
      assertCurrent();
      if (!live.current) { candidate.dispose(); throw new Error('The playtest workspace has closed.'); }
      if (view.version_id !== target.id || view.content_hash !== target.content_hash || view.ticket_price !== '0')
        throw new Error('Prepared playtest does not match its immutable version.');
      attach(candidate); setCurrent(null);
    } catch (cause) { candidate.dispose(); throw cause; }
  };
  const request = async (target: Target, assertCurrent: () => void) => {
    assertCurrent();
    if (running.current || pending.current) throw new Error('A playtest operation is already pending.');
    if (supportError) throw new Error(supportError);
    running.current = true; setBusy(true); setError(null);
    try {
      const metadata = await currentPlaytest();
      if (!live.current) throw new Error('The playtest workspace has closed.');
      assertCurrent(); setCurrent(metadata);
      if (metadata) {
        if (metadata.version_id === target.id && session.current?.snapshot().phase !== 'read_only') {
          try { await recover(metadata, assertCurrent); assertCurrent(); return; } catch (cause) { setError(cause); }
        }
        await new Promise<void>((resolve, reject) => {
          pending.current = { target, assertCurrent, resolve, reject }; setConfirmation(true);
        });
      } else await prepareNew(target, assertCurrent);
    } catch (cause) { if (live.current) setError(cause); throw cause; }
    finally { running.current = false; if (live.current) setBusy(false); }
  };
  useImperativeHandle(ref, () => ({ prepare: (target) => preparation.mutateAsync(target), reset }));
  const abandonAndSwitch = async () => {
    if (!current || abandonment.isPending) return;
    try {
      const input = abandonment.variables?.id === current.id ? abandonment.variables : { id: current.id, revision: current.revision };
      const view = await abandonment.mutateAsync(input);
      if (!live.current) return;
      if (!finished(view.state)) throw new Error('The old playtest has not ended.');
      session.current?.dispose(); session.current = null; setController(null); setSnapshot(null);
      setCurrent(null); setConfirmation(false); verified(view);
      const next = pending.current; pending.current = null;
      if (next) {
        try { await prepareNew(next.target, next.assertCurrent); next.resolve(); }
        catch (cause) { next.reject(cause); throw cause; }
      }
    } catch (cause) { if (live.current) setError(cause); }
  };
  const inspection = useRetainedOperation(async (action: 'continue' | 'abandon', _key, context) => {
    if (running.current) return;
    running.current = true;
    try {
      const metadata = await currentPlaytest();
      context.commit(() => { setCurrent(metadata); setError(null); });
      if (metadata && action === 'continue') await recover(metadata, context.assertCurrent);
      else if (metadata) context.commit(() => setConfirmation(true));
    } finally { running.current = false; }
  }, () => undefined, ['admin', 'fatfish'], { clearSecrets: reset });
  const working = busy || inspection.isPending || abandonment.isPending;
  const start = async () => {
    if (running.current || !session.current || snapshot?.challenge?.state !== 'prepared') return;
    running.current = true; setBusy(true); setError(null);
    try { await session.current.start(); }
    catch (cause) { if (live.current) setError(cause); }
    finally { running.current = false; if (live.current) setBusy(false); }
  };
  return <section className="fatfish-playtest" aria-label={text('administrator_playtest')}>
    <h3>{text('no_charge_playtest')}</h3>
    <p>{text('playtesting_verifies_a_fixed_version_without_charges_or_rewards_start_')}</p>
    {supportError ? <p role="alert">{supportError}</p> : null}
    {current ? <p>{current.level_title} · {text('version')} {current.version_number ?? '—'} · {current.state}</p> : null}
    {versionID && contentHash ? <button type="button" disabled={working || !!supportError} onClick={() => void preparation.mutateAsync({ id: versionID, content_hash: contentHash }).catch(() => undefined)}>
      {text('prepare_playtest')}</button> : null}
    {current && !controller ? <button type="button" disabled={working} onClick={() => {
      void inspection.mutateAsync('continue').catch(() => undefined);
    }}>{text('continue_current_playtest')}</button> : null}
    {current || (snapshot?.challenge && !finished(snapshot.challenge.state)) ? <button type="button" disabled={working} onClick={() => {
      void inspection.mutateAsync('abandon').catch(() => undefined);
    }}>{text('abandon_playtest')}</button> : null}
    {snapshot?.challenge?.state === 'prepared' ? <button type="button" disabled={working || snapshot.phase === 'read_only'} onClick={() => void start()}>{text('start_playtest')}</button> : null}
    {controller ? <FatFishPlayer controller={controller} mode="playtest" onTerminal={verified} /> : null}
    {snapshot?.phase === 'verifying' ? <p role="status">{text('verification_is_in_progress_wait_for_this_result_before_starting_anoth')}</p> : null}
    {snapshot?.challenge ? <details><summary>{text('version_details')}</summary><code>{snapshot.challenge.content_hash}</code></details> : null}
    {error || inspection.error ? <ErrorState error={error ?? inspection.error} /> : null}
    <ConfirmDialog open={confirmation} title={text('abandon_the_current_playtest')}
      description={text('this_playtest_will_end_without_a_pass_proof_you_can_then_prepare_anoth')}
      confirmLabel={text('abandon_playtest')} busy={abandonment.isPending} danger
      onConfirm={() => void abandonAndSwitch()} onCancel={() => {
        setConfirmation(false);
        pending.current?.reject(new Error('The current playtest was kept.')); pending.current = null;
      }} />
  </section>;
});
