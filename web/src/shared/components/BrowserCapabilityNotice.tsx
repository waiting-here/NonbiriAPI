import { useTranslation } from 'react-i18next';
import type { BrowserCapabilityResult } from '@shared/operations/browserCapabilities';
const labels = {
  secureRandom: 'common.capability.secureRandom',
  sessionStorage: 'common.capability.sessionStorage',
  indexedDB: 'common.capability.indexedDB',
  locks: 'common.capability.locks',
  broadcastChannel: 'common.capability.broadcastChannel',
} as const;
export function BrowserCapabilityNotice({ result }: { result: BrowserCapabilityResult }) {
  const { t } = useTranslation();
  if (result.available) return null;
  return (
    <div className="nb-operation-feedback nb-operation-feedback--failed" role="alert">
      <p>{t('common.capability.body')}</p>
      <ul>
        {result.missing.map((capability) => (
          <li key={capability}>{t(labels[capability])}</li>
        ))}
      </ul>
      <p>{t('common.capability.next')}</p>
    </div>
  );
}
