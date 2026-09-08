import type {
  RecurringLimitRuleInput,
  RecurringLimitRuleView,
} from '@shared/operations/recurringLimits';

const RULE_STRUCTURAL_FIELDS = [
  'mode',
  'interval',
  'alignment',
  'time_zone',
  'week_starts_on',
  'metric',
] as const;

export interface RecurringLimitDraft extends RecurringLimitRuleInput {
  draftId: string;
}

export function recurringLimitDraftFromView(
  view: RecurringLimitRuleView,
  draftId = view.id ?? '',
): RecurringLimitDraft {
  return { ...view, draftId };
}

export function recurringLimitRuleStructureChanged(
  baseline: RecurringLimitRuleInput,
  draft: RecurringLimitRuleInput,
): boolean {
  return RULE_STRUCTURAL_FIELDS.some((field) => baseline[field] !== draft[field]);
}
