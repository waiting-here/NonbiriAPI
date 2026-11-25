/* eslint-disable react-refresh/only-export-components -- Share the onboarding flags with their address control. */
import { useEffect, useState, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { copyText } from '@shared/utils/clipboard';

type OnboardingFlag = 'client' | 'hidden';
const memory = new Map<string, boolean>();
const listeners = new Set<() => void>();
function readFlag(accountId: string, flag: OnboardingFlag): boolean {
  const key = `nb.onboarding.${accountId}.${flag}`;
  if (memory.has(key)) return memory.get(key)!;
  try {
    return localStorage.getItem(key) === '1';
  } catch {
    return false;
  }
}
export function markOnboarding(accountId: string, flag: OnboardingFlag, value = true) {
  const key = `nb.onboarding.${accountId}.${flag}`;
  memory.set(key, value);
  try {
    if (value) localStorage.setItem(key, '1');
    else localStorage.removeItem(key);
    memory.delete(key);
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
export function useOnboardingFlag(accountId: string, flag: OnboardingFlag) {
  return useSyncExternalStore(
    subscribe,
    () => readFlag(accountId, flag),
    () => false,
  );
}
export function apiAddress(): string {
  return `${window.location.origin}/v1`;
}
export function ApiAddressCopy({ accountId }: { accountId: string }) {
  const { t } = useTranslation();
  const [result, setResult] = useState<boolean>();
  useEffect(() => {
    if (result === undefined) return;
    const timer = window.setTimeout(() => setResult(undefined), 2500);
    return () => window.clearTimeout(timer);
  }, [result]);
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
            if (ok) markOnboarding(accountId, 'client');
          });
        }}
      >
        {result ? t('common.copied') : t('common.copy')}
      </button>
      {result === false ? <span role="status">{t('common.copyFailed')}</span> : null}
    </span>
  );
}
