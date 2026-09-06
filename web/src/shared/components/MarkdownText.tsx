import Markdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
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

const components: Components = {
  h1: ({ children }) => <h3>{children}</h3>,
  h2: ({ children }) => <h4>{children}</h4>,
  h3: ({ children }) => <h5>{children}</h5>,
  h4: ({ children }) => <h6>{children}</h6>,
  a: ({ href, children }) =>
    href ? (
      <a
        href={href}
        target={href.startsWith('#') || href.startsWith('/') ? undefined : '_blank'}
        rel="noopener noreferrer nofollow"
        referrerPolicy="no-referrer"
      >
        {children}
      </a>
    ) : (
      <span>{children}</span>
    ),
  // Donor-authored descriptions must not trigger external image requests
  // while another account reads them.
  img: ({ alt }) => <span>{alt}</span>,
  table: ({ children }) => (
    <div className="nb-markdown__table">
      <table>{children}</table>
    </div>
  ),
};

export function MarkdownText({
  children,
  className = '',
}: {
  children: string;
  className?: string;
}) {
  return (
    <div className={`nb-markdown ${className}`.trim()}>
      <Markdown remarkPlugins={[remarkGfm]} components={components} urlTransform={safeLink}>
        {children}
      </Markdown>
    </div>
  );
}
