import { useState } from 'react';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { transportRules, type TransportRule } from '@shared/transportRule';
import { TransportRuleField } from './TransportRuleField';
function Editor() {
  const [rule, setRule] = useState<TransportRule>('passthrough');
  return <TransportRuleField value={rule} onChange={setRule} />;
}
describe('shared model transport field', () => {
  it.each(['en', 'zh'] as const)('explains the selected rule in %s', async (locale) => {
    const view = await renderWithProviders(<Editor />, { locale, station: 'user' });
    const select = screen.getByRole('combobox', {
      name: view.i18n.t('common.transportRule.label'),
    });
    expect(select).toHaveValue('passthrough');
    for (const rule of transportRules) {
      await view.user.selectOptions(select, rule);
      expect(select).toHaveValue(rule);
      expect(select).toHaveAccessibleDescription(view.i18n.t(`common.transportRule.help.${rule}`));
    }
  });
});
