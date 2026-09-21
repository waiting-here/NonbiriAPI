import { ApiError } from '@shared/query/http';
import { operationKey } from './api';
import type {
  DiscoveryEvidence,
  DiscoveryRef,
  DiscoverySelection,
  DiscoveryTarget,
} from './donationDiscovery';

export type DiscoveryStatus = 'succeeded' | 'failed' | 'ineligible' | 'conflict' | 'superseded';
export interface DiscoveryResult extends DiscoveryRef {
  status: DiscoveryStatus;
  count?: string;
  reason?: DiscoveryEvidence['safe_class'];
}
interface Transport {
  select: (donationID: string | null, cursor: string | null) => Promise<DiscoverySelection>;
  start: (item: DiscoveryRef, key: string) => Promise<DiscoveryEvidence>;
  read: (item: DiscoveryRef) => Promise<DiscoveryEvidence>;
  guard: () => void;
  progress: () => void;
  wait: () => Promise<void>;
}

// Selection and work are streamed one bounded page at a time. A lost response
// keeps the exact request key; resume never guesses that a send did not happen.
export class DonationDiscoveryRun {
  phase: 'running' | 'paused' | 'done' = 'paused';
  error: unknown = null;
  readonly counts = { selected: 0, processed: 0, succeeded: 0, failed: 0, skipped: 0 };
  readonly results: DiscoveryResult[] = [];
  current: DiscoveryRef | null = null;
  private queue: DiscoveryRef[] = [];
  private cursor: string | null = null;
  private selectionDone = false;
  private pending: { item: DiscoveryRef; key: string; revision?: string } | null = null;
  private stopped = false;
  private running = false;

  constructor(
    private readonly target: DiscoveryTarget,
    private readonly io: Transport,
  ) {
    if ('key_id' in target) {
      this.queue = [target];
      this.counts.selected = 1;
      this.selectionDone = true;
    }
  }
  stop() {
    this.stopped = true;
  }
  private finish(status: DiscoveryStatus, evidence?: DiscoveryEvidence) {
    if (!this.pending) return;
    const result = {
      ...this.pending.item,
      status,
      count: evidence?.count ?? undefined,
      reason: evidence?.safe_class,
    };
    if (this.results.length === 100) this.results.shift();
    this.results.push(result);
    this.counts.processed++;
    if (status === 'succeeded') this.counts.succeeded++;
    else if (status === 'failed') this.counts.failed++;
    else this.counts.skipped++;
    this.pending = null;
    this.current = null;
    this.io.progress();
  }
  async resume() {
    if (this.running || this.phase === 'done') return;
    this.running = true;
    this.stopped = false;
    this.error = null;
    this.phase = 'running';
    this.io.progress();
    let polls = 0;
    try {
      while (!this.stopped) {
        this.io.guard();
        if (!this.pending) {
          if (this.queue.length === 0) {
            if (this.selectionDone) {
              this.phase = 'done';
              break;
            }
            const page = await this.io.select(this.target.donation_id, this.cursor);
            this.io.guard();
            if (page.next_cursor !== null && page.next_cursor === this.cursor)
              throw new ApiError('invalid_response', 'Model selection did not advance.', 200);
            this.queue = page.items;
            this.counts.selected += page.items.length;
            this.cursor = page.next_cursor;
            this.selectionDone = this.cursor === null;
            this.io.progress();
            continue;
          }
          this.pending = { item: this.queue.shift()!, key: operationKey() };
          polls = 0;
        }
        this.current = this.pending.item;
        this.io.progress();
        try {
          if (!this.pending.revision) {
            const accepted = await this.io.start(this.pending.item, this.pending.key);
            this.io.guard();
            this.pending.revision = accepted.revision;
          }
          if (this.stopped) break;
          const evidence = await this.io.read(this.pending.item);
          this.io.guard();
          if (evidence.revision !== this.pending.revision) this.finish('superseded');
          else if (evidence.state === 'succeeded') this.finish('succeeded', evidence);
          else if (evidence.state === 'failed') this.finish('failed', evidence);
          else {
            if (++polls >= 60)
              throw new ApiError(
                'discovery_pending',
                'This model check is still pending. Resume to check its result.',
                0,
              );
            await this.io.wait();
          }
        } catch (error) {
          if (
            error instanceof ApiError &&
            (error.status === 404 || error.code === 'resource_locked')
          )
            this.finish('ineligible');
          else if (error instanceof ApiError && error.status === 409) this.finish('conflict');
          else if (error instanceof ApiError && error.status === 400) this.finish('failed');
          else throw error;
        }
      }
    } catch (error) {
      this.error = error;
    } finally {
      if (this.phase !== 'done') this.phase = 'paused';
      this.running = false;
      this.io.progress();
    }
  }
}
