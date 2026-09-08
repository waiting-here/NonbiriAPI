import { fireEvent, screen } from '@testing-library/react';
import type { FormEvent } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { PagePagination } from './PagePagination';
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

  it.each(['0', '1'])('keeps every page-size option for %s total items', async (totalItems) => {
    const onPageSizeChange = vi.fn();
    const view = await renderWithProviders(
      <PagePagination
        metadata={metadata({ page: '1', total_items: totalItems, total_pages: '1' })}
        onPageChange={vi.fn()}
        onPageSizeChange={onPageSizeChange}
      />,
      { station: 'admin', role: 'admin' },
    );

    const select = screen.getByRole('combobox', { name: 'Items per page' });
    expect([...select.querySelectorAll('option')].map((option) => option.value)).toEqual(
      PAGE_SIZES.map(String),
    );
    await view.user.selectOptions(select, '100');
    expect(onPageSizeChange).toHaveBeenCalledWith(100);
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
    await view.user.click(screen.getByRole('button', { name: 'Go' }));
    expect(onPageChange).toHaveBeenCalledTimes(2);
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Enter a whole page number from 1 to 2147483647.',
    );

    await view.user.clear(input);
    await view.user.type(input, '999');
    await view.user.click(screen.getByRole('button', { name: 'Go' }));
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
    const go = screen.getByRole('button', { name: 'Go' });
    const select = screen.getByRole('combobox', { name: 'Items per page' });
    const input = screen.getByRole('textbox', { name: 'Go to page' });
    expect(previous).toBeDisabled();
    expect(next).toBeDisabled();
    expect(go).toBeDisabled();
    expect(select).toBeDisabled();
    expect(input).toBeDisabled();

    fireEvent.click(next);
    fireEvent.click(next);
    fireEvent.click(go);
    expect(onPageChange).not.toHaveBeenCalled();
    expect(onPageSizeChange).not.toHaveBeenCalled();
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
