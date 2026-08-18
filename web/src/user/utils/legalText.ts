// Legal override text parser: a tiny, safe line-markup renderer.
//
// Operators author legal documents (privacy policy / terms of service) as
// plain text with a few line-prefix conventions. This module turns that text
// into structured blocks that React renders as text nodes only — there is no
// HTML parsing, no dangerouslySetInnerHTML, and no raw markup, so an override
// document can never become an HTML/JS injection vector regardless of what an
// operator (or a compromised admin account) writes.
//
// Conventions:
//   "## Heading"      -> <h2>
//   "### Heading"     -> <h3>
//   "- item"          -> <li> (consecutive lines form one <ul>)
//   "> note"         -> <aside class="legal-block-notice"> (one per line)
//   "" (blank line)  -> paragraph / list separator
//   any other line    -> <p> (consecutive non-blank lines join into one <p>)
//
// Unrecognized prefixes are treated as plain paragraph text, so an operator
// never loses content by using an unsupported marker.

export type LegalBlock =
  | { kind: 'h2'; text: string }
  | { kind: 'h3'; text: string }
  | { kind: 'p'; text: string }
  | { kind: 'ul'; items: string[] }
  | { kind: 'note'; text: string };

export function parseLegalBlocks(input: string): LegalBlock[] {
  const blocks: LegalBlock[] = [];
  const lines = input.split(/\r\n|\r|\n/);

  let paragraph: string[] = [];
  let list: string[] = [];

  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    blocks.push({ kind: 'p', text: paragraph.join(' ') });
    paragraph = [];
  };
  const flushList = () => {
    if (list.length === 0) return;
    blocks.push({ kind: 'ul', items: list.slice() });
    list = [];
  };
  const flushAll = () => {
    flushList();
    flushParagraph();
  };

  for (const raw of lines) {
    const line = raw.trimEnd();
    if (line === '') {
      flushAll();
      continue;
    }
    if (line.startsWith('## ')) {
      flushAll();
      blocks.push({ kind: 'h2', text: line.slice(3).trim() });
      continue;
    }
    if (line.startsWith('### ')) {
      flushAll();
      blocks.push({ kind: 'h3', text: line.slice(4).trim() });
      continue;
    }
    if (line.startsWith('- ')) {
      flushParagraph();
      list.push(line.slice(2).trim());
      continue;
    }
    if (line.startsWith('> ')) {
      flushAll();
      blocks.push({ kind: 'note', text: line.slice(2).trim() });
      continue;
    }
    // Plain paragraph line: accumulate; consecutive lines join with a space.
    flushList();
    paragraph.push(line.trim());
  }
  flushAll();
  return blocks;
}
