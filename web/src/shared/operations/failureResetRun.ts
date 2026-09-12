import { operationKey } from './api';
import type {
  FailureResetBatch,
  FailureResetRef,
  FailureResetTarget,
  FailureSelection,
  FailureSelectionPage,
  FailureResetStatus,
} from './failureReset';

interface Entry extends FailureResetRef {
  status?: FailureResetStatus;
}
interface Transport {
  select: (selection: FailureSelection, cursor: string | null) => Promise<FailureSelectionPage>;
  reset: (items: FailureResetRef[], key: string) => Promise<FailureResetBatch>;
  guard: () => void;
  progress: () => void;
  yield: () => Promise<void>;
}
export class FailureResetRun {
  phase: 'selecting' | 'resetting' | 'paused' | 'done' = 'selecting';
  error: unknown = null;
  readonly entries = new Map<string, Entry>();
  private readonly donations = new Map<string, { revision: string; conflict: boolean }>();
  private targetIndex = 0;
  private cursor: string | null = null;
  private selectionDone = false;
  private queue: Entry[] = [];
  private offset = 0;
  private pending: { items: FailureResetRef[]; key: string } | null = null;
  private stopped = false;
  private running = false;
  private processed = 0;
  private resetCount = 0;
  private skipped = 0;
  constructor(
    private readonly targets: FailureResetTarget[],
    private readonly io: Transport,
  ) {}
  stop() {
    this.stopped = true;
  }
  get counts() {
    return {
      selected: this.entries.size,
      processed: this.processed,
      reset: this.resetCount,
      skipped: this.skipped,
    };
  }
  private mark(entry: Entry, status: FailureResetStatus) {
    if (entry.status) return;
    entry.status = status;
    this.processed++;
    if (status === 'reset') this.resetCount++;
    else this.skipped++;
  }
  private add(item: FailureResetRef) {
    const donation = this.donations.get(item.donation_id);
    if (donation && donation.revision !== item.expected_revision) donation.conflict = true;
    else if (!donation)
      this.donations.set(item.donation_id, { revision: item.expected_revision, conflict: false });
    if (!this.entries.has(item.key_id)) this.entries.set(item.key_id, { ...item });
  }
  async resume() {
    if (this.running || this.phase === 'done') return;
    this.running = true;
    this.stopped = false;
    this.error = null;
    try {
      this.io.guard();
      this.phase = this.selectionDone ? 'resetting' : 'selecting';
      this.io.progress();
      while (!this.selectionDone && !this.stopped) {
        const target = this.targets[this.targetIndex];
        if (!target) {
          this.selectionDone = true;
          this.queue = Array.from(this.entries.values());
          this.phase = 'resetting';
          for (const entry of this.queue)
            if (this.donations.get(entry.donation_id)!.conflict) this.mark(entry, 'conflict');
          this.io.progress();
          await this.io.yield();
          break;
        }
        if ('view' in target) {
          this.io.guard();
          const page = await this.io.select(target, this.cursor);
          this.io.guard();
          for (const item of page.items) this.add(item);
          this.cursor = page.next_cursor;
          if (this.cursor === null) this.targetIndex++;
        } else {
          for (let count = 0; count < 100; count++) {
            const next = this.targets[this.targetIndex];
            if (!next || 'view' in next) break;
            this.add(next);
            this.targetIndex++;
          }
        }
        this.io.progress();
        await this.io.yield();
      }
      while (this.selectionDone && !this.stopped) {
        this.io.guard();
        if (!this.pending) {
          const items: FailureResetRef[] = [];
          while (this.offset < this.queue.length && items.length < 100) {
            const entry = this.queue[this.offset++];
            const donation = this.donations.get(entry.donation_id)!;
            if (donation.conflict) this.mark(entry, 'conflict');
            if (!entry.status)
              items.push({
                donation_id: entry.donation_id,
                key_id: entry.key_id,
                expected_revision: donation.revision,
              });
          }
          if (items.length === 0) {
            this.phase = 'done';
            break;
          }
          this.pending = { items, key: '' };
        }
        if (!this.pending.key) this.pending.key = operationKey();
        const batch = await this.io.reset(this.pending.items, this.pending.key);
        this.io.guard();
        // Only revisions returned by our successful resets advance later chunks.
        for (const result of batch.results) {
          this.mark(this.entries.get(result.key_id)!, result.status);
          const donation = this.donations.get(result.donation_id)!;
          if (result.status === 'reset') donation.revision = result.revision!;
          if (result.status === 'conflict') donation.conflict = true;
        }
        this.pending = null;
        this.io.progress();
        await this.io.yield();
      }
      if (this.phase !== 'done') this.phase = 'paused';
    } catch (error) {
      this.phase = 'paused';
      this.error = error;
    } finally {
      this.running = false;
      this.io.progress();
    }
  }
}
