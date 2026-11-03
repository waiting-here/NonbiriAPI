import { useState } from 'react';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import common from '@shared/i18n/common/en.json';
import { renderWithProviders } from '../../../test/unit/support';
import type { DriverProvider } from './driversApi';
import { ReasoningEffortField } from './ReasoningEffortField';

const copy = common.common.agent.reasoningEffort;
function Editor({ provider, initial }: { provider: DriverProvider; initial: string }) {
  const [value, setValue] = useState(initial);
  return (
    <>
      <ReasoningEffortField provider={provider} value={value} onChange={setValue} />
      <textarea
        aria-label="Advanced JSON"
        value={value}
        onChange={(e) => setValue(e.target.value)}
      />
    </>
  );
}

describe('assistant reasoning effort', () => {
  it.each(['chat_completions', 'responses', 'chatgpt', 'codex'] as const)(
    'updates %s parameters and removes only effort when returning to defaults',
    async (provider) => {
      const chat = provider === 'chat_completions';
      const initial = chat
        ? { reasoning_effort: 'low', parallel_tool_calls: false }
        : { reasoning: { effort: 'low', summary: 'auto' }, parallel_tool_calls: false };
      const { user } = await renderWithProviders(
        <Editor provider={provider} initial={JSON.stringify(initial)} />,
        { station: 'admin', role: 'admin' },
      );
      const field = screen.getByRole('combobox', { name: copy.label });
      const json = screen.getByRole('textbox', { name: 'Advanced JSON' });
      expect(field).toHaveValue('low');
      expect(screen.queryByRole('option', { name: copy.max }) !== null).toBe(
        provider === 'responses' || provider === 'chatgpt',
      );
      await user.selectOptions(field, 'high');
      expect(JSON.parse((json as HTMLTextAreaElement).value)).toEqual(
        chat
          ? { reasoning_effort: 'high', parallel_tool_calls: false }
          : { reasoning: { effort: 'high', summary: 'auto' }, parallel_tool_calls: false },
      );
      await user.selectOptions(field, '');
      expect(JSON.parse((json as HTMLTextAreaElement).value)).toEqual(
        chat
          ? { parallel_tool_calls: false }
          : { reasoning: { summary: 'auto' }, parallel_tool_calls: false },
      );
    },
  );

  it('reflects advanced edits and preserves custom values until a new option is chosen', async () => {
    const { user } = await renderWithProviders(
      <Editor provider="chat_completions" initial='{"reasoning_effort":"future","seed":7}' />,
      { station: 'admin', role: 'admin' },
    );
    const field = screen.getByRole('combobox', { name: copy.label });
    const json = screen.getByRole('textbox', { name: 'Advanced JSON' });
    expect(field).toHaveValue('custom');
    expect(json).toHaveValue('{"reasoning_effort":"future","seed":7}');
    await user.clear(json);
    await user.paste('{"reasoning_effort":"medium","seed":7}');
    expect(field).toHaveValue('medium');
    await user.selectOptions(field, 'none');
    expect(JSON.parse((json as HTMLTextAreaElement).value)).toEqual({
      reasoning_effort: 'none',
      seed: 7,
    });
  });

  it('keeps incomplete JSON and non-object reasoning intact until the advanced draft is repaired', async () => {
    const { user } = await renderWithProviders(<Editor provider="responses" initial="{" />, {
      station: 'admin',
      role: 'admin',
    });
    const field = screen.getByRole('combobox', { name: copy.label });
    const json = screen.getByRole('textbox', { name: 'Advanced JSON' });
    expect(field).toBeDisabled();
    expect(field).toHaveAccessibleDescription(copy.fixJson);
    expect(json).toHaveValue('{');
    await user.clear(json);
    await user.paste('{"reasoning":[]}');
    expect(field).toBeDisabled();
    expect(json).toHaveValue('{"reasoning":[]}');
    await user.clear(json);
    await user.paste('{}');
    expect(field).toBeEnabled();
    await user.selectOptions(field, 'max');
    expect(JSON.parse((json as HTMLTextAreaElement).value)).toEqual({
      reasoning: { effort: 'max' },
    });
    await user.selectOptions(field, '');
    expect(json).toHaveValue('{}');
  });
});
