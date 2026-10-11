import { NumberField } from './NumberField';
import { useFatFishText } from './copy';

const directions = [
  [7, '↗', 'north_east'],
  [0, '→', 'east'],
  [1, '↘', 'south_east'],
  [2, '↓', 'south'],
  [3, '↙', 'south_west'],
  [4, '←', 'west'],
  [5, '↖', 'north_west'],
  [6, '↑', 'north'],
] as const;
export function HeadingInput({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number;
  onChange(value: number): void;
}) {
  const text = useFatFishText();
  return (
    <fieldset className="fatfish-heading">
      <legend>{label}</legend>
      <div className="fatfish-heading-buttons" role="group" aria-label={label}>
        {directions.map(([heading, arrow, key]) => (
          <button
            type="button"
            key={key}
            aria-label={text(key)}
            aria-pressed={value === heading * 512}
            onClick={() => onChange(heading * 512)}
          >
            {arrow}
          </button>
        ))}
      </div>
      <NumberField
        label={text('heading_degrees')}
        value={Math.round((value * 3600) / 4096) / 10}
        min={0}
        max={359.9}
        step={0.1}
        onChange={(degrees) => {
          if (degrees >= 0 && degrees < 360) onChange(Math.round((degrees * 4096) / 360) % 4096);
        }}
      />
    </fieldset>
  );
}
