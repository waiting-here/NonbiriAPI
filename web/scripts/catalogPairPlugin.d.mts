import type { Plugin } from 'vite';

declare const catalogPairPlugin: (options?: { webRoot?: string }) => Plugin;

export default catalogPairPlugin;
