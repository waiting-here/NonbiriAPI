import { describe, expect, it } from 'vitest';
import { MAX_HISTORY_PAGE, MAX_HISTORY_UNIX_SECOND } from './data';
import { parseCreditHistorySearch } from './url';

const anchor = 'op_000000000000000000001A';

describe('credit history URL state', () => {
  it('defaults to the first page and the stored page size', () => {
    const state = parseCreditHistorySearch('', 50);

    expect(state.filter).toEqual({ page: '1', page_size: 50 });
    expect(state.canonicalSearch).toBe('');
  });

  it('round-trips activity asset and policy category filters', () => {
    for (const query of [
      'asset_type=sketch_paper&category=picture_book',
      'asset_type=sketch_brush&category=picture_book',
      'asset_type=general&category=inactivity',
      'asset_type=all&category=inactivity',
    ]) {
      const state = parseCreditHistorySearch(query);
      expect(state.canonicalSearch).toBe(query);
      expect(state.invalidAsset).toBe(false);
      expect(state.invalidCategory).toBe(false);
      expect(parseCreditHistorySearch(state.canonicalSearch).filter).toEqual(state.filter);
    }
    const invalid = parseCreditHistorySearch('asset_type=unknown_asset&category=unknown_category');
    expect(invalid.canonicalSearch).toBe('');
    expect(invalid.invalidAsset).toBe(true);
    expect(invalid.invalidCategory).toBe(true);
  });

  it('keeps legal int64 pages, ten rows, and zero as a valid time bound', () => {
    const state = parseCreditHistorySearch(
      `page=${MAX_HISTORY_PAGE}&page_size=10&anchor=${anchor}&from=0&to=${MAX_HISTORY_UNIX_SECOND}`,
    );

    expect(state.filter).toEqual({
      page: MAX_HISTORY_PAGE.toString(),
      page_size: 10,
      anchor,
      from: 0,
      to: MAX_HISTORY_UNIX_SECOND,
    });
    expect(state.canonicalSearch).toBe(
      `page=${MAX_HISTORY_PAGE}&page_size=10&anchor=${anchor}&from=0&to=${MAX_HISTORY_UNIX_SECOND}`,
    );
    expect(state.invalidPage).toBe(false);
  });

  it('drops duplicate or invalid values and unknown parameters during canonicalization', () => {
    const state = parseCreditHistorySearch(
      'page=0&page=2&page_size=30&anchor=bad&category=charity&category=donation&unknown=x',
      20,
    );

    expect(state.filter).toEqual({ page: '1', page_size: 20 });
    expect(state.canonicalSearch).toBe('page=1&page_size=20');
    expect(state.invalidPage).toBe(true);
    expect(state.invalidPageSize).toBe(true);
    expect(state.invalidAnchor).toBe(true);
    expect(state.invalidCategory).toBe(true);
    expect(state.unknownParameter).toBe(true);
  });

  it('removes an invalid time range instead of issuing a request for it', () => {
    const state = parseCreditHistorySearch('from=100&to=100');

    expect(state.filter).toEqual({ page: '1', page_size: 20 });
    expect(state.invalidRange).toBe(true);
    expect(state.invalidFrom).toBe(false);
    expect(state.invalidTo).toBe(false);
    expect(state.canonicalSearch).toBe('');
  });
});
