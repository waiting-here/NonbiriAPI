/** Only return to a list that the caller explicitly supports. */
export function listReturnPath(state: unknown, listPath: string, ...otherLists: string[]): string {
  if (!state || typeof state !== 'object' || Array.isArray(state)) return listPath;
  const value = (state as Record<string, unknown>).returnTo;
  if (
    typeof value !== 'string' ||
    value.length > 4096 ||
    [...value].some((character) => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127)
  )
    return listPath;
  return [listPath, ...otherLists].some((path) => value === path || value.startsWith(`${path}?`))
    ? value
    : listPath;
}
