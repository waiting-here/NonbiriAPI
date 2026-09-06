import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { MarkdownText } from './MarkdownText';

describe('MarkdownText', () => {
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
    expect(container.querySelector('script, svg, math, img, iframe, form, input, a[href]')).toBeNull();
    expect(container.querySelector('[id], [style], [onerror], [onfocus]')).toBeNull();
  });
});
