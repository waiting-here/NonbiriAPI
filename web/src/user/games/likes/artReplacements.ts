import type { ArtSlot } from './art';

// Each stable slot can replace its source and crop independently.
export const artReplacements: Readonly<
  Partial<Record<string, Pick<ArtSlot, 'source' | 'sourceFile' | 'focus' | 'placeholder'>>>
> = {};
