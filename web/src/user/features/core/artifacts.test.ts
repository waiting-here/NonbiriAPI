import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { CORE_COPY_DESCRIPTORS, CORE_COPY_KEYS } from './copy';
import { CORE_ROUTE_DESCRIPTORS, CORE_ROUTE_PATHS } from './descriptors';

function workspaceFile(path: string): string {
  return readFileSync(resolve(process.cwd(), path), 'utf8');
}

describe('core integration descriptors', () => {
  it('exports one complete bilingual descriptor for every local copy key', () => {
    expect(CORE_COPY_DESCRIPTORS).toHaveLength(CORE_COPY_KEYS.length);
    expect(new Set(CORE_COPY_KEYS).size).toBe(CORE_COPY_KEYS.length);
    expect(CORE_COPY_DESCRIPTORS.map(({ key }) => key)).toEqual(CORE_COPY_KEYS);
    for (const descriptor of CORE_COPY_DESCRIPTORS) {
      expect(descriptor.en.trim(), descriptor.key).not.toBe('');
      expect(descriptor.zh.trim(), descriptor.key).not.toBe('');
    }
  });

  it('exports the exact route wiring surface without changing the global registry', () => {
    expect(
      CORE_ROUTE_DESCRIPTORS.map(({ id, path, navigation }) => ({ id, path, navigation })),
    ).toEqual([
      { id: 'home', path: '/', navigation: true },
      { id: 'endpoints', path: '/endpoints', navigation: true },
      { id: 'endpoint-detail', path: '/endpoints/:endpointId', navigation: false },
      { id: 'caller-key', path: '/keys', navigation: false },
      { id: 'models', path: '/models', navigation: true },
      { id: 'account', path: '/account', navigation: false },
    ]);
    expect(CORE_ROUTE_PATHS.endpointDetail('11/22')).toBe('/endpoints/11%2F22');
    const copyKeys = new Set(CORE_COPY_KEYS);
    for (const descriptor of CORE_ROUTE_DESCRIPTORS)
      expect(copyKeys.has(descriptor.titleKey)).toBe(true);
  });
});

describe('core narrow viewport and untrusted-text boundary', () => {
  it('keeps the 390px layout single-column with 44px coarse-pointer targets', () => {
    const css = workspaceFile('src/user/features/core/core.css');
    expect(css).toMatch(/@media \(max-width: 26rem\)/);
    expect(css).toMatch(/@media \(pointer: coarse\), \(max-width: 26rem\)/);
    expect(css).toMatch(/min-block-size:\s*2\.75rem/);
    expect(css).toMatch(/overflow-wrap:\s*anywhere/);
    expect(css).toMatch(/grid-template-columns:\s*minmax\(0, 1fr\)/);
  });

  it('does not introduce raw HTML rendering in the five pages or feature components', () => {
    const sources = [
      'src/user/pages/HomePage.tsx',
      'src/user/pages/EndpointsPage.tsx',
      'src/user/pages/KeysPage.tsx',
      'src/user/pages/ModelsPage.tsx',
      'src/user/pages/AccountPage.tsx',
      'src/user/features/core/components.tsx',
      'src/user/features/core/EndpointDetail.tsx',
      'src/user/features/core/EndpointWizard.tsx',
      'src/user/features/core/ModelsWorkspace.tsx',
      'src/user/features/core/AccountWorkspace.tsx',
    ];
    for (const source of sources)
      expect(workspaceFile(source), source).not.toContain('dangerouslySetInnerHTML');
  });

  it('leaves enough UTF-16 input capacity for every valid astral scalar boundary', () => {
    const models = workspaceFile('src/user/features/core/ModelsWorkspace.tsx');
    const endpoints = workspaceFile('src/user/features/core/EndpointDetail.tsx');
    expect(models.match(/maxLength=\{128\}/g)).toHaveLength(2);
    expect(endpoints.match(/maxLength=\{1024\}/g)).toHaveLength(3);
    expect(endpoints.match(/maxLength=\{256\}/g)).toHaveLength(2);
  });
});
