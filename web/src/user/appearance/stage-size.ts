import { createContext, useContext, useLayoutEffect, useState } from 'react';

type Size = readonly [number, number];
export const SizeContext = createContext<Size | null>(null);
export const windowSize = (): Size => [innerWidth, innerHeight];

export function useStageSize() {
  const size = useContext(SizeContext);
  const [fallback, setFallback] = useState(windowSize);
  const inStage = size !== null;
  useLayoutEffect(() => {
    if (inStage) return;
    const resize = () => setFallback(windowSize());
    window.addEventListener('resize', resize);
    return () => window.removeEventListener('resize', resize);
  }, [inStage]);
  return size ?? fallback;
}
