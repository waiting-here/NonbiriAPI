/* eslint-disable react-refresh/only-export-components -- Share the onboarding flags with their address control. */
import { useState, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { copyText } from '@shared/utils/clipboard';

type OnboardingFlag = 'client' | 'hidden';
const memory = { client: false, hidden: false };
const listeners = new Set<() => void>();
function readFlag(flag: OnboardingFlag): boolean {
  try {
    return localStorage.getItem(`nb.onboarding.${flag}`) === '1' || memory[flag];
  } catch {
    return memory[flag];
  }
}
export function markOnboarding(flag: OnboardingFlag) {
  memory[flag] = true;
  try {
    localStorage.setItem(`nb.onboarding.${flag}`, '1');
    memory[flag] = false;
  } catch {
    // Keep the completion flag for this visit if browser storage is unavailable.
  }
  listeners.forEach((notify) => notify());
}
function subscribe(notify: () => void) {
  listeners.add(notify);
  window.addEventListener('storage', notify);
  return () => {
    listeners.delete(notify);
    window.removeEventListener('storage', notify);
  };
}
export function useOnboardingFlag(flag: OnboardingFlag) {
  return useSyncExternalStore(
    subscribe,
    () => readFlag(flag),
    () => false,
  );
}
export function apiAddress(): string {
  return `${window.location.origin}/v1`;
}
export function ApiAddressCopy() {
  const { t } = useTranslation();
  const [result, setResult] = useState<boolean>();
  return (
    <span className="nb-copy home-address-copy">
      <code>{apiAddress()}</code>
      <button
        type="button"
        className="nb-btn nb-btn--secondary nb-btn--sm"
        aria-label={t('common.copyValue', { label: t('user.core.keys.apiAddress') })}
        onClick={() => {
          void copyText(apiAddress()).then((ok) => {
            setResult(ok);
            if (ok) markOnboarding('client');
          });
        }}
      >
        {result ? t('common.copied') : t('common.copy')}
      </button>
      {result === false ? <span role="status">{t('common.copyFailed')}</span> : null}
    </span>
  );
}
