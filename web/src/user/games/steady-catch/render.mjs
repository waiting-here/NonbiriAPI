/* global getComputedStyle, matchMedia, Image, window, Path2D */
import { GAME_ICON_PATHS, ICON_STROKE_WIDTH } from '@shared/components/iconPaths';
export function createRenderer(canvas, phrases, english) {
  const ctx = canvas.getContext('2d'),
    font = getComputedStyle(canvas).fontFamily;
  const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const icons = Object.fromEntries(
    Object.entries(GAME_ICON_PATHS).map(([name, path]) => [name, new Path2D(path)]),
  );
  const sprite = new Image();
  let assetReady = false,
    tile = 640,
    w = 600,
    h = 560,
    dpr = 1,
    sceneTime = 0;
  const catalog = { phrases: phrases.map((p) => ({ ...p, rarity: p.gold ? 'gold' : 'normal' })) };
  let state = {
    mode: 'ready',
    x: 432,
    time: 0,
    direction: 0,
    action: 'idle',
    actionUntil: 0,
    invulnerable: 0,
    effects: { shield: 0, slow: 0, magnet: 0, double: 0 },
    items: [],
    particles: [],
    floaters: [],
  };
  const hazards = english
    ? [
        '429 Rate limit',
        'Confident hallucination',
        'Context lost',
        '502 Offline',
        'Repetition loop',
        'Broken JSON',
      ]
    : ['429 限流', '自信的幻觉', '上下文丢失', '502 离线', '复读死循环', 'JSON 崩了'];
  const props = english
    ? ['Context shield', 'Low temperature', 'Attention magnet', 'Double tokens', 'Regenerate']
    : ['上下文护盾', '低温采样', '注意力磁铁', 'Token 翻倍', '重新生成'];
  function catchY() {
    return h - (h < 330 ? 74 : w < 500 ? 100 : 114);
  }
  function padHalf() {
    return state.mode === 'ready' ? (w < 500 ? 37 : 43) : (43000 / 600000) * w;
  }
  function spriteSize() {
    return h < 330 ? 150 : w < 500 ? 208 : 238;
  }
  function resize() {
    const r = canvas.getBoundingClientRect();
    w = Math.max(1, r.width);
    h = Math.max(1, r.height);
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
  }
  sprite.onload = () => {
    assetReady = true;
    tile = sprite.naturalWidth / 2;
  };
  sprite.src = '/assets/steady-catch/player.webp';
  function roundRect(x, y, ww, hh, r) {
    ctx.beginPath();
    ctx.roundRect(x, y, ww, hh, r);
  }
  function textFit(text, maxWidth, size = 16) {
    ctx.font = '650 ' + size + 'px ' + font;
    while (size > 13 && ctx.measureText(text).width > maxWidth) {
      size--;
      ctx.font = '650 ' + size + 'px ' + font;
    }
    return size;
  }
  function drawIcon(name, x, y, size) {
    ctx.save();
    ctx.translate(x, y);
    ctx.scale(size / 24, size / 24);
    ctx.lineWidth = ICON_STROKE_WIDTH;
    ctx.lineCap = ctx.lineJoin = 'round';
    ctx.stroke(icons[name]);
    ctx.restore();
  }
  function drawCard(item) {
    const { kind, payload, x, y, width, height } = item,
      gold = kind === 'phrase' && payload.rarity === 'gold';
    const colors =
      kind === 'hazard'
        ? ['#ffe0e3', '#a93851', '#fc879c']
        : kind === 'prop'
          ? ['#c6f8dc', '#246950', '#73d3a3']
          : gold
            ? ['#ffe5ac', '#755321', '#f7c16a']
            : ['#e9f4f7', '#254a60', '#b4d4df'];
    ctx.save();
    if (kind === 'phrase' && item.checked) ctx.filter = 'grayscale(40%)';
    ctx.translate(x, y);
    ctx.rotate(item.tilt + (reducedMotion ? 0 : Math.sin(sceneTime * 1.2 + item.wobble) * 0.012));
    ctx.shadowColor = '#0005';
    ctx.shadowBlur = 10;
    ctx.shadowOffsetY = 4;
    if (kind === 'hazard') {
      ctx.beginPath();
      ctx.moveTo(-width / 2 + 8, -height / 2);
      ctx.lineTo(width / 2 - 8, -height / 2);
      ctx.lineTo(width / 2, 0);
      ctx.lineTo(width / 2 - 8, height / 2);
      ctx.lineTo(-width / 2 + 8, height / 2);
      ctx.lineTo(-width / 2, 0);
      ctx.closePath();
    } else roundRect(-width / 2, -height / 2, width, height, 10);
    ctx.fillStyle = colors[0];
    ctx.fill();
    ctx.shadowBlur = 0;
    ctx.shadowOffsetY = 0;
    ctx.lineWidth = kind === 'hazard' ? 2 : 1.5;
    ctx.strokeStyle = colors[2];
    if (kind === 'hazard' && !reducedMotion)
      ctx.globalAlpha = 0.8 + Math.sin((sceneTime * Math.PI) / 0.6) * 0.2;
    ctx.stroke();
    ctx.globalAlpha = 1;
    ctx.fillStyle = colors[1];
    ctx.textBaseline = 'middle';
    ctx.textAlign = 'left';
    ctx.font = '700 13px ' + font;
    const tag =
      kind === 'phrase'
        ? gold
          ? english
            ? 'CN meme'
            : '名场面'
          : english
            ? 'CN meme'
            : '八股'
        : kind === 'hazard'
          ? english
            ? 'Avoid'
            : '躲开'
          : english
            ? 'Power'
            : '道具';
    ctx.strokeStyle = colors[1];
    const icon = kind === 'hazard' ? 'warning' : kind === 'prop' ? 'shield' : 'spark';
    const labelWidth = ctx.measureText(tag).width;
    const withIcon = labelWidth + 38 <= width;
    if (withIcon || kind === 'hazard') drawIcon(icon, -width / 2 + 8, -height / 2 + 6, 15);
    ctx.fillText(tag, -width / 2 + (withIcon || kind === 'hazard' ? 28 : 8), -height / 2 + 14);
    ctx.textAlign = 'center';
    textFit(payload.text, width - 20, w < 500 ? 16 : 17);
    ctx.fillText(payload.text, 0, 10);
    ctx.restore();
  }
  function drawBackground() {
    const bg = ctx.createLinearGradient(0, 0, 0, h);
    const act = Math.min(2, Math.floor(state.time / 30));
    const colors = [
      ['#112a40', '#0d2235', '#15363c'],
      ['#1a2440', '#241f46', '#2a2050'],
      ['#2a1a2a', '#341d28', '#3a1e24'],
    ][act];
    colors.forEach((color, index) => bg.addColorStop([0, 0.65, 1][index], color));
    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, w, h);
    const glow = ctx.createRadialGradient(w * 0.72, h * 0.62, 0, w * 0.72, h * 0.62, w * 0.65);
    glow.addColorStop(0, '#297c7016');
    glow.addColorStop(1, '#14334600');
    ctx.fillStyle = glow;
    ctx.fillRect(0, 0, w, h);
    ctx.fillStyle = '#b5dcea18';
    for (let i = 0; i < 40; i++) {
      const x = (((i * 137 + 32) % 997) / 997) * w,
        y = (((i * 73 + 16) % 631) / 631) * (h - 40);
      ctx.beginPath();
      ctx.globalAlpha =
        act === 2 && !reducedMotion ? 0.5 + (Math.sin(sceneTime * 5 + i) + 1) / 4 : 1;
      ctx.arc(x, y, i % 7 === 0 ? 1.6 : 0.8, 0, Math.PI * 2);
      ctx.fill();
      if (act === 1) {
        const rainY = (y + (reducedMotion ? 0 : sceneTime * 45)) % h;
        const length = 14 + state.time / 3;
        ctx.strokeStyle = '#b8aad527';
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(x, rainY);
        ctx.lineTo(x - length / 4, rainY + length);
        ctx.stroke();
      }
    }
    ctx.globalAlpha = 1;
    ctx.strokeStyle = '#6fa9b225';
    ctx.lineWidth = 1;
    ctx.setLineDash([3, 9]);
    ctx.beginPath();
    ctx.moveTo(0, catchY());
    ctx.lineTo(w, catchY());
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = '#a5cbd069';
    ctx.font = '12px ' + font;
    ctx.textAlign = 'right';
    ctx.fillText(english ? 'Catch line' : '接物线', w - 13, catchY() + 18);
    ctx.strokeStyle = '#51898550';
    ctx.beginPath();
    ctx.moveTo(0, h - 17);
    ctx.lineTo(w, h - 17);
    ctx.stroke();
  }
  function drawPlayer() {
    if (!assetReady) return;
    const size = spriteSize(),
      x = state.x,
      cy = catchY();
    let frame = 0;
    if (state.mode === 'playing' && state.actionUntil > state.time)
      frame = state.action === 'hit' ? 3 : 2;
    else if (state.mode === 'playing' && state.direction) frame = Math.floor(sceneTime * 9) % 2;
    const bob = reducedMotion
      ? 0
      : state.mode === 'playing' && state.direction
        ? Math.sin(sceneTime * 17) * 1.4
        : Math.sin(sceneTime * 2) * 1;
    const sy = cy - size * 0.51;
    ctx.save();
    ctx.fillStyle = '#030c1970';
    ctx.beginPath();
    ctx.ellipse(x, h - 18, size * 0.22, 7, 0, 0, Math.PI * 2);
    ctx.fill();
    if (state.combo >= 10) {
      ctx.strokeStyle = '#ffd87c8c';
      ctx.lineWidth = 2;
      ctx.shadowColor = '#ffd87c';
      ctx.shadowBlur = reducedMotion ? 0 : 14;
      ctx.beginPath();
      ctx.ellipse(x, sy + size * 0.5, size * 0.39, size * 0.43, 0, 0, Math.PI * 2);
      ctx.stroke();
      ctx.shadowBlur = 0;
    }
    if (state.effects.shield > state.time) {
      ctx.fillStyle = '#91efdb0c';
      ctx.strokeStyle = '#b5f9deae';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.ellipse(x, sy + size * 0.5, size * 0.42, size * 0.42, 0, 0, Math.PI * 2);
      ctx.fill();
      ctx.stroke();
    }
    if (state.effects.magnet > state.time) {
      ctx.strokeStyle = '#b7e9db55';
      ctx.setLineDash([4, 9]);
      ctx.beginPath();
      ctx.ellipse(x, cy, 142, 30, 0, Math.PI, Math.PI * 2);
      ctx.stroke();
      ctx.setLineDash([]);
    }
    if (state.invulnerable > state.time && Math.floor(sceneTime * 10) % 2 && !reducedMotion)
      ctx.globalAlpha = 0.6;
    ctx.drawImage(
      sprite,
      (frame % 2) * tile,
      Math.floor(frame / 2) * tile,
      tile,
      tile,
      x - size / 2,
      sy + bob,
      size,
      size,
    );
    ctx.globalAlpha = 1;
    // This thin functional halo shows the exact, forgiving horizontal catch area.
    ctx.strokeStyle = state.effects.shield > state.time ? '#e1ffdb' : '#9cefce';
    ctx.shadowColor = '#9cefce';
    ctx.shadowBlur = 8;
    ctx.lineWidth = 3;
    ctx.beginPath();
    ctx.ellipse(x, cy, padHalf(), 5, 0, Math.PI * 0.08, Math.PI * 0.92);
    ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.restore();
  }
  function drawDemo() {
    const examples = [catalog.phrases[18], catalog.phrases[0], catalog.phrases[54]];
    const positions =
      w < 500
        ? [
            [0.77, 75],
            [0.71, 185],
            [0.4, 260],
          ]
        : [
            [0.75, 95],
            [0.66, 204],
            [0.83, 318],
          ];
    examples.forEach((p, i) => {
      const ww = Math.min(200, w * 0.62),
        it = {
          kind: 'phrase',
          payload: p,
          x: w * positions[i][0],
          y: positions[i][1],
          width: ww,
          height: 61,
          tilt: (i - 1) * 0.07,
          wobble: i,
        };
      drawCard(it, true);
    });
  }
  function render() {
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    drawBackground();
    if (state.mode === 'ready') drawDemo();
    for (const item of state.items) {
      if (item.landing) {
        ctx.fillStyle = '#ff899433';
        ctx.beginPath();
        ctx.ellipse(item.x, catchY(), item.width / 2, 6, 0, 0, Math.PI * 2);
        ctx.fill();
      }
      drawCard(item);
    }
    drawPlayer();
    ctx.save();
    for (const p of state.particles) {
      ctx.globalAlpha = Math.max(0, p.life / p.total);
      ctx.fillStyle = p.color;
      ctx.fillRect(p.x - p.size / 2, p.y - p.size / 2, p.size, p.size);
    }
    ctx.globalAlpha = 1;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.font = '800 21px ' + font;
    for (const p of state.floaters) {
      ctx.globalAlpha = Math.min(1, p.life * 2);
      ctx.fillStyle = p.color;
      ctx.fillText(p.text, p.x, p.y);
    }
    ctx.restore();
  }

  function paint(value, active, dt, event) {
    if (active || !value) sceneTime += dt;
    const previous = state;
    state = {
      ...previous,
      mode: value ? (active ? 'playing' : 'paused') : 'ready',
      x: value ? (value.x / 600000) * w : w * 0.72,
      time: value ? value.tick / 60 : 0,
      combo: value?.combo ?? 0,
      direction: value && active ? Math.sign((value.x / 600000) * w - previous.x) : 0,
      invulnerable: (value?.invulnerable ?? 0) / 60,
      effects: Object.fromEntries(
        Object.entries(value?.effects ?? previous.effects).map(([k, v]) => [k, value ? v / 60 : 0]),
      ),
      items: (value?.items ?? []).map((i) => ({
        ...i,
        landing:
          value.tick >= 3600 &&
          i.kind === 'hazard' &&
          !i.checked &&
          (446000 - i.y - i.height / 2) /
            (i.speed * (value.effects.slow > value.tick ? 0.58 : 1)) <=
            24,
        payload:
          i.kind === 'phrase'
            ? catalog.phrases[i.payload]
            : { text: (i.kind === 'hazard' ? hazards : props)[i.payload] },
        x: (i.x / 600000) * w,
        y: (i.y / 446000) * catchY(),
        width: (i.width / 600000) * w,
        height: (i.height / 446000) * catchY(),
        tilt: ((i.id % 7) - 3) * 0.012,
        wobble: i.id,
      })),
    };
    if (event) {
      if (event.kind !== 'miss') {
        state.action = event.kind === 'hit' ? 'hit' : 'catch';
        state.actionUntil = state.time + 0.45;
      }
      const color =
        event.kind === 'miss' ? '#b8cadb' : event.kind === 'hit' ? '#ff9eac' : '#b7f4da';
      if (!reducedMotion && event.kind !== 'miss')
        for (let i = 0; i < 12; i++) {
          const a = Math.random() * Math.PI * 2,
            v = 40 + Math.random() * 100;
          state.particles.push({
            x: state.x,
            y: catchY(),
            vx: Math.cos(a) * v,
            vy: Math.sin(a) * v - 25,
            life: 0.55 + Math.random() * 0.3,
            total: 0.85,
            color,
            size: 2 + Math.random() * 3,
          });
        }
      if (event.text)
        state.floaters.push({
          x: event.x === undefined ? state.x : (event.x / 600000) * w,
          y: catchY() - 25,
          life: event.kind === 'miss' ? 0.6 : 1,
          text: event.text,
          color,
        });
    }
    if (active) {
      for (const p of state.particles) {
        p.life -= dt;
        p.x += p.vx * dt;
        p.y += p.vy * dt;
        p.vy += 130 * dt;
      }
      for (const p of state.floaters) {
        p.life -= dt;
        p.y -= 37 * dt;
      }
    }
    state.particles = state.particles.filter((p) => p.life > 0).slice(-150);
    state.floaters = state.floaters.filter((p) => p.life > 0);
    render();
  }
  return {
    resize,
    paint,
    destroy() {
      sprite.onload = null;
    },
  };
}
