import { describe, expect, it, vi } from 'vitest';
import { modelFixture, taskFixture } from './fixtures';
import type { ParameterRule } from './publicTypes';
import { decodeModel, decodeQueue, decodeTask, getImage } from './publicApi';
import {
  initialValues,
  maxCurrencyUnits,
  multipliedPrice,
  normalizeLines,
  prepareSubmission,
  promptBytes,
  textLength,
  validScalar,
} from './parameters';

describe('image activity public boundary', () => {
  it('checks numeric steps with decimal arithmetic instead of a floating tolerance', () => {
    const rule = {
      key: 'guidance' as const,
      supported: true,
      required: false,
      type: 'number' as const,
      minimum: 0,
      step: 0.1,
    };
    expect(validScalar(rule, 0.3)).toBe(true);
    expect(validScalar(rule, 0.1 + 0.2)).toBe(false);
    expect(validScalar({ ...rule, step: 1e-8 }, 3e-8)).toBe(true);
  });

  it('rejects private model, queue and task fields instead of carrying them into the browser', () => {
    expect(decodeModel(modelFixture()).revision).toBe('2');
    for (const field of ['upstream_model_id', 'base_url', 'metadata', 'secret', 'mapping'])
      expect(() => decodeModel({ ...modelFixture(), [field]: 'private' })).toThrow();
    expect(() => decodeTask({ ...taskFixture(), upstream_task_id: 'private' })).toThrow();
    expect(() =>
      decodeQueue({
        queued: 1,
        running: 0,
        dispatch_paused: false,
        own: [{ task_id: taskFixture().id, position: 1, accepted_at: 1, user_id: '2' }],
      }),
    ).toThrow();
  });
  it('distinguishes reservation, full-price partial success and full refund without subtracting the refund twice', () => {
    expect(decodeTask(taskFixture()).billing_state).toBe('reserved');
    const success = taskFixture({
      status: 'succeeded',
      billing_state: 'charged',
      actual_images: 2,
      completed_at: 1700000010,
      result_expires_at: 1700000610,
      result_available: true,
      images: [
        { index: 0, mime: 'image/png', bytes: 8 },
        { index: 1, mime: 'image/png', bytes: 8 },
      ],
    });
    expect(decodeTask(success).charge).toEqual({ paper: '8', brush: '4' });
    expect(
      decodeTask(
        taskFixture({
          status: 'failed',
          billing_state: 'refunded',
          charge: { paper: '0', brush: '0' },
          refund: { paper: '8', brush: '4' },
        }),
      ).refund.brush,
    ).toBe('4');
    expect(() =>
      decodeTask({ ...success, images: [...success.images, success.images[0]] }),
    ).toThrow();
    expect(decodeTask({ ...success, images: [], result_available: false }).actual_images).toBe(2);
  });
  it('uses exact whole-unit prices beyond Number precision and checks multiplication overflow', () => {
    expect(multipliedPrice('9007199254740993', '1', 4)).toEqual({
      paper: '36028797018963972',
      brush: '4',
    });
    expect(multipliedPrice(String(maxCurrencyUnits), '0', 2)).toBeNull();
    expect(multipliedPrice('0', '0', 1)).toBeNull();
    expect(multipliedPrice('01', '0', 1)).toBeNull();
    expect(multipliedPrice('1', '0', 17)).toBeNull();
  });
  it('measures normalized text in the declared units and applies defaults before combination rules', () => {
    expect(normalizeLines('绘😀\r\nx')).toBe('绘😀\nx');
    expect(textLength('绘😀\r\nx', 'utf8_bytes')).toBe(9);
    expect(textLength('绘😀\r\nx', 'unicode_scalars')).toBe(4);
    expect(textLength('绘😀\r\nx', 'utf16_units')).toBe(5);
    expect(promptBytes('a\r\n', '😀')).toBe(6);
    const model = modelFixture();
    model.combinations = [
      {
        keys: ['n', 'quality'],
        allowed: [
          [1, 'standard'],
          [4, 'high'],
        ],
      },
    ];
    const values = { ...initialValues(model), prompt: 'fixture prompt' };
    const first = prepareSubmission(model, values);
    expect('input' in first && first.input.expected_model_revision).toBe('2');
    expect(prepareSubmission(model, { ...values, n: '4' })).toEqual({ problem: 'combination' });
    expect(prepareSubmission(model, { ...values, n: '4', quality: 'high' })).toMatchObject({
      price: { paper: '8', brush: '4' },
    });
    expect(prepareSubmission(model, { ...values, prompt: '😀'.repeat(17000) })).toEqual({
      problem: 'prompt',
    });
  });
  it('normalizes an omitted image count before bounds and combination validation', () => {
    const model = modelFixture();
    const countRule = model.parameters.find((rule) => rule.key === 'n')!;
    delete countRule.default;
    model.combinations = [{ keys: ['n', 'quality'], allowed: [[1, 'standard']] }];
    expect(initialValues(model).n).toBe('1');
    expect(prepareSubmission(model, { prompt: 'fixture prompt' })).toMatchObject({
      input: { n: 1, quality: 'standard' },
      price: { paper: '2', brush: '1' },
    });
    expect(prepareSubmission(model, { prompt: 'fixture prompt', n: '' })).toMatchObject({
      input: { n: 1 },
    });
    countRule.minimum = 2;
    expect(prepareSubmission(model, { prompt: 'fixture prompt' })).toEqual({ problem: 'n' });
    countRule.default = 3;
    model.combinations = [{ keys: ['n', 'quality'], allowed: [[3, 'standard']] }];
    expect(prepareSubmission(model, { prompt: 'fixture prompt' })).toMatchObject({
      input: { n: 3 },
      price: { paper: '6', brush: '3' },
    });
  });
  it('validates canonical dimensions with independent ranges and minimum-anchored steps', () => {
    const rule: ParameterRule = {
      key: 'size',
      supported: true,
      required: true,
      type: 'string',
      length_unit: 'utf8_bytes',
      default: '80x144',
      dimensions: {
        format: 'width_height',
        width: { minimum: 48, maximum: 240, step: 16 },
        height: { minimum: 80, maximum: 272, step: 32 },
      },
    };
    const model = modelFixture();
    model.parameters.push(rule);
    expect(decodeModel(model).parameters.at(-1)).toEqual(rule);
    expect(prepareSubmission(model, { prompt: 'fixture prompt' })).toMatchObject({
      input: { size: '80x144' },
    });
    expect(validScalar(rule, '112x176')).toBe(true);
    for (const size of [
      '048x80',
      '48X80',
      '48 x 80',
      '48x80.0',
      '48x96',
      '49x80',
      '16x80',
      '48x99999',
    ])
      expect(validScalar(rule, size)).toBe(false);
    expect(() =>
      decodeModel({
        ...model,
        parameters: [
          ...model.parameters.slice(0, -1),
          {
            ...rule,
            dimensions: { ...rule.dimensions, width: { minimum: 48, maximum: 240, step: 0 } },
          },
        ],
      }),
    ).toThrow();
    expect(() =>
      decodeModel({
        ...model,
        parameters: [...model.parameters.slice(0, -1), { ...rule, enum: ['48x96'] }],
      }),
    ).toThrow();
    expect(() =>
      decodeModel({
        ...model,
        parameters: [...model.parameters.slice(0, -1), { ...rule, supported: false }],
      }),
    ).toThrow();
  });
  it('retrieves bounded image bytes only from a constructed same-origin path', async () => {
    const fetch = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async () =>
        new Response(new Uint8Array([1, 2, 3]), {
          headers: { 'Content-Type': 'image/png', 'Content-Length': '3' },
        }),
    );
    vi.stubGlobal('fetch', fetch);
    const info = { index: 0, mime: 'image/png' as const, bytes: 3 };
    const blob = await getImage(taskFixture().id, info, new AbortController().signal);
    expect(blob.size).toBe(3);
    expect(fetch.mock.calls[0][0]).toBe(
      '/api/limited-activities/picture-book/tasks/' + taskFixture().id + '/images/0',
    );
    expect(fetch.mock.calls[0][1]).toMatchObject({
      credentials: 'same-origin',
      redirect: 'error',
      cache: 'no-store',
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () => new Response(new Uint8Array(4), { headers: { 'Content-Type': 'image/png' } }),
      ),
    );
    await expect(getImage(taskFixture().id, info, new AbortController().signal)).rejects.toThrow();
  });
});
