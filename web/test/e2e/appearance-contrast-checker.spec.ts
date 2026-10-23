import { expect, test } from './test';
import { contrastFailures, pixelContrastSamples } from './appearance-contrast';

test('contrast checker rejects opaque gradient endpoints and interior colours', async ({
  page,
}) => {
  await page.setContent(`<style>
    .sample { width: 340px; padding: 16px; font: 16px sans-serif; }
    .unsafe { color: #000; background: linear-gradient(90deg, #fff, #000); }
    .interior { color: #767676; background: linear-gradient(90deg, #fff, #000); }
    .glass { color: #172334; background: linear-gradient(120deg, rgb(245 250 255 / .96), rgb(238 246 255 / .90)); }
    .brand { color: #fff; background: linear-gradient(120deg, #12386c, #222447); }
    .decorated { position: relative; color: #000; background: #fff; }
    .decorated::before { content: ''; position: absolute; inset: 0; background: #000; }
    .decorated span { position: relative; }
    .clipped { width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); color: #000; background: #000; }
  </style>
  <div class="sample unsafe">Unacceptable gradient endpoint</div>
  <div class="sample interior">Unacceptable gradient interior</div>
  <div class="sample glass">Readable translucent glass</div>
  <div class="sample brand">Readable brand gradient</div>
  <div class="sample decorated"><span>Unreadable pseudo-element background</span></div>
  <div class="clipped">Visually hidden copy</div>
  <details class="sample"><summary>Details</summary><p style="color: #000; background: #000">Collapsed copy</p></details>`);
  const estimate = await contrastFailures(page);
  expect(estimate.map((failure) => failure.text)).toEqual([
    'Unacceptable gradient endpoint',
    'Unacceptable gradient interior',
  ]);
  const raster = await pixelContrastSamples(page);
  expect(
    raster.filter((sample) => sample.ratio < sample.required).map((sample) => sample.text),
  ).toEqual([
    'Unacceptable gradient endpoint',
    'Unacceptable gradient interior',
    'Unreadable pseudo-element background',
  ]);
  expect(
    raster.find((sample) => sample.text === 'Readable translucent glass')!.ratio,
  ).toBeGreaterThan(4.5);
  expect(raster.find((sample) => sample.text === 'Readable brand gradient')!.ratio).toBeGreaterThan(
    4.5,
  );
});
