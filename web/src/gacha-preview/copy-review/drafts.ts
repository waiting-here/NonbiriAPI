import type { CopyChange, CopyEntry, SourceLocator } from './types';

export const storageKey = 'nb.gacha-copy-review.v1';
const format = 'nonbiri-gacha-copy-review';

interface ReviewPackage {
  format: typeof format;
  version: 1;
  changes: CopyChange[];
  drafts?: CopyChange[];
  pending?: CopyChange[];
}

export interface ReviewDraft {
  edits: Map<string, CopyChange>;
  pending: CopyChange[];
}

export const emptyDraft = (): ReviewDraft => ({ edits: new Map(), pending: [] });
export const copyKey = ({ source, language }: Pick<CopyChange, 'source' | 'language'>) =>
  JSON.stringify([source.file, source.key, language]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function readSource(source: unknown): SourceLocator | undefined {
  if (
    !isRecord(source) ||
    typeof source.file !== 'string' ||
    !source.file ||
    typeof source.key !== 'string' ||
    !source.key
  )
    return;
  const line = source.line;
  return {
    file: source.file,
    key: source.key,
    ...(typeof line === 'number' && Number.isSafeInteger(line) && line > 0 ? { line } : {}),
  };
}

function readChanges(value: unknown): CopyChange[] {
  if (!Array.isArray(value)) throw new Error('Invalid change list');
  return value.map((change: unknown) => {
    if (
      !isRecord(change) ||
      (change.language !== 'zh' && change.language !== 'en') ||
      typeof change.original !== 'string' ||
      typeof change.value !== 'string'
    )
      throw new Error('Invalid change record');
    const source = readSource(change.source);
    if (!source) throw new Error('Invalid source locator');
    return { source, language: change.language, original: change.original, value: change.value };
  });
}

export function parsePackage(raw: string): ReviewPackage {
  const data: unknown = JSON.parse(raw.replace(/^\uFEFF/, ''));
  if (!isRecord(data) || data.format !== format || data.version !== 1) {
    throw new Error('Unsupported review package');
  }
  return {
    format,
    version: 1,
    changes: readChanges(data.changes),
    drafts: data.drafts === undefined ? [] : readChanges(data.drafts),
    pending: data.pending === undefined ? [] : readChanges(data.pending),
  };
}

function matches(left: CopyChange, right: CopyChange) {
  return (
    copyKey(left) === copyKey(right) &&
    left.original === right.original &&
    left.value === right.value
  );
}

function asChange({ source, original, language }: CopyEntry, value: string): CopyChange {
  return { source, original, language, value };
}

export function mergePackage(
  draft: ReviewDraft,
  document: ReviewPackage,
  sources: ReadonlyMap<string, CopyEntry>,
) {
  const edits = new Map(draft.edits);
  const pending = [...draft.pending];
  let added = 0;
  let repeated = 0;
  const retain = (change: CopyChange) => {
    if (!pending.some((current) => matches(current, change))) pending.push(change);
    else repeated++;
  };
  for (const change of [...document.changes, ...(document.drafts ?? [])]) {
    const key = copyKey(change);
    const source = sources.get(key);
    const local = edits.get(key);
    if (change.value === source?.original || (local && matches(local, change))) {
      repeated++;
    } else if (source && source.original === change.original && !local) {
      edits.set(key, asChange(source, change.value));
      added++;
    } else {
      retain(change);
    }
  }
  for (const change of document.pending ?? []) {
    const key = copyKey(change);
    const local = edits.get(key);
    if (local ? matches(local, change) : sources.get(key)?.original === change.value) {
      repeated++;
    } else {
      retain(change);
    }
  }
  return {
    draft: { edits, pending },
    added,
    repeated,
    conflicted: pending.length - draft.pending.length,
  };
}

export function updateDraft(draft: ReviewDraft, entry: CopyEntry, value: string): ReviewDraft {
  const edits = new Map(draft.edits);
  const key = copyKey(entry);
  if (value === entry.original) edits.delete(key);
  else edits.set(key, asChange(entry, value));
  return { ...draft, edits };
}

export const pendingKey = (change: CopyChange) =>
  JSON.stringify([copyKey(change), change.original, change.value]);

export function resolvePending(
  draft: ReviewDraft,
  change: CopyChange,
  entry?: CopyEntry,
): ReviewDraft {
  const pending = draft.pending.filter((candidate) => !matches(change, candidate));
  if (pending.length === draft.pending.length) return draft;
  const next = { ...draft, pending };
  return entry ? updateDraft(next, entry, change.value) : next;
}

function sameEdit(left?: CopyChange, right?: CopyChange) {
  return left && right ? matches(left, right) : left === right;
}

// Apply only this tab's changes since it last read storage. Keep competing writes for review.
export function reconcileDraft(
  base: ReviewDraft,
  next: ReviewDraft,
  latest: ReviewDraft,
): ReviewDraft {
  const edits = new Map(latest.edits);
  const pending = new Map(latest.pending.map((change) => [pendingKey(change), change]));
  const previousPending = new Set(base.pending.map(pendingKey));
  const nextPending = new Map(next.pending.map((change) => [pendingKey(change), change]));
  for (const key of previousPending) {
    if (!nextPending.has(key)) pending.delete(key);
  }
  for (const [key, change] of nextPending) {
    if (!previousPending.has(key)) pending.set(key, change);
  }
  for (const key of new Set([...base.edits.keys(), ...next.edits.keys()])) {
    const before = base.edits.get(key);
    const after = next.edits.get(key);
    const current = latest.edits.get(key);
    if (sameEdit(before, after) || sameEdit(current, after)) continue;
    if (sameEdit(current, before)) {
      if (after) edits.set(key, after);
      else edits.delete(key);
      continue;
    }
    const proposal = after ?? (before && { ...before, value: before.original });
    if (proposal) pending.set(pendingKey(proposal), proposal);
  }
  return { edits, pending: [...pending.values()] };
}

export function placeholders(value: string): string[] {
  return value.match(/\{\{[^{}]+\}\}|\$\{[^{}]+\}/g)?.sort() ?? [];
}

export function keepsPlaceholders(change: CopyChange): boolean {
  const expected = placeholders(change.original);
  const actual = placeholders(change.value);
  return (
    expected.length === actual.length && expected.every((token, index) => token === actual[index])
  );
}

export function packageDraft(draft: ReviewDraft) {
  const document: ReviewPackage = { format, version: 1, changes: [] };
  const awaiting = new Set(draft.pending.map(copyKey));
  for (const change of draft.edits.values()) {
    if (keepsPlaceholders(change) && !awaiting.has(copyKey(change))) document.changes.push(change);
    else (document.drafts ??= []).push(change);
  }
  if (draft.pending.length) document.pending = draft.pending;
  return document;
}

export function serializeDraft(draft: ReviewDraft): string {
  return JSON.stringify(packageDraft(draft), null, 2) + '\n';
}
