export const limitedActivityKeys = {
  root: (account: string) => ['user', 'limited-activities', account] as const,
  detail: (account: string) => ['user', 'limited-activities', account, 'detail'] as const,
  wallet: (account: string) => ['user', 'limited-activities', account, 'wallet'] as const,
};
