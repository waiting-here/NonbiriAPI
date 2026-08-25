import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const sourceFiles = [
  readFileSync(
    new URL('../../pages/DebugPage.tsx', import.meta.url.replace(/storageBoundary\.test\.ts$/, '')),
    'utf8',
  ),
  ...['api.ts', 'eventStore.ts', 'sseClient.ts', 'useDebugSession.ts'].map((name) =>
    readFileSync(new URL(name, import.meta.url.replace(/storageBoundary\.test\.ts$/, '')), 'utf8'),
  ),
];

describe('Debug UI storage boundary', () => {
  it('does not use browser persistence, URL state, query cache, or HTML injection', () => {
    const source = sourceFiles.join('\n').toLowerCase();
    expect(source).not.toMatch(
      /localstorage|sessionstorage|indexeddb|caches\.open|serviceworker|dangerouslysetinnerhtml/,
    );
    expect(source).not.toContain('eventsource');
    expect(source).not.toContain('usequery');
    expect(source).not.toContain('usemutation');
  });

  it('only renders captured values through text/pre output and copies on explicit action', () => {
    const page = sourceFiles[0];
    expect(page).toContain('<pre');
    expect(page).toContain('navigator.clipboard.writeText');
    expect(page).not.toContain('dangerouslySetInnerHTML');
  });
});
