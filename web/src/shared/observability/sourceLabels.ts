import type { TFunction } from 'i18next';

const qualityKeys: Record<string, string> = {
  direct_peer: 'common.sourceQuality.direct',
  trusted_forwarded: 'common.sourceQuality.forwarded',
  peer_fallback: 'common.sourceQuality.fallback',
};
const flagKeys: Record<string, string> = {
  truncated: 'common.sourceQuality.truncated',
  multiple: 'common.sourceQuality.multiple',
  invalid: 'common.sourceQuality.invalid',
};
export function sourceQualityLabel(value: string, t: TFunction): string {
  return qualityKeys[value] ? t(qualityKeys[value]) : value;
}
export function sourceFlagLabel(value: string, t: TFunction): string {
  return flagKeys[value] ? t(flagKeys[value]) : value;
}
