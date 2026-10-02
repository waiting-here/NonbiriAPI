import { useState } from 'react';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import {
  buildRolePolicy,
  draftFromRolePolicy,
  type RolePolicy,
  type RolePolicyDraft,
} from '@shared/rolePolicy';
import { RolePolicyEditor } from './RolePolicyEditor';

function Form({ initial }: { initial?: RolePolicy }) {
  const [draft, setDraft] = useState<RolePolicyDraft>(() => draftFromRolePolicy(initial));
  const [saved, setSaved] = useState<RolePolicy>();
  const built = buildRolePolicy(draft);
  return (
    <>
      <RolePolicyEditor value={draft} onChange={setDraft} />
      <button disabled={Boolean(built.error)} onClick={() => setSaved(built.policy)}>
        Save roles
      </button>
      <output aria-label="Saved roles">{saved ? JSON.stringify(saved) : ''}</output>
    </>
  );
}

describe('structured message roles', () => {
  it('keeps the protocol default and supports separate developer and fallback actions with keyboard focus', async () => {
    const view = await renderWithProviders(<Form />, { station: 'admin', locale: 'en' });
    expect(view.container.querySelector('details')).not.toHaveAttribute('open');
    await view.user.click(screen.getByText(/Default:.*role rules/));
    const fallback = screen.getByRole('combobox', { name: 'Default action for unlisted roles' });
    expect(fallback).toHaveValue('native');
    await view.user.selectOptions(fallback, 'reject');
    await view.user.click(screen.getByRole('button', { name: 'Add a role rule' }));
    const name = screen.getByRole('textbox', { name: 'Role name' });
    expect(name).toHaveFocus();
    await view.user.type(name, 'developer');
    expect(name.closest('details')).toHaveAttribute('open');
    await view.user.selectOptions(
      screen.getByRole('combobox', { name: 'Handling action' }),
      'system',
    );
    await view.user.click(screen.getByRole('button', { name: 'Save roles' }));
    expect(screen.getByLabelText('Saved roles')).toHaveTextContent(
      '{"default_action":"reject","rules":{"developer":"system"}}',
    );
    expect(screen.getByText(/Tool messages and results use/)).toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Remove rule 1' }));
    expect(screen.getByRole('button', { name: 'Add a role rule' })).toHaveFocus();
  });

  it('preserves duplicate drafts until corrected instead of replacing another rule', async () => {
    const view = await renderWithProviders(
      <Form initial={{ default_action: 'native', rules: { developer: 'passthrough' } }} />,
      { station: 'admin', locale: 'en' },
    );
    await view.user.click(screen.getByText(/Default:.*role rules/));
    await view.user.click(screen.getByRole('button', { name: 'Add a role rule' }));
    const names = screen.getAllByRole('textbox', { name: 'Role name' });
    await view.user.type(names[1], 'developer');
    expect(names[0]).toHaveValue('developer');
    expect(names[1]).toHaveValue('developer');
    expect(screen.getByRole('alert')).toHaveTextContent('This role is already configured');
    expect(screen.getByRole('alert').closest('details')).toHaveAttribute('open');
    expect(screen.getByRole('button', { name: 'Save roles' })).toBeDisabled();
    await view.user.clear(names[1]);
    await view.user.type(names[1], 'critic');
    expect(names[1].closest('details')).toHaveAttribute('open');
    await view.user.selectOptions(
      screen.getAllByRole('combobox', { name: 'Handling action' })[1],
      'user',
    );
    await view.user.click(screen.getByRole('button', { name: 'Save roles' }));
    expect(screen.getByLabelText('Saved roles')).toHaveTextContent(
      '{"default_action":"native","rules":{"developer":"passthrough","critic":"user"}}',
    );
  });
});
