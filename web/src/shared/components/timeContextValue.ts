import { createContext, useContext } from 'react';
import { browserTimeContext, type TimeContext } from '../time';

export const browserContext = browserTimeContext();
export const unavailableSiteContext: TimeContext = { mode: 'site', offset_minutes: null };
export const DisplayTimeContext = createContext<TimeContext>(browserContext);

export function useDisplayTimeContext(): TimeContext {
  return useContext(DisplayTimeContext);
}

export function useSiteTimeOffset(): number | null {
  const context = useDisplayTimeContext();
  return context.mode === 'site' ? context.offset_minutes : null;
}
