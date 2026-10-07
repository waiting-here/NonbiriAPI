import { useEffect, useRef } from 'react';
import { mountLake, type LakeBridge } from './native.mjs';
import html from './native.html?raw';
import './native.css';

export function NativeLake({ bridge }: { bridge: LakeBridge }) {
  const root = useRef<HTMLDivElement>(null);
  const current = useRef(bridge);
  useEffect(() => {
    current.current = bridge;
  });
  useEffect(() => {
    if (!root.current) return;
    // Each native mount installs listeners and localizes the original nodes.
    // Rebuild them after cleanup so both listeners and static copy start fresh.
    root.current.innerHTML = html;
    const forwarding = new Proxy({} as LakeBridge, {
      get: (_target, key: keyof LakeBridge) => current.current[key],
    });
    const game = mountLake(root.current, forwarding);
    return () => game.dispose();
  }, [bridge.language]);
  return <div className="lake-original" ref={root} dangerouslySetInnerHTML={{ __html: html }} />;
}
