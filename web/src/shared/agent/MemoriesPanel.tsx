import { useCallback, useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ApiError } from '@shared/query/http';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { LoadingState } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { useSearchState } from '@shared/operations/useSearchState';
import { Button, Field, Note, Panel, PanelBody, PanelHead, Tabs } from '@shared/components/ui';
import {
  agentRoot,
  getMemory,
  getMemoryOwners,
  pageMetadata,
  putMemory,
  type AgentRole,
  type MemoryKind,
} from './api';
import { AgentError, AgentMarkdown, AgentTime, MemoryDiff } from './AgentParts';

function MemoryEditor({
  role,
  account,
  kind,
  active,
  owner = '',
  onRetain,
  onCurrentOwner,
}: {
  role: AgentRole;
  account: string;
  kind: MemoryKind;
  active: boolean;
  owner?: string;
  onRetain: (value: boolean) => void;
  onCurrentOwner: (id: string) => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const key = [...agentRoot(role, account), 'memory', kind, owner];
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => getMemory(role, kind, signal, owner),
    enabled: active,
    retry: false,
    gcTime: 0,
  });
  const [draft, setDraft] = useState<string>();
  const [revision, setRevision] = useState<string>();
  const content = draft ?? query.data?.content ?? '';
  const expected = revision ?? query.data?.revision ?? '0';
  const operation = useRetainedOperation<{ content: string; expected_revision: string }, unknown>(
    (input, _key, context) => putMemory(role, kind, input, context.signal, owner),
    async (input, error, context) => {
      if (error && responseOutcomeUnknown(error)) {
        const current = await getMemory(role, kind, context.signal, owner);
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
  const retain = locked || (draft !== undefined && draft !== query.data?.content);
  useEffect(() => onRetain(retain), [onRetain, retain]);
  useEffect(() => {
    if (kind === 'personal' && !owner && query.data?.owner_id) onCurrentOwner(query.data.owner_id);
  }, [kind, owner, query.data?.owner_id, onCurrentOwner]);
  return query.isPending ? (
    <LoadingState />
  ) : query.error ? (
    <AgentError error={query.error} onRetry={() => void query.refetch()} />
  ) : !(query.data.writable ?? (role === 'admin' || kind === 'personal')) ? (
    <div className="nb-stack">
      <Note>{t('common.agent.sharedMemoryReadOnly')}</Note>
      <AgentMarkdown>{query.data.content}</AgentMarkdown>
    </div>
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
  const [kind, setKind] = useState<MemoryKind>('personal');
  const [params, setParams] = useSearchState();
  const [currentOwner, setCurrentOwner] = useState('');
  const selectedOwner = role === 'admin' ? (params.get('ai_memory_owner') ?? '') : '';
  const owner = selectedOwner === currentOwner ? '' : selectedOwner;
  const currentEditor = kind + ':' + (kind === 'personal' ? owner : '');
  const ownerReady =
    role !== 'admin' || kind !== 'personal' || !selectedOwner || Boolean(currentOwner);
  const [retainedEditors, setRetainedEditors] = useState<string[]>([]);
  const retainEditor = useCallback(
    (id: string, retain: boolean) =>
      setRetainedEditors((current) => {
        if (retain) return current.includes(id) ? current : [...current, id];
        return current.includes(id) ? current.filter((value) => value !== id) : current;
      }),
    [],
  );
  const selectOwner = (id: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (id && id !== currentOwner) next.set('ai_memory_owner', id);
      else next.delete('ai_memory_owner');
      return next;
    });
  return (
    <Panel>
      <PanelHead title={t('common.agent.memories')} description={t('common.agent.memoryHelp')} />
      <PanelBody>
        <Tabs
          label={t('common.agent.memories')}
          value={kind}
          onChange={setKind}
          tabs={[
            { value: 'personal', label: t('common.agent.personalMemory') },
            { value: 'chat', label: t('common.agent.chatMemory') },
            { value: 'auto_review', label: t('common.agent.reviewMemory') },
          ]}
        />
        {role === 'admin' ? (
          <div hidden={kind !== 'personal'}>
            <MemoryOwnerPicker
              account={account}
              active={active && kind === 'personal'}
              owner={owner}
              onSelect={selectOwner}
              onCurrentOwner={setCurrentOwner}
            />
          </div>
        ) : null}
        {[...new Set([currentEditor, ...retainedEditors])].map((id) => {
          const [editorKind, editorOwner] = id.split(':') as [MemoryKind, string];
          return (
            <div key={id} hidden={id !== currentEditor}>
              {id === currentEditor && !ownerReady ? (
                <LoadingState />
              ) : (
                <MemoryEditor
                  role={role}
                  account={account}
                  kind={editorKind}
                  owner={editorOwner}
                  active={active && id === currentEditor}
                  onRetain={(retain) => retainEditor(id, retain)}
                  onCurrentOwner={setCurrentOwner}
                />
              )}
            </div>
          );
        })}
      </PanelBody>
    </Panel>
  );
}

function MemoryOwnerPicker({
  account,
  active,
  owner,
  onSelect,
  onCurrentOwner,
}: {
  account: string;
  active: boolean;
  owner: string;
  onSelect: (id: string) => void;
  onCurrentOwner: (id: string) => void;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'ai-memory-owners',
    scopeKey: account,
    resetKey: query,
    pageParam: 'ai_memory_page',
    pageSizeParam: 'ai_memory_page_size',
  });
  const list = useQuery({
    queryKey: [...agentRoot('admin', account), 'memory-owners', query, pager.page, pager.pageSize],
    queryFn: ({ signal }) => getMemoryOwners(query, pager.page, pager.pageSize, signal),
    enabled: active,
    retry: false,
  });
  const currentOwner = list.data?.current_owner_id;
  useEffect(() => {
    if (currentOwner) onCurrentOwner(currentOwner);
  }, [currentOwner, onCurrentOwner]);
  return (
    <div className="nb-stack nb-stack--sm">
      <Field label={t('common.agent.memoryOwnerSearch')}>
        {(props) => (
          <input
            {...props}
            type="search"
            value={query}
            placeholder={t('common.agent.memoryOwnerPlaceholder')}
            onChange={(event) => setQuery(event.target.value)}
          />
        )}
      </Field>
      <Field label={t('common.agent.memoryOwner')}>
        {(props) => (
          <select {...props} value={owner} onChange={(event) => onSelect(event.target.value)}>
            <option value="">{t('common.agent.myMemory')}</option>
            {owner && !list.data?.items.some((item) => item.id === owner) ? (
              <option value={owner}>{t('common.agent.targetUser', { id: owner })}</option>
            ) : null}
            {list.data?.items
              .filter((item) => item.id !== currentOwner)
              .map((item) => (
                <option key={item.id} value={item.id}>
                  {item.label || item.id} · {item.id}
                </option>
              ))}
          </select>
        )}
      </Field>
      {list.error ? (
        <AgentError error={list.error} onRetry={() => void list.refetch()} />
      ) : list.data ? (
        <PagePagination
          metadata={pageMetadata(list.data)}
          requestedPage={pager.page}
          busy={list.isFetching}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
        />
      ) : (
        <LoadingState />
      )}
    </div>
  );
}
