import { formatErrorJSON } from './formatError';

self.onmessage = (event: MessageEvent<{ raw: string; truncated: boolean }>) => {
  self.postMessage(formatErrorJSON(event.data.raw, event.data.truncated));
};
