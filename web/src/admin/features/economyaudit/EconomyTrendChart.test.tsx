import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import type { ChartConfiguration } from 'chart.js';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import type { Asset, Series } from './api';
import { EconomyTrendChart } from './EconomyTrendChart';

const chartMock = vi.hoisted(() => ({ configurations: [] as unknown[], active: [] as unknown[] }));
vi.mock('chart.js', () => ({
  Chart: class {
    static register() {}
    tooltip = { setActiveElements: (active: unknown) => chartMock.active.push(active) };
    constructor(_context: unknown, configuration: unknown) {
      chartMock.configurations.push(configuration);
    }
    getDatasetMeta() {
      return {
        data: Array.from({ length: 4 }, () => ({ getCenterPoint: () => ({ x: 4, y: 4 }) })),
      };
    }
    setActiveElements(active: unknown) {
      chartMock.active.push(active);
    }
    update() {}
    destroy() {}
  },
  CategoryScale: class {},
  Legend: class {},
  LineController: class {},
  LineElement: class {},
  LinearScale: class {},
  PointElement: class {},
  Tooltip: class {},
}));

function makeSeries(asset: Asset, amounts: readonly [string, string][]): Series {
  return {
    metadata: {
      asset,
      from: 1_800_000_000,
      to: 1_800_000_000 + amounts.length * 3600,
      offset_minutes: 0,
      ledger_seq: '3',
      projected_seq: '3',
      snapshot_at: 1_800_000_000,
      coverage: {
        status: 'complete',
        first_ledger_seq: '1',
        first_occurred_at: 1_800_000_000,
        unclassified_operations: '0',
        opening_known: true,
      },
    },
    bucket: 'hour',
    data: amounts.map(([issued, reclaimed], index) => ({
      start: 1_800_000_000 + index * 3600,
      end: 1_800_000_000 + (index + 1) * 3600,
      metrics: {
        issued,
        reclaimed,
        user_income: '0',
        user_expense: '0',
        internal_transfer: '0',
        operations: '1',
      },
    })),
  };
}

function latestConfiguration(): ChartConfiguration<'line'> {
  return chartMock.configurations.at(-1) as ChartConfiguration<'line'>;
}

describe('economy trend chart', () => {
  it('lets keyboard users select the first, next, and last exact time buckets', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
      () => ({}) as CanvasRenderingContext2D,
    );
    await renderWithProviders(
      <EconomyTrendChart
        series={makeSeries('general', [
          ['0', '0'],
          ['9000000000000000000000', '1000'],
          ['3000', '2000'],
        ])}
      />,
      { station: 'admin', locale: 'en' },
    );
    const slider = screen.getByRole('slider', { name: 'Issuance and retirement time bucket' });
    fireEvent.focus(slider);
    expect(slider).toHaveAttribute('aria-valuenow', '1');
    fireEvent.keyDown(slider, { key: 'ArrowRight' });
    expect(slider).toHaveAttribute('aria-valuenow', '2');
    expect(screen.getByText(/9,000,000,000,000,000,000/)).toBeVisible();
    expect(chartMock.active.at(-1)).toEqual([
      { datasetIndex: 0, index: 1 },
      { datasetIndex: 1, index: 1 },
    ]);
    fireEvent.keyDown(slider, { key: 'End' });
    expect(slider).toHaveAttribute('aria-valuenow', '3');
    fireEvent.keyDown(slider, { key: 'Home' });
    expect(slider).toHaveAttribute('aria-valuenow', '1');
    fireEvent.keyDown(slider, { key: 'ArrowLeft' });
    expect(slider).toHaveAttribute('aria-valuenow', '1');
  });

  it('labels the selected asset and updates contrast colors when the theme changes', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
      () => ({}) as CanvasRenderingContext2D,
    );
    const nativeStyle = window.getComputedStyle;
    vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => {
      if (element !== document.documentElement) return nativeStyle(element);
      const dark = document.documentElement.dataset.theme === 'dark';
      return {
        getPropertyValue: (name: string) =>
          ({
            '--nb-color-text': dark ? '#eef5fb' : '#172536',
            '--nb-color-text-muted': dark ? '#b2c1d0' : '#607084',
            '--nb-color-border': dark ? '#2d4052' : '#d8e2ec',
            '--nb-color-surface-raised': dark ? '#1d2c3c' : '#ffffff',
            '--nb-color-success': dark ? '#64d19e' : '#138a5b',
            '--nb-color-warning': dark ? '#ffca68' : '#aa6800',
          })[name] ?? '',
      } as CSSStyleDeclaration;
    });
    await renderWithProviders(
      <EconomyTrendChart series={makeSeries('sketch_paper', [['2000', '0']])} />,
      { station: 'admin', locale: 'en' },
    );
    const first = latestConfiguration();
    expect(first.options?.scales?.y?.title?.text).toBe('Sketch paper');
    expect(first.data.datasets[0].borderColor).toBe('#138a5b');
    expect(first.options?.scales?.y?.grid?.color).toBe('#d8e2ec');
    const initialCharts = chartMock.configurations.length;
    act(() => {
      document.documentElement.dataset.theme = 'dark';
    });
    await waitFor(() => expect(chartMock.configurations.length).toBeGreaterThan(initialCharts));
    const dark = latestConfiguration();
    expect(dark.data.datasets[0].borderColor).toBe('#64d19e');
    expect(dark.options?.scales?.y?.ticks?.color).toBe('#b2c1d0');
    expect(dark.options?.scales?.y?.grid?.color).toBe('#2d4052');
  });

  it('shows a meaningful empty state and keeps a single all-zero point selectable', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
      () => ({}) as CanvasRenderingContext2D,
    );
    const empty = await renderWithProviders(<EconomyTrendChart series={makeSeries('game', [])} />, {
      station: 'admin',
      locale: 'en',
    });
    expect(screen.getByText('No trend data for this interval.')).toBeVisible();
    expect(screen.queryByRole('slider')).toBeNull();
    empty.unmount();
    await renderWithProviders(<EconomyTrendChart series={makeSeries('game', [['0', '0']])} />, {
      station: 'admin',
      locale: 'en',
    });
    const slider = screen.getByRole('slider');
    fireEvent.focus(slider);
    expect(slider).toHaveAttribute('aria-valuemax', '1');
    expect(screen.getByText(/New issuance 0 · Permanent retirement 0/)).toBeVisible();
    expect(latestConfiguration().data.datasets[0].data).toEqual([0]);
  });
});
