import { formatCount } from '@shared/utils/formatNumber';

export interface ChartRow {
  label: string;
  count: string;
  details?: string[];
}
export const PLOT_SCALE = 1_000_000;
export function chartRatio(value: string, maximum: bigint): number {
  return maximum > 0n ? Number((BigInt(value) * BigInt(PLOT_SCALE)) / maximum) : 0;
}
export function countAtRatio(value: number, maximum: bigint): string {
  return maximum > 0n && Number.isFinite(value)
    ? ((maximum * BigInt(Math.max(0, Math.round(value)))) / BigInt(PLOT_SCALE)).toString()
    : '0';
}
function xml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&apos;');
}
export interface ChartPalette {
  background: string;
  foreground: string;
  muted: string;
  border: string;
  bars: string[];
}
export type TextMeasure = (text: string, size: number) => number;
const estimateWidth: TextMeasure = (text, size) =>
  [...text].reduce((sum, character) => {
    if (character === ' ') return sum + size * 0.4;
    if (/[MW@]/.test(character) || (character.codePointAt(0) ?? 0) > 255) return sum + size * 1.05;
    return sum + size * 0.68;
  }, 0);
export function wrapChartText(
  value: string,
  width: number,
  size: number,
  measure: TextMeasure = estimateWidth,
): string[] {
  const lines: string[] = [];
  let line = '';
  for (const character of value) {
    if (line && measure(line + character, size) > width) {
      lines.push(line);
      line = character;
    } else line += character;
  }
  if (line || !lines.length) lines.push(line);
  return lines;
}
export function browserTextMeasure(): TextMeasure | undefined {
  let context: CanvasRenderingContext2D | null;
  try {
    context = document.createElement('canvas').getContext('2d');
  } catch {
    context = null;
  }
  if (!context) return undefined;
  return (text, size) => {
    context.font = size + 'px system-ui,sans-serif';
    return context.measureText(text).width;
  };
}
export function chartSVG(
  title: string,
  subtitle: string,
  rows: readonly ChartRow[],
  palette: ChartPalette,
  options: { measure?: TextMeasure; valueLabel?: string } = {},
): string {
  const measure = options.measure ?? estimateWidth;
  const titleLines = wrapChartText(title, 1536, 30, measure);
  const subtitleLines = wrapChartText(subtitle, 1536, 22, measure);
  const subtitleY = 40 + titleLines.length * 38;
  let y = subtitleY + subtitleLines.length * 28 + 54;
  const maximum = rows.reduce(
    (value, row) => (BigInt(row.count) > value ? BigInt(row.count) : value),
    0n,
  );
  const header =
    titleLines
      .map(
        (line, index) =>
          '<text x="32" y="' +
          (40 + index * 38) +
          '" font-size="30" font-weight="600" fill="' +
          xml(palette.foreground) +
          '" xml:space="preserve">' +
          xml(line) +
          '</text>',
      )
      .join('') +
    subtitleLines
      .map(
        (line, index) =>
          '<text class="chart-pool-name" x="32" y="' +
          (subtitleY + index * 28) +
          '" font-size="22" fill="' +
          xml(palette.muted) +
          '" xml:space="preserve">' +
          xml(line) +
          '</text>',
      )
      .join('') +
    (options.valueLabel
      ? '<text x="1560" y="' +
        (y - 20) +
        '" text-anchor="end" font-size="20" fill="' +
        xml(palette.muted) +
        '">' +
        xml(options.valueLabel) +
        '</text>'
      : '');
  const bars = rows
    .map((row, index) => {
      const labels = wrapChartText(row.label, 392, 22, measure);
      const details = (row.details ?? []).flatMap((line) => wrapChartText(line, 392, 18, measure));
      const height = Math.max(64, labels.length * 28 + details.length * 24 + 16);
      const center = y + height / 2;
      const value = formatCount(row.count).display;
      let countSize = 22;
      while (countSize > 10 && measure(value, countSize) > 440) countSize--;
      const labelText =
        '<text class="chart-row-label" x="32" y="' +
        (y + 22) +
        '" font-size="22" fill="' +
        xml(palette.foreground) +
        '" xml:space="preserve">' +
        labels
          .map(
            (line, lineIndex) =>
              '<tspan x="32" dy="' + (lineIndex ? 28 : 0) + '">' + xml(line) + '</tspan>',
          )
          .join('') +
        '</text>';
      const detailText = details
        .map(
          (line, lineIndex) =>
            '<text x="32" y="' +
            (y + labels.length * 28 + 18 + lineIndex * 24) +
            '" font-size="18" fill="' +
            xml(palette.muted) +
            '" xml:space="preserve">' +
            xml(line) +
            '</text>',
        )
        .join('');
      const result =
        '<g><title>' +
        xml([row.label, value, ...(row.details ?? [])].join(' · ')) +
        '</title>' +
        labelText +
        detailText +
        '<rect x="460" y="' +
        (center - 16) +
        '" width="' +
        ((chartRatio(row.count, maximum) / PLOT_SCALE) * 620).toFixed(3) +
        '" height="32" rx="6" fill="' +
        xml(palette.bars[index % palette.bars.length] ?? palette.foreground) +
        '"/>' +
        '<text class="chart-row-count" x="1560" y="' +
        center +
        '" text-anchor="end" dominant-baseline="middle" font-size="' +
        countSize +
        '" fill="' +
        xml(palette.foreground) +
        '">' +
        xml(value) +
        '</text>' +
        '<line x1="32" x2="1568" y1="' +
        (y + height - 4) +
        '" y2="' +
        (y + height - 4) +
        '" stroke="' +
        xml(palette.border) +
        '"/></g>';
      y += height;
      return result;
    })
    .join('');
  const height = y + 32;
  return (
    '<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="' +
    height +
    '" viewBox="0 0 1600 ' +
    height +
    '" role="img"><title>' +
    xml(title) +
    '</title><rect width="1600" height="' +
    height +
    '" fill="' +
    xml(palette.background) +
    '"/><g font-family="system-ui,sans-serif">' +
    header +
    bars +
    '</g></svg>'
  );
}
export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
