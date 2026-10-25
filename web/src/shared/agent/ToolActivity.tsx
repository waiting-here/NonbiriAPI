import { useTranslation } from 'react-i18next';
import { useMemo } from 'react';
import { Icon, type IconName } from '@shared/components/Icon';
import { Fold } from '@shared/components/ui';
import { formatCount } from '@shared/utils/formatNumber';
import { AgentData, RawData } from './AgentParts';
import type { AgentMessage, AgentPlan } from './api';
import { readToolCalls } from './toolActivityData';

const toolIcons: Record<string, IconName> = {
  donations: 'charity',
  donation_models: 'models',
  review_history: 'logs',
  charity_models: 'models',
  users: 'users',
  site_status: 'dashboard',
  web_search: 'search',
  propose_operations: 'steward',
  edit_memory: 'reports',
};
const toolLabels: Record<string, string> = {
  donations: 'common.agent.tools.donations',
  donation_models: 'common.agent.tools.donation_models',
  review_history: 'common.agent.tools.review_history',
  charity_models: 'common.agent.tools.charity_models',
  users: 'common.agent.tools.users',
  site_status: 'common.agent.tools.site_status',
  web_search: 'common.agent.tools.web_search',
  propose_operations: 'common.agent.tools.propose_operations',
  edit_memory: 'common.agent.tools.edit_memory',
};
const activityLabels = {
  returned: 'common.agent.activity.states.returned',
  completed: 'common.agent.activity.states.completed',
  approved: 'common.agent.activity.states.approved',
  failed: 'common.agent.activity.states.failed',
  rejected: 'common.agent.activity.states.rejected',
  waiting_confirmation: 'common.agent.activity.states.waiting_confirmation',
  invalidated: 'common.agent.activity.states.invalidated',
  running: 'common.agent.activity.states.running',
  unrecorded: 'common.agent.activity.states.unrecorded',
  interrupted: 'common.agent.activity.states.interrupted',
  cancelled: 'common.agent.activity.states.cancelled',
};
function parsed(value: unknown): unknown {
  if (typeof value !== 'string') return value;
  try {
    return JSON.parse(value) as unknown;
  } catch {
    return value;
  }
}
function object(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

export function ToolActivity({
  message,
  results,
  plans = [],
  running = false,
}: {
  message: AgentMessage;
  results?: Map<number, AgentMessage>;
  plans?: AgentPlan[];
  running?: boolean;
}) {
  const { t } = useTranslation();
  const calls = useMemo(() => readToolCalls(message), [message]);
  const callData = useMemo(
    () =>
      calls.map((call, index) => ({
        call,
        response: results?.get(index),
        inputValue: parsed(call.arguments),
        outputValue: parsed(results?.get(index)?.content),
      })),
    [calls, results],
  );
  if (!calls.length)
    return <RawData title={t('common.agent.toolCalls')} value={message.tool_calls} />;
  return (
    <div className="agent-tool-stack">
      {callData.map(({ call, response, inputValue, outputValue }, index) => {
        const input = object(inputValue);
        const showRaw = !toolIcons[call.name] || !call.id || call.arguments === undefined;
        const inputTitle = t(
          showRaw ? 'common.agent.toolCalls' : 'common.agent.activity.arguments',
        );
        const output = object(outputValue);
        const plan = plans.find((item) => call.id && item.tool_call_ids?.includes(call.id));
        const error = typeof output.error === 'string' ? output.error : '';
        const state =
          error || output.status === 'failed'
            ? 'failed'
            : output.status === 'interrupted' || output.status === 'cancelled'
              ? output.status
              : output.status === 'rejected' || plan?.state === 'rejected'
                ? 'rejected'
                : output.status === 'completed'
                  ? 'completed'
                  : response
                    ? 'returned'
                    : plan?.state === 'pending'
                      ? 'waiting_confirmation'
                      : plan?.state === 'approved'
                        ? 'approved'
                        : plan?.state === 'superseded' || plan?.state === 'invalidated'
                          ? 'invalidated'
                          : running
                            ? 'running'
                            : 'unrecorded';
        const tone =
          state === 'failed'
            ? 'bad'
            : ['waiting_confirmation', 'interrupted', 'cancelled'].includes(state)
              ? 'warn'
              : state === 'completed'
                ? 'ok'
                : 'quiet';
        const title =
          call.name === 'edit_memory' && input.action === 'read'
            ? t('common.agent.activity.readMemory')
            : toolIcons[call.name]
              ? t(toolLabels[call.name]!)
              : call.name || t('common.agent.toolCalls');
        const subject =
          call.name === 'edit_memory'
            ? input.kind === 'chat'
              ? t('common.agent.chatMemory')
              : input.kind === 'auto_review'
                ? t('common.agent.reviewMemory')
                : ''
            : typeof input.query === 'string'
              ? input.query
              : typeof input.donation_id === 'string'
                ? t('common.agent.donation') + ' #' + input.donation_id
                : '';
        const items = [output.items, output.data, output.models, output.results].find(
          Array.isArray,
        );
        const detail =
          error ||
          (typeof output.reason === 'string' ? output.reason : '') ||
          (typeof output.message === 'string' ? output.message : '') ||
          (typeof output.revision === 'string' && call.name === 'edit_memory'
            ? t('common.agent.activity.memoryVersion', { version: output.revision })
            : items
              ? t('common.agent.activity.returnedItems', {
                  count: items.length,
                  value: formatCount(items.length).display,
                })
              : plan
                ? t('common.agent.activity.operationCount', {
                    count: plan.operations.length,
                    value: formatCount(plan.operations.length).display,
                  })
                : '');
        return (
          <Fold
            key={index}
            className={'agent-tool-activity agent-tool-activity--' + tone}
            title={
              <span className="agent-tool-title">
                <Icon name={toolIcons[call.name] ?? 'settings'} />
                {title}
              </span>
            }
            summary={
              subject || detail ? (
                <span
                  className="agent-tool-summary"
                  title={[subject, detail].filter(Boolean).join(' · ')}
                >
                  {[subject, detail].filter(Boolean).join(' · ')}
                </span>
              ) : undefined
            }
            meta={
              <span className={'agent-tool-status agent-tool-status--' + tone}>
                <Icon
                  name={
                    tone === 'ok'
                      ? 'check'
                      : tone === 'bad'
                        ? 'error'
                        : state === 'rejected'
                          ? 'close'
                          : 'info'
                  }
                />
                {t(activityLabels[state])}
              </span>
            }
            lazy
          >
            <h4>{inputTitle}</h4>
            <AgentData title={inputTitle} value={showRaw ? call.raw : inputValue} />
            {response ? (
              <>
                <h4>{t('common.agent.toolResult')}</h4>
                <AgentData title={t('common.agent.toolResult')} value={outputValue} />
              </>
            ) : null}
          </Fold>
        );
      })}
    </div>
  );
}
