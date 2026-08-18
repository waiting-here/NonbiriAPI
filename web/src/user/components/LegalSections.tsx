import { parseLegalBlocks } from '../utils/legalText';

// Renders an operator-authored legal override document. The text is parsed
// into structured blocks and emitted as React text nodes only — no
// dangerouslySetInnerHTML, no raw HTML — so an override can never become an
// injection vector regardless of what an operator writes.
export function LegalSections({ override }: { override: string }) {
  const blocks = parseLegalBlocks(override);
  return (
    <>
      {blocks.map((block, index) => {
        switch (block.kind) {
          case 'h2':
            return <h2 key={index}>{block.text}</h2>;
          case 'h3':
            return <h3 key={index}>{block.text}</h3>;
          case 'p':
            return <p key={index}>{block.text}</p>;
          case 'ul':
            return (
              <ul key={index}>
                {block.items.map((item, i) => (
                  <li key={i}>{item}</li>
                ))}
              </ul>
            );
          case 'note':
            return (
              <aside className="legal-notice" key={index}>
                {block.text}
              </aside>
            );
          default:
            return null;
        }
      })}
    </>
  );
}
