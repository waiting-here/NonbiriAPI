import { createElement, type ReactNode } from 'react';
import { ApiError } from '@shared/query/http';

const ALLOWED = new Set(['P', 'BR', 'H2', 'H3', 'H4', 'STRONG', 'EM', 'OL', 'UL', 'LI', 'BLOCKQUOTE', 'CODE', 'PRE', 'A']);

function safeHref(value: string): string {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new ApiError('invalid_response', 'The announcement renderer returned an invalid link.', 200);
  }
  if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password || !parsed.hostname) {
    throw new ApiError('invalid_response', 'The announcement renderer returned an unsafe link.', 200);
  }
  return value;
}

function nodeToReact(node: Node, key: string): ReactNode {
  if (node.nodeType === Node.TEXT_NODE) return node.textContent ?? '';
  if (!(node instanceof Element) || !ALLOWED.has(node.tagName)) {
    throw new ApiError('invalid_response', 'The announcement renderer returned unsupported content.', 200);
  }
  const children = Array.from(node.childNodes).map((child, index) => nodeToReact(child, `${key}-${index}`));
  if (node.tagName === 'A') {
    const attributes = Array.from(node.attributes);
    if (attributes.some((attribute) => !['href', 'rel'].includes(attribute.name))) {
      throw new ApiError('invalid_response', 'The announcement renderer returned unsupported link attributes.', 200);
    }
    const href = safeHref(node.getAttribute('href') ?? '');
    const rel = node.getAttribute('rel');
    if (rel !== null && rel !== 'noopener noreferrer') {
      throw new ApiError('invalid_response', 'The announcement renderer returned an unsafe link relationship.', 200);
    }
    return createElement('a', { key, href, rel: 'noopener noreferrer' }, children);
  }
  if (node.attributes.length !== 0) {
    throw new ApiError('invalid_response', 'The announcement renderer returned unsupported attributes.', 200);
  }
  return createElement(node.tagName.toLowerCase(), { key }, children);
}

export function parseSafeAnnouncementHTML(value: string): ReactNode[] {
  if (typeof DOMParser === 'undefined') throw new ApiError('invalid_response', 'Announcement rendering is unavailable.', 200);
  const document = new DOMParser().parseFromString(value, 'text/html');
  if (document.querySelector('parsererror')) throw new ApiError('invalid_response', 'The announcement renderer returned invalid content.', 200);
  if (document.head.childNodes.length !== 0) {
    throw new ApiError('invalid_response', 'The announcement renderer returned unsupported content.', 200);
  }
  return Array.from(document.body.childNodes).map((node, index) => nodeToReact(node, String(index)));
}

export function SafeAnnouncementBody({ html }: { html: string }) {
  let content: ReactNode[];
  try {
    content = parseSafeAnnouncementHTML(html);
  } catch {
    return <p className="field-error" role="alert">The announcement body could not be rendered safely.</p>;
  }
  return <div className="ops-announcement-body">{content}</div>;
}
