import { useNavigation } from 'react-router';
import { useTranslation } from 'react-i18next';

export function NavigationProgress() {
  const navigation = useNavigation();
  const { t } = useTranslation();
  if (navigation.state === 'idle') return null;
  return (
    <div className="nb-navigation-progress" role="progressbar" aria-label={t('common.loading')} />
  );
}
