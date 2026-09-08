export interface NumberedPagination {
  page: string;
  page_size: number;
  total_items: string;
  total_pages: string;
}

export interface NumberedPage<T> {
  data: T[];
  next_cursor: null;
  pagination: NumberedPagination;
}

/** Build a server page from a complete fixture collection and the request window. */
export function numberedPage<T>(
  rows: readonly T[],
  params: URLSearchParams,
  totalItems = rows.length,
): NumberedPage<T> {
  const pageSize = Number(params.get('page_size'));
  if (![10, 20, 50, 100].includes(pageSize)) {
    throw new Error(`Unexpected numbered fixture page size: ${params.get('page_size')}`);
  }
  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize));
  const requestedPage = Number(params.get('page'));
  if (!Number.isSafeInteger(requestedPage) || requestedPage < 1) {
    throw new Error(`Unexpected numbered fixture page: ${params.get('page')}`);
  }
  const page = Math.min(requestedPage, totalPages);
  const offset = (page - 1) * pageSize;
  return {
    data: rows.slice(offset, offset + pageSize),
    next_cursor: null,
    pagination: {
      page: String(page),
      page_size: pageSize,
      total_items: String(totalItems),
      total_pages: String(totalPages),
    },
  };
}

/** Build a response when the fixture already contains only the requested page rows. */
export function numberedResponse<T>(
  data: readonly T[],
  page: string | number,
  pageSize: number,
  totalItems = data.length,
  totalPages = Math.max(1, Math.ceil(totalItems / pageSize)),
): NumberedPage<T> {
  return {
    data: [...data],
    next_cursor: null,
    pagination: {
      page: String(page),
      page_size: pageSize,
      total_items: String(totalItems),
      total_pages: String(totalPages),
    },
  };
}
