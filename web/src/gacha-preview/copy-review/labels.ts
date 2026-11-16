import type { Category, Language, LocalizedLabel } from './types';

export const categories: Record<Category, LocalizedLabel> = {
  theme: ['主题', 'Themes'],
  card: ['卡牌', 'Cards'],
  voice: ['台词与演出', 'Dialogue and reveals'],
  pool: ['奖池与封面', 'Pools and covers'],
  screen: ['界面', 'Interface'],
};

export const languages: Record<Language, LocalizedLabel> = {
  zh: ['简体中文', 'Simplified Chinese'],
  en: ['英语', 'English'],
};

export const labels = {
  title: ['文案校对', 'Copy review'],
  back: ['返回抽卡预览', 'Back to gacha preview'],
  instructions: [
    '修改会保存在当前浏览器。校对完成后，可下载或复制修改包。',
    'Edits are saved in this browser. Download or copy the changes when ready.',
  ],
  import: ['导入修改包', 'Import changes'],
  copy: ['复制修改包', 'Copy changes'],
  download: ['下载修改包', 'Download changes'],
  filters: ['筛选文案', 'Filter copy'],
  search: ['搜索', 'Search'],
  searchHint: ['搜索原文、主题或角色', 'Search original copy, themes or characters'],
  category: ['类别', 'Category'],
  theme: ['主题', 'Theme'],
  character: ['角色', 'Character'],
  language: ['文案语言', 'Copy language'],
  rarity: ['稀有度', 'Rarity'],
  all: ['全部', 'All'],
  changedOnly: ['仅看已修改', 'Show edited only'],
  clear: ['清除筛选', 'Clear filters'],
  results: ['校对结果', 'Review results'],
  original: ['原文', 'Original'],
  draft: ['修改稿', 'Edited text'],
  reset: ['恢复原文', 'Restore original'],
  modified: ['已修改', 'Edited'],
  none: ['没有匹配的文案', 'No matching copy'],
  noneBody: ['试试其他关键词，或清除筛选。', 'Try a different search or clear the filters.'],
  unchanged: ['还没有修改', 'No edits yet'],
  unchangedBody: [
    '关闭仅看已修改，即可继续校对。',
    'Turn off Show edited only to continue reviewing.',
  ],
  storageError: [
    '浏览器未能保存草稿。离开前请下载修改包。',
    'The browser could not save your draft. Download a copy before leaving.',
  ],
  storageReadError: [
    '浏览器草稿无法读取。可导入先前下载的修改包。',
    'The browser draft could not be loaded. Import a previously downloaded copy to resume.',
  ],
  invalidFile: [
    '无法读取这个修改包。请选择从文案校对页下载的 JSON 文件。',
    'This file could not be read. Choose a JSON file downloaded from Copy review.',
  ],
  copyOk: ['已复制修改包。', 'Changes copied.'],
  copyError: ['复制失败。请下载修改包。', 'Copy failed. Download the changes instead.'],
  placeholders: ['请保留原文中的占位符。', 'Keep the placeholders from the original.'],
  reviewConflicts: ['待核对的修改', 'Edits to review'],
  conflictHint: [
    '核对原文和修改稿后，选择是否采用。待核对的修改仍会随修改包导出。',
    'Compare the source with the proposed edit before choosing. Unresolved edits are included in the download.',
  ],
  sourceDrift: ['原文在这次修改后有更新。', 'The original has changed since this edit was saved.'],
  differentDraft: [
    '同一条文案已有不同的本地草稿。',
    'This entry already has a different local draft.',
  ],
  sourceMissing: [
    '当前版本找不到对应的文案。',
    'This entry could not be found in the current version.',
  ],
  previousOriginal: ['修改时的原文', 'Original before this edit'],
  incoming: ['待采用的修改', 'Proposed edit'],
  currentOriginal: ['现有原文', 'Current original'],
  currentDraft: ['本地草稿', 'Local draft'],
  replace: ['采用这条修改', 'Use proposed edit'],
  keep: ['保留本地草稿', 'Keep local draft'],
  dismiss: ['略过此修改', 'Skip this edit'],
  missingEntry: ['找不到该条文案', 'Unavailable entry'],
} satisfies Record<string, LocalizedLabel>;

export type LabelKey = keyof typeof labels;
