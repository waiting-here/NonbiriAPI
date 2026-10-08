import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { MarkdownText } from './MarkdownText';

describe('MarkdownText', () => {
  it('uses legal heading levels without changing other markdown contexts', () => {
    const { rerender } = render(
      <MarkdownText headingShift={0}>{'# Data\n\n## Retention\n\n### Records'}</MarkdownText>,
    );
    expect(screen.getByRole('heading', { name: 'Data', level: 2 })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Retention', level: 2 })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Records', level: 3 })).toBeInTheDocument();
    rerender(<MarkdownText>{'# Data'}</MarkdownText>);
    expect(screen.getByRole('heading', { name: 'Data', level: 3 })).toBeInTheDocument();
  });
  it('renders headings, lists, emphasis, code and tables as readable elements', () => {
    const { container } = render(
      <MarkdownText>
        {
          '# Guide\n\n- **Important**\n- `model/name`\n\n| Model | Rate |\n| --- | --- |\n| Example | 1 |\n\n```json\n{"stream":true}\n```'
        }
      </MarkdownText>,
    );
    expect(screen.getByRole('heading', { name: 'Guide', level: 3 })).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(2);
    expect(container.querySelector('strong')).toHaveTextContent('Important');
    expect(screen.getByRole('table')).toHaveTextContent('Example');
    expect(container.querySelector('pre code')).toHaveTextContent('{"stream":true}');
  });

  it('keeps HTML inert, rejects executable links and avoids external image loads', () => {
    const { container } = render(
      <MarkdownText>
        {
          '<img src=x onerror=alert(1)>\n\n[bad](javascript:alert) [encoded](jav&#x61;script:alert) ![picture](https://example.test/pixel)\n\n[guide](https://example.test/help)'
        }
      </MarkdownText>,
    );
    expect(container.querySelector('img, script, iframe')).toBeNull();
    expect(screen.getAllByRole('link')).toHaveLength(1);
    expect(screen.getByRole('link', { name: 'guide' })).toHaveAttribute(
      'rel',
      'noopener noreferrer nofollow',
    );
    expect(container).toHaveTextContent('<img src=x onerror=alert(1)>');
    expect(container).toHaveTextContent('picture');
  });

  it.each([
    '<svg><a href="javascript:alert(1)">x</a></svg><script>alert(1)</script>',
    '[x](data:text/html,test) [x](//example.test/path) [x](/\\example.test/path)',
    '![<img src=x onerror=alert(1)>](https://example.test/pixel)',
    '<form id=location><input name=attributes autofocus onfocus=alert(1)></form>',
  ])('keeps hostile markup and URLs inert: %s', (value) => {
    const { container } = render(<MarkdownText>{value}</MarkdownText>);
    expect(
      container.querySelector('script, svg, math, img, iframe, form, input, a[href]'),
    ).toBeNull();
    expect(container.querySelector('[id], [style], [onerror], [onfocus]')).toBeNull();
  });

  it('renders numeric character references as inert text and escapes autolink destinations', () => {
    const { container } = render(
      <MarkdownText>
        {
          '&#x3c;img src=x onerror=alert(1)&#x3e; &#65; &#x1f41f;\n\n<https://example.test/help?q=&quot;onmouseover=alert(1)>'
        }
      </MarkdownText>,
    );
    expect(container.querySelector('img, script, [onerror], [onmouseover]')).toBeNull();
    expect(container).toHaveTextContent('<img src=x onerror=alert(1)> A 🐟');
    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('href', 'https://example.test/help?q=&quot;onmouseover=alert(1)');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer nofollow');
  });

  it('normalizes reference labels while filtering their encoded destinations', () => {
    render(
      <MarkdownText>
        {
          '[Guide][  DOC ] [unsafe][BAD] [query][Query]\n\n[doc]: https://example.test/help\n[bad]: jav&#x61;script:alert(1)\n[query]: https://example.test/help?a=1&amp;b=2'
        }
      </MarkdownText>,
    );
    expect(screen.getAllByRole('link')).toHaveLength(2);
    expect(screen.getByRole('link', { name: 'Guide' })).toHaveAttribute(
      'href',
      'https://example.test/help',
    );
    expect(screen.getByRole('link', { name: 'query' })).toHaveAttribute(
      'href',
      'https://example.test/help?a=1&amp;b=2',
    );
    expect(screen.queryByRole('link', { name: 'unsafe' })).toBeNull();
    expect(screen.getByText(/unsafe/)).toBeInTheDocument();
  });

  it('preserves bounded unresolved reference-heavy text without producing links', () => {
    const value = '[missing] '.repeat(1_000).trim();
    const { container } = render(<MarkdownText>{value}</MarkdownText>);
    expect(container).toHaveTextContent(value);
    expect(container.querySelector('a, img, script')).toBeNull();
  });
});
