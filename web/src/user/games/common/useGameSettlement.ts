import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { gameKeys } from './snapshot';

/** Async results can arrive through polling or a stream, without a local mutation. */
export function useGameSettlement(resultID: string | undefined) {
  const queryClient = useQueryClient();
  useEffect(() => {
    if (resultID) {
      void queryClient.invalidateQueries({ queryKey: gameKeys.snapshot }, { cancelRefetch: false });
    }
  }, [queryClient, resultID]);
}
