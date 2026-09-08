import type { PageMetadata, PageSize } from '@shared/operations/pageNumbers';
import type { CatalogView } from './types';

export interface PageWindow {
  page: string;
  pageSize: PageSize;
}

export interface NumberedPage<T> {
  data: T[];
  next_cursor: null;
  pagination: PageMetadata;
}

export type NumberedCatalogView = Omit<CatalogView, 'next_cursor'> & {
  next_cursor: null;
  pagination: PageMetadata;
};
