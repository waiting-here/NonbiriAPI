import { describe, expect, it } from 'vitest';
import { chartRatio, chartSVG, countAtRatio, PLOT_SCALE, wrapChartText } from './chartExport';

describe('statistics chart exports', () => {
  it('scales beyond-safe-integer counts without rounding the raw count', () => {
    const maximum = 9007199254740993n;
    expect(chartRatio(maximum.toString(), maximum)).toBe(PLOT_SCALE);
    expect(countAtRatio(PLOT_SCALE, maximum)).toBe(maximum.toString());
    expect(chartRatio('0', 0n)).toBe(0);
  });
  it('keeps exact counts and escapes all text in SVG downloads', () => {
    const svg = chartSVG(
      'Counts <all>',
      'Pool & period',
      [{ label: '<script>alert("x")</script>', count: '9007199254740993' }],
      {
        background: 'white',
        foreground: 'black',
        muted: 'gray',
        border: 'gray',
        bars: ['blue'],
      },
    );
    expect(svg).toContain('9,007,199,254,740,993');
    expect(svg).toContain('&lt;script&gt;');
    expect(svg).not.toContain('<script>');
    expect(svg).toContain('Pool &amp; period');
    expect(svg).toContain('width="620.000"');
  });
  it('wraps complete Chinese and English names and reserves the count column for U128 totals', () => {
    const chinese = '完整中文卡牌名称与期次名称'.repeat(8);
    const english = 'Wide WWW card name with every original word preserved '.repeat(5);
    const pool = '完整奖池名称 '.repeat(30);
    const maximum = '340282366920938463463374607431768211455';
    const svg = chartSVG(
      'Card counts',
      pool,
      [
        { label: chinese, count: maximum },
        { label: english, count: '1' },
      ],
      {
        background: 'white',
        foreground: 'black',
        muted: 'gray',
        border: 'gray',
        bars: ['blue'],
      },
    );
    const document = new DOMParser().parseFromString(svg, 'image/svg+xml');
    expect(document.querySelector('parsererror')).toBeNull();
    expect(
      [...document.querySelectorAll('.chart-row-label')].map((node) => node.textContent),
    ).toEqual([chinese, english]);
    expect(
      [...document.querySelectorAll('.chart-pool-name')].map((node) => node.textContent).join(''),
    ).toBe(pool);
    expect(
      Number(document.querySelector('.chart-row-count')!.getAttribute('font-size')),
    ).toBeLessThan(22);
    expect(wrapChartText(chinese, 392, 22).join('')).toBe(chinese);
    expect(document.documentElement.getAttribute('width')).toBe('1600');
    expect(svg).not.toContain('…');
  });
});
