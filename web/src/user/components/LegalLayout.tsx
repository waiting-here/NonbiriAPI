import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Fold } from '@shared/components/ui';
import './legal-layout.css';

interface Heading {
  id: string;
  text: string;
  level: string;
}

export function LegalLayout({
  documentKey,
  children,
}: {
  documentKey: 'privacy' | 'terms';
  children: ReactNode;
}) {
  const { t, i18n } = useTranslation();
  const body = useRef<HTMLDivElement>(null);
  const [headings, setHeadings] = useState<Heading[]>([]);
  useEffect(() => {
    const nodes = Array.from(body.current?.querySelectorAll<HTMLHeadingElement>('h2, h3') ?? []);
    const next = nodes.map((heading, index) => {
      const id = `${documentKey}-section-${index + 1}`;
      const text = heading.textContent ?? '';
      heading.id = id;
      const anchor = document.createElement('a');
      anchor.href = `#${id}`;
      anchor.className = 'legal-heading-anchor';
      anchor.setAttribute('aria-label', text);
      anchor.textContent = '#';
      heading.append(anchor);
      return { id, text, level: heading.tagName };
    });
    setHeadings((previous) =>
      JSON.stringify(previous) === JSON.stringify(next) ? previous : next,
    );
    return () =>
      nodes.forEach((heading) => heading.querySelector('.legal-heading-anchor')?.remove());
  }, [children, documentKey, i18n.resolvedLanguage]);
  const links = (
    <ol>
      {headings.map((heading) => (
        <li key={heading.id} className={heading.level === 'H3' ? 'legal-toc__sub' : undefined}>
          <a href={`#${heading.id}`}>{heading.text}</a>
        </li>
      ))}
    </ol>
  );
  return (
    <div className="legal-layout">
      <nav className="legal-toc legal-toc--desktop" aria-label={t('user.legal.contents')}>
        <h2>{t('user.legal.contents')}</h2>
        {links}
      </nav>
      <div className="legal-toc legal-toc--mobile">
        <Fold title={t('user.legal.contents')}>
          <nav aria-label={t('user.legal.contents')}>{links}</nav>
        </Fold>
      </div>
      <div className="legal-layout__body" ref={body}>
        {children}
      </div>
    </div>
  );
}
