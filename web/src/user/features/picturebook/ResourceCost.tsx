import { useTranslation } from 'react-i18next';
import { ExactCount } from '../core/components';

export function ResourceCost({ value }: { value: { paper: string; brush: string } }) {
  const { t } = useTranslation();
  return (
    <span className="picturebook-resource-cost">
      <span>
        <ExactCount value={value.paper} /> {t('common.picturebook.units.paper')}
      </span>
      <span>
        {' '}
        + <ExactCount value={value.brush} /> {t('common.picturebook.units.brushes')}
      </span>
    </span>
  );
}
