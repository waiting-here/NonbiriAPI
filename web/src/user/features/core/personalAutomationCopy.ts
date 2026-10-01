import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';

const keys = {
  title: 'user.personalAutomation.title',
  intro: 'user.personalAutomation.intro',
  identityTitle: 'user.personalAutomation.identityTitle',
  identity: 'user.personalAutomation.identity',
  scope: 'user.personalAutomation.scope',
  lookupTitle: 'user.personalAutomation.lookupTitle',
  lookup: 'user.personalAutomation.lookup',
  readExample: 'user.personalAutomation.readExample',
  method: 'user.personalAutomation.method',
  path: 'user.personalAutomation.path',
  purpose: 'user.personalAutomation.purpose',
  endpoints: 'user.personalAutomation.endpoints',
  endpoint: 'user.personalAutomation.endpoint',
  keys: 'user.personalAutomation.keys',
  key: 'user.personalAutomation.key',
  models: 'user.personalAutomation.models',
  model: 'user.personalAutomation.model',
  bindings: 'user.personalAutomation.bindings',
  pagination: 'user.personalAutomation.pagination',
  importTitle: 'user.personalAutomation.importTitle',
  import: 'user.personalAutomation.import',
  importBody: 'user.personalAutomation.importBody',
  importRequest: 'user.personalAutomation.importRequest',
  bindTitle: 'user.personalAutomation.bindTitle',
  bind: 'user.personalAutomation.bind',
  catalog: 'user.personalAutomation.catalog',
  bindBody: 'user.personalAutomation.bindBody',
  bindRequest: 'user.personalAutomation.bindRequest',
  resultTitle: 'user.personalAutomation.resultTitle',
  result: 'user.personalAutomation.result',
  resultExample: 'user.personalAutomation.resultExample',
  retry: 'user.personalAutomation.retry',
  limits: 'user.personalAutomation.limits',
  statuses: 'user.personalAutomation.statuses',
} as const;

export function usePersonalAutomationCopy() {
  return useRegisteredCopy(keys);
}
