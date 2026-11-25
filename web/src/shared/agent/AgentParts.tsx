import { DriverFailure, decodeOperationFailure } from './DriverFailure';
import { stateLabels, textDiff } from './presentation';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { ApiError } from '@shared/query/http';
import { ErrorState, EmptyState } from '@shared/components/States';
import { MarkdownText } from '@shared/components/MarkdownText';
import { CopyValue } from '@shared/components/CopyValue';
import { Fold, Note, StatusBadge } from '@shared/components/ui';
import { useDateTimeFormatter } from '@shared/utils/datetime';

export function AgentState({ state }: { state: string }) {
  const { t } = useTranslation();
  const tone = ['succeeded', 'completed', 'approved'].includes(state)
    ? 'ok'
    : ['pending', 'running', 'waiting_confirmation'].includes(state)
      ? 'warn'
      : ['failed', 'interrupted', 'rejected'].includes(state)
        ? 'bad'
        : ['cancelled', 'invalidated', 'superseded', 'unknown'].includes(state)
          ? 'neutral'
          : 'info';
  return (
    <StatusBadge tone={tone}>{t(stateLabels[state] ?? 'common.agent.states.unknown')}</StatusBadge>
  );
}
export function AgentError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useTranslation();
  const operation =
    error instanceof ApiError ? decodeOperationFailure(error.operationDiagnostic) : null;
  if (operation) return <DriverFailure failure={operation} onRetry={onRetry} />;
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
  const instant = value == null ? null : new Date(value);
  return (
    <span className="agent-number">
      {instant && Number.isFinite(instant.getTime()) ? format(instant.toISOString()) : '—'}
    </span>
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
  const diff = textDiff(before, after);
  type Line = (typeof diff)[number] | { kind: 'omit'; count: number };
  let rows: Line[] = diff;
  if (diff.length > 400) {
    rows = [];
    for (let start = 0; start < diff.length;) {
      if (diff[start]!.kind !== 'same') {
        rows.push(diff[start++]!);
        continue;
      }
      let end = start + 1;
      while (end < diff.length && diff[end]!.kind === 'same') end++;
      if (end - start <= 7) rows.push(...diff.slice(start, end));
      else
        rows.push(
          ...diff.slice(start, start + 3),
          { kind: 'omit', count: end - start - 6 },
          ...diff.slice(end - 3, end),
        );
      start = end;
    }
    if (rows.length > 400) {
      const hidden = rows
        .slice(200, -200)
        .reduce((sum, line) => sum + (line.kind === 'omit' ? line.count : 1), 0);
      rows = [...rows.slice(0, 200), { kind: 'omit', count: hidden }, ...rows.slice(-200)];
    }
  }
  return (
    <div className="agent-diff" aria-label={t('common.agent.changes')}>
      <pre>
        {rows.map((line, index) => (
          <span key={index} className={'agent-diff__' + line.kind}>
            {line.kind === 'add' ? '+ ' : line.kind === 'remove' ? '− ' : '  '}
            {line.kind === 'omit'
              ? t('common.agent.diffOmitted', { count: line.count })
              : line.text.length > 1000
                ? line.text.slice(0, 1000) + '…'
                : line.text}
            {'\n'}
          </span>
        ))}
      </pre>
    </div>
  );
}
