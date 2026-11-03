import { useTranslation } from 'react-i18next';
import { Button, Field } from '@shared/components/ui';
import type { Asset, Item } from './api';
import { assetName } from './copy';

export function RewardEditor({
  value,
  assets,
  onChange,
  disabled = false,
}: {
  value: Item[];
  assets: Asset[];
  onChange: (value: Item[]) => void;
  disabled?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const available = assets.filter((asset) => !value.some((item) => item.asset === asset.id));
  return (
    <div className="reward-stack">
      {value.map((item, index) => (
        <div className="reward-item" key={index}>
          <Field label={t('common.rewards.asset')}>
            {(props) => (
              <select
                {...props}
                disabled={disabled}
                value={item.asset}
                onChange={(event) =>
                  onChange(
                    value.map((entry, i) =>
                      i === index ? { asset: event.target.value, quantity: '1' } : entry,
                    ),
                  )
                }
              >
                {assets
                  .filter((asset) => asset.id === item.asset || available.includes(asset))
                  .map((asset) => (
                    <option key={asset.id} value={asset.id}>
                      {assetName(assets, asset.id, i18n.language.startsWith('en'))}
                    </option>
                  ))}
              </select>
            )}
          </Field>
          <Field label={t('common.rewards.quantity')}>
            {(props) => (
              <input
                {...props}
                required
                disabled={disabled}
                inputMode="decimal"
                value={item.quantity}
                onChange={(event) =>
                  onChange(
                    value.map((entry, i) =>
                      i === index ? { ...entry, quantity: event.target.value } : entry,
                    ),
                  )
                }
              />
            )}
          </Field>
          <Button
            type="button"
            disabled={disabled || value.length === 1}
            onClick={() => onChange(value.filter((_, i) => i !== index))}
          >
            {t('common.rewards.remove')}
          </Button>
        </div>
      ))}
      <div>
        <Button
          type="button"
          disabled={disabled || value.length >= 32 || available.length === 0}
          onClick={() => {
            const asset = available[0];
            if (asset) onChange([...value, { asset: asset.id, quantity: '1' }]);
          }}
        >
          {t('common.rewards.addReward')}
        </Button>
      </div>
    </div>
  );
}
