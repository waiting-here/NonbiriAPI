import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Note, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import { ErrorState, LoadingState } from '@shared/components/States';
import { getMfa, type MfaStatus } from '@shared/operations/mfa';
import { formatDateTime } from '@shared/utils/datetime';
import { MfaChallenge } from './MfaChallenge';

export function AccountMfaPanel() {
  const { t } = useTranslation();
  const [status, setStatus] = useState<MfaStatus | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [mode, setMode] = useState<'enable' | 'disable' | 'verify' | null>(null);
  const lifetime = useRef<AbortController | null>(null);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const next = await getMfa(signal);
      if (!signal.aborted) setStatus(next);
    } catch (failure) {
      if (!signal.aborted) setError(failure);
    }
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    queueMicrotask(() => {
      if (!controller.signal.aborted) void load(controller.signal);
    });
    return () => {
      controller.abort();
      lifetime.current = null;
    };
  }, [load]);
  return (
    <Panel>
      <PanelHead title={t('mfa.accountTitle')} description={t('mfa.accountDescription')} />
      <PanelBody>
        <div className="mfa-stack">
          {error ? (
            <ErrorState
              error={error}
              onRetry={() => {
                const signal = lifetime.current?.signal;
                if (signal) {
                  setError(null);
                  void load(signal);
                }
              }}
            />
          ) : !status ? (
            <LoadingState />
          ) : (
            <>
              <p>{t(status.enrolled ? 'mfa.enabled' : 'mfa.notEnabled')}</p>
              {status.required ? <Note>{t('mfa.requiredPolicy')}</Note> : null}
              {status.enrolled && status.verified_until ? (
                <p className="mfa-help">
                  {t('mfa.verifiedUntil', { time: formatDateTime(status.verified_until) })}
                </p>
              ) : null}
              <div className="mfa-actions">
                {!status.enrolled ? (
                  <Button type="button" variant="primary" onClick={() => setMode('enable')}>
                    {t('mfa.enable')}
                  </Button>
                ) : (
                  <>
                    {!status.verified ? (
                      <Button type="button" variant="primary" onClick={() => setMode('verify')}>
                        {t('mfa.verify')}
                      </Button>
                    ) : null}
                    {!status.required ? (
                      <Button
                        type="button"
                        variant="danger-outline"
                        onClick={() => setMode('disable')}
                      >
                        {t('mfa.disable')}
                      </Button>
                    ) : null}
                  </>
                )}
              </div>
            </>
          )}
        </div>
      </PanelBody>
      {mode ? (
        <MfaChallenge
          mode={mode}
          dialog
          onCancel={() => setMode(null)}
          onSuccess={(next) => {
            setStatus(next);
            setMode(null);
          }}
        />
      ) : null}
    </Panel>
  );
}
