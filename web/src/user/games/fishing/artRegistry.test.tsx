import { readFileSync } from 'node:fs';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { FishingArtwork } from './FishingArtwork';
import {
  fishArtwork,
  fishFamilies,
  fishingArtwork,
  junkArtwork,
  resolveFishingArtwork,
  treasureArtwork,
} from './artRegistry';

const expectedFamilies = {
  minnow: ['whitebait', 'gudgeon', 'horse_mouth', 'smelt'],
  eel: ['loach', 'japanese_eel'],
  panfish: ['crucian', 'tilapia', 'ayu', 'stream_carp'],
  catfish: ['yellow_catfish', 'catfish'],
  carp: ['common_carp', 'grass_carp', 'silver_carp', 'bighead_carp', 'black_carp', 'koi'],
  predator: ['snakehead', 'mandarin_fish', 'yellowcheek'],
  salmonid: ['rainbow_trout', 'taimen'],
} as const;

describe('fishing artwork registry', () => {
  it('freezes the complete 23 fish / 8 junk / 3 treasure roster', () => {
    expect(fishArtwork).toHaveLength(23);
    expect(junkArtwork).toHaveLength(8);
    expect(treasureArtwork).toHaveLength(3);
    expect(fishingArtwork).toHaveLength(34);
    expect(new Set(fishingArtwork.map(({ key }) => key)).size).toBe(34);
    expect(Object.isFrozen(fishArtwork)).toBe(true);
    expect(fishingArtwork.every(Object.isFrozen)).toBe(true);
    expect(junkArtwork.map(({ key }) => key)).toEqual([
      'boot',
      'seaweed',
      'plastic_bag',
      'branch',
      'old_tire',
      'glasses',
      'phone_case',
      'fry',
    ]);
    expect(treasureArtwork.map(({ key }) => key)).toEqual(['bottle', 'clover', 'shell']);
  });

  it('maps all 23 species to the seven frozen families', () => {
    expect(fishFamilies).toEqual(Object.keys(expectedFamilies));
    for (const family of fishFamilies) {
      expect(
        fishArtwork.filter((descriptor) => descriptor.family === family).map(({ key }) => key),
      ).toEqual(expectedFamilies[family]);
    }
  });

  it('gives every species a distinct palette, fin, pattern, and barbel combination', () => {
    const visualSignatures = fishArtwork.map(
      ({ palette, fin, pattern, barbels }) => `${palette}/${fin}/${pattern}/${barbels}`,
    );
    expect(new Set(visualSignatures).size).toBe(23);
    for (const descriptor of fishingArtwork) {
      expect(descriptor.messageKey).toBe(`games.fishing.items.${descriptor.key}`);
      expect(resolveFishingArtwork(descriptor.key)).toBe(descriptor);
    }
  });

  it('uses a stable local unknown fallback', () => {
    expect(resolveFishingArtwork('not-in-the-roster')).toMatchObject({
      kind: 'unknown',
      key: 'unknown',
      messageKey: 'games.fishing.items.unknown',
    });
  });
});

describe('FishingArtwork', () => {
  it('renders every registered item in the common 160x96 coordinate system', () => {
    for (const descriptor of fishingArtwork) {
      const { container, unmount } = render(
        <FishingArtwork itemKey={descriptor.key} label={`Localized ${descriptor.key}`} />,
      );
      const figure = container.querySelector('figure');
      const svg = container.querySelector('svg');
      expect(figure).toHaveAttribute('data-art-key', descriptor.key);
      expect(figure).toHaveAttribute('data-art-kind', descriptor.kind);
      expect(svg).toHaveAttribute('viewBox', '0 0 160 96');
      expect(svg).toHaveAttribute('preserveAspectRatio', 'xMidYMid meet');
      expect(svg).toHaveAttribute('aria-hidden', 'true');
      expect(svg?.querySelectorAll('path, circle, ellipse, rect').length).toBeGreaterThan(1);
      unmount();
    }
  });

  it('provides one accessible localized name without duplicate SVG speech', () => {
    const { container } = render(<FishingArtwork itemKey="koi" label="Localized koi" />);
    expect(screen.getByRole('img', { name: 'Localized koi' })).toBeInTheDocument();
    expect(screen.getByText('Localized koi')).toBeInTheDocument();
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
  });

  it('can keep the localized name off-screen while retaining the image label', () => {
    render(<FishingArtwork itemKey="boot" label="Localized boot" showLabel={false} />);
    expect(screen.getByRole('img', { name: 'Localized boot' })).toHaveAttribute(
      'aria-label',
      'Localized boot',
    );
    expect(screen.queryByText('Localized boot')).not.toBeInTheDocument();
  });

  it('hides decorative artwork when an adjacent name is already read', () => {
    const { container } = render(
      <FishingArtwork itemKey="clover" label="Localized clover" decorative />,
    );
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(screen.queryByText('Localized clover')).not.toBeInTheDocument();
    expect(container.querySelector('figure')).toHaveAttribute('aria-hidden', 'true');
  });

  it('renders an unknown item as a named neutral local shape', () => {
    const { container } = render(<FishingArtwork itemKey="future_item" label="Unknown catch" />);
    expect(screen.getByRole('img', { name: 'Unknown catch' })).toBeInTheDocument();
    expect(container.querySelector('figure')).toHaveAttribute('data-art-key', 'unknown');
    expect(container.querySelector('figure')).toHaveAttribute('data-art-kind', 'unknown');
  });

  it('keeps all rendered SVG free of executable or external content', () => {
    const { container } = render(
      <div>
        {fishingArtwork.map(({ key }) => (
          <FishingArtwork key={key} itemKey={key} label={key} />
        ))}
        <FishingArtwork itemKey="unknown-key" label="unknown" />
      </div>,
    );
    const markup = container.innerHTML.toLowerCase();
    expect(markup).not.toMatch(
      /<script|<image|<use|<foreignobject|\shref=|https?:|data:|url\s*\(|onload=|onerror=/,
    );
  });

  it('ships only code-native source with responsive, contrast, and motion fallbacks', () => {
    const sources = ['FishingArtwork.tsx', 'artRegistry.ts', 'fishing-art.css'].map((name) =>
      readFileSync(new URL(name, import.meta.url), 'utf8'),
    );
    const joined = sources.join('\n').toLowerCase();
    expect(joined).not.toMatch(
      /dangerouslysetinnerhtml|<script|<image|<use|<foreignobject|\shref\s*=|https?:|data:|url\s*\(|\.png|\.jpe?g|\.gif|\.webp/,
    );
    expect(joined).not.toMatch(/[\u{1f000}-\u{1faff}]/u);

    const css = sources[2];
    expect(css).toContain('max-inline-size: 100%');
    expect(css).toContain("[data-theme='dark']");
    expect(css).toContain('@media (forced-colors: active)');
    expect(css).toContain('@media (prefers-reduced-motion: reduce)');
    expect(css).toContain('animation: none');
  });
});
