import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Button, DataTable, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import { LoadingState } from '@shared/components/States';
import { agentRoot } from './api';
import { AgentError } from './AgentParts';
import { CodexConnection } from './CodexConnection';
import { ChatGPTConnection } from './ChatGPTConnection';
import { DriverBindings } from './DriverBindings';
import { DriverProfileEditor } from './DriverProfileEditor';
import { getDrivers, newProfile, type DriverConfiguration } from './driversApi';

export function DriverPanel({ account, active }: { account: string; active: boolean }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const queryKey = [...agentRoot('admin', account), 'drivers'];
  const config = useQuery({
    queryKey,
    queryFn: ({ signal }) => getDrivers(signal),
    enabled: active,
    retry: false,
  });
  const [selected, setSelected] = useState<string | null>(null);
  const update = (value: DriverConfiguration) => client.setQueryData(queryKey, value);
  const initial =
    selected === 'new'
      ? newProfile()
      : config.data?.profiles.find((profile) => profile.id === selected);
  return (
    <div className="nb-stack">
      <ChatGPTConnection account={account} active={active} />
      <CodexConnection account={account} active={active} />
      <Panel aria-label={t('common.agent.drivers.sources')}>
        <PanelHead
          title={t('common.agent.drivers.sources')}
          description={t('common.agent.drivers.sourcesHelp')}
          actions={
            <Button type="button" onClick={() => setSelected('new')}>
              {t('common.agent.drivers.addSource')}
            </Button>
          }
        />
        <PanelBody>
          <div className="agent-form-fields">
            {config.isPending ? (
              <LoadingState />
            ) : config.error ? (
              <AgentError error={config.error} onRetry={() => void config.refetch()} />
            ) : (
              <>
                <DataTable
                  caption={t('common.agent.drivers.sources')}
                  rows={config.data.profiles.filter((profile) => profile.id !== '0')}
                  rowKey={(profile) => profile.id}
                  columns={[
                    {
                      key: 'name',
                      header: t('common.agent.drivers.name'),
                      cell: 'title',
                      render: (profile) => <strong>{profile.name}</strong>,
                    },
                    {
                      key: 'provider',
                      header: t('common.agent.drivers.provider'),
                      cell: 'meta',
                      render: (profile) =>
                        profile.settings.provider === 'codex'
                          ? 'Codex'
                          : profile.settings.provider === 'chatgpt'
                            ? 'ChatGPT'
                            : profile.settings.provider === 'responses'
                              ? 'Responses API'
                              : 'Chat Completions API',
                    },
                    {
                      key: 'model',
                      header: t('common.connections.model'),
                      cell: 'meta',
                      render: (profile) => profile.settings.model || '—',
                    },
                    {
                      key: 'actions',
                      header: t('common.agent.actions'),
                      cell: 'action',
                      render: (profile) => (
                        <Button type="button" size="sm" onClick={() => setSelected(profile.id)}>
                          {t('common.edit')}
                        </Button>
                      ),
                    },
                  ]}
                />
                {initial ? (
                  <DriverProfileEditor
                    key={selected}
                    initial={initial}
                    config={config.data}
                    account={account}
                    onConfiguration={update}
                    onSelect={setSelected}
                    onClose={() => setSelected(null)}
                  />
                ) : null}
              </>
            )}
          </div>
        </PanelBody>
      </Panel>
      {config.data ? (
        <Panel aria-label={t('common.agent.drivers.bindings')}>
          <PanelHead
            title={t('common.agent.drivers.bindings')}
            description={t('common.agent.drivers.bindingsHelp')}
          />
          <PanelBody>
            <DriverBindings config={config.data} account={account} onConfiguration={update} />
          </PanelBody>
        </Panel>
      ) : null}
    </div>
  );
}
