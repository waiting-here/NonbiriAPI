import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Field,
  FormActions,
  Note,
  Panel,
  PanelBody,
  PanelHead,
} from '@shared/components/ui';
import { LoadingState } from '@shared/components/States';
import { useOperation } from '@shared/operations/useOperation';
import { agentRoot } from './api';
import { AgentError, AgentTime } from './AgentParts';
import { beginCodex, cancelCodex, disconnectCodex, getCodex } from './codexApi';

type Intent =
  | { action: 'begin'; revision: string }
  | { action: 'disconnect'; revision: string }
  | { action: 'cancel'; id: string };

export function CodexConnection({ account, active }: { account: string; active: boolean }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const root = agentRoot('admin', account);
  const key = [...root, 'codex'];
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => getCodex(signal),
    enabled: active,
    retry: false,
    gcTime: 0,
    refetchInterval: (q) =>
      active && ['starting', 'pending'].includes(q.state.data?.login?.state ?? '') ? 1500 : false,
  });
  const revision = query.data?.revision;
  useEffect(() => {
    void client.invalidateQueries({ queryKey: [...agentRoot('admin', account), 'drivers'] });
  }, [client, account, revision]);
  const operation = useOperation<Intent, never, void>({
    authorityRoot: root,
    execute: async (intent, _secret, _key, context) => {
      const value =
        intent.action === 'begin'
          ? await beginCodex(intent.revision, context.signal)
          : intent.action === 'cancel'
            ? await cancelCodex(intent.id, context.signal)
            : await disconnectCodex(intent.revision, context.signal);
      context.commit(() => client.setQueryData(key, value));
    },
    reconcile: async (intent, _failure, context) => {
      const value = await getCodex(context.signal);
      context.commit(() => client.setQueryData(key, value));
      return {
        operationConfirmed:
          intent.action === 'begin'
            ? Boolean(value.login || value.connected)
            : intent.action === 'disconnect'
              ? !value.connected && value.revision !== intent.revision
              : !value.login ||
                value.login.id !== intent.id ||
                !['pending', 'starting'].includes(value.login.state),
      };
    },
  });
  const status = query.data;
  const pending = status?.login && ['pending', 'starting'].includes(status.login.state);
  const unresolved = operation.outcome === 'unknown' || operation.outcome === 'conflict';
  const run = (intent: Intent) => void operation.run(intent).catch(() => undefined);
  return (
    <Panel aria-label={t('common.agent.codex.title')}>
      <PanelHead title={t('common.agent.codex.title')} description={t('common.agent.codex.help')} />
      <PanelBody>
        <div className="nb-stack">
          {query.isPending ? (
            <LoadingState />
          ) : query.error ? (
            <AgentError error={query.error} onRetry={() => void query.refetch()} />
          ) : null}
          {status ? (
            <>
              <p>
                {t(
                  status.connected
                    ? 'common.agent.codex.connected'
                    : 'common.agent.codex.disconnected',
                )}
                {status.connected && status.account_label ? <> · {status.account_label}</> : null}
              </p>
              {pending && status.login ? (
                <>
                  <p>{t('common.agent.codex.instruction')}</p>
                  <a href="https://auth.openai.com/codex/device" target="_blank" rel="noreferrer">
                    {t('common.agent.codex.openOfficial')}
                  </a>
                  {status.login.user_code ? (
                    <Field label={t('common.agent.codex.code')} size="short">
                      {(props) => (
                        <input
                          {...props}
                          className="mono"
                          readOnly
                          value={status.login?.user_code ?? ''}
                          autoComplete="off"
                        />
                      )}
                    </Field>
                  ) : (
                    <LoadingState />
                  )}
                  <p className="nb-small nb-muted">
                    {t('common.agent.codex.expires')}{' '}
                    <AgentTime value={status.login.expires_at * 1000} />
                  </p>
                  <Note>{t('common.agent.codex.keepPrivate')}</Note>
                </>
              ) : status.login && ['failed', 'expired'].includes(status.login.state) ? (
                <Note tone="warn">
                  {t(
                    status.login.error_code === 'access_denied'
                      ? 'common.agent.codex.denied'
                      : status.login.state === 'expired'
                        ? 'common.agent.codex.expired'
                        : 'common.agent.codex.failed',
                  )}
                </Note>
              ) : null}
              {status.connected ? (
                <p className="nb-small nb-muted">{t('common.agent.codex.disconnectHelp')}</p>
              ) : null}
              <FormActions>
                {pending && status.login ? (
                  <Button
                    type="button"
                    disabled={operation.isPending}
                    onClick={() => run({ action: 'cancel', id: status.login!.id })}
                  >
                    {t('common.cancel')}
                  </Button>
                ) : status.connected ? (
                  <Button
                    type="button"
                    variant="danger-outline"
                    disabled={operation.isPending}
                    onClick={() => run({ action: 'disconnect', revision: status.revision })}
                  >
                    {t('common.agent.codex.disconnect')}
                  </Button>
                ) : (
                  <Button
                    type="button"
                    variant="primary"
                    disabled={operation.isPending}
                    onClick={() => run({ action: 'begin', revision: status.revision })}
                  >
                    {t('common.agent.codex.connect')}
                  </Button>
                )}
                {unresolved ? (
                  <Button
                    type="button"
                    disabled={operation.isPending}
                    onClick={() => void operation.check()}
                  >
                    {t('common.agent.drivers.checkStatus')}
                  </Button>
                ) : null}
              </FormActions>
            </>
          ) : null}
          {operation.error ? <AgentError error={operation.error} /> : null}
          {operation.refreshError ? (
            <AgentError error={operation.refreshError} onRetry={() => void operation.check()} />
          ) : null}
        </div>
      </PanelBody>
    </Panel>
  );
}
