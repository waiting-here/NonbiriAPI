import { useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Field,
  Fields,
  FormActions,
  Fold,
  Note,
  Panel,
  PanelBody,
  PanelHead,
} from '@shared/components/ui';
import { LoadingState } from '@shared/components/States';
import { useOperation } from '@shared/operations/useOperation';
import { agentRoot } from './api';
import { AgentError, AgentTime } from './AgentParts';
import {
  getChatGPT,
  getChatGPTTarget,
  importChatGPT,
  logoutChatGPT,
  type ChatGPTEnvelope,
  type ChatGPTStatus,
} from './driversApi';

type ConnectionIntent =
  { action: 'import'; revision: string } | { action: 'logout'; revision: string };

export function ChatGPTConnection({ account, active }: { account: string; active: boolean }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const root = agentRoot('admin', account);
  const statusKey = [...root, 'chatgpt'];
  const status = useQuery({
    queryKey: statusKey,
    queryFn: ({ signal }) => getChatGPT(signal),
    enabled: active,
    retry: false,
  });
  const [signingIn, setSigningIn] = useState(false);
  const [newAccount, setNewAccount] = useState(false);
  const [filename, setFilename] = useState('');
  const file = useRef<File | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const [remoteRevocation, setRemoteRevocation] = useState<boolean | null>(null);
  const [checked, setChecked] = useState<ChatGPTStatus | null>(null);
  const clearFile = () => {
    file.current = null;
    setFilename('');
    if (fileInput.current) fileInput.current.value = '';
  };
  const action = useOperation<ConnectionIntent, File, void>({
    authorityRoot: root,
    clearSecrets: clearFile,
    execute: async (intent, upload, _key, context) => {
      if (intent.action === 'logout') {
        const result = await logoutChatGPT(intent.revision, context.signal);
        context.commit(() => {
          setRemoteRevocation(result.remote_revocation_confirmed);
          clearFile();
        });
        return;
      }
      if (!upload) throw new Error(t('common.agent.drivers.chooseFile'));
      const text = await upload.text();
      context.assertCurrent();
      let envelope: unknown;
      try {
        envelope = JSON.parse(text);
      } catch {
        throw new Error(t('common.agent.drivers.invalidFile'));
      }
      if (
        !envelope ||
        typeof envelope !== 'object' ||
        Array.isArray(envelope) ||
        !('ciphertext' in envelope) ||
        typeof envelope.ciphertext !== 'string'
      ) {
        throw new Error(t('common.agent.drivers.invalidFile'));
      }
      const value = await importChatGPT(envelope as ChatGPTEnvelope, context.signal);
      context.commit(() => {
        client.setQueryData(statusKey, value);
        clearFile();
        setChecked(null);
        setSigningIn(false);
        setRemoteRevocation(null);
      });
    },
    reconcile: async (_intent, failure, context) => {
      const value = await getChatGPT(context.signal);
      context.commit(() => {
        client.setQueryData(statusKey, value);
        if (failure) setChecked(value);
      });
    },
  });
  const target = useOperation<boolean, never, void>({
    authorityRoot: root,
    execute: async (switchAccount, _secret, _key, context) => {
      const value = await getChatGPTTarget(switchAccount, context.signal);
      context.commit(() => {
        const url = URL.createObjectURL(
          new Blob([JSON.stringify(value, null, 2) + '\n'], { type: 'application/json' }),
        );
        const anchor = document.createElement('a');
        anchor.href = url;
        anchor.download = 'instance-target.json';
        anchor.click();
        URL.revokeObjectURL(url);
      });
    },
    reconcile: async (_intent, _failure, context) => {
      const value = await getChatGPT(context.signal);
      context.commit(() => client.setQueryData(statusKey, value));
    },
  });
  const busy = action.isPending || target.isPending;
  const unresolved = action.outcome === 'unknown' || action.outcome === 'conflict';
  const localUsage = status.data?.local_usage;
  return (
    <Panel aria-label={t('common.agent.drivers.connection')}>
      <PanelHead
        title={t('common.agent.drivers.connection')}
        description={t('common.agent.drivers.connectionHelp')}
      />
      <PanelBody>
        <div className="nb-stack">
          {status.isPending ? (
            <LoadingState />
          ) : status.error ? (
            <AgentError error={status.error} onRetry={() => void status.refetch()} />
          ) : (
            <>
              <Note tone={status.data.connected ? 'ok' : 'info'}>
                <p>
                  {t(
                    status.data.connected
                      ? 'common.agent.drivers.connected'
                      : 'common.agent.drivers.disconnected',
                  )}
                  {status.data.account_label ? ' · ' + status.data.account_label : ''}
                </p>
                {status.data.needs_login ? <p>{t('common.agent.drivers.needsLogin')}</p> : null}
              </Note>
              <Fold title={t('common.agent.drivers.usageStatus')} defaultOpen>
                <p className="nb-small nb-muted">{t('common.agent.drivers.refreshHelp')}</p>
                {status.data.updated_at ? (
                  <p>
                    {t('common.agent.drivers.connectionUpdated')}{' '}
                    <AgentTime value={status.data.updated_at} />
                  </p>
                ) : null}
                {status.data.expires_at ? (
                  <p>
                    {t('common.agent.drivers.expires')}{' '}
                    <AgentTime value={status.data.expires_at * 1000} />
                  </p>
                ) : null}
                {status.data.official_usage?.availability === 'unavailable' ? (
                  <Note>
                    <p>{t('common.agent.drivers.officialUnavailable')}</p>
                    <a href="https://chatgpt.com/settings/usage" target="_blank" rel="noreferrer">
                      {t('common.agent.drivers.openOfficialUsage')}
                    </a>
                  </Note>
                ) : null}
                {localUsage ? (
                  <div className="nb-stack">
                    <p>
                      <strong>{t('common.agent.drivers.localUsage')}</strong>
                    </p>
                    <p className="nb-small nb-muted">{t('common.agent.drivers.localUsageHelp')}</p>
                    <p className="nb-small">
                      <AgentTime value={localUsage.window_start} /> —{' '}
                      <AgentTime value={localUsage.window_end} />
                    </p>
                    <div className="agent-token-grid">
                      {(
                        [
                          'requests',
                          'input_tokens',
                          'cached_input_tokens',
                          'cache_write_input_tokens',
                          'output_tokens',
                          'reasoning_tokens',
                          'unknown_count',
                        ] as const
                      ).map((metric) => (
                        <div key={metric}>
                          <span>{t(`common.agent.drivers.metrics.${metric}`)}</span>
                          <strong className="agent-number">{localUsage[metric]}</strong>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
                {status.data.last_limit_error ? (
                  <Note tone="warn">
                    <p>{t('common.agent.drivers.lastLimit')}</p>
                    <AgentTime value={status.data.last_limit_error.occurred_at} />
                  </Note>
                ) : null}
                <div className="nb-actions">
                  <Button
                    type="button"
                    size="sm"
                    disabled={busy || status.isFetching}
                    onClick={() => void status.refetch()}
                  >
                    {t('common.agent.drivers.refreshStatus')}
                  </Button>
                </div>
              </Fold>
              <FormActions>
                <Button
                  type="button"
                  variant="primary"
                  disabled={busy || unresolved}
                  onClick={() => {
                    setSigningIn(true);
                    setNewAccount(false);
                  }}
                >
                  {t('common.agent.drivers.signIn')}
                </Button>
                {status.data.connected ? (
                  <>
                    <Button
                      type="button"
                      disabled={busy || unresolved}
                      onClick={() => {
                        setSigningIn(true);
                        setNewAccount(true);
                      }}
                    >
                      {t('common.agent.drivers.switchAccount')}
                    </Button>
                    <Button
                      type="button"
                      variant="danger-outline"
                      disabled={busy || unresolved}
                      onClick={() =>
                        void action
                          .run({ action: 'logout', revision: status.data.revision })
                          .catch(() => undefined)
                      }
                    >
                      {t('common.agent.drivers.disconnect')}
                    </Button>
                  </>
                ) : null}
              </FormActions>
            </>
          )}
          <Fold
            title={t('common.agent.drivers.signIn')}
            open={signingIn}
            onOpenChange={setSigningIn}
            lazy
          >
            <div className="nb-stack">
              <p>{t('common.agent.drivers.localLoginHelp')}</p>
              <Fields>
                <Field label={t('common.agent.drivers.loginAccount')}>
                  {(props) => (
                    <select
                      {...props}
                      value={newAccount ? 'new' : 'same'}
                      disabled={busy || unresolved}
                      onChange={(event) => setNewAccount(event.target.value === 'new')}
                    >
                      <option value="same">{t('common.agent.drivers.sameAccount')}</option>
                      <option value="new">{t('common.agent.drivers.switchAccount')}</option>
                    </select>
                  )}
                </Field>
              </Fields>
              <ol className="agent-login-steps">
                <li>
                  <p>{t('common.agent.drivers.downloadStep')}</p>
                  <Button
                    type="button"
                    disabled={busy || unresolved}
                    onClick={() => void target.run(newAccount).catch(() => undefined)}
                  >
                    {t('common.agent.drivers.downloadTarget')}
                  </Button>
                </li>
                <li>
                  <p>{t('common.agent.drivers.helperStep')}</p>
                  <pre className="agent-raw">
                    {
                      'go build -o nonbiri-chatgpt-login.exe ./cmd/nonbiri-chatgpt-login\n./nonbiri-chatgpt-login.exe --target instance-target.json --output connection.json'
                    }
                  </pre>
                  <p className="nb-small">{t('common.agent.drivers.helperKeep')}</p>
                </li>
                <li>
                  <p>{t('common.agent.drivers.uploadStep')}</p>
                  <Field
                    label={t('common.agent.drivers.connectionFile')}
                    help={filename || t('common.agent.drivers.fileHelp')}
                  >
                    {(props) => (
                      <input
                        {...props}
                        type="file"
                        ref={fileInput}
                        accept="application/json,.json"
                        disabled={busy || unresolved}
                        onChange={(event) => {
                          file.current = event.target.files?.[0] ?? null;
                          setFilename(file.current?.name ?? '');
                        }}
                      />
                    )}
                  </Field>
                  <Button
                    type="button"
                    disabled={busy || unresolved || !filename || !status.data}
                    onClick={() => {
                      if (status.data && file.current)
                        void action
                          .run({ action: 'import', revision: status.data.revision }, file.current)
                          .catch(() => undefined);
                    }}
                  >
                    {t('common.agent.drivers.importConnection')}
                  </Button>
                </li>
              </ol>
              <a
                href="https://github.com/waiting-here/NonbiriAPI/blob/master/docs/chatgpt-agent.md"
                target="_blank"
                rel="noreferrer"
              >
                {t('common.agent.drivers.loginGuide')}
              </a>
            </div>
          </Fold>
          {action.error ? <AgentError error={action.error} /> : null}
          {target.error ? <AgentError error={target.error} /> : null}
          {action.refreshError ? (
            <AgentError error={action.refreshError} onRetry={() => void action.check()} />
          ) : null}
          {target.refreshError ? <AgentError error={target.refreshError} /> : null}
          {unresolved ? (
            <Note tone="warn">
              <p>{t('common.agent.drivers.connectionUncertain')}</p>
              <Button type="button" disabled={busy} onClick={() => void action.check()}>
                {t('common.agent.drivers.checkStatus')}
              </Button>
              {checked ? (
                <>
                  <p>
                    {t('common.agent.drivers.checkedConnection', {
                      revision: checked.revision,
                      account: checked.account_label || '—',
                    })}
                  </p>
                  <Button
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      clearFile();
                      action.reset();
                      setChecked(null);
                    }}
                  >
                    {t('common.agent.drivers.useCurrentConnection')}
                  </Button>
                </>
              ) : null}
            </Note>
          ) : null}
          {remoteRevocation !== null ? (
            <Note tone={remoteRevocation ? 'ok' : 'warn'}>
              {t(
                remoteRevocation
                  ? 'common.agent.drivers.loggedOut'
                  : 'common.agent.drivers.revocationUnconfirmed',
              )}
            </Note>
          ) : null}
        </div>
      </PanelBody>
    </Panel>
  );
}
