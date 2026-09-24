import { useState } from 'react';
import { decimalAmount, scaledAmount } from './amounts';

export function AmountInput({
  label,
  value,
  percent = false,
  onChange,
  zh,
}: {
  label: string;
  value: string;
  percent?: boolean;
  onChange: (value: string) => void;
  zh: boolean;
}) {
  const places = percent ? 2 : 3;
  const [draft, setDraft] = useState(() => decimalAmount(value, places));
  return (
    <label>
      {label}
      <input
        inputMode="decimal"
        value={draft}
        required
        maxLength={43}
        onChange={(e) => {
          const raw = e.target.value;
          setDraft(raw);
          const parsed = scaledAmount(raw, places);
          const valid =
            parsed !== null && (!percent || (BigInt(parsed) >= 1n && BigInt(parsed) <= 10000n));
          e.target.setCustomValidity(
            valid
              ? ''
              : zh
                ? percent
                  ? '请输入 0.01 到 100 的百分比，最多两位小数。'
                  : '请输入非负积分，最多三位小数。'
                : percent
                  ? 'Enter a percentage from 0.01 to 100 with at most two decimal places.'
                  : 'Enter non-negative credits with at most three decimal places.',
          );
          onChange(valid ? parsed : '');
        }}
      />
    </label>
  );
}
