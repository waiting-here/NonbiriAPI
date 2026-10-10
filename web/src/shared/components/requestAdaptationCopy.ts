import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';

export type RequestAdaptationCopyKey =
  | 'title'
  | 'description'
  | 'openaiSupport'
  | 'convertedSupport'
  | 'allSupport'
  | 'inherited'
  | 'sourceModel'
  | 'sourceBinding'
  | 'revision'
  | 'forwardHeaders'
  | 'fixedHeaders'
  | 'bodyDefaults'
  | 'bodyForced'
  | 'nativePaths'
  | 'listHelp'
  | 'bodyHelp'
  | 'fixedHelp'
  | 'mode'
  | 'replace'
  | 'inherit'
  | 'name'
  | 'value'
  | 'action'
  | 'keep'
  | 'change'
  | 'clear'
  | 'add'
  | 'empty'
  | 'masked'
  | 'effective'
  | 'save'
  | 'saved'
  | 'loading'
  | 'loadError'
  | 'stale'
  | 'saveError'
  | 'unknown'
  | 'conflict'
  | 'invalidJSON'
  | 'duplicate'
  | 'refresh'
  | 'readOnly'
  | 'example';
const serviceKeys = new Set<RequestAdaptationCopyKey>([
  'title',
  'description',
  'forwardHeaders',
  'fixedHeaders',
  'bodyDefaults',
  'bodyForced',
  'nativePaths',
  'listHelp',
  'bodyHelp',
  'fixedHelp',
  'save',
  'saved',
  'loading',
  'loadError',
  'example',
]);

export function useRequestAdaptationCopy(endpoint = false) {
  const { t } = useTranslation();
  return useCallback(
    (key: RequestAdaptationCopyKey) =>
      t(
        'requestAdaptation.' +
          (endpoint && serviceKeys.has(key) ? 'service' : 'editor') +
          '.' +
          key,
      ),
    [t, endpoint],
  );
}
