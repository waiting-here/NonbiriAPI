import { fireEvent, screen } from '@testing-library/react';
import type { FormEvent } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { PagePagination } from './PagePagination';
import { CursorPagination } from './CursorPagination';
import { Pagination } from '../components/States';
import { normalizePageMetadata, PAGE_SIZES, type PageMetadata } from './pageNumbers';

function metadata(overrides: Partial<PageMetadata> = {}): PageMetadata {
  return {
    page: '2',
    page_size: 20,
    total_items: '200',
    total_pages: '10',
    ...overrides,
  };
}

describe('PagePagination', () => {
  it('hides a single cursor/legacy page and preserves their callbacks on later pages', async () => {
    const previous = vi.fn();
    const next = vi.fn();
    const view = await renderWithProviders(
      <CursorPagination page={1} nextCursor={null} onPrevious={previous} onNext={next} />,
      { station: 'user' },
    );
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
    view.rerender(
      <CursorPagination page={2} nextCursor="cursor-next" onPrevious={previous} onNext={next} />,
    );
    await view.user.click(screen.getByRole('button', { name: 'Previous' }));
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(previous).toHaveBeenCalledOnce();
    expect(next).toHaveBeenCalledWith('cursor-next');
    const onChange = vi.fn();
    view.rerender(<Pagination page={1} hasNext={false} onChange={onChange} />);
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
    view.rerender(<Pagination page={2} hasNext onChange={onChange} />);
    await view.user.click(screen.getByRole('button', { name: 'Previous' }));
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(onChange.mock.calls).toEqual([[1], [3]]);
  });

  it('keeps large decimal values as exact strings instead of rounding through Number', () => {
    expect(
      normalizePageMetadata({
        page: '2147483647',
        page_size: 10,
        total_items: '9007199254740993',
        total_pages: '900719925474100',
      }),
    ).toEqual({
      page: '2147483647',
      page_size: 10,
      total_items: '9007199254740993',
      total_pages: '900719925474100',
    });
  });

  it('hides an empty pager and shows only the total for a small single page', async () => {
    const props = { onPageChange: vi.fn(), onPageSizeChange: vi.fn() };
    const view = await renderWithProviders(
      <PagePagination
        metadata={metadata({ page: '1', total_items: '0', total_pages: '1' })}
        {...props}
      />,
      { station: 'admin' },
    );
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
    view.rerender(
      <PagePagination
        metadata={metadata({ page: '1', total_items: '1', total_pages: '1' })}
        {...props}
      />,
    );
    expect(screen.getByRole('navigation')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });

  it('retains the complete page-size options on a single page above the minimum size', async () => {
    const onPageSizeChange = vi.fn();
    const view = await renderWithProviders(
      <PagePagination
        metadata={metadata({ page: '1', page_size: 100, total_items: '30', total_pages: '1' })}
        onPageChange={vi.fn()}
        onPageSizeChange={onPageSizeChange}
      />,
      { station: 'admin' },
    );
    const select = screen.getByRole('combobox', { name: 'Items per page' });
    expect([...select.querySelectorAll('option')].map((option) => option.value)).toEqual(
      PAGE_SIZES.map(String),
    );
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    await view.user.selectOptions(select, '100');
    expect(onPageSizeChange).toHaveBeenCalledWith(100);
  });

  it('shows every page for a short list and a compact window for a long list', async () => {
    const onPageChange = vi.fn();
    const props = { onPageChange, onPageSizeChange: vi.fn() };
    const view = await renderWithProviders(
      <PagePagination
        metadata={metadata({ page: '2', total_items: '60', total_pages: '3' })}
        {...props}
      />,
      { station: 'admin' },
    );
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(
      [...view.container.querySelectorAll('button[aria-current="page"]')].map(
        (button) => button.textContent,
      ),
    ).toEqual(['2']);
    await view.user.click(screen.getByRole('button', { name: '3' }));
    expect(onPageChange).toHaveBeenLastCalledWith('3');
    view.rerender(
      <PagePagination
        metadata={metadata({ page: '6', total_items: '240', total_pages: '12' })}
        {...props}
      />,
    );
    expect(
      [...view.container.querySelectorAll('button.nb-pager__number')].map(
        (button) => button.textContent,
      ),
    ).toEqual(['1', '5', '6', '7', '12']);
    expect(view.container.querySelectorAll('span.nb-pager__number')).toHaveLength(2);
    expect(screen.getByRole('textbox', { name: 'Go to page' })).toHaveValue('6');
    await view.user.tab();
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Previous' }));
    await view.user.keyboard('{Enter}');
    expect(onPageChange).toHaveBeenLastCalledWith('5');
  });

  it('keeps clamping feedback for a server-selected single page and suppresses it while busy', async () => {
    const props = {
      metadata: metadata({ page: '1', total_items: '1', total_pages: '1' }),
      requestedPage: '5',
      onPageChange: vi.fn(),
      onPageSizeChange: vi.fn(),
    };
    const view = await renderWithProviders(<PagePagination {...props} />, { station: 'admin' });
    expect(screen.getByRole('status')).toHaveTextContent('Showing page 1.');
    view.rerender(<PagePagination {...props} busy />);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('limits audit page sizes to 20, 50, and 100 without changing the shared defaults', async () => {
    const onPageSizeChange = vi.fn();
    const view = await renderWithProviders(
      <PagePagination
        metadata={metadata()}
        pageSizes={[20, 50, 100]}
        onPageChange={vi.fn()}
        onPageSizeChange={onPageSizeChange}
      />,
      { station: 'admin', role: 'admin' },
    );
    const select = screen.getByRole('combobox', { name: 'Items per page' });
    expect([...select.querySelectorAll('option')].map((option) => option.value)).toEqual([
      '20',
      '50',
      '100',
    ]);
    fireEvent.change(select, { target: { value: '10' } });
    expect(onPageSizeChange).not.toHaveBeenCalled();
    await view.user.selectOptions(select, '50');
    expect(onPageSizeChange).toHaveBeenCalledWith(50);
  });

  it('navigates, clamps an oversized jump, and keeps an invalid jump local', async () => {
    const onPageChange = vi.fn();
    const view = await renderWithProviders(
      <PagePagination
        metadata={metadata()}
        onPageChange={onPageChange}
        onPageSizeChange={vi.fn()}
      />,
      { station: 'admin', role: 'admin' },
    );

    await view.user.click(screen.getByRole('button', { name: 'Previous' }));
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(onPageChange).toHaveBeenNthCalledWith(1, '1');
    expect(onPageChange).toHaveBeenNthCalledWith(2, '3');

    const input = screen.getByRole('textbox', { name: 'Go to page' });
    await view.user.clear(input);
    await view.user.type(input, '0');
    await view.user.keyboard('{Enter}');
    expect(onPageChange).toHaveBeenCalledTimes(2);
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Enter a whole page number from 1 to 2147483647.',
    );

    await view.user.clear(input);
    await view.user.type(input, '999');
    await view.user.keyboard('{Enter}');
    expect(onPageChange).toHaveBeenNthCalledWith(3, '10');
    expect(input).toHaveValue('10');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('disables navigation and page-size changes while loading', async () => {
    const onPageChange = vi.fn();
    const onPageSizeChange = vi.fn();
    await renderWithProviders(
      <PagePagination
        metadata={metadata()}
        onPageChange={onPageChange}
        onPageSizeChange={onPageSizeChange}
        busy
      />,
      { station: 'admin', role: 'admin' },
    );

    const previous = screen.getByRole('button', { name: 'Previous' });
    const next = screen.getByRole('button', { name: 'Next' });
    const select = screen.getByRole('combobox', { name: 'Items per page' });
    const input = screen.getByRole('textbox', { name: 'Go to page' });
    expect(previous).toBeDisabled();
    expect(next).toBeDisabled();
    expect(select).toBeDisabled();
    expect(input).toBeDisabled();
    for (const button of screen.getAllByRole('button')) expect(button).toBeDisabled();

    fireEvent.click(next);
    fireEvent.click(next);
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onPageChange).not.toHaveBeenCalled();
    expect(onPageSizeChange).not.toHaveBeenCalled();
  });

  it('ignores a forged page-size value outside the shared allowlist', async () => {
    const onPageSizeChange = vi.fn();
    await renderWithProviders(
      <PagePagination
        metadata={metadata()}
        onPageChange={vi.fn()}
        onPageSizeChange={onPageSizeChange}
      />,
      { station: 'admin', role: 'admin' },
    );

    fireEvent.change(screen.getByRole('combobox', { name: 'Items per page' }), {
      target: { value: '30' },
    });
    expect(onPageSizeChange).not.toHaveBeenCalled();
  });

  it('preserves full integer page identifiers for existing domain protocols', async () => {
    const onPageChange = vi.fn();
    const view = await renderWithProviders(
      <PagePagination
        metadata={{
          page: '1',
          page_size: 10,
          total_items: '9223372036854775807',
          total_pages: '922337203685477581',
        }}
        maxPage={9223372036854775807n}
        onPageChange={onPageChange}
        onPageSizeChange={vi.fn()}
      />,
      { station: 'user', role: 'user' },
    );
    const input = screen.getByRole('textbox', { name: 'Go to page' });
    await view.user.clear(input);
    await view.user.type(input, '9007199254740993');
    await view.user.keyboard('{Enter}');
    expect(onPageChange).toHaveBeenLastCalledWith('9007199254740993');
    await view.user.clear(input);
    await view.user.type(input, '9223372036854775808');
    await view.user.keyboard('{Enter}');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(onPageChange).toHaveBeenCalledTimes(1);
  });

  it('handles Enter without submitting an outer form', async () => {
    const onSubmit = vi.fn((event: FormEvent) => event.preventDefault());
    const onPageChange = vi.fn();
    const view = await renderWithProviders(
      <form onSubmit={onSubmit}>
        <PagePagination
          metadata={metadata()}
          onPageChange={onPageChange}
          onPageSizeChange={vi.fn()}
        />
      </form>,
      { station: 'admin', role: 'admin' },
    );

    const input = screen.getByRole('textbox', { name: 'Go to page' });
    await view.user.clear(input);
    await view.user.type(input, '4');
    await view.user.keyboard('{Enter}');

    expect(onPageChange).toHaveBeenCalledWith('4');
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
