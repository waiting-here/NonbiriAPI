import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ApiError } from '@shared/query/http';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { LoadingState } from '@shared/components/States';
import { Button, Field, Note, Panel, PanelBody, PanelHead, Tabs } from '@shared/components/ui';
import { agentRoot, getMemory, putMemory, type AgentRole, type MemoryKind } from './api';
import { AgentError, AgentMarkdown, AgentTime, MemoryDiff } from './AgentParts';

function MemoryEditor({
  role,
  account,
  kind,
  active,
}: {
  role: AgentRole;
  account: string;
  kind: MemoryKind;
  active: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const key = [...agentRoot(role, account), 'memory', kind];
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => getMemory(role, kind, signal),
    enabled: active,
    retry: false,
  });
  const [draft, setDraft] = useState<string>();
  const [revision, setRevision] = useState<string>();
  const content = draft ?? query.data?.content ?? '';
  const expected = revision ?? query.data?.revision ?? '0';
  const operation = useRetainedOperation<{ content: string; expected_revision: string }, unknown>(
    (input, _key, context) => putMemory(role, kind, input, context.signal),
    async (input, error, context) => {
      if (error && responseOutcomeUnknown(error)) {
        const current = await getMemory(role, kind, context.signal);
        context.commit(() => client.setQueryData(key, current));
        if (current.content !== input.content) return;
      }
      await client.invalidateQueries({ queryKey: key }, { throwOnError: true });
      if (!error || responseOutcomeUnknown(error))
        context.commit(() => {
          setDraft(undefined);
          setRevision(undefined);
        });
      if (error && responseOutcomeUnknown(error)) return { operationConfirmed: true };
    },
    role === 'admin' ? ['admin'] : ['user'],
  );
  const conflict = operation.error instanceof ApiError && operation.error.code === 'conflict';
  const locked =
    operation.isPending || operation.outcome === 'unknown' || Boolean(operation.refreshError);
  return query.isPending ? (
    <LoadingState />
  ) : query.error ? (
    <AgentError error={query.error} onRetry={() => void query.refetch()} />
  ) : (
    <div className="nb-stack">
      <p className="nb-small nb-muted">
        {t('common.agent.memoryUpdated', {
          revision: query.data.revision,
          actor: query.data.updated_by || '—',
        })}{' '}
        · <AgentTime value={query.data.updated_at} />
      </p>
      <div className="agent-before-after">
        <Field label={t('common.agent.memoryContent')}>
          {(props) => (
            <textarea
              {...props}
              className="agent-memory-editor"
              rows={18}
              value={content}
              disabled={locked}
              onChange={(event) => {
                setDraft(event.target.value);
                setRevision((current) => current ?? query.data.revision);
              }}
            />
          )}
        </Field>
        <section>
          <h3>{t('common.agent.preview')}</h3>
          <AgentMarkdown>{content}</AgentMarkdown>
        </section>
      </div>
      <div className="nb-actions">
        <Button
          disabled={locked || content === query.data.content || conflict}
          onClick={() => operation.mutate({ content, expected_revision: expected })}
        >
          {t('common.save')}
        </Button>
        <Button
          variant="secondary"
          disabled={locked || draft === undefined}
          onClick={() => {
            setDraft(undefined);
            setRevision(undefined);
            operation.reset();
          }}
        >
          {t('common.cancel')}
        </Button>
      </div>
      {operation.error ? <AgentError error={operation.error} /> : null}
      {conflict ? (
        <Note tone="warn">
          <p>{t('common.agent.memoryConflict')}</p>
          <Button variant="secondary" onClick={() => void query.refetch()}>
            {t('common.agent.loadCurrent')}
          </Button>
          {query.data.revision !== expected ? (
            <>
              <MemoryDiff before={query.data.content} after={content} />
              <Button
                variant="secondary"
                onClick={() => {
                  setRevision(query.data.revision);
                  operation.reset();
                }}
              >
                {t('common.agent.keepDraft')}
              </Button>
            </>
          ) : null}
        </Note>
      ) : null}
      {operation.outcome === 'unknown' ? (
        <Note tone="warn">
          <p>{t('common.agent.unknownWrite')}</p>
          <Button
            variant="secondary"
            onClick={() => operation.variables && operation.mutate(operation.variables)}
            disabled={operation.isPending}
          >
            {t('common.connections.checkResult')}
          </Button>
        </Note>
      ) : null}
      {operation.isSuccess ? <Note tone="ok">{t('common.outcome.saved')}</Note> : null}
      {operation.refreshError ? (
        <AgentError error={operation.refreshError} onRetry={() => void operation.refresh()} />
      ) : null}
    </div>
  );
}
export function MemoriesPanel({
  role,
  account,
  active,
}: {
  role: AgentRole;
  account: string;
  active: boolean;
}) {
  const { t } = useTranslation();
  const [kind, setKind] = useState<MemoryKind>('chat');
  return (
    <Panel>
      <PanelHead title={t('common.agent.memories')} description={t('common.agent.memoryHelp')} />
      <PanelBody>
        <Tabs
          label={t('common.agent.memories')}
          value={kind}
          onChange={setKind}
          tabs={[
            { value: 'chat', label: t('common.agent.chatMemory') },
            { value: 'auto_review', label: t('common.agent.reviewMemory') },
          ]}
        />
        {(['chat', 'auto_review'] as const).map((value) => (
          <div key={value} hidden={kind !== value}>
            <MemoryEditor
              role={role}
              account={account}
              kind={value}
              active={active && kind === value}
            />
          </div>
        ))}
      </PanelBody>
    </Panel>
  );
}
