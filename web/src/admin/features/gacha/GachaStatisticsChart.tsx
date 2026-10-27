import { useEffect, useRef, useState } from 'react';
import {
  BarController,
  BarElement,
  CategoryScale,
  Chart,
  LinearScale,
  Tooltip,
  type ChartConfiguration,
} from 'chart.js';
import { useTranslation } from 'react-i18next';
import { Button, DataTable, Fold, Note } from '@shared/components/ui';
import { EmptyState } from '@shared/components/States';
import { formatCompact, formatCount } from '@shared/utils/formatNumber';
import {
  chartRatio,
  chartSVG,
  browserTextMeasure,
  countAtRatio,
  downloadBlob,
  PLOT_SCALE,
  type ChartPalette,
  type ChartRow,
} from './chartExport';

Chart.register(BarController, BarElement, CategoryScale, LinearScale, Tooltip);
function palette(): ChartPalette {
  const style = getComputedStyle(document.documentElement);
  const token = (name: string) => style.getPropertyValue('--nb-color-' + name).trim();
  return {
    background: token('surface'),
    foreground: token('text'),
    muted: token('text-muted'),
    border: token('border'),
    bars: ['info', 'accent', 'success', 'warning', 'danger', 'text-muted'].map(token),
  };
}
export function GachaStatisticsChart({
  title,
  poolName,
  metric,
  rows,
  filename,
}: {
  title: string;
  poolName: string;
  metric: string;
  rows: readonly ChartRow[];
  filename: string;
}) {
  const { t } = useTranslation();
  const canvas = useRef<HTMLCanvasElement>(null);
  const [theme, setTheme] = useState(0);
  const [downloadError, setDownloadError] = useState(false);
  const [exporting, setExporting] = useState(false);
  useEffect(() => {
    const observer = new MutationObserver(() => setTheme((value) => value + 1));
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    const element = canvas.current;
    if (!element || rows.length === 0) return;
    let context: CanvasRenderingContext2D | null;
    try {
      context = element.getContext('2d');
    } catch {
      context = null;
    }
    if (!context) return;
    const colors = palette();
    const maximum = rows.reduce(
      (value, row) => (BigInt(row.count) > value ? BigInt(row.count) : value),
      0n,
    );
    const labels = rows.map((row) =>
      [...row.label].length > 22 ? [...row.label].slice(0, 21).join('') + '…' : row.label,
    );
    const labelFont = '12px ' + getComputedStyle(element).fontFamily;
    const measure = (value: string) => {
      context.font = labelFont;
      return context.measureText(value).width;
    };
    // Keep exact values in tooltips, the data table and downloads. A count that
    // cannot fit beside a bar must not run outside the canvas on small screens.
    const valueLabels = rows.map((row) => {
      const value = formatCompact(row.count).display;
      return measure(value) <= 84 ? value : '';
    });
    const valueSpace = Math.max(0, ...valueLabels.map(measure)) + 16;
    const config: ChartConfiguration<'bar'> = {
      type: 'bar',
      data: {
        labels,
        datasets: [
          {
            label: metric,
            data: rows.map((row) => chartRatio(row.count, maximum)),
            backgroundColor: rows.map((_, index) => colors.bars[index % colors.bars.length]!),
            borderRadius: 5,
            maxBarThickness: 30,
          },
        ],
      },
      options: {
        indexAxis: 'y',
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        layout: { padding: { right: valueSpace } },
        color: colors.foreground,
        interaction: { mode: 'nearest', intersect: false },
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: colors.background,
            titleColor: colors.foreground,
            bodyColor: colors.foreground,
            borderColor: colors.border,
            borderWidth: 1,
            callbacks: {
              title: (items) => rows[items[0]?.dataIndex ?? 0]?.label ?? '',
              label: (item) => metric + ': ' + formatCount(rows[item.dataIndex]!.count).display,
              afterLabel: (item) => rows[item.dataIndex]?.details ?? [],
            },
          },
        },
        scales: {
          y: {
            grid: { display: false },
            ticks: {
              color: colors.foreground,
              font: { size: 12 },
              callback: function (value) {
                const label = labels[Number(value)] ?? '';
                const limit = Math.max(40, this.chart.width * 0.36 - 16);
                const parts = [...label];
                while (parts.length > 1 && measure(parts.join('')) > limit) parts.pop();
                return parts.length < [...label].length ? parts.join('') + '…' : label;
              },
            },
            border: { display: false },
          },
          x: {
            title: { display: true, text: metric, color: colors.muted },
            min: 0,
            max: maximum > 0n ? PLOT_SCALE : 1,
            grid: { color: colors.border },
            ticks: {
              color: colors.muted,
              maxTicksLimit: 3,
              maxRotation: 0,
              minRotation: 0,
              callback: function (value) {
                const label =
                  maximum === 0n
                    ? Number(value) === 0
                      ? '0'
                      : ''
                    : formatCompact(countAtRatio(Number(value), maximum)).display;
                return measure(label) <= Math.max(24, this.width / 3 - 8) ? label : '';
              },
            },
          },
        },
      },
      plugins: [
        {
          id: 'gachaStatisticsSurface',
          beforeDraw: (chart) => {
            const ctx = chart.ctx;
            ctx.save();
            ctx.globalCompositeOperation = 'destination-over';
            ctx.fillStyle = colors.background;
            ctx.fillRect(0, 0, chart.width, chart.height);
            ctx.restore();
          },
        },
        {
          id: 'gachaStatisticsCounts',
          afterDatasetsDraw: (chart) => {
            const ctx = chart.ctx;
            ctx.save();
            ctx.fillStyle = colors.foreground;
            ctx.font = labelFont;
            ctx.textBaseline = 'middle';
            chart.getDatasetMeta(0).data.forEach((bar, index) => {
              const position = bar as BarElement;
              ctx.fillText(valueLabels[index]!, position.x + 8, position.y);
            });
            ctx.restore();
          },
        },
      ],
    };
    const chart = new Chart(context, config);
    return () => chart.destroy();
  }, [rows, metric, theme]);
  const exportSVG = () =>
    chartSVG(title, poolName, rows, palette(), {
      measure: browserTextMeasure(),
      valueLabel: metric,
    });
  const png = () => {
    if (exporting) return;
    setDownloadError(false);
    setExporting(true);
    let imageURL: string | undefined;
    const fail = () => {
      if (imageURL) URL.revokeObjectURL(imageURL);
      setDownloadError(true);
      setExporting(false);
    };
    try {
      imageURL = URL.createObjectURL(
        new Blob([exportSVG()], { type: 'image/svg+xml;charset=utf-8' }),
      );
      const image = new Image();
      image.onerror = fail;
      image.onload = () => {
        try {
          const output = document.createElement('canvas');
          output.width = image.naturalWidth;
          output.height = image.naturalHeight;
          const context = output.getContext('2d');
          if (!context) {
            fail();
            return;
          }
          context.drawImage(image, 0, 0);
          if (imageURL) URL.revokeObjectURL(imageURL);
          imageURL = undefined;
          output.toBlob((blob) => {
            if (blob) downloadBlob(blob, filename + '.png');
            else setDownloadError(true);
            setExporting(false);
          }, 'image/png');
        } catch {
          fail();
        }
      };
      image.src = imageURL;
    } catch {
      fail();
    }
  };
  return (
    <figure className="gacha-stats-chart">
      <div className="gacha-stats-chart__head">
        <h3>{title}</h3>
        {rows.length ? (
          <div className="nb-actions">
            <Button size="sm" onClick={png} disabled={exporting}>
              {t(exporting ? 'common.working' : 'admin.gachaStats.downloadPNG')}
            </Button>
            <Button
              size="sm"
              onClick={() =>
                downloadBlob(
                  new Blob([exportSVG()], {
                    type: 'image/svg+xml;charset=utf-8',
                  }),
                  filename + '.svg',
                )
              }
            >
              {t('admin.gachaStats.downloadSVG')}
            </Button>
          </div>
        ) : null}
      </div>
      {downloadError ? <Note tone="bad">{t('admin.gachaStats.chartDownloadError')}</Note> : null}
      {rows.length ? (
        <>
          <div className="gacha-stats-chart__canvas">
            <canvas ref={canvas} role="img" aria-label={title} />
          </div>
          <Fold title={t('admin.gachaStats.chartData')}>
            <DataTable
              caption={title}
              rows={rows}
              rowKey={(row) => row.label}
              columns={[
                {
                  key: 'label',
                  header: t('admin.gachaStats.category'),
                  cell: 'title',
                  render: (row) => <span className="gacha-stats-name">{row.label}</span>,
                },
                {
                  key: 'count',
                  header: metric,
                  cell: 'meta',
                  mobileLabel: metric,
                  align: 'num',
                  render: (row) => (
                    <span className="gacha-stats-count">{formatCount(row.count).display}</span>
                  ),
                },
              ]}
            />
          </Fold>
        </>
      ) : (
        <EmptyState
          title={t('admin.gachaStats.noChartTitle')}
          body={t('admin.gachaStats.noChartBody')}
        />
      )}
    </figure>
  );
}
