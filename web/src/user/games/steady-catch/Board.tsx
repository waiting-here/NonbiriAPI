import { useEffect, useRef } from 'react';
import { type Phrase } from './engine';
import { CatchSession } from './session';
import { useCatchText } from './copy';

const hazards = ['断章取义', '虚假引用', '提示词注入', '无限复读', '上下文丢失', '幻觉来袭'];
const props = ['护盾', '慢慢来', '吸梗磁铁', '双倍得分', '生命 +1'];
function wrapped(ctx: CanvasRenderingContext2D, text: string, width: number) {
  const lines: string[] = [];
  let line = '';
  for (const char of text) {
    if (line && ctx.measureText(line + char).width > width) {
      lines.push(line);
      line = '';
    }
    line += char;
  }
  if (line) lines.push(line);
  return lines;
}
export function CatchBoard({
  session,
  phrases,
}: {
  session: CatchSession;
  phrases: readonly Phrase[];
}) {
  const t = useCatchText();
  const canvas = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const node = canvas.current!;
    const ctx = node.getContext('2d')!;
    const player = new Image();
    player.src = '/assets/steady-catch/player.webp';
    let frame = 0,
      redraw = true;
    let previous = session.state;
    let lastCaught = previous.caught,
      smileUntil = 0;
    const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;
    let dark = false,
      textSize = 18;
    const resize = () => {
      const bounds = node.getBoundingClientRect();
      const ratio = Math.min(2, devicePixelRatio || 1);
      node.width = Math.max(1, Math.round(bounds.width * ratio));
      node.height = Math.max(1, Math.round(bounds.height * ratio));
      textSize = Math.max(18, Math.min(30, Math.ceil((13 * 600) / bounds.width)));
      redraw = true;
    };
    const theme = () => {
      dark = getComputedStyle(node).getPropertyValue('--catch-dark').trim() === '1';
      redraw = true;
    };
    const sizeObserver = new ResizeObserver(resize);
    sizeObserver.observe(node);
    const themeObserver = new MutationObserver(theme);
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });
    player.onload = () => {
      redraw = true;
    };
    resize();
    theme();
    const paint = () => {
      frame = requestAnimationFrame(paint);
      session.frame();
      const state = session.state;
      if (!redraw && previous === state) return;
      redraw = false;
      previous = state;
      ctx.setTransform(node.width / 600, 0, 0, node.height / 560, 0, 0);
      ctx.clearRect(0, 0, 600, 560);
      ctx.fillStyle = dark ? '#203a35' : '#eaf7f0';
      ctx.fillRect(0, 0, 600, 560);
      ctx.strokeStyle = dark ? '#2a5148' : '#d4e9dd';
      ctx.lineWidth = 1;
      for (let x = 0; x < 600; x += 40) {
        ctx.beginPath();
        ctx.moveTo(x, 0);
        ctx.lineTo(x, 560);
        ctx.stroke();
      }
      for (let y = 0; y < 560; y += 40) {
        ctx.beginPath();
        ctx.moveTo(0, y);
        ctx.lineTo(600, y);
        ctx.stroke();
      }
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      for (const item of state.items) {
        const x = item.x / 1000,
          y = item.y / 1000,
          w = item.width / 1000;
        const gold = item.kind === 'phrase' && phrases[item.payload].gold;
        ctx.fillStyle =
          item.kind === 'hazard'
            ? '#7d293c'
            : item.kind === 'prop'
              ? '#215c83'
              : gold
                ? '#735314'
                : dark
                  ? '#f1f7ee'
                  : '#fff';
        ctx.beginPath();
        ctx.roundRect(x - w / 2, y - 31, w, 62, 11);
        ctx.fill();
        ctx.strokeStyle = item.kind === 'hazard' ? '#ef7e95' : gold ? '#edc451' : '#75b79b';
        ctx.lineWidth = gold ? 3 : 1.5;
        ctx.stroke();
        const text =
          item.kind === 'phrase'
            ? phrases[item.payload].text
            : item.kind === 'hazard'
              ? hazards[item.payload]
              : props[item.payload];
        let font = textSize;
        ctx.font = '600 ' + font + 'px system-ui';
        let lines = wrapped(ctx, text, w - 16);
        while (lines.length > 3 && font > 12) {
          font--;
          ctx.font = '600 ' + font + 'px system-ui';
          lines = wrapped(ctx, text, w - 16);
        }
        ctx.fillStyle = item.kind === 'phrase' && !gold ? '#143c32' : '#fff';
        lines
          .slice(0, 3)
          .forEach((line, index) =>
            ctx.fillText(line, x, y + (index - (Math.min(3, lines.length) - 1) / 2) * (font + 1)),
          );
      }
      const x = state.x / 1000;
      if (state.effects.shield > state.tick) {
        ctx.strokeStyle = '#72d9ed';
        ctx.lineWidth = 4;
        ctx.beginPath();
        ctx.ellipse(x, 488, 55, 65, 0, 0, Math.PI * 2);
        ctx.stroke();
      }
      ctx.fillStyle = dark ? '#84e2b4' : '#197a59';
      ctx.beginPath();
      ctx.roundRect(x - 43, 442, 86, 8, 4);
      ctx.fill();
      ctx.globalAlpha =
        state.invulnerable > state.tick && !reducedMotion && state.tick % 12 < 6 ? 0.5 : 1;
      if (player.complete && player.naturalWidth) {
        if (state.caught > lastCaught) smileUntil = state.tick + 18;
        lastCaught = state.caught;
        const pose =
          state.invulnerable > state.tick
            ? 3
            : smileUntil > state.tick
              ? 2
              : state.direction && !reducedMotion
                ? Math.floor(state.tick / 7) % 2
                : 0;
        const tile = player.naturalWidth / 2;
        ctx.drawImage(
          player,
          (pose % 2) * tile,
          Math.floor(pose / 2) * tile,
          tile,
          tile,
          x - 98,
          346,
          196,
          196,
        );
      } else {
        ctx.fillStyle = '#54ae87';
        ctx.beginPath();
        ctx.arc(x, 490, 30, 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.globalAlpha = 1;
    };
    frame = requestAnimationFrame(paint);
    return () => {
      cancelAnimationFrame(frame);
      sizeObserver.disconnect();
      themeObserver.disconnect();
      player.onload = null;
    };
  }, [session, phrases]);
  return (
    <canvas
      ref={canvas}
      className="catch-canvas"
      tabIndex={0}
      aria-label={t(
        '接物场地。左右方向键或 A、D 移动，空格释放护盾。也可拖动或点击场地。',
        'Catch field. Move with Left/Right or A/D, use Space for shield, or drag and tap.',
      )}
      onPointerDown={(event) => {
        event.currentTarget.focus({ preventScroll: true });
        event.currentTarget.setPointerCapture(event.pointerId);
        const box = event.currentTarget.getBoundingClientRect();
        session.aim(((event.clientX - box.left) / box.width) * 600000);
      }}
      onPointerMove={(event) => {
        if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
        const box = event.currentTarget.getBoundingClientRect();
        session.aim(((event.clientX - box.left) / box.width) * 600000);
      }}
      onKeyDown={(event) => {
        const key = event.key.toLowerCase();
        if (['arrowleft', 'arrowright', 'a', 'd', ' '].includes(key)) {
          event.preventDefault();
          if (key === ' ') session.shield();
          else session.move(key === 'a' || key === 'arrowleft' ? -1 : 1);
        }
      }}
      onKeyUp={(event) => {
        if (['arrowleft', 'arrowright', 'a', 'd'].includes(event.key.toLowerCase()))
          session.move(0);
      }}
      onBlur={() => session.move(0)}
    />
  );
}
