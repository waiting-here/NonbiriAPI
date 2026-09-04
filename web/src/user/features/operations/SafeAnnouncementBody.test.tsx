import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { SafeAnnouncementBody, parseSafeAnnouncementHTML } from './SafeAnnouncementBody';

describe('safe announcement rendering', () => {
  it('renders only the bounded server-markup vocabulary', () => {
    render(<SafeAnnouncementBody html={'<h2>Notice</h2><p>Hello <strong>world</strong>.</p><a href="https://example.com/path">Read more</a>'} />);
    expect(screen.getByRole('heading', { name: 'Notice' })).toBeInTheDocument();
    const link = screen.getByRole('link', { name: 'Read more' });
    expect(link).toHaveAttribute('href', 'https://example.com/path');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
  });

  it.each([
    '<img src="https://example.com/pixel.png">',
    '<script>alert(1)</script>',
    '<a href="javascript:alert(1)">unsafe</a>',
    '<a href="https://user:password@example.com/">credentials</a>',
    '<p class="unexpected">attributes</p>',
  ])('fails closed for unsupported or unsafe content: %s', (html) => {
    render(<SafeAnnouncementBody html={html} />);
    expect(screen.getByRole('alert')).toHaveTextContent(/could not be rendered safely/i);
    expect(document.querySelector('img, script')).toBeNull();
  });

  it('rejects relative links instead of resolving them against the current station', () => {
    expect(() => parseSafeAnnouncementHTML('<a href="/admin">internal</a>')).toThrow(/invalid|unsafe/i);
  });
});
