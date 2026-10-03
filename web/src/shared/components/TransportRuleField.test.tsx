import { useState } from 'react';
import { screen, within } from '@testing-library/react';
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
    const group = screen.getByRole('radiogroup', {
      name: view.i18n.t('common.transportRule.label'),
    });
    expect(
      within(group).getByRole('radio', {
        name: view.i18n.t('common.transportRule.options.passthrough'),
      }),
    ).toBeChecked();
    for (const rule of transportRules) {
      const radio = within(group).getByRole('radio', {
        name: view.i18n.t(`common.transportRule.options.${rule}`),
      });
      await view.user.click(radio);
      expect(radio).toBeChecked();
      expect(radio).toHaveAttribute('value', rule);
      expect(group).toHaveAccessibleDescription(view.i18n.t(`common.transportRule.help.${rule}`));
    }
  });
});
