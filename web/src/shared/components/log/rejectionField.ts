import type { TFunction } from 'i18next';

export function rejectionFieldLabel(field: string, t: TFunction): string {
  const topLevel = /^request\.fields\[(\d+)\]$/.exec(field);
  if (topLevel) return t('logs.rejectionTopLevelField', { position: topLevel[1] });

  const match = /^messages\[(\d+)\](?:\.(content|tool_calls)\[(\d+)\])?(?:\.|$)/.exec(field);
  if (!match) return field;
  const message = Number(match[1]) + 1;
  const item = Number(match[3]) + 1;
  if (match[2] === 'content') return t('logs.rejectionContentBlock', { message, item, field });
  if (match[2] === 'tool_calls') return t('logs.rejectionToolCall', { message, item, field });
  return t('logs.rejectionMessage', { message, field });
}
