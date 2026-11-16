export type Language = 'zh' | 'en';
export type LocalizedLabel = [zh: string, en: string];
export type Category = 'theme' | 'card' | 'voice' | 'pool' | 'screen';

export interface SourceLocator {
  file: string;
  key: string;
  line?: number;
}

export interface CopyChange {
  source: SourceLocator;
  language: Language;
  original: string;
  value: string;
}

export interface CopyEntry extends Omit<CopyChange, 'value'> {
  category: Category;
  area: LocalizedLabel;
  field: LocalizedLabel;
  theme?: string;
  character?: string;
  rarity?: string;
}

export interface ReviewSources {
  entries: CopyEntry[];
  themes: { id: string; name: LocalizedLabel }[];
  characters: string[];
  rarities: string[];
}
