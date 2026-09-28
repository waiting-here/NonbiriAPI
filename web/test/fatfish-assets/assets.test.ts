import fs from 'node:fs';
import { createHash } from 'node:crypto';
import { describe, expect, it } from 'vitest';

interface FileEntry {
  url: string;
  source: string;
  source_sha256?: string;
  transform: string;
  license: string;
  bytes: number;
  sha256: string;
}
interface SvgEntry extends FileEntry {
  id: string;
  viewBox: number[];
  source_sha256: string;
}
interface Atlas {
  format: string;
  defaultDisplayHeight: number;
  sources: Record<string, { file: string; width: number; height: number }>;
  animations: Record<string, { frames: string[]; durationsMs: number[]; loop: boolean }>;
  frames: Record<string, { source: string; rect: number[]; pivot: number[]; referenceHeight: number }>;
}

const root = new URL('../../public/assets/fatfish/', import.meta.url);
const manifest = JSON.parse(fs.readFileSync(new URL('manifest.json', root), 'utf8')) as {
  format: string;
  version: number;
  license: string;
  tool_visuals: Record<string, string>;
  svg: SvgEntry[];
  character: { atlas_url: string; default_display_height: number; frame_count: number; files: FileEntry[] };
  cover: FileEntry;
  music: FileEntry & { title: string; artist: string; license_file: string; attribution_url: string };
};
const assetFile = (url: string) => {
  expect(url).toMatch(/^\/assets\/fatfish\/[A-Za-z0-9/.-]+$/);
  expect(url).not.toContain('..');
  return new URL(`.${url.replace('/assets/fatfish', '')}`, root);
};
const verifyFile = (entry: FileEntry, license = 'AGPL-3.0') => {
  expect(entry.source.length).toBeGreaterThan(0);
  expect(entry.transform.length).toBeGreaterThan(0);
  expect(entry.license).toBe(license);
  const data = fs.readFileSync(assetFile(entry.url));
  expect(data.length).toBe(entry.bytes);
  expect(createHash('sha256').update(data).digest('hex')).toBe(entry.sha256);
  return data;
};
const assertStaticSVG = (data: Buffer) => {
  const text = data.toString('utf8');
  expect(text).toMatch(/^<svg\b/);
  expect(text).toMatch(/<\/svg>\s*$/);
  expect(text).not.toMatch(/<!DOCTYPE|<!ENTITY|<script\b|<foreignObject\b|<iframe\b|<object\b|@import|\son[a-z]+\s*=/i);
  for (const [, href] of text.matchAll(/\b(?:xlink:)?href\s*=\s*"([^"]*)"/g)) {
    expect(href.startsWith('#') || /^data:image\/png;base64,[A-Za-z0-9+/=]+$/.test(href)).toBe(true);
  }
  for (const [, url] of text.matchAll(/url\(([^)]*)\)/g)) expect(url.trim()).toMatch(/^['"]?#[a-zA-Z0-9_-]+['"]?$/);
  return text;
};
const pngSize = (data: Buffer) => {
  expect(data.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))).toBe(true);
  expect(data.toString('ascii', 12, 16)).toBe('IHDR');
  return [data.readUInt32BE(16), data.readUInt32BE(20)];
};

describe('fat fish public art', () => {
  it('provides exactly 71 hashed, attributed, static SVGs with corrected buttons', () => {
    expect(manifest.format).toBe('nonbiri-fatfish-assets');
    expect(manifest.version).toBe(1);
    expect(manifest.license).toBe('AGPL-3.0');
    expect(manifest.svg).toHaveLength(71);
    expect(new Set(manifest.svg.map((entry) => entry.id)).size).toBe(71);
    expect(fs.readdirSync(new URL('svg/', root)).filter((name) => name.endsWith('.svg')).sort()).toEqual(
      manifest.svg.map((entry) => `${entry.id}.svg`).sort(),
    );
    for (const entry of manifest.svg) {
      expect(entry.url).toBe(`/assets/fatfish/svg/${entry.id}.svg`);
      expect(entry.source_sha256).toMatch(/^[0-9a-f]{64}$/);
      const text = assertStaticSVG(verifyFile(entry));
      expect(text.match(/viewBox="([^"]+)"/)?.[1]).toBe(entry.viewBox.join(' '));
      if (entry.id.startsWith('button-')) {
        expect(entry.viewBox).toEqual([0, 0, 260, 78]);
        expect(text).toContain('width="260" height="78"');
        expect(text).toMatch(/<rect\b/);
        expect(text).not.toMatch(/<title>\s*&lt;rect/);
      }
    }
  });

  it('maps supported movable pieces to their matching vector art', () => {
    expect(Object.keys(manifest.tool_visuals).sort()).toEqual(['barrier', 'cup', 'fan', 'light', 'memory']);
    expect(manifest.tool_visuals).toEqual({ barrier: 'keycap-barrier', memory: 'memory-module',
      fan: 'inference-card', light: 'cooling-fins', cup: 'cache-puck' });
    const ids = new Set(manifest.svg.map((entry) => entry.id));
    for (const id of Object.values(manifest.tool_visuals)) expect(ids.has(id)).toBe(true);
  });

  it('retains 24 bounded source frames at the actual PNG dimensions', () => {
    expect(manifest.character.atlas_url).toBe('/assets/fatfish/character/atlas.json');
    expect(manifest.character.files).toHaveLength(3);
    const files = new Map(manifest.character.files.map((entry) => [entry.url, verifyFile(entry)]));
    for (const entry of manifest.character.files) expect(entry.source_sha256).toMatch(/^[0-9a-f]{64}$/);
    const atlas = JSON.parse(files.get(manifest.character.atlas_url)!.toString('utf8')) as Atlas;
    expect(atlas.format).toBe('fat-fish-atlas-v1');
    expect(atlas.defaultDisplayHeight).toBe(48);
    expect(manifest.character.default_display_height).toBe(48);
    expect(manifest.character.frame_count).toBe(24);
    expect(Object.keys(atlas.frames)).toHaveLength(24);
    expect(Object.keys(atlas.sources).sort()).toEqual(['expressions', 'walk']);
    for (const source of Object.values(atlas.sources)) {
      expect(['walk.png', 'expressions.png']).toContain(source.file);
      expect(pngSize(files.get(`/assets/fatfish/character/${source.file}`)!)).toEqual([source.width, source.height]);
    }
    for (const [name, frame] of Object.entries(atlas.frames)) {
      const image = atlas.sources[frame.source];
      expect(image, name).toBeDefined();
      const [x, y, width, height] = frame.rect;
      expect([x, y, width, height].every(Number.isInteger)).toBe(true);
      expect(x).toBeGreaterThanOrEqual(0);
      expect(y).toBeGreaterThanOrEqual(0);
      expect(width).toBeGreaterThan(0);
      expect(height).toBeGreaterThan(0);
      expect(x + width).toBeLessThanOrEqual(image.width);
      expect(y + height).toBeLessThanOrEqual(image.height);
      expect(frame.pivot).toHaveLength(2);
      expect(frame.referenceHeight).toBeGreaterThan(0);
    }
    const animated = Object.values(atlas.animations).flatMap((animation) => {
      expect(animation.frames).toHaveLength(animation.durationsMs.length);
      expect(animation.loop).toBe(true);
      expect(animation.durationsMs.every((duration) => duration > 0)).toBe(true);
      return animation.frames;
    });
    expect(animated).toHaveLength(24);
    expect(new Set(animated).size).toBe(24);
    for (const frame of animated) expect(atlas.frames[frame]).toBeDefined();
  });

  it('provides a separately hashed illustrated raster cover', () => {
    expect(manifest.cover.url).toBe('/assets/fatfish/cover.png');
    expect(pngSize(verifyFile(manifest.cover))).toEqual([1536, 1024]);
  });

  it('bundles the original music with its separate license and attribution', () => {
    expect(manifest.music.title).toBe('Monkeys Spinning Monkeys');
    expect(manifest.music.artist).toBe('Kevin MacLeod');
    const data = verifyFile(manifest.music, 'CC-BY-4.0');
    expect(data.length).toBeGreaterThan(1_000_000);
    const license = fs.readFileSync(assetFile(manifest.music.license_file), 'utf8');
    expect(license).toContain('Attribution 4.0 International');
    const notice = fs.readFileSync(assetFile(manifest.music.attribution_url), 'utf8');
    expect(notice).toContain(manifest.music.title);
    expect(notice).toContain(manifest.music.artist);
    expect(notice).toContain('https://creativecommons.org/licenses/by/4.0/');
    expect(notice).toContain('without editing or transcoding');
  });
});
