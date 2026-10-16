import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ErrorState, LoadingState } from '@shared/components/States';
import { Button, Field, Fold, Note, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import { ApiError, isApiError } from '@shared/query/http';
import { getMfa, setupMfa, submitMfa, type MfaSetup, type MfaStatus } from '@shared/operations/mfa';
import { formatDateTime } from '@shared/utils/datetime';
import './mfa.css';

function MfaQr({ uri }: { uri: string }) {
  const { t } = useTranslation();
  const canvas = useRef<HTMLCanvasElement>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let active = true;
    const target = canvas.current;
    if (!target) return;
    void import('qrcode')
      .then(async ({ toCanvas }) => {
        if (!active) return;
        await toCanvas(target, uri, { width: 224, margin: 3, errorCorrectionLevel: 'M' });
        if (!active) {
          target.width = 0;
          target.height = 0;
        }
      })
      .catch(() => {
        if (active) setFailed(true);
      });
    return () => {
      active = false;
      target.width = 0;
      target.height = 0;
    };
  }, [uri]);
  return failed ? (
    <Note tone="warn">{t('mfa.qrUnavailable')}</Note>
  ) : (
    <canvas ref={canvas} className="mfa-qr" role="img" aria-label={t('mfa.qrLabel')} />
  );
}

function MfaError({ error, disabling }: { error: unknown; disabling: boolean }) {
  const { t } = useTranslation();
  if (isApiError(error)) {
    const keys: Record<string, string> = {
      mfa_invalid_code: 'mfa.invalidCode',
      mfa_setup_expired: 'mfa.setupExpired',
      rate_limited: 'mfa.rateLimited',
      conflict: 'mfa.conflict',
    };
    const key = error.code === 'forbidden' && disabling ? 'mfa.disableRequired' : keys[error.code];
    if (key) return <Note tone="bad">{t(key)}</Note>;
  }
  return <ErrorState error={error} />;
}

export function MfaChallenge({
  mode = 'verify',
  dialog = false,
  onCancel,
  onSuccess,
}: {
  mode?: 'verify' | 'enable' | 'disable';
  dialog?: boolean;
  onCancel?: () => void;
  onSuccess: (status: MfaStatus) => void | Promise<void>;
}) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<MfaStatus | null>(null);
  const [setup, setSetup] = useState<MfaSetup | null>(null);
  const [code, setCode] = useState('');
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(true);
  const lifetime = useRef<AbortController | null>(null);
  const form = useRef<HTMLFormElement>(null);
  const onSuccessRef = useRef(onSuccess);
  useEffect(() => {
    onSuccessRef.current = onSuccess;
  }, [onSuccess]);
  const load = useCallback(
    async (signal: AbortSignal) => {
      try {
        const next = await getMfa(signal);
        if (signal.aborted) return;
        setStatus(next);
        if (mode === 'disable') {
          if (!next.enrolled) await onSuccessRef.current(next);
        } else if (
          (next.enrolled && next.verified) ||
          (!next.enrolled && !next.required && mode !== 'enable')
        ) {
          await onSuccessRef.current(next);
        } else if (!next.enrolled) {
          const material = await setupMfa(signal);
          if (!signal.aborted) setSetup(material);
        }
      } catch (failure) {
        if (!signal.aborted) setError(failure);
      } finally {
        if (!signal.aborted) setBusy(false);
      }
    },
    [mode],
  );
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
  useEffect(() => {
    if (!setup) return;
    const expire = () => {
      setSetup(null);
      setCode('');
      setError(new ApiError('mfa_setup_expired', 'Authenticator setup expired.', 409));
    };
    const timer = setTimeout(expire, Math.max(0, setup.expires_at * 1000 - Date.now()));
    return () => clearTimeout(timer);
  }, [setup]);
  const submit = async () => {
    const signal = lifetime.current?.signal;
    if (!signal || busy || !status || !/^\d{6}$/.test(code)) return;
    const submitted = code;
    setCode('');
    setBusy(true);
    setError(null);
    try {
      const next = await submitMfa(
        mode === 'disable' ? 'disable' : status.enrolled ? 'verify' : 'confirm',
        submitted,
        signal,
      );
      if (signal.aborted) return;
      setSetup(null);
      await onSuccessRef.current(next);
    } catch (failure) {
      if (signal.aborted) return;
      if (isApiError(failure) && failure.code === 'mfa_setup_expired') setSetup(null);
      setError(failure);
    } finally {
      if (!signal.aborted) setBusy(false);
    }
  };
  const disabling = mode === 'disable';
  const title = t(
    disabling ? 'mfa.disableTitle' : status?.enrolled ? 'mfa.verifyTitle' : 'mfa.setupTitle',
  );
  const description = t(
    disabling
      ? 'mfa.disableDescription'
      : status?.enrolled
        ? 'mfa.verifyDescription'
        : 'mfa.setupDescription',
  );
  const canSubmit = Boolean(
    status && (status.enrolled || setup) && !(disabling && status.required),
  );
  const confirmLabel = t(
    disabling ? 'mfa.disableConfirm' : status?.enrolled ? 'mfa.verify' : 'mfa.confirmSetup',
  );
  const fields = (
    <>
      {busy && !status ? <LoadingState /> : null}
      {error ? <MfaError error={error} disabling={disabling} /> : null}
      {setup ? (
        <div className="mfa-setup">
          <MfaQr uri={setup.otpauth_uri} />
          <Fold title={t('mfa.manualKey')}>
            <Field label={t('mfa.setupKey')} help={t('mfa.manualHelp')}>
              {(props) => (
                <input
                  {...props}
                  className="mono mfa-secret"
                  readOnly
                  value={setup.secret}
                  autoComplete="off"
                  spellCheck={false}
                />
              )}
            </Field>
          </Fold>
          <p className="mfa-help">
            {t('mfa.setupUntil', { time: formatDateTime(setup.expires_at) })}
          </p>
        </div>
      ) : null}
      {disabling && status?.required ? <Note tone="warn">{t('mfa.disableRequired')}</Note> : null}
      {canSubmit ? (
        <form
          ref={form}
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <Field label={t('mfa.code')} help={t(disabling ? 'mfa.freshCodeHelp' : 'mfa.codeHelp')}>
            {(props) => (
              <input
                {...props}
                type="text"
                inputMode="numeric"
                autoComplete="one-time-code"
                pattern="[0-9]{6}"
                maxLength={6}
                value={code}
                disabled={busy}
                onChange={(event) => setCode(event.target.value.replace(/[^0-9]/g, '').slice(0, 6))}
              />
            )}
          </Field>
          <button type="submit" hidden disabled={busy} aria-hidden="true" tabIndex={-1} />
        </form>
      ) : null}
      {error && (!status || (!status.enrolled && !setup)) ? (
        <Button
          type="button"
          disabled={busy}
          onClick={() => {
            const signal = lifetime.current?.signal;
            if (signal) {
              setBusy(true);
              setError(null);
              setCode('');
              setSetup(null);
              void load(signal);
            }
          }}
        >
          {t('mfa.restart')}
        </Button>
      ) : null}
      {!disabling ? <p className="mfa-help">{t('mfa.lostAuthenticator')}</p> : null}
    </>
  );
  if (dialog)
    return (
      <ConfirmDialog
        open
        title={title}
        description={description}
        confirmLabel={confirmLabel}
        confirmDisabled={!canSubmit || !/^\d{6}$/.test(code)}
        busy={busy}
        danger={disabling}
        onCancel={() => {
          lifetime.current?.abort();
          setSetup(null);
          setCode('');
          onCancel?.();
        }}
        onConfirm={() => form.current?.requestSubmit()}
      >
        {fields}
      </ConfirmDialog>
    );
  return (
    <Panel className="mfa-challenge">
      <PanelHead title={title} description={description} />
      <PanelBody>
        <div className="mfa-stack">
          {fields}
          <Button
            type="button"
            variant="primary"
            disabled={busy || !canSubmit || !/^\d{6}$/.test(code)}
            onClick={() => form.current?.requestSubmit()}
          >
            {busy ? t('common.working') : confirmLabel}
          </Button>
        </div>
      </PanelBody>
    </Panel>
  );
}
