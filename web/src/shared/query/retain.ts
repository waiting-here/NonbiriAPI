import type { QueryKey } from '@tanstack/react-query';

export function retainQueryScope(scope: QueryKey) {
  return <T>(previous: T | undefined, query: { queryKey: QueryKey } | undefined): T | undefined =>
    query && scope.every((part, index) => Object.is(query.queryKey[index], part))
      ? previous
      : undefined;
}
