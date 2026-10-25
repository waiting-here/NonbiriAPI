import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { Tabs } from '@shared/components/ui';
import { useSearchState } from '@shared/operations/useSearchState';
import type { AgentRole } from './api';
import { ChatPanel } from './ChatPanel';
import { ReviewsPanel } from './ReviewsPanel';
import { MemoriesPanel } from './MemoriesPanel';
import { AdminSettings } from './AdminSettings';
import { RequestsPanel } from './RequestsPanel';
import { ArchivesPanel } from './ArchivesPanel';
import './agent.css';

const tabs = {
  chat: 'common.agent.chat',
  reviews: 'common.agent.reviews',
  memories: 'common.agent.memories',
  settings: 'common.agent.configuration',
  requests: 'common.agent.requestsAndUsage',
  archives: 'common.agent.archives',
} as const;
type Tab = keyof typeof tabs;
export function AgentWorkspace({ role, account }: { role: AgentRole; account: string }) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchState();
  const requested = params.get('ai_tab') ?? 'chat';
  const value: Tab =
    requested in tabs && (role === 'admin' || ['chat', 'reviews', 'memories'].includes(requested))
      ? (requested as Tab)
      : 'chat';
  const [visited, setVisited] = useState<Tab[]>([value]);
  if (!visited.includes(value)) setVisited([...visited, value]);
  const select = (value: Tab) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (value === 'chat') next.delete('ai_tab');
      else next.set('ai_tab', value);
      return next;
    });
  return (
    <div className="page agent-workspace">
      <PageHeader
        icon={role === 'steward' ? 'steward' : undefined}
        title={t('common.agent.title')}
        description={t('common.agent.description')}
        back={
          role === 'steward' ? (
            <Link to="/steward">{t('common.agent.backManagement')}</Link>
          ) : undefined
        }
      />
      <Tabs
        label={t('common.agent.title')}
        value={value}
        onChange={select}
        tabs={(Object.keys(tabs) as Tab[])
          .filter((tab) => role === 'admin' || ['chat', 'reviews', 'memories'].includes(tab))
          .map((tab) => ({ value: tab, label: t(tabs[tab]) }))}
      />
      {visited.map((tab) => (
        <div key={tab} hidden={tab !== value} role="tabpanel">
          {tab === 'chat' ? (
            <ChatPanel role={role} account={account} active={tab === value} />
          ) : tab === 'reviews' ? (
            <ReviewsPanel role={role} account={account} active={tab === value} />
          ) : tab === 'memories' ? (
            <MemoriesPanel role={role} account={account} active={tab === value} />
          ) : tab === 'settings' ? (
            <AdminSettings account={account} active={tab === value} />
          ) : tab === 'requests' ? (
            <RequestsPanel account={account} active={tab === value} />
          ) : (
            <ArchivesPanel account={account} active={tab === value} />
          )}
        </div>
      ))}
    </div>
  );
}
