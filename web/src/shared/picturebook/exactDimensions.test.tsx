import { useState } from 'react';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { exactSizeCandidates, quotePricing } from './capabilities';
import {
  initialValues,
  exactModelSizes,
  prepareSubmission,
  previewPrice,
  retainModelValues,
  type ParameterValues,
} from './parameters';
import { ModelParameterFields } from './ModelParameterFields';
import { modelFixture } from './fixtures';
import type { ImageModel } from './publicTypes';

const model = (): ImageModel => ({
  ...modelFixture(),
  size_capability: {
    mode: 'width_height',
    width: { minimum: 256, maximum: 1024, step: 256 },
    height: { minimum: 256, maximum: 1024, step: 256 },
    max_pixels: 786432,
    auto: true,
  },
  parameters: [
    ...modelFixture().parameters,
    { key: 'size', supported: true, required: true, type: 'string' },
  ],
  pricing: {
    default: { paper: '1', brush: '0' },
    fallback: 'unavailable',
    tiers: [
      { tier: 'standard', paper: '2', brush: '0' },
      { tier: 'auto', paper: '4', brush: '0' },
    ],
    sizes: [
      { width: 768, height: 512, paper: '8', brush: '0' },
      { width: 512, height: 768, paper: '7', brush: '1' },
      { width: 257, height: 256, paper: '3', brush: '0' },
      { width: 1024, height: 1024, paper: '3', brush: '0' },
    ],
  },
});
function Fields({ value, draft }: { value: ImageModel; draft?: ParameterValues }) {
  const [values, setValues] = useState(draft ?? initialValues(value));
  return (
    <ModelParameterFields
      model={value}
      values={values}
      onChange={(patch) => setValues((old) => ({ ...old, ...patch }))}
    />
  );
}

describe('exact dimension pairs', () => {
  it('filters ranges, step, pixels, capability tuples and price units, then sorts and deduplicates whole pairs', () => {
    const value = model(),
      policy = value.pricing!,
      capability = value.size_capability!;
    const pairs = exactSizeCandidates(capability, {
      ...policy,
      sizes: [
        ...policy.sizes,
        policy.sizes[0],
        { width: 256, height: 256, paper: '0', brush: '0' },
      ],
    });
    expect(pairs.map((row) => [row.width, row.height])).toEqual([
      [512, 768],
      [768, 512],
    ]);
    value.size_capability = {
      ...capability,
      combinations: [{ width: 512, height: 768, tier: 'standard' }],
    };
    expect(exactModelSizes(value)?.map((row) => row.width)).toEqual([512]);
    value.combinations = [{ keys: ['size', 'quality'], allowed: [['768x512', 'standard']] }];
    expect(exactModelSizes(value)).toEqual([]);
  });
  it('blocks unpriced tier bypass and old draft values without changing fixed tiers, auto, or other fallback', () => {
    const value = model();
    value.size_capability!.combinations = [
      { width: 512, height: 768, tier: 'standard' },
      { width: 768, height: 512, tier: 'standard' },
    ];
    value.pricing!.sizes = value.pricing!.sizes.filter((row) => row.width === 512);
    expect(previewPrice(value, { size: '768x512' })).toBeNull();
    expect(prepareSubmission(value, { prompt: 'kept', size: '768x512' })).toEqual({
      problem: 'size',
    });
    expect(previewPrice(value, { size: 'auto' })?.unit.paper).toBe('4');
    expect(
      quotePricing(value.pricing!, { width: 768, height: 512, tier: 'standard' }, 1, {
        mode: 'ratio_size_map',
        combinations: [{ ratio: 'landscape', width: 768, height: 512, tier: 'standard' }],
      })?.basis,
    ).toBe('tier');
    expect(
      quotePricing(
        { ...value.pricing!, fallback: 'default' },
        { width: 768, height: 512, tier: 'standard' },
        1,
        value.size_capability,
      )?.basis,
    ).toBe('tier');
    expect(
      retainModelValues(value, {
        prompt: 'kept',
        negative_prompt: 'also kept',
        n: '2',
        size: '768x512',
      }),
    ).toMatchObject({ prompt: 'kept', negative_prompt: 'also kept', n: '2', size: '768x512' });
  });
  it('applies exact rows to legacy dimensions while preserving default dimensions behavior', () => {
    const value = model();
    delete value.size_capability;
    value.parameters[value.parameters.length - 1].dimensions = {
      format: 'width_height',
      width: { minimum: 256, maximum: 1024, step: 256 },
      height: { minimum: 256, maximum: 1024, step: 256 },
    };
    expect(initialValues(value).size).toBe('512x768');
    expect(previewPrice(value, { size: '512x768' })?.unit.paper).toBe('7');
    expect(
      exactModelSizes({ ...value, pricing: { ...value.pricing!, fallback: 'default' } }),
    ).toBeNull();
  });
  it.each(['user', 'admin'] as const)(
    'shows whole choices on %s, preserves invalid restored inputs, and requires a new choice after a pricing change',
    async (station) => {
      const value = model();
      const view = await renderWithProviders(
        <Fields value={value} draft={{ prompt: 'retained prompt', size: '512x512' }} />,
        { station },
      );
      expect(screen.queryByRole('spinbutton', { name: 'Width' })).toBeNull();
      const select = screen.getByRole('combobox', { name: 'Exact size' });
      expect(
        within(select)
          .getAllByRole('option')
          .map((option) => option.getAttribute('value')),
      ).toEqual(['', '512x768', '768x512']);
      expect(select).toHaveValue('');
      expect(screen.getByText(/Choose an available size again/)).toBeVisible();
      await view.user.selectOptions(select, '512x768');
      const next = { ...value, pricing: { ...value.pricing!, sizes: [] } };
      view.rerender(<Fields value={next} />);
      expect(screen.getByRole('combobox', { name: 'Exact size' })).toHaveValue('');
      expect(screen.getByRole('combobox', { name: 'Exact size' })).toBeDisabled();
      expect(screen.getByText(/No sizes are available/)).toBeVisible();
      expect(screen.getByLabelText(/Prompt/)).toHaveValue('retained prompt');
    },
  );
});
