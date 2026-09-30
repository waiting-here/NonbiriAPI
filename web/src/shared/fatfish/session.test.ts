import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import golden from './engine/golden.json';
import motionV2Golden from './engine/motion_v2_golden.json';
import motionV3Golden from './engine/motion_v3_golden.json';
import { seedCommit, seedCommitForVersion } from './engine/canonical';
import { decodeHex } from './engine/sha256';
import type { Level } from './engine/types';
import type { FatFishChallenge, FatFishChallengeTransport } from './api';
import { createFatFishSessionController, targetTick, targetTickWithWall } from './session';

const saved = new Map<string, unknown>();
const capabilities = new Map<string, string>();
const readOnly = new Set<string>();
let keyCount = 0;
let blockedWrite: Promise<void> | null = null;
let blockedOpen: Promise<void> | null = null;
let failWrite = false;
let closedDatabases = 0;
let writesWithoutLock = 0;
vi.mock('./storage', () => ({
  requireLocalPlaySupport: () => undefined,
  openFishDatabase: async () => {
    if (blockedOpen) await blockedOpen;
    return { close: () => { closedDatabases++; } };
  },
  pruneExpiredFishSessions: async () => undefined,
  freshKey: () => `key_${String(++keyCount).padStart(25, '0')}`,
  pendingCapability: () => ({ capability: 'ab'.repeat(32), key: 'pending_key_12345678901234567890' }),
  clearPendingCapability: () => undefined,
  isReadOnlyChallenge: (id: string) => readOnly.has(id),
  markReadOnlyChallenge: (id: string) => { readOnly.add(id); },
  clearReadOnlyChallenge: (id: string) => { readOnly.delete(id); },
  capabilityHash: () => 'cd'.repeat(32),
  bindCapability: (id: string, capability: string) => { capabilities.set(id, capability); },
  readCapability: (id: string) => {
    const value = capabilities.get(id);
    if (!value) throw new Error('missing capability');
    return value;
  },
  clearLocalCapability: (id: string) => { capabilities.delete(id); },
  writeFishSession: async (_db: unknown, record: { id: string }) => {
    if (blockedWrite) await blockedWrite;
    if (failWrite) throw new Error('IDB write failed');
    if (!locks.has(`nonbiri-fatfish:${record.id}`)) writesWithoutLock++;
    saved.set(record.id, structuredClone(record));
  },
  readFishSession: async (_db: unknown, id: string) => {
    const value = saved.get(id);
    if (!value) throw new Error('missing input log');
    return structuredClone(value);
  },
  deleteFishSession: async (_db: unknown, id: string) => { saved.delete(id); },
}));

const case0 = golden.cases[0];
const id = 'ffc_test';
const seed = case0.seed;
const hash = case0.result.content_hash;
const base: FatFishChallenge = {
  id, state: 'prepared', period_id: 'ffp_test', node_id: 'ffn_test', version_id: 'ffv_test',
  node_revision: '1', content_hash: hash, engine_version: 1, scoring_version: 1,
  seed_commit: seedCommit(id, 'ffp_test', 'ffn_test', hash, decodeHex(seed)),
  prepared_at_ms: 1000, prepare_until_ms: 61000, start_at_ms: null, end_at_ms: null,
  submit_until_ms: null, server_now_ms: 1000, ticket_price: '2',
};
const active: FatFishChallenge = {
  ...base, state: 'active', seed, level: case0.level as unknown as Level,
  start_at_ms: 1000, end_at_ms: 11000, submit_until_ms: 1811000,
};
const v2Case = motionV2Golden.cases.find((fixture) => fixture.name === 'minimum-and-capacity-v2')!;
const v2Base: FatFishChallenge = {
  ...base, engine_version: 2, content_hash: v2Case.result.content_hash,
  seed_commit: seedCommitForVersion(id, 'ffp_test', 'ffn_test', v2Case.result.content_hash, 2, 1, decodeHex(v2Case.seed)),
};
const v2Active: FatFishChallenge = {
  ...active, ...v2Base, state: 'active', seed: v2Case.seed, level: v2Case.level as unknown as Level,
  start_at_ms: 1000, end_at_ms: 11000, submit_until_ms: 1811000,
};
const v3Case = motionV3Golden.find((fixture) => fixture.name === 'minimum-and-capacity-v3')!;
const v3Base: FatFishChallenge = {
  ...base, engine_version: 3, content_hash: v3Case.result.content_hash,
  seed_commit: seedCommitForVersion(id, 'ffp_test', 'ffn_test', v3Case.result.content_hash, 3, 1, decodeHex(v3Case.seed)),
};
const v3Active: FatFishChallenge = {
  ...active, ...v3Base, state: 'active', seed: v3Case.seed, level: v3Case.level as unknown as Level,
  start_at_ms: 1000, end_at_ms: 11000, submit_until_ms: 1811000,
};
const locks = new Set<string>();
class Channel {
  onmessage: ((event: MessageEvent) => void) | null = null;
  postMessage(): void { /* The lock is the same-tab authority in these tests. */ }
  close(): void { /* noop */ }
}
beforeEach(() => {
  saved.clear(); capabilities.clear(); readOnly.clear(); locks.clear(); keyCount = 0;
  blockedWrite = null; failWrite = false;
  blockedOpen = null; closedDatabases = 0; writesWithoutLock = 0;
  vi.stubGlobal('BroadcastChannel', Channel);
  Object.defineProperty(navigator, 'locks', { configurable: true, value: {
    request: (name: string, _options: unknown, callback: (lock: { name: string } | null) => Promise<void>) => {
      if (locks.has(name)) return Promise.resolve(callback(null));
      locks.add(name);
      return Promise.resolve(callback({ name })).finally(() => locks.delete(name));
    },
  } });
  vi.spyOn(performance, 'now').mockReturnValue(0);
});
afterEach(() => vi.restoreAllMocks());

describe('fat fish tab session', () => {
  it('anchors ticks to server time and monotonic elapsed, including the exact start boundary', () => {
    expect(targetTick(4000, 1000, 10, 3009, 10)).toBe(0);
    expect(targetTick(4000, 1000, 10, 3010, 10)).toBe(0);
    expect(targetTick(4000, 1000, 10, 3027, 10)).toBe(1);
    expect(targetTick(4000, 1000, 10, 300000, 10)).toBe(600);
    expect(targetTickWithWall(4000, 1000, 10, 10, 10000, 14000, 0, 10000, 10)).toBe(60);
    expect(targetTickWithWall(4000, 1000, 10, 10, 10000, 9000, 2000, 10000, 10)).toBe(120);
  });
  it('catches up a sleeping tab by wall time and never rewinds after a backward wall change', async () => {
    const fixture = golden.cases[11];
    const date = vi.spyOn(Date, 'now').mockReturnValue(10000);
    const prepared: FatFishChallenge = { ...base, content_hash: fixture.result.content_hash,
      seed_commit: seedCommit(id, 'ffp_test', 'ffn_test', fixture.result.content_hash, decodeHex(fixture.seed)) };
    const playing: FatFishChallenge = { ...prepared, state: 'active', seed: fixture.seed,
      level: fixture.level as unknown as Level, start_at_ms: 1000, end_at_ms: 11000,
      submit_until_ms: 1811000 };
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => prepared, start: async () => playing, read: async () => playing,
      submit: async () => ({ ...playing, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    try {
      await session.prepare(); await session.start();
      date.mockReturnValue(12000);
      session.advance(128);
      expect(session.snapshot().state?.tick).toBe(120);
      date.mockReturnValue(9000);
      session.advance(128);
      expect(session.snapshot().state?.tick).toBe(120);
      await session.flush();
      expect((saved.get(id) as { anchor_elapsed_ms: number }).anchor_elapsed_ms).toBe(2000);
    } finally { session.dispose(); date.mockRestore(); }
  });
  it('keeps a copied live tab read-only and does not hand it control after the owner leaves', async () => {
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base, start: async () => active, read: async () => active,
      submit: async () => ({ ...active, state: 'verifying' }),
    };
    const original = createFatFishSessionController(transport);
    await original.prepare();
    const copy = createFatFishSessionController(transport);
    await expect(copy.recover(id)).rejects.toThrow(/another tab/i);
    original.dispose();
    await Promise.resolve();
    expect(copy.snapshot().canPlay).toBe(false);
    await expect(copy.start()).rejects.toThrow(/cannot control/i);
    copy.dispose();
    const reloadedCopy = createFatFishSessionController(transport);
    await expect(reloadedCopy.recover(id)).rejects.toThrow(/another tab/i);
    expect(reloadedCopy.snapshot().canPlay).toBe(false);
    reloadedCopy.dispose();
  });
  it('releases a Web Lock granted only after the waiting controller was disposed', async () => {
    let grant: (() => void) | null = null;
    let released = false;
    Object.defineProperty(navigator, 'locks', { configurable: true, value: {
      request: (name: string, _options: unknown, callback: (lock: { name: string }) => Promise<void>) =>
        new Promise<void>((resolve, reject) => {
          grant = () => { void callback({ name }).then(() => { released = true; resolve(); }, reject); };
        }),
    } });
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base, start: async () => active, read: async () => active,
      submit: async () => ({ ...active, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    const preparing = session.prepare();
    await vi.waitFor(() => expect(grant).not.toBeNull());
    session.dispose();
    (grant as (() => void) | null)?.();
    await expect(preparing).rejects.toThrow(/another tab/i);
    await vi.waitFor(() => expect(released).toBe(true));
    expect(saved.has(id)).toBe(false);
  });
  it('closes a database opened after unmount before retaining its handle', async () => {
    let releaseOpen: () => void = () => undefined;
    blockedOpen = new Promise<void>((resolve) => { releaseOpen = resolve; });
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base, start: async () => active, read: async () => active,
      submit: async () => ({ ...active, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    const preparing = session.prepare();
    session.dispose();
    releaseOpen();
    await expect(preparing).rejects.toThrow(/closed/i);
    expect(closedDatabases).toBe(1);
    expect(saved.has(id)).toBe(false);
  });
  it('holds the Web Lock through an initial prepared record write interrupted by disposal', async () => {
    let releaseWrite: () => void = () => undefined;
    blockedWrite = new Promise<void>((resolve) => { releaseWrite = resolve; });
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base, start: async () => active, read: async () => active,
      submit: async () => ({ ...active, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    const preparing = session.prepare();
    await vi.waitFor(() => expect(locks.size).toBe(1));
    session.dispose();
    expect(locks.size).toBe(1);
    releaseWrite();
    await expect(preparing).rejects.toThrow(/closed/i);
    await vi.waitFor(() => expect(locks.size).toBe(0));
    expect(writesWithoutLock).toBe(0);
  });
  it('stops multi-chunk catch-up when disposed between batches', async () => {
    const fixture = golden.cases[11];
    const prepared: FatFishChallenge = { ...base, content_hash: fixture.result.content_hash,
      seed_commit: seedCommit(id, 'ffp_test', 'ffn_test', fixture.result.content_hash, decodeHex(fixture.seed)) };
    const playing: FatFishChallenge = { ...prepared, state: 'active', seed: fixture.seed,
      level: fixture.level as unknown as Level, start_at_ms: 1000, end_at_ms: 11000,
      submit_until_ms: 1811000, server_now_ms: 4000 };
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => prepared, start: async () => playing, read: async () => playing,
      submit: async () => ({ ...playing, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    await session.prepare();
    let disposedAtBatch = false;
    session.subscribe(() => {
      if (session.snapshot().state?.tick === 128 && !disposedAtBatch) {
        disposedAtBatch = true;
        session.dispose();
      }
    });
    await expect(session.start()).rejects.toThrow(/closed/i);
    expect(disposedAtBatch).toBe(true);
  });
  it.each([
    [1, base, active, case0], [2, v2Base, v2Active, v2Case], [3, v3Base, v3Active, v3Case],
  ] as const)('recovers v%i and retains one submit key and payload after a lost response', async (version, prepared, playing, fixture) => {
    const attempts: { key: string; payload: unknown }[] = [];
    let read = playing;
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => prepared, start: async () => playing, read: async () => read,
      submit: async (_id, payload, key) => {
        attempts.push({ key, payload: structuredClone(payload) });
        if (attempts.length === 1) throw new Error('network failed');
        read = { ...playing, state: 'verifying' };
        return read;
      },
    };
    const first = createFatFishSessionController(transport);
    await first.prepare();
    await first.start();
    first.dispose();
    await vi.waitFor(() => expect(locks.size).toBe(0));
    const restored = createFatFishSessionController(transport);
    await restored.recover(id);
    expect(restored.snapshot().state?.tick).toBe(0);
    vi.spyOn(performance, 'now').mockReturnValue(200);
    restored.advance(64);
    expect(restored.snapshot().provisional).toMatchObject({
      engine_version: version, scoring_version: 1, content_hash: fixture.result.content_hash,
      final_state_hash: fixture.result.final_state_hash,
    });
    expect(restored.snapshot().challenge?.level?.engine_version).toBe(version);
    await expect(restored.submit()).rejects.toThrow(/network failed/);
    expect(saved.has(id)).toBe(true);
    await restored.submit();
    expect(attempts).toHaveLength(2);
    expect(attempts[0]).toEqual(attempts[1]);
    expect(restored.snapshot().phase).toBe('verifying');
    read = { ...playing, state: 'settled_pass' };
    await restored.poll();
    expect(saved.has(id)).toBe(false);
    expect(capabilities.has(id)).toBe(false);
    restored.dispose();
  });
  it.each([[4, 1], [1, 2]])('rejects an unsupported prepared engine/scoring version %i/%i before storing a challenge', async (engineVersion, scoringVersion) => {
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => ({ ...base, engine_version: engineVersion, scoring_version: scoringVersion }),
      start: async () => active, read: async () => active, submit: async () => active,
    };
    const session = createFatFishSessionController(transport);
    try {
      await expect(session.prepare()).rejects.toThrow(/unsupported rules version/);
      expect(saved.size).toBe(0);
      expect(locks.size).toBe(0);
    } finally { session.dispose(); }
  });
  for (const action of ['start', 'recover'] as const) {
    it.each([
      { name: 'v2 metadata with a v1 level', prepared: base, playing: active, invalid: { ...active, engine_version: 2 } },
      { name: 'v1 metadata with a v2 level', prepared: base, playing: active, invalid: { ...active, level: v2Active.level } },
      { name: 'a version change after preparation', prepared: base, playing: active,
        invalid: { ...active, engine_version: 2, level: v2Active.level } },
      { name: 'scoring metadata with a mismatched level', prepared: base, playing: active,
        invalid: { ...active, level: { ...active.level!, scoring_version: 2 } as unknown as Level } },
      { name: 'unknown engine version', prepared: base, playing: active, invalid: { ...active, engine_version: 4 } },
      { name: 'unknown scoring version', prepared: base, playing: active, invalid: { ...active, scoring_version: 2 } },
      { name: 'v2 using a v1 seed commitment', prepared: v2Base, playing: v2Active,
        invalid: { ...v2Active, seed_commit: seedCommit(id, 'ffp_test', 'ffn_test', v2Base.content_hash, decodeHex(v2Case.seed)) } },
    ])(`rejects $name on ${action} and keeps the persisted input log`, async ({ prepared, playing, invalid }) => {
      let current: FatFishChallenge = playing;
      const transport: FatFishChallengeTransport = {
        prepareScope: 'user:ffp_test:ffn_test:1', prepare: async () => prepared,
        start: async () => current, read: async () => current, submit: async () => current,
      };
      const first = createFatFishSessionController(transport);
      let restored: ReturnType<typeof createFatFishSessionController> | null = null;
      try {
        await first.prepare();
        if (action === 'recover') {
          await first.start();
          first.dispose();
          await vi.waitFor(() => expect(locks.size).toBe(0));
          restored = createFatFishSessionController(transport);
        }
        const inputLog = structuredClone((saved.get(id) as { inputs: unknown[] }).inputs);
        current = invalid;
        const session = restored ?? first;
        await expect(action === 'recover' ? session.recover(id) : session.start()).rejects.toThrow(/version|commitment/);
        expect(session.snapshot().state).toBeNull();
        expect((saved.get(id) as { inputs: unknown[] }).inputs).toEqual(inputLog);
        expect(playing.level?.engine_version).toBe(prepared.engine_version);
      } finally { first.dispose(); restored?.dispose(); }
    });
  }
  it('reposts only the persisted accepted digest while verifying and clears on the terminal receipt', async () => {
    const attempts: { key: string; payload: unknown }[] = [];
    let status: FatFishChallenge = { ...active, state: 'verifying' };
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base,
      start: async () => active,
      read: async () => status,
      submit: async (_id, payload, key) => {
        attempts.push({ key, payload: structuredClone(payload) });
        return status;
      },
    };
    const session = createFatFishSessionController(transport);
    await session.prepare();
    await session.start();
    vi.spyOn(performance, 'now').mockReturnValue(200);
    session.advance(64);
    await session.submit();
    await session.poll();
    expect(attempts).toHaveLength(2);
    expect(attempts[0]).toEqual(attempts[1]);
    await session.poll();
    expect(attempts).toHaveLength(2);
    vi.spyOn(performance, 'now').mockReturnValue(10201);
    status = { ...active, state: 'settled_pass' };
    await session.poll();
    expect(attempts).toHaveLength(2);
    expect(saved.has(id)).toBe(false);
    session.dispose();
  });
  it('does not consume a placed input until its tick batch is durable, then resumes the real engine', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(10000);
    const fixture = golden.cases[5];
    const toolHash = fixture.result.content_hash;
    const prepared: FatFishChallenge = { ...base, content_hash: toolHash,
      seed_commit: seedCommit(id, 'ffp_test', 'ffn_test', toolHash, decodeHex(fixture.seed)) };
    const playing: FatFishChallenge = { ...prepared, state: 'active', seed: fixture.seed,
      level: fixture.level as unknown as Level, start_at_ms: 1000, end_at_ms: 11000,
      submit_until_ms: 1811000 };
    let authoritative = playing;
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => prepared, start: async () => playing, read: async () => authoritative,
      submit: async () => ({ ...playing, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    await session.prepare(); await session.start();
    const tool = fixture.level.tools?.[0];
    if (!tool) throw new Error('The committed fixture must include a movable tool.');
    let releaseWrite: () => void = () => undefined;
    blockedWrite = new Promise<void>((resolve) => { releaseWrite = resolve; });
    await session.place(tool.id, 64, 64);
    vi.spyOn(performance, 'now').mockReturnValue(200);
    session.advance(64);
    expect(session.snapshot().state?.tick).toBe(0);
    expect((saved.get(id) as { inputs: unknown[] }).inputs).toHaveLength(0);
    releaseWrite();
    await session.flush();
    session.advance(64);
    expect(session.snapshot().state?.tick).toBeGreaterThan(0);
    expect((saved.get(id) as { inputs: unknown[] }).inputs).toHaveLength(1);
    const applied = session.snapshot().state;
    session.dispose();
    await vi.waitFor(() => expect(locks.size).toBe(0));
    authoritative = { ...playing, server_now_ms: 1200 };
    const reloaded = createFatFishSessionController(transport);
    await reloaded.recover(id);
    expect(reloaded.snapshot().state).toEqual(applied);
    reloaded.dispose();
  });
  it('holds the challenge lock until an asynchronous dispose has drained the input write', async () => {
    const fixture = golden.cases[5];
    const prepared: FatFishChallenge = { ...base, content_hash: fixture.result.content_hash,
      seed_commit: seedCommit(id, 'ffp_test', 'ffn_test', fixture.result.content_hash, decodeHex(fixture.seed)) };
    const playing: FatFishChallenge = { ...prepared, state: 'active', seed: fixture.seed,
      level: fixture.level as unknown as Level, start_at_ms: 1000, end_at_ms: 11000,
      submit_until_ms: 1811000 };
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => prepared, start: async () => playing, read: async () => playing,
      submit: async () => ({ ...playing, state: 'verifying' }),
    };
    const tool = fixture.level.tools?.[0];
    if (!tool) throw new Error('The committed fixture must include a movable tool.');
    const session = createFatFishSessionController(transport);
    await session.prepare(); await session.start();
    let releaseWrite: () => void = () => undefined;
    blockedWrite = new Promise<void>((resolve) => { releaseWrite = resolve; });
    await session.place(tool.id, 64, 64);
    vi.spyOn(performance, 'now').mockReturnValue(200);
    session.advance(64);
    session.dispose();
    expect(locks.size).toBe(1);
    expect((saved.get(id) as { inputs: unknown[] }).inputs).toHaveLength(0);
    releaseWrite();
    await vi.waitFor(() => expect(locks.size).toBe(0));
    expect((saved.get(id) as { inputs: unknown[] }).inputs).toHaveLength(1);
  });
  it('does not release the lock before a second queued flush writes', async () => {
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base, start: async () => active, read: async () => active,
      submit: async () => ({ ...active, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    await session.prepare(); await session.start();
    let releaseWrite: () => void = () => undefined;
    blockedWrite = new Promise<void>((resolve) => { releaseWrite = resolve; });
    const first = session.flush();
    const second = session.flush();
    session.dispose();
    expect(locks.size).toBe(1);
    releaseWrite();
    await Promise.all([first, second]);
    await vi.waitFor(() => expect(locks.size).toBe(0));
    expect(writesWithoutLock).toBe(0);
  });
  it('clears retained input and capability after terminal receipt even when the prior write failed', async () => {
    let status: FatFishChallenge = active;
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base, start: async () => active, read: async () => status,
      submit: async () => ({ ...active, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    await session.prepare(); await session.start();
    failWrite = true;
    await expect(session.flush()).rejects.toThrow(/IDB write failed/);
    expect(session.snapshot().phase).toBe('error');
    status = { ...active, state: 'expired' };
    await session.poll();
    expect(saved.has(id)).toBe(false);
    expect(capabilities.has(id)).toBe(false);
    session.dispose();
  });
  it('retries a failed local write without leaving the write chain poisoned', async () => {
    let status: FatFishChallenge = active;
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base, start: async () => active, read: async () => status,
      submit: async () => ({ ...active, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    await session.prepare(); await session.start();
    failWrite = true;
    await expect(session.flush()).rejects.toThrow(/IDB write failed/);
    failWrite = false;
    await session.flush();
    expect(saved.has(id)).toBe(true);
    status = { ...active, state: 'expired' };
    await session.poll();
    expect(saved.has(id)).toBe(false);
    expect(capabilities.has(id)).toBe(false);
    session.dispose();
  });
  it('serializes polls with abandonment and never lets a late active read replace a terminal receipt', async () => {
    let resolveRead: (value: FatFishChallenge) => void = () => undefined;
    let abandonCalls = 0;
    let reads = 0;
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base, start: async () => active,
      read: () => { reads++; return new Promise<FatFishChallenge>((resolve) => { resolveRead = resolve; }); },
      submit: async () => ({ ...active, state: 'verifying' }),
      abandon: async () => { abandonCalls++; return { ...active, state: 'abandoned' }; },
    };
    const session = createFatFishSessionController(transport);
    await session.prepare(); await session.start();
    const polling = session.poll();
    const abandoning = session.abandon();
    await vi.waitFor(() => expect(reads).toBe(1));
    expect(abandonCalls).toBe(0);
    resolveRead(active);
    await polling;
    await abandoning;
    expect(abandonCalls).toBe(1);
    expect(session.snapshot().challenge?.state).toBe('abandoned');
    expect(saved.has(id)).toBe(false);
    session.dispose();
  });
  it('does not apply a read completed after disposal or re-read after terminal cleanup', async () => {
    let resolveRead: (value: FatFishChallenge) => void = () => undefined;
    let reads = 0;
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base, start: async () => active,
      read: () => { reads++; return new Promise<FatFishChallenge>((resolve) => { resolveRead = resolve; }); },
      submit: async () => ({ ...active, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    await session.prepare(); await session.start();
    const first = session.poll();
    const second = session.poll();
    await vi.waitFor(() => expect(reads).toBe(1));
    resolveRead({ ...active, state: 'settled_pass' });
    await Promise.all([first, second]);
    expect(reads).toBe(1);
    expect(session.snapshot().challenge?.state).toBe('settled_pass');
    expect(saved.has(id)).toBe(false);
    session.dispose();
    await vi.waitFor(() => expect(locks.size).toBe(0));

    const another = createFatFishSessionController(transport);
    await another.prepare(); await another.start();
    const pending = another.poll();
    await vi.waitFor(() => expect(reads).toBe(2));
    another.dispose();
    resolveRead({ ...active, state: 'settled_pass' });
    await pending;
    expect(another.snapshot().challenge?.state).toBe('active');
    expect(saved.has(id)).toBe(true);
  });
  it('blocks a new flush while terminal cleanup waits for the previous write', async () => {
    let status: FatFishChallenge = active;
    const transport: FatFishChallengeTransport = {
      prepareScope: 'user:ffp_test:ffn_test:1',
      prepare: async () => base, start: async () => active, read: async () => status,
      submit: async () => ({ ...active, state: 'verifying' }),
    };
    const session = createFatFishSessionController(transport);
    await session.prepare(); await session.start();
    let releaseWrite: () => void = () => undefined;
    blockedWrite = new Promise<void>((resolve) => { releaseWrite = resolve; });
    const writing = session.flush();
    status = { ...active, state: 'expired' };
    const clearing = session.poll();
    await vi.waitFor(() => expect(session.snapshot().phase).toBe('terminal'));
    await expect(session.flush()).rejects.toThrow(/cannot control/i);
    releaseWrite();
    await Promise.all([writing, clearing]);
    expect(saved.has(id)).toBe(false);
    expect(capabilities.has(id)).toBe(false);
    expect(writesWithoutLock).toBe(0);
    session.dispose();
  });
});
