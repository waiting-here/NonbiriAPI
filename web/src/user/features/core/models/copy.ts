import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';

const keys = {
  description: 'user.models.description',
  callName: 'user.models.callName',
  prefixHelp: 'user.models.prefixHelp',
  clientValue: 'user.models.clientValue',
  sources: 'user.models.sources',
  sourceCount: 'user.models.sourceCount',
  sourceTotal: 'user.models.sourceTotal',
  orderedSummary: 'user.models.orderedSummary',
  randomSummary: 'user.models.randomSummary',
  retrySummary: 'user.models.retrySummary',
  emptyBody: 'user.models.emptyBody',
  addService: 'user.models.addService',
  advanced: 'user.models.advanced',
  advancedHelp: 'user.models.advancedHelp',
  default: 'user.models.default',
  customized: 'user.models.customized',
  retryHelp: 'user.models.retryHelp',
  toolsHelp: 'user.models.toolsHelp',
  sourceSearch: 'user.models.sourceSearch',
  sourcePlaceholder: 'user.models.sourcePlaceholder',
  sourceSearchHelp: 'user.models.sourceSearchHelp',
  filterService: 'user.models.filterService',
  allServices: 'user.models.allServices',
  searchCount: 'user.models.searchCount',
  manual: 'user.models.manual',
  manualHelp: 'user.models.manualHelp',
  manualSaved: 'user.models.manualSaved',
  service: 'user.models.service',
  key: 'user.models.key',
  modelName: 'user.models.modelName',
  moreSource: 'user.models.moreSource',
  dirtyCount: 'user.models.dirtyCount',
  discard: 'user.models.discard',
  deleteHelp: 'user.models.deleteHelp',
} as const;

export function useModelText() {
  return useRegisteredCopy(keys).t;
}
