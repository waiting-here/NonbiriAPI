import { useEffect, useId, useMemo, useRef, useState } from 'react';
import {
  CategoryScale,
  Chart,
  Legend,
  LineController,
  LineElement,
  LinearScale,
  PointElement,
  Tooltip,
  type ChartConfiguration,
} from 'chart.js';
import { displayAmount, siteDateTime, type Series } from './api';
import { chartAmountAtRatio, chartRatio } from './chartMath';
import { assetLabel, useEconomyText } from './copy';

Chart.register(
  CategoryScale,
  LinearScale,
  LineController,
  LineElement,
  PointElement,
  Tooltip,
  Legend,
);

type FlowKey = 'issued' | 'reclaimed';
const PLOT_SCALE = 1_000_000;

function chartColors() {
  const style = getComputedStyle(document.documentElement);
  const token = (name: string, fallback: string) => style.getPropertyValue(name).trim() || fallback;
  return {
    foreground: token('--nb-color-text', '#172536'),
    muted: token('--nb-color-text-muted', '#607084'),
    grid: token('--nb-color-border', '#d8e2ec'),
    surface: token('--nb-color-surface-raised', '#ffffff'),
    issued: token('--nb-color-success', '#138a5b'),
    reclaimed: token('--nb-color-warning', '#aa6800'),
  };
}

export function EconomyTrendChart({ series }: { readonly series: Series }) {
  const t = useEconomyText();
  const captionId = useId();
  const canvas = useRef<HTMLCanvasElement>(null);
  const chart = useRef<Chart<'line'> | null>(null);
  const [selected, setSelected] = useState<number | null>(null);
  const [themeRevision, setThemeRevision] = useState(0);
  const points = series.data;
  const selectedIndex =
    selected === null || points.length === 0 ? null : Math.min(selected, points.length - 1);
  const selectedPoint = selectedIndex === null ? undefined : points[selectedIndex];
  const labels = useMemo(
    () =>
      points.map((point) =>
        siteDateTime(point.start, series.metadata.offset_minutes).slice(0, 16).replace('T', ' '),
      ),
    [points, series.metadata.offset_minutes],
  );
  const selectedText = selectedPoint
    ? `${labels[selectedIndex!]} · ${t('新增发行', 'New issuance')} ${displayAmount(selectedPoint.metrics.issued)} · ${t('永久回收', 'Permanent retirement')} ${displayAmount(selectedPoint.metrics.reclaimed)}`
    : '';

  useEffect(() => {
    const observer = new MutationObserver(() => setThemeRevision((value) => value + 1));
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const element = canvas.current;
    if (!element || points.length === 0) return undefined;
    let context: CanvasRenderingContext2D | null;
    try {
      context = element.getContext('2d');
    } catch {
      context = null;
    }
    if (!context) return undefined;

    const colors = chartColors();
    const maximum = points.reduce((max, point) => {
      const issued = BigInt(point.metrics.issued);
      const reclaimed = BigInt(point.metrics.reclaimed);
      return issued > max
        ? reclaimed > issued
          ? reclaimed
          : issued
        : reclaimed > max
          ? reclaimed
          : max;
    }, 0n);
    const values = (key: FlowKey) => points.map((point) => chartRatio(point.metrics[key], maximum));
    const exact = (key: FlowKey, index: number) =>
      displayAmount(points[index]?.metrics[key] ?? '0');
    const configuration: ChartConfiguration<'line'> = {
      type: 'line',
      data: {
        labels,
        datasets: [
          {
            label: t('新增发行', 'New issuance'),
            data: values('issued'),
            borderColor: colors.issued,
            backgroundColor: colors.issued,
            pointRadius: 4,
            pointHoverRadius: 6,
            pointHitRadius: 12,
            tension: 0.15,
          },
          {
            label: t('永久回收', 'Permanent retirement'),
            data: values('reclaimed'),
            borderColor: colors.reclaimed,
            backgroundColor: colors.reclaimed,
            borderDash: [5, 3],
            pointRadius: 4,
            pointHoverRadius: 6,
            pointHitRadius: 12,
            tension: 0.15,
          },
        ],
      },
      options: {
        color: colors.foreground,
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        interaction: { mode: 'index', intersect: false },
        onClick: (_event, elements) => {
          if (elements[0]) setSelected(elements[0].index);
        },
        plugins: {
          legend: { display: true, labels: { color: colors.foreground } },
          tooltip: {
            backgroundColor: colors.surface,
            titleColor: colors.foreground,
            bodyColor: colors.foreground,
            borderColor: colors.grid,
            borderWidth: 1,
            callbacks: {
              title: (items) => (items[0] ? labels[items[0].dataIndex] : ''),
              label: (item) =>
                `${item.dataset.label ?? ''}: ${exact(item.datasetIndex === 0 ? 'issued' : 'reclaimed', item.dataIndex)}`,
            },
          },
        },
        scales: {
          x: {
            grid: { color: colors.grid },
            ticks: {
              color: colors.muted,
              maxTicksLimit: 12,
              maxRotation: 0,
              autoSkipPadding: 16,
              callback: (value) => labels[Number(value)]?.split(' ') ?? '',
            },
            title: { display: true, color: colors.foreground, text: t('业务时间', 'Site time') },
          },
          y: {
            beginAtZero: true,
            max: PLOT_SCALE,
            grid: { color: colors.grid },
            ticks: {
              color: colors.muted,
              callback: (value) =>
                maximum === 0n && Number(value) !== 0
                  ? ''
                  : displayAmount(chartAmountAtRatio(Number(value), maximum)),
            },
            title: {
              display: true,
              color: colors.foreground,
              text: assetLabel(series.metadata.asset, t),
            },
          },
        },
      },
    };
    chart.current = new Chart(context, configuration);
    return () => {
      chart.current?.destroy();
      chart.current = null;
    };
  }, [series, points, labels, t, themeRevision]);

  useEffect(() => {
    const current = chart.current;
    if (!current || selectedIndex === null) return;
    const active = [0, 1].map((datasetIndex) => ({ datasetIndex, index: selectedIndex }));
    const position = (
      current.getDatasetMeta(0).data[selectedIndex] as PointElement | undefined
    )?.getCenterPoint();
    current.setActiveElements(active);
    if (position) current.tooltip?.setActiveElements(active, position);
    current.update('none');
  }, [selectedIndex, series, themeRevision]);

  function selectFromKeyboard(key: string): boolean {
    if (points.length === 0) return false;
    const last = points.length - 1;
    switch (key) {
      case 'ArrowRight':
        setSelected((value) => Math.min((value ?? -1) + 1, last));
        return true;
      case 'ArrowLeft':
        setSelected((value) => Math.max((value ?? 0) - 1, 0));
        return true;
      case 'Home':
        setSelected(0);
        return true;
      case 'End':
        setSelected(last);
        return true;
      default:
        return false;
    }
  }

  return (
    <figure className="audit-chart">
      {points.length > 0 ? (
        <>
          <div className="audit-chart-canvas">
            <canvas
              ref={canvas}
              role="slider"
              tabIndex={0}
              aria-label={t('发行与回收趋势时段', 'Issuance and retirement time bucket')}
              aria-describedby={captionId}
              aria-valuemin={1}
              aria-valuemax={points.length}
              aria-valuenow={(selectedIndex ?? 0) + 1}
              aria-valuetext={selectedText || labels[0]}
              onFocus={() => setSelected((value) => value ?? 0)}
              onKeyDown={(event) => {
                if (selectFromKeyboard(event.key)) event.preventDefault();
              }}
            />
          </div>
          <output className="audit-chart-selection" aria-live="polite">
            {selectedText ||
              t('聚焦图表以查看首个时段。', 'Focus the chart to inspect the first time bucket.')}
          </output>
        </>
      ) : (
        <p>{t('所选区间没有趋势数据。', 'No trend data for this interval.')}</p>
      )}
      <figcaption id={captionId}>
        {t(
          '绿色实线表示新增发行，橙色虚线表示永久回收。用左右方向键切换时段，Home 和 End 跳到首尾；下表列出精确金额。',
          'Green solid line shows new issuance; orange dashed line shows permanent retirement. Use Left and Right to select a time bucket, Home and End for the first and last. Exact amounts are also in the table.',
        )}
      </figcaption>
    </figure>
  );
}
