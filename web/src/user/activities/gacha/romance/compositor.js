import { makeCharacterRenderer as makeClaude } from './elopement-character.js';
import {
  makeCharacterRenderer as makeGLM,
  makeBackgroundRenderer,
  invitationPose,
} from './heart-thief-character.js';
import { portraitWindows } from '../portrait';
import { heartThiefGroundPoint, heartThiefStance } from './heart-thief-stance';
import { romanceAppearancePlacement, romanceBackdropPlacement } from './layout';
import { elopementEnvironment, elopementDust } from './elopement-environment';
import { heartThiefEnvironment, heartThiefDust, heartThiefLight } from './heart-thief-environment';
import { romanceAssetSources } from './assets';

const clamp = (x) => Math.max(0, Math.min(1, x));
const ease = (x) => {
  const p = clamp(x);
  return p * p * (3 - 2 * p);
};
function blink(t, start, close, hold, open) {
  const p = t - start;
  if (p < 0 || p > close + hold + open) return 0;
  if (p < close) return ease(p / close);
  if (p < close + hold) return 1;
  return 1 - ease((p - close - hold) / open);
}
function load(url) {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve(image);
    image.onerror = () => reject(Error('Artwork could not be loaded.'));
    image.src = url;
  });
}

export async function createScene(id, polished = false, options = {}) {
  const claude = id === 'elopement';
  const refined = polished;
  const sources = romanceAssetSources(id, options.highResolution, options.dark);
  const images = Object.fromEntries(
    await Promise.all(
      Object.entries(sources).map(async ([name, source]) => [name, await load(source)]),
    ),
  );
  const character = claude
    ? makeClaude(images)
    : makeGLM(images.character, images['hair-backing-v3']);
  const background = claude
    ? { draw: () => images.background }
    : makeBackgroundRenderer(images.background);
  const environment = refined
    ? claude
      ? elopementEnvironment(images.background)
      : heartThiefEnvironment(images.background, background)
    : undefined;
  const dust = claude ? elopementDust : heartThiefDust;
  const width = claude ? 1024 : 1086,
    height = claude ? 1536 : 1448;
  const lit = document.createElement('canvas');
  lit.width = width;
  lit.height = height;
  function lighting(body, dark, time) {
    if (!polished) return body;
    const ctx = lit.getContext('2d');
    ctx.clearRect(0, 0, width, height);
    ctx.save();
    if (!claude && refined) {
      const [x, y] = heartThiefStance.anchor;
      ctx.translate(x, y);
      ctx.rotate(heartThiefStance.angle);
      ctx.translate(-x, -y);
    }
    ctx.drawImage(body, 0, 0, width, height);
    ctx.restore();
    ctx.save();
    ctx.globalCompositeOperation = 'source-atop';
    if (claude) {
      ctx.fillStyle = dark ? 'rgba(31,49,92,.29)' : 'rgba(91,107,143,.055)';
      ctx.fillRect(0, 0, width, height);
      const side = ctx.createLinearGradient(0, height * 0.4, width, height * 0.15);
      side.addColorStop(0, dark ? 'rgba(125,153,220,.12)' : 'rgba(255,192,116,.14)');
      side.addColorStop(0.53, 'rgba(255,219,165,0)');
      side.addColorStop(1, dark ? 'rgba(255,207,139,.14)' : 'rgba(105,143,203,.075)');
      ctx.fillStyle = side;
      ctx.fillRect(0, 0, width, height);
      const ground = ctx.createLinearGradient(0, height * 0.8, 0, height);
      ground.addColorStop(0, 'rgba(190,208,240,0)');
      ground.addColorStop(
        1,
        `rgba(143,168,213,${dark ? 0.1 : 0.055 + 0.008 * Math.sin(time * 0.18)})`,
      );
      ctx.fillStyle = ground;
      ctx.fillRect(0, 0, width, height);
      ctx.restore();
      return lit;
    }
    ctx.fillStyle = dark ? 'rgba(31,44,77,.19)' : 'rgba(61,86,137,.06)';
    ctx.fillRect(0, 0, width, height);
    const { candle, moon } = heartThiefLight(time);
    const side = ctx.createLinearGradient(0, height * 0.4, width, height * 0.1);
    side.addColorStop(0, `rgba(255,184,99,${(dark ? 0.18 : 0.07) * candle})`);
    side.addColorStop(0.5, 'rgba(176,207,255,0)');
    side.addColorStop(1, `rgba(166,200,255,${(dark ? 0.075 : 0.17) * (0.9 + 0.1 * moon)})`);
    ctx.fillStyle = side;
    ctx.fillRect(0, 0, width, height);
    ctx.restore();
    return lit;
  }
  function pose(time, motion, forcedBlink) {
    const phase = (claude ? 8 : 12) + (motion ? time : 0);
    const cycle = time % (claude ? 17.6 : 17.4);
    const eyelid =
      forcedBlink ??
      (motion
        ? claude
          ? [2.2, 7.4, 13.1].reduce((v, s) => v + blink(cycle, s, 0.13, 0.045, 0.24), 0)
          : [1.9, 6.7, 12.25].reduce((v, s) => v + blink(cycle, s, 0.115, 0.035, 0.21), 0)
        : 0);
    const body = claude
      ? character.draw(
          phase,
          1,
          1,
          eyelid,
          motion ? 0.006 * Math.sin((time * Math.PI * 2) / 8.8) : 0,
          1,
        )
      : character.draw(phase, invitationPose(9), eyelid);
    return { phase, eyelid, body };
  }
  function shadow(ctx, hero, scale) {
    ctx.save();
    if (!claude && refined) {
      const density = Math.abs(ctx.getTransform().a);
      // The near toe and stiletto heel support the pose; the far foot stays lifted.
      for (const [x, y, rx, ry, angle, alpha, blur] of [
        [430, 1435, 74, 19, -0.22, 0.19, 13],
        [452, 1411, 57, 15, -0.68, 0.28, 8],
        [566, 1430, 53, 9, 0.18, 0.13, 12],
        [421, 1437, 16, 3, -0.18, 0.66, 1.2],
        [486, 1382, 5, 2, 0, 0.62, 1.2],
      ]) {
        ctx.fillStyle = `rgba(5,8,18,${alpha})`;
        ctx.shadowColor = `rgba(5,8,18,${alpha * 0.6})`;
        ctx.shadowBlur = blur * scale * density;
        const ground = heartThiefGroundPoint(x, y);
        ctx.beginPath();
        ctx.ellipse(
          hero.x + ground[0] * scale,
          hero.y + ground[1] * scale,
          rx * scale,
          ry * scale,
          angle + heartThiefStance.angle,
          0,
          Math.PI * 2,
        );
        ctx.fill();
      }
      ctx.restore();
      return;
    }
    ctx.fillStyle = 'rgba(8,15,26,.32)';
    ctx.shadowColor = 'rgba(8,15,26,.28)';
    ctx.shadowBlur = refined ? 6 * scale : 6;
    const feet = claude
      ? [
          [523, 1518, 59, 10],
          [588, 1405, 42, 8],
        ]
      : [
          [455, 1422, 51, 8],
          [650, 1290, 25, 6],
        ];
    for (const [x, y, rx, ry] of feet) {
      ctx.beginPath();
      ctx.ellipse(
        hero.x + x * scale,
        hero.y + y * scale,
        rx * scale,
        ry * scale,
        0,
        0,
        Math.PI * 2,
      );
      ctx.fill();
    }
    ctx.restore();
  }
  function paintCharacter(canvas, dark = false, time = 0, forcedBlink = 0) {
    const ctx = canvas.getContext('2d');
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.save();
    ctx.scale(
      canvas.width / width,
      canvas.height / (!claude && refined ? heartThiefStance.plateHeight : height),
    );
    shadow(ctx, { x: 0, y: 0 }, 1);
    ctx.drawImage(lighting(pose(time, true, forcedBlink).body, dark, time), 0, 0, width, height);
    ctx.restore();
    canvas.dataset.blink = forcedBlink.toFixed(4);
  }
  function paint(
    canvas,
    time,
    variant = 'card',
    dark = false,
    layer = 'complete',
    motion = true,
    forcedBlink = null,
    viewportWidth = canvas.width,
  ) {
    const ctx = canvas.getContext('2d');
    ctx.imageSmoothingQuality = 'high';
    const card = variant === 'card',
      cover = variant === 'cover',
      appearance = refined && variant === 'appearance';
    const window = polished && card ? portraitWindows[id] : [0, 0, 1, 1];
    const stageWidth = appearance ? canvas.width : card ? ((1024 * 2) / 3) * window[2] : 1536,
      stageHeight = appearance ? canvas.height : card ? 1024 * window[3] : 864;
    const ratio = canvas.width / viewportWidth;
    const cssPlacement = appearance
      ? romanceAppearancePlacement(viewportWidth, canvas.height / ratio, id)
      : null;
    const placement = cssPlacement
      ? Object.fromEntries(Object.entries(cssPlacement).map(([key, value]) => [key, value * ratio]))
      : null;
    const scale = appearance
      ? placement.height / height
      : card
        ? claude
          ? 0.59
          : 0.62
        : cover
          ? claude
            ? 0.65
            : 0.66
          : claude
            ? 0.5
            : 0.52;
    const hero = {
      x: appearance
        ? placement.left
        : card
          ? claude
            ? 690
            : 570
          : cover
            ? claude
              ? 795
              : 810
            : claude
              ? 875
              : 795,
      y: appearance ? placement.top : card ? 64 : cover ? 40 : claude ? 54 : 115,
      s: scale,
    };
    const cropX = card
      ? hero.x + (width * hero.s) / 2 - 1024 / 3 + ((1024 * 2) / 3) * window[0]
      : 0;
    const { phase, eyelid, body } = pose(time, motion, forcedBlink);
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.save();
    ctx.scale(canvas.width / stageWidth, canvas.height / stageHeight);
    ctx.translate(-cropX, -(card ? 1024 * window[1] : 0));
    const backdrop = appearance
      ? !claude && layer === 'background'
        ? { left: 0, top: 0, scale: canvas.width / 1536 }
        : romanceBackdropPlacement(canvas.width, canvas.height, viewportWidth, id)
      : { left: 0, top: 0, scale: 1 };
    if (layer !== 'hero') {
      ctx.save();
      ctx.translate(backdrop.left, backdrop.top);
      ctx.scale(backdrop.scale, backdrop.scale);
      const projection = ctx.getTransform();
      background.setProjectionScale?.(Math.max(Math.abs(projection.a), Math.abs(projection.d)));
      if (environment) {
        environment.draw(ctx, motion ? time : 0, dark);
        dust(ctx, motion ? time : 0, dark, false);
      } else {
        ctx.drawImage(background.draw(phase), 0, 0, 1536, 1024);
        const glow = ctx.createRadialGradient(
          claude ? 115 : 55,
          claude ? 350 : 275,
          20,
          claude ? 115 : 55,
          claude ? 350 : 275,
          claude ? 820 : 320,
        );
        glow.addColorStop(
          0,
          claude
            ? 'rgba(255,189,118,' + (0.07 + 0.025 * Math.sin(phase * 0.6)) + ')'
            : 'rgba(255,176,87,' + (0.055 + 0.012 * Math.sin(phase * 3.2)) + ')',
        );
        glow.addColorStop(1, 'rgba(255,190,120,0)');
        ctx.fillStyle = glow;
        ctx.fillRect(0, 0, 1536, 1024);
      }
      ctx.restore();
    }
    if (layer !== 'background') {
      shadow(ctx, hero, scale);
      ctx.drawImage(lighting(body, dark, time), hero.x, hero.y, width * scale, height * scale);
    }
    if (layer === 'complete') {
      ctx.save();
      ctx.translate(backdrop.left, backdrop.top);
      ctx.scale(backdrop.scale, backdrop.scale);
      if (refined) dust(ctx, motion ? time : 0, dark, true);
      ctx.restore();
    }
    ctx.restore();
    if (!refined && variant === 'appearance' && layer === 'complete') {
      ctx.save();
      ctx.globalCompositeOperation = 'source-atop';
      ctx.fillStyle = dark
        ? claude
          ? 'rgba(16,24,48,.18)'
          : 'rgba(7,15,35,.13)'
        : claude
          ? 'rgba(255,229,196,.045)'
          : 'rgba(207,222,252,.075)';
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      ctx.restore();
    }
    canvas.dataset.blink = eyelid.toFixed(4);
    canvas.dataset.time = time.toFixed(3);
    canvas.dataset.scene = id;
  }
  return {
    paint,
    paintCharacter,
    dispose() {
      character.dispose?.();
      background.dispose?.();
      environment?.dispose();
    },
  };
}
/* global Image, document */
