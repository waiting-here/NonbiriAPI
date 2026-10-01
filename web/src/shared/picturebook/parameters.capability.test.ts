import { describe, expect, it } from 'vitest';
import { initialValues, prepareSubmission, previewPrice } from './parameters';
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
  it('preserves non-first discovered ratio and resolution defaults and their exact price', () => {
    const next: ImageModel = {
      ...model,
      size_capability: {
        mode: 'resolution_ratio_grid',
        combinations: [
          { ratio: '1:1', resolution: 'small', width: 512, height: 512, tier: 'small' },
          { ratio: '4:3', resolution: 'medium', width: 1024, height: 768, tier: 'medium' },
        ],
      },
      parameters: model.parameters.map((rule) =>
        rule.key === 'aspect_ratio'
          ? { ...rule, default: '4:3' }
          : rule.key === 'resolution'
            ? { ...rule, default: 'medium' }
            : rule,
      ),
    };
    const values = initialValues(next);
    expect(values).toMatchObject({ aspect_ratio: '4:3', resolution: 'medium', size: '1024x768' });
    expect(previewPrice(next, values)).toMatchObject({
      unit: { paper: '5', brush: '0' },
      basis: 'size',
    });
    expect(prepareSubmission(next, { ...values, prompt: 'hello' })).toHaveProperty(
      'input.size',
      '1024x768',
    );
  });

  it('keeps a ratio-only default and a valid width-height default ahead of the first combination', () => {
    const ratio: ImageModel = {
      ...model,
      size_capability: {
        mode: 'ratio_resolution',
        combinations: [{ ratio: '1:1' }, { ratio: '16:9' }],
      },
      parameters: model.parameters
        .filter((rule) => !['size', 'resolution'].includes(rule.key))
        .map((rule) => (rule.key === 'aspect_ratio' ? { ...rule, default: '16:9' } : rule)),
    };
    expect(initialValues(ratio).aspect_ratio).toBe('16:9');
    const dimensions: ImageModel = {
      ...model,
      pricing: { ...model.pricing!, fallback: 'default' },
      size_capability: {
        mode: 'width_height',
        width: { minimum: 256, maximum: 2048, step: 8 },
        height: { minimum: 256, maximum: 2048, step: 8 },
        combinations: [
          { width: 512, height: 512 },
          { width: 1024, height: 1024 },
        ],
      },
      parameters: model.parameters
        .filter((rule) => !['aspect_ratio', 'resolution'].includes(rule.key))
        .map((rule) => (rule.key === 'size' ? { ...rule, default: '1024x1024' } : rule)),
    };
    expect(initialValues(dimensions).size).toBe('1024x1024');
  });

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

  it('submits a ratio-only size without resolution or a stringified undefined value', () => {
    const next: ImageModel = {
      ...model,
      pricing: { default: model.price, fallback: 'default', tiers: [], sizes: [] },
      size_capability: {
        mode: 'ratio_resolution',
        combinations: [{ ratio: '1:1' }, { ratio: '16:9' }],
      },
      parameters: model.parameters.filter((rule) => !['size', 'resolution'].includes(rule.key)),
    };
    const result = prepareSubmission(next, {
      ...initialValues(next),
      prompt: 'hello',
      aspect_ratio: '16:9',
      resolution: '',
    });
    expect(result).toHaveProperty('input.aspect_ratio', '16:9');
    expect(result).not.toHaveProperty('input.resolution');
    expect(result).not.toHaveProperty('input.size');
    expect(JSON.stringify(result)).not.toContain('undefined');
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
