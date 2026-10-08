import { useMemo } from 'react';
import { Marked, Renderer } from 'marked';
import DOMPurify from 'dompurify';
import '../styles/markdown.css';

function safeLink(value: string): string {
  if (Array.from(value).some((character) => character.charCodeAt(0) < 32)) return '';
  if (value.startsWith('#')) return value;
  if (value.startsWith('/') && !value.startsWith('//') && !value.includes('\\')) return value;
  try {
    const url = new URL(value);
    return ['https:', 'http:', 'mailto:'].includes(url.protocol) && !url.username && !url.password
      ? value
      : '';
  } catch {
    return '';
  }
}

function escapeHTML(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

type HeadingShift = 0 | 2;

function createMarkdown(headingShift: HeadingShift) {
  return new Marked({
    gfm: true,
    breaks: false,
    renderer: {
      html: ({ text }) => escapeHTML(text),
      // Images remain text, so reading donor content sends no media requests.
      image: ({ text }) => escapeHTML(text),
      heading({ tokens, depth }) {
        const level = Math.max(2, Math.min(depth + headingShift, 6));
        return `<h${level}>${this.parser.parseInline(tokens)}</h${level}>`;
      },
      link({ href, tokens }) {
        const text = this.parser.parseInline(tokens);
        const url = safeLink(href);
        if (!url) return text;
        const target = url.startsWith('#') || url.startsWith('/') ? '' : ' target="_blank"';
        return `<a href="${escapeHTML(url)}"${target} rel="noopener noreferrer nofollow" referrerpolicy="no-referrer">${text}</a>`;
      },
      table(token) {
        return `<div class="nb-markdown__table">${Renderer.prototype.table.call(this, token)}</div>`;
      },
    },
  });
}

const parsers = { 0: createMarkdown(0), 2: createMarkdown(2) };

function renderMarkdown(value: string, headingShift: HeadingShift): string {
  try {
    const markdown = parsers[headingShift];
    return DOMPurify.sanitize(markdown.parse(value, { async: false }), {
      ALLOWED_TAGS: [
        'p',
        'br',
        'hr',
        'h2',
        'h3',
        'h4',
        'h5',
        'h6',
        'strong',
        'em',
        'del',
        'ul',
        'ol',
        'li',
        'blockquote',
        'pre',
        'code',
        'a',
        'div',
        'table',
        'thead',
        'tbody',
        'tr',
        'th',
        'td',
        'input',
      ],
      ALLOWED_ATTR: [
        'href',
        'target',
        'rel',
        'referrerpolicy',
        'class',
        'start',
        'type',
        'disabled',
        'checked',
      ],
      ALLOW_DATA_ATTR: false,
      ALLOW_ARIA_ATTR: false,
    });
  } catch {
    return escapeHTML(value);
  }
}

export function MarkdownText({
  children,
  className = '',
  headingShift = 2,
}: {
  children: string;
  className?: string;
  headingShift?: HeadingShift;
}) {
  const html = useMemo(() => renderMarkdown(children, headingShift), [children, headingShift]);
  return (
    <div className={`nb-markdown ${className}`.trim()} dangerouslySetInnerHTML={{ __html: html }} />
  );
}
