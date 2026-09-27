import { describe, expect, it } from 'vitest';
import { initialValues, prepareSubmission } from './parameters';
import type { ImageModel } from './publicTypes';

const model: ImageModel = {
  id: 'imdl_0123456789abcdefghijkQ',
  display_name: 'Sample',
  description: '',
  revision: '2',
  pricing_revision: '4',
  price: { paper: '1', brush: '0' },
  pricing: {
    default: { paper: '1', brush: '0' },
    fallback: 'unavailable',
    tiers: [{ tier: 'medium', paper: '3', brush: '1' }],
    sizes: [{ width: 1024, height: 768, paper: '5', brush: '0' }],
  },
  size_capability: {
    mode: 'resolution_ratio_grid',
    combinations: [
      { ratio: '4:3', resolution: 'medium', width: 1024, height: 768, tier: 'medium' },
    ],
  },
  parameters: [
    {
      key: 'prompt',
      supported: true,
      required: true,
      type: 'string',
      min_length: 1,
      length_unit: 'utf8_bytes',
    },
    {
      key: 'n',
      supported: true,
      required: false,
      type: 'integer',
      minimum: 1,
      maximum: 16,
      default: 1,
    },
    { key: 'size', supported: true, required: true, type: 'string', length_unit: 'utf8_bytes' },
    {
      key: 'aspect_ratio',
      supported: true,
      required: true,
      type: 'string',
      length_unit: 'utf8_bytes',
    },
    {
      key: 'resolution',
      supported: true,
      required: true,
      type: 'string',
      length_unit: 'utf8_bytes',
    },
  ],
  combinations: [],
};

describe('capability-aware submission', () => {
  it('fills all linked size fields and freezes both model and price revisions', () => {
    const values = { ...initialValues(model), prompt: 'hello', n: '2' };
    const result = prepareSubmission(model, values);
    expect(result).toEqual({
      input: {
        model_id: model.id,
        expected_model_revision: '2',
        expected_pricing_revision: '4',
        prompt: 'hello',
        n: 2,
        size: '1024x768',
        aspect_ratio: '4:3',
        resolution: 'medium',
      },
      unit: { paper: '5', brush: '0' },
      price: { paper: '10', brush: '0' },
      basis: 'size',
    });
    expect(prepareSubmission(model, { ...values, size: '768x1024' })).toEqual({ problem: 'size' });
  });

  it('does not price a valid but unavailable combination', () => {
    const next: ImageModel = {
      ...model,
      size_capability: {
        mode: 'ratio_resolution',
        combinations: [{ ratio: '1:1', resolution: 'small' }],
      },
      parameters: model.parameters.filter((rule) => rule.key !== 'size'),
    };
    expect(
      prepareSubmission(next, {
        prompt: 'hello',
        n: '1',
        aspect_ratio: '1:1',
        resolution: 'small',
      }),
    ).toEqual({ problem: 'price' });
  });
});
