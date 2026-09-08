/** Accept only a return location within the exact caller-owned list route. */
export function listReturnPath(state: unknown, listPath: string): string {
  if (!state || typeof state !== 'object' || Array.isArray(state)) return listPath;
  const value = (state as Record<string, unknown>).returnTo;
  if (
    typeof value !== 'string' ||
    value.length > 4096 ||
    [...value].some((character) => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127)
  )
    return listPath;
  return value === listPath || value.startsWith(`${listPath}?`) ? value : listPath;
}
