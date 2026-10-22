import { useTranslation } from 'react-i18next';
import { NATIVE_SERVICE_LABEL_KEYS, type NativeService } from '@shared/nativeServices';
import { MODEL_TYPE_LABEL_KEYS } from '@shared/modelTypes';
import type { ServiceRoute } from '@shared/connections';
import './ModelTypesField.css';

export function NativeServicesSummary({ value }: { value: readonly NativeService[] }) {
  const { t } = useTranslation();
  return (
    <span className="model-types-summary">
      {value.length
        ? value.map((service) => <span key={service}>{t(NATIVE_SERVICE_LABEL_KEYS[service])}</span>)
        : t('common.nativeServices.pending')}
    </span>
  );
}
export function ServiceRoutesSummary({ value }: { value: readonly ServiceRoute[] }) {
  const { t } = useTranslation();
  return (
    <ul className="nb-list">
      {value.map((route) => (
        <li key={route.operation + ':' + route.native_service}>
          <span>
            {t(MODEL_TYPE_LABEL_KEYS[route.operation])} →{' '}
            {t(NATIVE_SERVICE_LABEL_KEYS[route.native_service])} ·{' '}
            {route.mode === 'native'
              ? t('common.connections.nativeRoute')
              : t('common.connections.convertedRoute')}
          </span>
        </li>
      ))}
    </ul>
  );
}
