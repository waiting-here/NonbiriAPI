import { stateLabels, textDiff } from './presentation';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { ApiError } from '@shared/query/http';
import { ErrorState, EmptyState } from '@shared/components/States';
import { MarkdownText } from '@shared/components/MarkdownText';
import { CopyValue } from '@shared/components/CopyValue';
import { Fold, Note } from '@shared/components/ui';
import { useDateTimeFormatter } from '@shared/utils/datetime';

export function AgentState({ state }: { state: string }) {
  const { t } = useTranslation();
  return (
    <span
      className={
        'nb-badge agent-state agent-state--' +
        (['failed', 'interrupted'].includes(state)
          ? 'bad'
          : state === 'waiting_confirmation'
            ? 'warn'
            : 'normal')
      }
    >
      {t(stateLabels[state] ?? 'common.agent.states.unknown')}
    </span>
  );
}
export function AgentError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useTranslation();
  return error instanceof ApiError &&
    ['steward_permission_denied', 'forbidden'].includes(error.code) ? (
    <Note tone="warn">{t('common.agent.permissionHelp')}</Note>
  ) : (
    <ErrorState error={error} onRetry={onRetry} />
  );
}
export function AgentEmpty({ body }: { body: string }) {
  const { t } = useTranslation();
  return <EmptyState title={t('common.agent.empty')} body={body} />;
}
export function AgentTime({ value }: { value?: number | null }) {
  const format = useDateTimeFormatter();
  return (
    <span className="agent-number">{value ? format(new Date(value).toISOString()) : '—'}</span>
  );
}
export function RawData({ title, value }: { title: ReactNode; value: unknown }) {
  return (
    <Fold title={title} lazy>
      <AgentData title={title} value={value} />
    </Fold>
  );
}
export function AgentData({ title, value }: { title: ReactNode; value: unknown }) {
  const { t } = useTranslation();
  const text = typeof value === 'string' ? value : (JSON.stringify(value, null, 2) ?? '');
  return (
    <>
      <CopyValue
        value={text}
        label={typeof title === 'string' ? title : t('common.agent.operationDetails')}
        showValue={false}
      />
      <pre className="agent-raw">{text}</pre>
    </>
  );
}
export function AgentMarkdown({ children }: { children: string }) {
  return (
    <div className="agent-markdown">
      <MarkdownText>{children}</MarkdownText>
    </div>
  );
}
export function MemoryDiff({ before, after }: { before: string; after: string }) {
  const { t } = useTranslation();
  return (
    <div className="agent-diff" aria-label={t('common.agent.changes')}>
      <pre>
        {textDiff(before, after).map((line, index) => (
          <span key={index} className={'agent-diff__' + line.kind}>
            {line.kind === 'add' ? '+ ' : line.kind === 'remove' ? '− ' : '  '}
            {line.text}
            {'\n'}
          </span>
        ))}
      </pre>
    </div>
  );
}
