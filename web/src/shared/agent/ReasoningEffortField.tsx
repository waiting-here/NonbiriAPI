import { useTranslation } from 'react-i18next';
import { Field } from '@shared/components/ui';
import type { DriverProvider } from './driversApi';

const options = [
  ['none', 'common.agent.reasoningEffort.none'],
  ['minimal', 'common.agent.reasoningEffort.minimal'],
  ['low', 'common.agent.reasoningEffort.low'],
  ['medium', 'common.agent.reasoningEffort.medium'],
  ['high', 'common.agent.reasoningEffort.high'],
  ['xhigh', 'common.agent.reasoningEffort.xhigh'],
  ['max', 'common.agent.reasoningEffort.max'],
] as const;

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

export function ReasoningEffortField({
  provider,
  value,
  onChange,
  disabled = false,
}: {
  provider: DriverProvider;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  let parameters: Record<string, unknown> | null = null;
  try {
    const parsed: unknown = JSON.parse(value);
    if (object(parsed)) parameters = parsed;
  } catch {
    // Preserve a partially edited advanced JSON draft until it can be merged safely.
  }
  const chat = provider === 'chat_completions';
  const reasoning = parameters?.reasoning;
  const editable = parameters !== null && (chat || reasoning === undefined || object(reasoning));
  const effort = chat
    ? parameters?.reasoning_effort
    : object(reasoning)
      ? reasoning.effort
      : undefined;
  const choices = options.filter(
    ([key]) => key !== 'max' || provider === 'responses' || provider === 'chatgpt',
  );
  const selected =
    effort === undefined ? '' : choices.some(([key]) => key === effort) ? String(effort) : 'custom';
  return (
    <Field
      label={t('common.agent.reasoningEffort.label')}
      help={t(
        editable ? 'common.agent.reasoningEffort.help' : 'common.agent.reasoningEffort.fixJson',
      )}
    >
      {(props) => (
        <select
          {...props}
          disabled={disabled || !editable}
          value={selected}
          onChange={(event) => {
            if (!parameters || !editable) return;
            const effort = event.target.value;
            const next = { ...parameters };
            if (chat) {
              if (effort) next.reasoning_effort = effort;
              else delete next.reasoning_effort;
            } else {
              const nextReasoning: Record<string, unknown> = object(reasoning)
                ? { ...reasoning }
                : {};
              if (effort) nextReasoning.effort = effort;
              else delete nextReasoning.effort;
              if (Object.keys(nextReasoning).length) next.reasoning = nextReasoning;
              else delete next.reasoning;
            }
            onChange(JSON.stringify(next, null, 2));
          }}
        >
          <option value="">{t('common.agent.reasoningEffort.default')}</option>
          {choices.map(([key, label]) => (
            <option key={key} value={key}>
              {t(label)}
            </option>
          ))}
          {selected === 'custom' ? (
            <option value="custom" disabled>
              {t('common.agent.reasoningEffort.custom')}
            </option>
          ) : null}
        </select>
      )}
    </Field>
  );
}
