import { roleLabels } from './presentation';
import { Fragment, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { LoadingState } from '@shared/components/States';
import { Button, Field, Fold, Note, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import { operationKey, responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { formatCount } from '@shared/utils/formatNumber';
import {
  agentRoot,
  getConversation,
  getMessages,
  getPlans,
  sendMessage,
  editMessage,
  clearConversation,
  cancelJob,
  activeJob,
  type AgentMessage,
  type AgentPlan,
  type AgentRole,
  type SendMessage,
  type Conversation,
} from './api';
import {
  AgentEmpty,
  AgentError,
  AgentMarkdown,
  AgentState,
  AgentTime,
  RawData,
} from './AgentParts';
import { OperationPlan } from './OperationPlan';
import { ToolActivity } from './ToolActivity';
import { indexToolResults, planMessageID } from './toolActivityData';

type Intent =
  | { kind: 'send' | 'edit'; id?: string; body: SendMessage }
  | { kind: 'clear'; generation: string }
  | { kind: 'cancel'; id: string };
export function ChatMessage({
  message,
  editable,
  onEdit,
  toolResults,
  plans,
  running,
}: {
  message: AgentMessage;
  editable?: boolean;
  onEdit?: (message: AgentMessage) => void;
  toolResults?: Map<number, AgentMessage>;
  plans?: AgentPlan[];
  running?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <article
      className={
        'agent-message agent-message--' +
        message.role +
        (message.tool_calls && !message.content ? ' agent-message--activity' : '')
      }
    >
      <div className="agent-message__head">
        <strong>{t(roleLabels[message.role] ?? 'common.agent.roles.tool')}</strong>
        <AgentTime value={message.created_at} />
        {!message.active ? <span>{t('common.agent.archived')}</span> : null}
        {editable ? (
          <Button variant="ghost" onClick={() => onEdit?.(message)}>
            {t('common.agent.editRegenerate')}
          </Button>
        ) : null}
      </div>
      {message.content && message.role !== 'tool' ? (
        <AgentMarkdown>{message.content}</AgentMarkdown>
      ) : null}
      {message.tool_calls != null ? (
        <ToolActivity message={message} results={toolResults} plans={plans} running={running} />
      ) : null}
      {message.role === 'tool' && message.content ? (
        <RawData title={t('common.agent.toolResult')} value={message.content} />
      ) : null}
    </article>
  );
}
export function ChatPanel({
  role,
  account,
  active,
}: {
  role: AgentRole;
  account: string;
  active: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const root = agentRoot(role, account);
  const query = useQuery({
    queryKey: [...root, 'chat'],
    queryFn: ({ signal }) => getConversation(role, signal),
    enabled: active,
    retry: false,
    refetchInterval: ({ state }) =>
      state.data?.job &&
      ['pending', 'running', 'waiting_confirmation'].includes(state.data.job.state)
        ? 1500
        : false,
  });
  const [draft, setDraft] = useState('');
  const [editing, setEditing] = useState<AgentMessage | null>(null);
  const [older, setOlder] = useState<AgentMessage[]>([]);
  const [olderPlans, setOlderPlans] = useState<AgentPlan[]>([]);
  const [more, setMore] = useState(true);
  const [olderBusy, setOlderBusy] = useState(false);
  const historyRequest = useRef(0);
  const [olderError, setOlderError] = useState<unknown>(null);
  const [confirmClear, setConfirmClear] = useState(false);
  const [generation, setGeneration] = useState(query.data?.generation);
  if (query.data && generation !== query.data.generation) {
    setGeneration(query.data.generation);
    if (older.length) setOlder([]);
    if (olderPlans.length) setOlderPlans([]);
    if (!more) setMore(true);
  }
  const refresh = () => client.invalidateQueries({ queryKey: root }, { throwOnError: true });
  const execute = async (intent: Intent, signal: AbortSignal) => {
    if (intent.kind === 'send') return sendMessage(role, intent.body, signal);
    if (intent.kind === 'edit') return editMessage(role, intent.id!, intent.body, signal);
    if (intent.kind === 'clear') return clearConversation(role, intent.generation, signal);
    return cancelJob(role, intent.id!, signal);
  };
  const operation = useRetainedOperation<Intent, unknown>(
    (intent, _key, context) => execute(intent, context.signal),
    async (intent, error, context) => {
      if (error && responseOutcomeUnknown(error)) {
        if (intent.kind === 'clear') {
          const current = await getConversation(role, context.signal);
          if (BigInt(current.generation) <= BigInt(intent.generation))
            await execute(intent, context.signal);
        } else await execute(intent, context.signal);
      }
      if (!error || responseOutcomeUnknown(error))
        context.commit(() => {
          if (intent.kind === 'send' || intent.kind === 'edit') {
            setDraft('');
            setEditing(null);
          }
          if (intent.kind === 'edit' || intent.kind === 'clear') {
            historyRequest.current++;
            setOlder([]);
            setOlderPlans([]);
            setOlderBusy(false);
            setOlderError(null);
          }
          if (intent.kind === 'clear') {
            setDraft('');
            setEditing(null);
            setConfirmClear(false);
          }
        });
      await refresh();
      if (error && responseOutcomeUnknown(error)) return { operationConfirmed: true };
    },
    role === 'admin' ? ['admin'] : ['user'],
  );
  const rows = [
    ...new Map([...older, ...(query.data?.messages ?? [])].map((row) => [row.id, row])).values(),
  ].sort((a, b) => (BigInt(a.id) < BigInt(b.id) ? -1 : 1));
  const plans = [
    ...new Map([...olderPlans, ...(query.data?.plans ?? [])].map((row) => [row.id, row])).values(),
  ];
  const toolResults = indexToolResults(rows);
  const planMessages = new Map(
    plans.map((plan) => [plan.id, planMessageID(plan, rows, query.data?.job)]),
  );
  const plansFor = (message: AgentMessage) =>
    plans.filter((plan) => planMessages.get(plan.id) === message.id);
  const lastUser = [...rows].reverse().find((row) => row.role === 'user');
  const locked =
    operation.isPending || operation.outcome === 'unknown' || Boolean(operation.refreshError);
  const running = activeJob(query.data?.job) || query.data?.job?.state === 'waiting_confirmation';
  const loadOlder = async () => {
    const generation = query.data?.generation;
    const request = ++historyRequest.current;
    const current = () =>
      request === historyRequest.current &&
      generation === client.getQueryData<Conversation>([...root, 'chat'])?.generation;
    setOlderBusy(true);
    setOlderError(null);
    try {
      const before = plans.length
        ? plans.reduce((id, row) => (BigInt(row.id) < BigInt(id) ? row.id : id), plans[0]!.id)
        : '0';
      const [value, history] = await Promise.all([
        getMessages(role, rows[0]?.id ?? '0'),
        getPlans(role, before),
      ]);
      if (!current()) return;
      setOlder((previous) => [...value, ...previous]);
      setOlderPlans((previous) => [...history.items, ...previous]);
      setMore(value.length === 100);
    } catch (error) {
      if (current()) setOlderError(error);
    } finally {
      if (request === historyRequest.current) setOlderBusy(false);
    }
  };
  return (
    <Panel>
      <PanelHead
        title={t('common.agent.chat')}
        actions={
          <Button
            variant="secondary"
            disabled={locked || !query.data?.id || query.data.id === '0'}
            onClick={() => setConfirmClear(true)}
          >
            {t('common.agent.clearChat')}
          </Button>
        }
      />
      <PanelBody>
        {query.isPending ? (
          <LoadingState />
        ) : query.error ? (
          <AgentError error={query.error} onRetry={() => void query.refetch()} />
        ) : (
          <>
            <Note>{t('common.agent.chatHelp')}</Note>
            {!query.data.enabled ? <Note>{t('common.agent.chatUnavailable')}</Note> : null}
            {query.data.summary ? (
              <Fold title={t('common.agent.compactedContext')}>
                <AgentMarkdown>{query.data.summary}</AgentMarkdown>
              </Fold>
            ) : null}
            {query.data.job ? (
              <div className="agent-job">
                <AgentState
                  state={query.data.job.queue.position > 0 ? 'pending' : query.data.job.state}
                />
                {['compaction', 'compact'].includes(query.data.job.kind) ? (
                  <span>{t('common.agent.compaction')}</span>
                ) : null}
                {running ? (
                  <>
                    {query.data.job.queue.position > 0 ? (
                      <span className="agent-number">
                        {t('common.agent.queue', {
                          position: formatCount(query.data.job.queue.position).display,
                          length: formatCount(query.data.job.queue.length).display,
                        })}
                      </span>
                    ) : null}
                    <Button
                      variant="secondary"
                      disabled={locked}
                      onClick={() => operation.mutate({ kind: 'cancel', id: query.data.job!.id })}
                    >
                      {t('common.agent.stop')}
                    </Button>
                  </>
                ) : null}
                {query.data.job.error ? (
                  <Fold title={t('common.agent.failureDetails')}>
                    <p>{query.data.job.error}</p>
                  </Fold>
                ) : null}
              </div>
            ) : null}
            <div className="agent-transcript" role="log" aria-label={t('common.agent.chat')}>
              {query.data.has_older && more ? (
                <Button variant="secondary" disabled={olderBusy} onClick={() => void loadOlder()}>
                  {t('common.agent.loadOlder')}
                </Button>
              ) : null}
              {olderError ? (
                <AgentError error={olderError} onRetry={() => void loadOlder()} />
              ) : null}
              {!rows.length ? (
                <AgentEmpty body={t('common.agent.chatEmpty')} />
              ) : (
                rows
                  .filter((row) => !toolResults.consumed.has(row.id))
                  .map((row) => (
                    <Fragment key={row.id}>
                      <ChatMessage
                        message={row}
                        toolResults={toolResults.results.get(row.id)}
                        plans={plansFor(row)}
                        running={Boolean(
                          running &&
                          query.data.job &&
                          row.generation === query.data.generation &&
                          row.created_at >= query.data.job.created_at,
                        )}
                        editable={
                          query.data.enabled &&
                          !locked &&
                          ((role === 'admin' &&
                            (row.role === 'user' || row.role === 'assistant')) ||
                            (role === 'steward' && row.id === lastUser?.id))
                        }
                        onEdit={(message) => {
                          setEditing(message);
                          setDraft(message.content);
                        }}
                      />
                      {plansFor(row).map((plan) => (
                        <OperationPlan
                          key={plan.id}
                          role={role}
                          account={account}
                          plan={plan}
                          readOnly={!query.data.enabled}
                        />
                      ))}
                    </Fragment>
                  ))
              )}
            </div>
            {plans
              .filter((plan) => !planMessages.get(plan.id))
              .map((plan) => (
                <OperationPlan
                  key={plan.id}
                  role={role}
                  account={account}
                  plan={plan}
                  readOnly={!query.data.enabled}
                />
              ))}
            {editing ? (
              <Note>
                <p>{t('common.agent.editingHelp')}</p>
                <Button
                  variant="secondary"
                  onClick={() => {
                    setEditing(null);
                    setDraft('');
                  }}
                >
                  {t('common.cancel')}
                </Button>
              </Note>
            ) : null}
            <form
              className="agent-composer nb-form"
              onSubmit={(event) => {
                event.preventDefault();
                if (!query.data.enabled || !draft.trim() || locked) return;
                operation.mutate({
                  kind: editing ? 'edit' : 'send',
                  id: editing?.id,
                  body: {
                    content: draft,
                    expected_generation: query.data.generation,
                    request_key: operationKey(),
                  },
                });
              }}
            >
              <Field label={t('common.agent.message')} help={t('common.agent.compactHelp')}>
                {(props) => (
                  <textarea
                    {...props}
                    rows={4}
                    value={draft}
                    disabled={locked || !query.data.enabled}
                    onChange={(event) => setDraft(event.target.value)}
                  />
                )}
              </Field>
              <div className="nb-actions">
                <Button
                  variant="primary"
                  type="submit"
                  disabled={
                    !query.data.enabled || locked || !draft.trim() || Boolean(running && !editing)
                  }
                >
                  {t(editing ? 'common.agent.regenerate' : 'common.agent.send')}
                </Button>
              </div>
            </form>
          </>
        )}
        {operation.error ? <AgentError error={operation.error} /> : null}
        {operation.outcome === 'unknown' ? (
          <Note tone="warn">
            <p>{t('common.agent.unknownWrite')}</p>
            <Button
              variant="secondary"
              disabled={operation.isPending}
              onClick={() => void operation.check()}
            >
              {t('common.connections.checkResult')}
            </Button>
          </Note>
        ) : null}
        {operation.refreshError ? (
          <AgentError error={operation.refreshError} onRetry={() => void operation.refresh()} />
        ) : null}
      </PanelBody>
      <ConfirmDialog
        open={confirmClear}
        title={t('common.agent.clearChat')}
        description={t('common.agent.clearHelp')}
        confirmLabel={t('common.agent.clearChat')}
        busy={operation.isPending}
        onCancel={() => setConfirmClear(false)}
        onConfirm={() =>
          query.data && operation.mutate({ kind: 'clear', generation: query.data.generation })
        }
      />
    </Panel>
  );
}
