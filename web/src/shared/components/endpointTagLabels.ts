export const endpointTagKeys = {
  abusive_third_party: 'management.endpoints.tag.abusive_third_party',
  community_charity: 'management.endpoints.tag.community_charity',
  noncommercial_gateway: 'management.endpoints.tag.noncommercial_gateway',
  user_gateway: 'management.endpoints.tag.user_gateway',
  steward_gateway: 'management.endpoints.tag.steward_gateway',
  invalid_entry: 'management.endpoints.tag.invalid_entry',
  mainstream: 'management.endpoints.tag.mainstream',
  mysterious: 'management.endpoints.tag.mysterious',
  other_third_party: 'management.endpoints.tag.other_third_party',
} as const;

const endpointTagTones: Record<string, string> = {
  abusive_third_party: 'bad',
  community_charity: 'ok',
  noncommercial_gateway: 'teal',
  user_gateway: 'info',
  steward_gateway: 'violet',
  mainstream: 'warn',
  mysterious: 'rose',
};

export function endpointTagBadgeClass(tag: string): string {
  const tone = endpointTagTones[tag];
  return tone ? `nb-badge nb-badge--${tone}` : 'nb-badge';
}
