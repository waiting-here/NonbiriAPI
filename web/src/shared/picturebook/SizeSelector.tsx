import { useId } from 'react';
import { usePictureBookText } from './copy';
import type { ParameterValues } from './parameters';
import type { SizeCapability, SizeCombination } from './capabilities';

function rowValues(row: SizeCombination): ParameterValues {
  return {
    aspect_ratio: row.ratio ?? '',
    resolution: row.resolution ?? '',
    size: row.width && row.height ? `${row.width}x${row.height}` : '',
  };
}

export function SizeSelector({
  capability,
  values,
  onChange,
}: {
  readonly capability: SizeCapability;
  readonly values: ParameterValues;
  readonly onChange: (next: ParameterValues) => void;
}) {
  const t = usePictureBookText();
  const id = useId();
  const rows = capability.combinations ?? [];
  const auto = values.size === 'auto';
  const ratios = [
    ...new Set(rows.map((row) => row.ratio).filter((ratio): ratio is string => Boolean(ratio))),
  ];
  const selectedRatio = auto ? '' : (values.aspect_ratio ?? '');
  const resolutions = [
    ...new Set(
      rows
        .filter((row) => row.ratio === selectedRatio)
        .map((row) => row.resolution)
        .filter((resolution): resolution is string => Boolean(resolution)),
    ),
  ];
  const choose = (row: SizeCombination) => onChange(rowValues(row));
  const explicit = () => {
    const first = rows[0];
    if (first) choose(first);
    else if (capability.width && capability.height)
      onChange({
        aspect_ratio: '',
        resolution: '',
        size: `${capability.width.minimum}x${capability.height.minimum}`,
      });
  };
  return (
    <fieldset className="picturebook-field">
      <legend>{t('尺寸', 'Size')}</legend>
      {capability.auto ? (
        <label>
          <input
            type="checkbox"
            checked={auto}
            onChange={(event) =>
              event.target.checked
                ? onChange({ aspect_ratio: '', resolution: '', size: 'auto' })
                : explicit()
            }
          />{' '}
          {t('自动选择尺寸', 'Choose size automatically')}
        </label>
      ) : null}
      {!auto && capability.mode === 'width_height' && capability.width && capability.height ? (
        <div className="picturebook-dimensions">
          {(['width', 'height'] as const).map((axis) => {
            const range = capability[axis]!;
            const [width = '', height = ''] = (values.size ?? '').split('x');
            return (
              <div className="picturebook-field" key={axis}>
                <label htmlFor={id + axis}>
                  {axis === 'width' ? t('宽度', 'Width') : t('高度', 'Height')}
                </label>
                <input
                  id={id + axis}
                  type="number"
                  inputMode="numeric"
                  autoComplete="off"
                  value={axis === 'width' ? width : height}
                  min={range.minimum}
                  max={range.maximum}
                  step={range.step}
                  onChange={(event) => {
                    const next =
                      axis === 'width' ? [event.target.value, height] : [width, event.target.value];
                    onChange({ size: next.join('x') });
                  }}
                />
                <small>
                  {range.minimum} – {range.maximum} px · {t('步长', 'Step')} {range.step}
                </small>
              </div>
            );
          })}
        </div>
      ) : null}
      {!auto && capability.mode !== 'width_height' ? (
        <>
          <label htmlFor={id + 'ratio'}>{t('宽高比', 'Aspect ratio')}</label>
          <select
            id={id + 'ratio'}
            value={selectedRatio}
            onChange={(event) => {
              const row = rows.find((item) => item.ratio === event.target.value);
              if (row) choose(row);
            }}
          >
            {ratios.map((ratio) => (
              <option key={ratio} value={ratio}>
                {ratio}
              </option>
            ))}
          </select>
          {capability.mode !== 'ratio_size_map' ? (
            <>
              <label htmlFor={id + 'resolution'}>{t('分辨率', 'Resolution')}</label>
              <select
                id={id + 'resolution'}
                value={values.resolution ?? ''}
                onChange={(event) => {
                  const row = rows.find(
                    (item) =>
                      item.ratio === selectedRatio && item.resolution === event.target.value,
                  );
                  if (row) choose(row);
                }}
              >
                {resolutions.map((resolution) => (
                  <option key={resolution} value={resolution}>
                    {resolution}
                  </option>
                ))}
              </select>
            </>
          ) : null}
          {values.size ? <output>{values.size}</output> : null}
        </>
      ) : null}
    </fieldset>
  );
}
