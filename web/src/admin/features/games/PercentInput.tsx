import { useState, type ComponentProps } from 'react';
import { Affix } from '@shared/components/ui';

type Props = Omit<ComponentProps<typeof Affix>, 'value' | 'onChange'> & {
  basisPoints: number;
  onBasisChange: (value: number) => void;
};
export function PercentInput({ basisPoints, onBasisChange, max = 100, ...props }: Props) {
  const [draft, setDraft] = useState<{ raw: string; parsed: number }>();
  const value =
    draft && Object.is(basisPoints, draft.parsed)
      ? draft.raw
      : Number.isFinite(basisPoints)
        ? String(basisPoints / 100)
        : '';
  return (
    <Affix
      {...props}
      type="text"
      inputMode="decimal"
      value={value}
      aria-invalid={Number.isNaN(basisPoints) || undefined}
      onChange={(event) => {
        const raw = event.target.value;
        const match = /^(0|[1-9]\d{0,2})(?:\.(\d{0,2}))?$/.exec(raw);
        const points = match
          ? Number(match[1]) * 100 + Number((match[2] ?? '').padEnd(2, '0'))
          : Number.NaN;
        const parsed = points <= Number(max) * 100 ? points : Number.NaN;
        setDraft({ raw, parsed });
        onBasisChange(parsed);
      }}
    />
  );
}
