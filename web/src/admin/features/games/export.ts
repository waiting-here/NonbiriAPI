import { getExportPage, type Dataset, type ExportPage, type Selection } from './history';
import type { GameID } from './copy';

export interface ExportProgress {
  parts: number;
  matches: number;
  rounds: number;
  expired: number;
  complete: boolean;
}
export type PartWriter = (bytes: Uint8Array, filename: string) => void | Promise<void>;
export class HistoryExport {
  readonly game: GameID;
  readonly dataset: Dataset;
  readonly selection: Selection;
  private cursor: string | null = null;
  private reading = false;
  private lastMatch = '';
  private lastRound = 0;
  progress: ExportProgress = { parts: 0, matches: 0, rounds: 0, expired: 0, complete: false };
  constructor(
    game: GameID,
    dataset: Dataset,
    selection: Selection,
    private readPage = getExportPage,
  ) {
    this.game = game;
    this.dataset = dataset;
    this.selection = Object.freeze({ ...selection });
  }
  async step(signal: AbortSignal, write: PartWriter) {
    if (this.reading || this.progress.complete)
      throw new Error('An export page is already running or complete.');
    this.reading = true;
    try {
      signal.throwIfAborted();
      const page: ExportPage = await this.readPage(
        this.game,
        this.dataset,
        this.selection,
        this.cursor,
        signal,
      );
      signal.throwIfAborted();
      if (page.next_cursor !== null && page.next_cursor === this.cursor)
        throw new Error('The export did not advance.');
      let lastMatch = this.lastMatch,
        lastRound = this.lastRound;
      for (const item of page.items) {
        if (item.kind === 'match') {
          if (item.match_ref === lastMatch) throw new Error('A match header was repeated.');
          lastMatch = item.match_ref;
          lastRound = 0;
        } else {
          if (item.match_ref !== lastMatch || item.round_no !== lastRound + 1)
            throw new Error('A round is missing its preceding record.');
          lastRound = item.round_no;
        }
      }
      if (page.items.length) {
        const bytes = new TextEncoder().encode(
          page.items.map((item) => JSON.stringify(item) + '\n').join(''),
        );
        if (bytes.length > 16 * 1024 * 1024) throw new Error('The download part is too large.');
        await write(
          bytes,
          `duel-history-${this.game}-${this.dataset}-part-${String(this.progress.parts + 1).padStart(4, '0')}.ndjson`,
        );
      }
      // Advance only after this complete page has been handed to the downloader.
      this.cursor = page.next_cursor;
      this.lastMatch = lastMatch;
      this.lastRound = lastRound;
      this.progress = {
        parts: this.progress.parts + (page.items.length ? 1 : 0),
        matches: this.progress.matches + page.items.filter((i) => i.kind === 'match').length,
        rounds: this.progress.rounds + page.items.filter((i) => i.kind === 'round').length,
        expired: this.progress.expired + page.expired_skipped,
        complete: page.next_cursor === null,
      };
      return this.progress;
    } finally {
      this.reading = false;
    }
  }
}
export function downloadPart(bytes: Uint8Array, filename: string) {
  const url = URL.createObjectURL(
    new Blob([new Uint8Array(bytes)], { type: 'application/x-ndjson;charset=utf-8' }),
  );
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  document.body.append(anchor);
  try {
    anchor.click();
  } finally {
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10_000);
  }
}
