import { array, integer, nullableString, record, string } from './wire';

export interface AutomaticReason {
  kind: string;
  schema_version: number;
  manual_text: string;
  rules: { name: string }[];
}
export function normalizeAutomaticReason(value: unknown): AutomaticReason | null {
  if (value == null) return null;
  const root = record(
    value,
    ['kind', 'schema_version', 'params', 'manual_text', 'rules'],
    'automatic reason',
    ['kind', 'schema_version', 'params'],
  );
  return {
    kind: string(root.kind, 'automatic reason kind', { min: 1, max: 64 }),
    schema_version: integer(root.schema_version, 'automatic reason version', 1, 100),
    manual_text:
      nullableString(root.manual_text ?? null, 'manual reason', {
        max: 4096,
        bytes: 4096,
        multiline: true,
      }) ?? '',
    rules: array(root.rules ?? [], 'reason rules', 100).map((value) => {
      const item = record(value, ['name', 'rule_id', 'revision'], 'reason rule', ['name']);
      return { name: string(item.name, 'rule name', { min: 1, max: 120, bytes: 480 }) };
    }),
  };
}
