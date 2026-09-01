import { useState } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import { useIssues } from '../features/operations/data';
import '@shared/operations/operations.css';

const SUMMARY: Record<string, { zh: string; en: string }> = {
  discovery_failed: { zh: '模型发现失败', en: 'Model discovery failed' },
  no_routable_binding: { zh: '没有可路由绑定', en: 'No routable binding' },
  credential_invalid: { zh: '凭据已被权威验证为无效', en: 'Credential is authoritatively invalid' },
  configuration_invalid: { zh: '配置已被权威验证为无效', en: 'Configuration is authoritatively invalid' },
};

export function IssuesPage() {
  const { i18n } = useTranslation();
  const zh = i18n.resolvedLanguage?.toLowerCase().startsWith('zh') ?? false;
  const [state, setState] = useState<'current' | 'closed'>('current');
  const pager = useCursorPager();
  const issues = useIssues(state, pager.cursor);
  const switchState = (next: 'current' | 'closed') => { setState(next); pager.reset(); };
  return <div className="page ops-stack">
    <PageHeader eyebrow="Operations" title={zh ? '问题中心' : 'Issue center'}
      description={zh ? '问题由权威资源状态自动打开和关闭；这里没有手动解决或未读状态。' : 'Authoritative resource state opens and closes issues automatically; there is no manual resolve or unread state.'} />
    <div className="ops-tabs" role="tablist" aria-label={zh ? '问题状态' : 'Issue state'}>
      <button type="button" role="tab" aria-selected={state === 'current'} className={`btn ${state === 'current' ? 'btn-primary' : 'btn-secondary'}`} onClick={() => switchState('current')}>{zh ? '当前问题' : 'Current'}</button>
      <button type="button" role="tab" aria-selected={state === 'closed'} className={`btn ${state === 'closed' ? 'btn-primary' : 'btn-secondary'}`} onClick={() => switchState('closed')}>{zh ? '已关闭历史' : 'Closed history'}</button>
    </div>
    {issues.isPending ? <LoadingState /> : issues.error ? <ErrorState error={issues.error} onRetry={() => void issues.refetch()} /> : <>
      {issues.data.projection_incomplete ? <p className="inline-notice" role="status">{zh ? '问题投影正在有界重建；权威资源状态仍然有效，列表可能暂不完整。' : 'The issue projection is rebuilding within bounds. Authoritative resource state remains effective, but this list may be incomplete.'}</p> : null}
      {issues.data.data.length === 0 ? <EmptyState title={zh ? '没有问题' : 'No issues'} body={state === 'current' ? (zh ? '当前没有已投影的问题。' : 'There are no currently projected issues.') : (zh ? '没有已关闭历史。' : 'There is no closed history.')} /> : <div className="ops-stack">
        {issues.data.data.map((issue) => <Card key={issue.id} className="ops-stack">
          <div className="card-title-row"><h2>{SUMMARY[issue.summary_code]?.[zh ? 'zh' : 'en'] ?? issue.summary_code}</h2>
            <StatusBadge active={issue.state === 'current'} label={issue.state} danger={issue.state === 'current'} /></div>
          {issue.safe_detail ? <p>{issue.safe_detail}</p> : null}
          <dl className="ops-kv">
            <dt>{zh ? '资源类型' : 'Resource'}</dt><dd>{issue.resource_kind}</dd>
            <dt>{zh ? '首次发现' : 'First seen'}</dt><dd>{formatDateTime(issue.first_seen_at)}</dd>
            <dt>{zh ? '最近发现' : 'Last seen'}</dt><dd>{formatDateTime(issue.last_seen_at)}</dd>
            <dt>{zh ? '观测次数' : 'Observations'}</dt><dd className="mono">{issue.count}</dd>
            {issue.closed_at !== null ? <><dt>{zh ? '关闭时间' : 'Closed'}</dt><dd>{formatDateTime(issue.closed_at)}</dd></> : null}
          </dl>
          {issue.deep_link ? <Link className="btn btn-secondary" to={issue.deep_link.route_id === 'endpoint-detail' ? `/endpoints/${encodeURIComponent(issue.deep_link.resource_id)}` : '/models'}>{zh ? '查看当前资源' : 'Open current resource'}</Link> : <p className="table-note">{zh ? '来源资源已删除或不再提供安全深链。' : 'The source resource was removed or no longer has a safe deep link.'}</p>}
        </Card>)}
      </div>}
      <CursorPagination page={pager.page} nextCursor={issues.data.next_cursor} onPrevious={pager.previous} onNext={pager.next}
        labels={{ previous: zh ? '上一页' : 'Previous', next: zh ? '下一页' : 'Next', page: zh ? '页' : 'Page' }} />
    </>}
  </div>;
}
