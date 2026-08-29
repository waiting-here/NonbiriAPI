import { readFileSync } from 'node:fs';
import { describe, expect, test } from 'vitest';

const tokensCSS = readFileSync(
  new URL(
    '../../src/shared/styles/tokens.css',
    import.meta.url.replace(/token-contrast\.test\.ts$/, ''),
  ),
  'utf8',
);

function darkToken(name: string): string {
  const darkTheme = tokensCSS.match(/\[data-theme='dark'\]\s*\{([\s\S]*?)\n\}/)?.[1];
  const value = darkTheme?.match(new RegExp(`--nb-color-${name}:\\s*(#[0-9a-fA-F]{3,8})`))?.[1];
  expect(value, `missing dark token ${name}`).toBeTruthy();
  return value as string;
}

function luminance(hex: string): number {
  const normalized = hex.slice(1).length === 3
    ? hex.slice(1).split('').map((part) => `${part}${part}`).join('')
    : hex.slice(1);
  const channels = [0, 2, 4].map((offset) => parseInt(normalized.slice(offset, offset + 2), 16) / 255);
  const linear = channels.map((channel) => channel <= 0.03928
    ? channel / 12.92
    : ((channel + 0.055) / 1.055) ** 2.4);
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

function contrast(first: string, second: string): number {
  const firstLuminance = luminance(first);
  const secondLuminance = luminance(second);
  return (Math.max(firstLuminance, secondLuminance) + 0.05)
    / (Math.min(firstLuminance, secondLuminance) + 0.05);
}

describe('dark theme token contrast golden', () => {
  test('accent links and primary controls meet ordinary-text contrast', () => {
    const background = darkToken('bg');
    const accent = darkToken('accent');
    const accentHover = darkToken('accent-hover');
    const accentContrast = darkToken('accent-contrast');
    const danger = darkToken('danger');
    const dangerHover = darkToken('danger-hover');
    const dangerContrast = darkToken('on-danger');

    expect(contrast(accent, background)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(accentHover, background)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(accentContrast, accent)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(accentContrast, accentHover)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(dangerContrast, danger)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(dangerContrast, dangerHover)).toBeGreaterThanOrEqual(4.5);
  });

  test('brand gradient endpoints remain readable with white on-accent ink', () => {
    const white = darkToken('on-accent');
    expect(contrast(white, darkToken('brand-start'))).toBeGreaterThanOrEqual(4.5);
    expect(contrast(white, darkToken('brand-end'))).toBeGreaterThanOrEqual(4.5);
  });
});
