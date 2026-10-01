import { readFileSync } from 'node:fs';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { contentHash } from '@shared/fatfish/engine/canonical';
import { renderWithProviders } from '../../../../test/unit/support';
import type { Level } from '@shared/fatfish/engine/types';
import { blankLevel, localValidation } from './draft';
import { ExamplePicker } from './ExamplePicker';

const reply = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
describe('Fat Fish example picker', () => {
  const manifest = (entry: { id: string; title: string; url: string; content_hash: string }) => ({
    format: 'nonbiri-fatfish-examples', version: 1, license: 'AGPL-3.0', source: 'Bundled examples',
    examples: [entry, ...Array.from({ length: 7 }, (_, index) => ({ ...entry, id: `other_${index}`, title: `Other ${index}` }))],
  });
  it('imports and downloads a bundled level', async () => {
    const level = blankLevel(), onImport = vi.fn();
    const path = '/examples/fatfish/01-example.fatfish.json';
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => String(input).endsWith('manifest.json')
      ? reply(manifest({ id: 'first', title: 'First example', url: path, content_hash: contentHash(level) }))
      : reply(level)));
    const view = await renderWithProviders(<ExamplePicker onImport={onImport} />, { station: 'admin', role: 'admin' });
    await view.user.click(await screen.findByRole('button', { name: 'First example' }));
    await waitFor(() => expect(onImport).toHaveBeenCalledWith({ title: 'First example', description: '', level, pendingFishCount: 0 }));
    const createObjectURL = vi.fn(() => 'blob:example');
    const revokeObjectURL = vi.fn();
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL });
    let downloaded = '';
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { downloaded = this.download; });
    await view.user.click(screen.getByRole('button', { name: 'Download JSON · First example' }));
    await waitFor(() => expect(click).toHaveBeenCalledTimes(1));
    expect(downloaded).toBe('first.fatfish.json');
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    click.mockRestore();
  });
  it('accepts all eight bundled examples with their published canonical hashes', () => {
    const raw = readFileSync('public/examples/fatfish/manifest.json', 'utf8');
    const bundled = JSON.parse(raw) as { examples: { url: string; content_hash: string }[] };
    expect(bundled.examples).toHaveLength(8);
    for (const example of bundled.examples) {
      expect(example.url).toMatch(/^\/examples\/fatfish\/[A-Za-z0-9._-]+\.fatfish\.json$/);
      const file = readFileSync(`public${example.url}`, 'utf8');
      expect(new TextEncoder().encode(file).byteLength).toBeLessThanOrEqual(256 * 1024);
      const level = JSON.parse(file) as Level;
      expect(level.engine_version).toBe(3);
      expect(localValidation(level)).toBeNull();
      expect(contentHash(level)).toBe(example.content_hash);
    }
  });
});
