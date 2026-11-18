export const creditHistoryKeys = {
  account: (accountID: string) => ['user', 'credit-history', accountID] as const,
};
