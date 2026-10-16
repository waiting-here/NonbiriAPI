import { makeCharacterRenderer as makeClaude } from './elopement-character.js';
import {
  makeCharacterRenderer as makeGLM,
  makeBackgroundRenderer,
  invitationPose,
} from './heart-thief-character.js';
import { portraitWindows } from '../portrait';

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

export async function createScene(id, polished = false) {
  const claude = id === 'elopement';
  const names = claude
    ? ['background', 'offer', 'arrival', 'closed', 'clean']
    : ['background', 'character', 'hair-backing-v3'];
  const images = Object.fromEntries(
    await Promise.all(
      names.map(async (name) => [
        name,
        await load('/assets/gacha/motion/' + (claude ? 'p-claude' : 'q-glm') + '/' + name + '.png'),
      ]),
    ),
  );
  const character = claude
    ? makeClaude(images)
    : makeGLM(images.character, images['hair-backing-v3']);
  const background = claude
    ? { draw: () => images.background }
    : makeBackgroundRenderer(images.background);
  const width = claude ? 1024 : 1086,
    height = claude ? 1536 : 1448;
  const lit = document.createElement('canvas');
  lit.width = width;
  lit.height = height;
  function lighting(body) {
    if (!polished) return body;
    const ctx = lit.getContext('2d');
    ctx.clearRect(0, 0, width, height);
    ctx.drawImage(body, 0, 0, width, height);
    ctx.save();
    ctx.globalCompositeOperation = 'source-atop';
    const key = ctx.createLinearGradient(0, 0, width, height * 0.3);
    key.addColorStop(0, claude ? 'rgba(255,214,147,.19)' : 'rgba(139,177,245,.015)');
    key.addColorStop(0.46, 'rgba(255,240,222,0)');
    key.addColorStop(1, claude ? 'rgba(67,96,151,.055)' : 'rgba(173,204,255,.17)');
    ctx.fillStyle = key;
    ctx.fillRect(0, 0, width, height);
    ctx.restore();
    return lit;
  }
  function atmosphere(ctx, phase, foreground) {
    if (!polished) return;
    ctx.save();
    if (!foreground) {
      // Moonlight falls inside the portrait crop; the distant curtains remain in their own regions.
      const x = claude ? 680 : 1180,
        y = claude ? 230 : 95;
      const glow = ctx.createRadialGradient(x, y, 8, x, y, claude ? 590 : 530);
      const pulse = 0.5 + 0.5 * Math.sin(phase * 0.32);
      glow.addColorStop(
        0,
        claude ? 'rgba(255,197,125,.19)' : 'rgba(150,183,255,' + (0.13 + 0.025 * pulse) + ')',
      );
      glow.addColorStop(0.6, claude ? 'rgba(255,191,124,.045)' : 'rgba(110,156,237,.04)');
      glow.addColorStop(1, 'rgba(130,170,240,0)');
      ctx.fillStyle = glow;
      ctx.fillRect(0, 0, 1536, 1024);
      if (!claude) {
        for (const [sx, sy, r] of [
          [635, 318, 2],
          [711, 346, 1.6],
          [1111, 325, 2.2],
          [1065, 405, 1.5],
        ]) {
          ctx.fillStyle = 'rgba(255,222,163,' + (0.14 + 0.12 * Math.sin(phase * 0.9 + sx)) + ')';
          ctx.beginPath();
          ctx.ellipse(sx, sy, r, r * 1.35, 0, 0, Math.PI * 2);
          ctx.fill();
        }
      }
    }
    // A few soft motes pass beside the silhouette, never over the face or invitation.
    const points = claude
      ? [
          [735, 355],
          [1195, 475],
          [778, 690],
          [1200, 715],
        ]
      : [
          [682, 330],
          [1175, 440],
          [698, 650],
          [1165, 705],
        ];
    for (const [i, p] of points.entries()) {
      if ((i % 2 === 1) !== foreground) continue;
      const x = p[0] + 8 * Math.sin(phase * 0.45 + i),
        y = p[1] + 12 * Math.cos(phase * 0.32 + i * 2),
        r = foreground ? 4.8 : 1.6;
      const g = ctx.createRadialGradient(x, y, 0, x, y, r * 2.4);
      const alpha = (foreground ? 0.12 : 0.23) * (0.65 + 0.35 * Math.sin(phase * 0.6 + i));
      g.addColorStop(0, (claude ? 'rgba(255,219,170,' : 'rgba(196,220,255,') + alpha + ')');
      g.addColorStop(1, 'rgba(255,255,255,0)');
      ctx.fillStyle = g;
      ctx.fillRect(x - r * 2.4, y - r * 2.4, r * 4.8, r * 4.8);
    }
    ctx.restore();
  }
  function paint(
    canvas,
    time,
    variant = 'card',
    dark = false,
    layer = 'complete',
    motion = true,
    forcedBlink = null,
  ) {
    const ctx = canvas.getContext('2d');
    ctx.imageSmoothingQuality = 'high';
    const card = variant === 'card',
      cover = variant === 'cover';
    const window = polished && card ? portraitWindows[id] : [0, 0, 1, 1];
    const stageWidth = card ? ((1024 * 2) / 3) * window[2] : 1536,
      stageHeight = card ? 1024 * window[3] : 864;
    const scale = card
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
      x: card ? (claude ? 690 : 570) : cover ? (claude ? 795 : 810) : claude ? 875 : 795,
      y: card ? 64 : cover ? 40 : claude ? 54 : 115,
      s: scale,
    };
    const cropX = card
      ? hero.x + (width * hero.s) / 2 - 1024 / 3 + ((1024 * 2) / 3) * window[0]
      : 0;
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
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.save();
    ctx.scale(canvas.width / stageWidth, canvas.height / stageHeight);
    ctx.translate(-cropX, -(card ? 1024 * window[1] : 0));
    if (layer !== 'hero') {
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
      atmosphere(ctx, phase, false);
    }
    if (layer !== 'background') {
      ctx.save();
      ctx.fillStyle = 'rgba(8,15,26,.32)';
      ctx.shadowColor = 'rgba(8,15,26,.28)';
      ctx.shadowBlur = 6;
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
      ctx.drawImage(lighting(body), hero.x, hero.y, width * scale, height * scale);
    }
    if (layer === 'complete') atmosphere(ctx, phase, true);
    ctx.restore();
    if (variant === 'appearance' && layer === 'complete') {
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
    dispose() {
      character.dispose?.();
      background.dispose?.();
    },
  };
}
/* global Image, document */
