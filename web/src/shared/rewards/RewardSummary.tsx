import { useTranslation } from 'react-i18next';
import { ExactCredits } from '../../user/features/core/components';
import type { Asset, Item, Result } from './api';
import { assetName } from './copy';
export function RewardSummary({
  items,
  assets,
  results = false,
}: {
  items: Item[] | Result[];
  assets: Asset[];
  results?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const english = i18n.language.startsWith('en');
  return (
    <ul className="reward-summary">
      {items.map((item) => {
        const r = results ? (item as Result) : undefined;
        return (
          <li key={item.asset}>
            <span>{assetName(assets, item.asset, english)}</span>
            <span className={r ? 'reward-number reward-number--grant' : 'reward-number'}>
              {r ? '+' : ''}
              <ExactCredits value={r ? r.granted : item.quantity} />
            </span>
            {r?.converted && (
              <span className="reward-detail">
                {t('common.rewards.converted')} {assetName(assets, r.converted.asset, english)}{' '}
                <span className="reward-number reward-number--grant">
                  +<ExactCredits value={r.converted.quantity} />
                </span>
              </span>
            )}
            {r?.skipped && (
              <span className="reward-detail">
                {t('common.rewards.skipped')} <ExactCredits value={r.skipped} />
              </span>
            )}
          </li>
        );
      })}
    </ul>
  );
}
