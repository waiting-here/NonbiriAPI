import en from './src/user/i18n/en.json' with { type: 'json' };
import zh from './src/user/i18n/zh.json' with { type: 'json' };

// The native game translates embedded Chinese phrases. Inline just this route's dictionaries.
export const lakeNotesDefine = {
  __LAKE_NOTES_NATIVE_COPY__: JSON.stringify([zh.user.lakeNotes, en.user.lakeNotes]),
};
