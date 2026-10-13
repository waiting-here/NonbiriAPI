/** Read a display response without forwarding fields this client does not consume. */
export function responseFields(
  value: unknown,
  required: readonly string[],
  fields: readonly string[],
  invalid: () => never,
): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) invalid();
  const record = value as Record<string, unknown>;
  if (required.some((key) => !Object.hasOwn(record, key))) invalid();
  return Object.fromEntries(
    fields.filter((key) => Object.hasOwn(record, key)).map((key) => [key, record[key]]),
  );
}
