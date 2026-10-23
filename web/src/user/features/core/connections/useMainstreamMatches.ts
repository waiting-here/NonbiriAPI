import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getEndpointCreateOptions } from '../api';
import { coreKeys } from '../queries';

function safeMatchingInput(input: string) {
  if (!input || input.length > 4096 || /[?#]/.test(input)) return false;
  try {
    const url = new URL(input);
    const authority = input.match(/^https?:\/\/([^/?#]*)/i)?.[1];
    return (
      (url.protocol === 'http:' || url.protocol === 'https:') &&
      authority !== undefined &&
      !authority.includes('@') &&
      !url.username &&
      !url.password
    );
  } catch {
    return false;
  }
}

export function useMainstreamMatches(accountId: string, baseURL: string, enabled: boolean) {
  const candidate = baseURL.trim();
  const input = safeMatchingInput(candidate) ? candidate : '';
  const [settled, setSettled] = useState({ accountId: '', input: '' });
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled({ accountId, input }), 350);
    return () => window.clearTimeout(timer);
  }, [accountId, input]);
  const current =
    enabled && Boolean(input) && settled.accountId === accountId && settled.input === input;
  const query = useQuery({
    queryKey: [...coreKeys.endpointCreateOptions(accountId), 'matching', input],
    queryFn: ({ signal }) => getEndpointCreateOptions(signal, input),
    enabled: current,
    staleTime: 60_000,
    gcTime: 60_000,
    retry: false,
  });
  return current ? query.data : undefined;
}
