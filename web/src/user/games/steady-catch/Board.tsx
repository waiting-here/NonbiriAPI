import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { type Phrase } from './engine';
import { CatchSession } from './session';
import { createRenderer, type RenderEvent } from './render.mjs';
import { CatchAudio } from './audio';
import { useCatchText } from './copy';

export interface CatchFeedback {
  text: string;
  points: number;
}
export function CatchBoard({
  session,
  phrases,
  audio,
  onCatch,
}: {
  session: CatchSession | null;
  phrases: readonly Phrase[];
  audio: CatchAudio;
  onCatch: (feedback: CatchFeedback) => void;
}) {
  const t = useCatchText();
  const { i18n } = useTranslation();
  const english = !i18n.resolvedLanguage?.startsWith('zh');
  const canvas = useRef<HTMLCanvasElement>(null),
    toast = useRef<HTMLDivElement>(null),
    live = useRef<HTMLDivElement>(null),
    keys = useRef(new Set<string>());
  useEffect(() => {
    const node = canvas.current!,
      renderer = createRenderer(node, phrases, english);
    const observer = new ResizeObserver(renderer.resize);
    observer.observe(node);
    renderer.resize();
    const text = (zh: string, en: string) => (english ? en : zh);
    const held = keys.current;
    const direction = () =>
      session?.move(
        Number(held.has('arrowright') || held.has('d')) -
          Number(held.has('arrowleft') || held.has('a')),
      );
    const clear = () => {
      held.clear();
      session?.move(0);
    };
    const off = session?.subscribe(() => {
      if (!session.active) clear();
    });
    const keydown = (e: KeyboardEvent) => {
      if (
        !session ||
        e.target instanceof HTMLInputElement ||
        e.target instanceof HTMLSelectElement ||
        e.target instanceof HTMLTextAreaElement ||
        (e.target instanceof HTMLElement && e.target.isContentEditable) ||
        node.closest('.catch-game')?.querySelector('dialog[open]')
      )
        return;
      const key = e.key.toLowerCase();
      if (['arrowleft', 'arrowright', 'a', 'd'].includes(key) && session.active) {
        e.preventDefault();
        held.add(key);
        direction();
      }
      if ((key === 'p' || key === 'escape') && !e.repeat && !session.terminal) {
        e.preventDefault();
        clear();
        void (session.active ? session.pause() : session.resume());
      }
      if (key === ' ' && session.active && !e.repeat) {
        e.preventDefault();
        session.shield();
      }
    };
    const keyup = (e: KeyboardEvent) => {
      if (held.delete(e.key.toLowerCase())) direction();
    };
    node.addEventListener('pointerdown', clear);
    window.addEventListener('keydown', keydown);
    window.addEventListener('keyup', keyup);
    window.addEventListener('blur', clear);
    let previous = session?.state,
      wasActive = false,
      frame = 0,
      lastTime = 0,
      toastUntil = 0,
      activeTime = 0;
    const announce = (message: string, danger = false, duration = 1.7) => {
      toast.current!.textContent = message;
      toast.current!.className = 'toast visible' + (danger ? ' danger-toast' : '');
      toastUntil = activeTime + duration;
    };
    function paint(now: number) {
      frame = requestAnimationFrame(paint);
      const dt = lastTime ? Math.min(0.045, (now - lastTime) / 1000) : 0.016;
      lastTime = now;
      session?.frame();
      const state = session?.state;
      if (session?.active) activeTime += dt;
      const events: RenderEvent[] = [];
      if (session?.active && !wasActive && state?.tick === 0) {
        announce(
          text('开场很温柔。先稳稳接几句。', 'A gentle opening. Catch a few phrases first.'),
          false,
          2.2,
        );
        live.current!.textContent = text(
          '游戏开始。接住八股卡，避开红色错误卡。',
          'Game started. Catch phrases and avoid red error cards.',
        );
      }
      wasActive = !!session?.active;
      if (state && previous && state.tick > previous.tick) {
        const hazards = text(
          '429 限流|自信的幻觉|上下文丢失|502 离线|复读死循环|JSON 崩了',
          '429 Rate limit|Confident hallucination|Context lost|502 Offline|Repetition loop|Broken JSON',
        ).split('|');
        for (const collection of session?.takeCollections() ?? []) {
          const { kind, payload, points, combo } = collection;
          if (kind === 'phrase') {
            onCatch({ text: phrases[payload].text, points });
            events.push({ kind: 'catch', text: '+' + points });
            if (combo === 5 || combo === 10)
              announce(
                text('连击 ', 'Combo ') +
                  combo +
                  text(' · 接物倍率 ×', ' · Multiplier ×') +
                  (1 + Math.min(2, Math.floor(combo / 5))),
              );
            if (collection.chargeReady)
              announce(
                text(
                  '护场已充满！需要时点按或按空格',
                  'Shield ready! Tap or press Space when needed.',
                ),
              );
          } else if (kind === 'hazard') {
            events.push(
              collection.blocked
                ? { kind: 'prop', text: text('已挡住', 'Blocked') }
                : { kind: 'hit', text: '−1 ♥' },
            );
            if (!collection.blocked)
              announce(hazards[payload] + ' · ' + text('耐心 −1', 'Health −1'), true);
          } else {
            events.push({ kind: 'prop' });
            if (payload === 4)
              announce(
                collection.hpDelta === 0
                  ? text('耐心已满 · 这次无需重新生成', 'Health full · No regeneration needed')
                  : text('重新生成 · 耐心 +1', 'Regenerate · Health +1'),
              );
            else
              announce(
                [
                  text('上下文护盾 · 护盾 7 秒', 'Context shield · Shield for 7 seconds'),
                  text('低温采样 · 降速 7 秒', 'Low temperature · Slow for 7 seconds'),
                  text('注意力磁铁 · 磁吸 7 秒', 'Attention magnet · Magnet for 7 seconds'),
                  text('Token 翻倍 · 双倍 7 秒', 'Double tokens · Double score for 7 seconds'),
                ][payload],
              );
          }
          audio.beep(
            kind === 'phrase' ? 'catch' : kind === 'hazard' && !collection.blocked ? 'hit' : 'prop',
            combo,
          );
        }
        if (
          previous.charge === 10 &&
          state.charge < 10 &&
          state.effects.shield > previous.effects.shield
        ) {
          events.push({ kind: 'prop' });
          audio.beep('prop', state.combo);
          announce(text('稳稳护场 · 4 秒内放心接', 'Steady shield · Catch freely for 4 seconds'));
        }
        if (Math.floor(state.tick / 1800) > Math.floor(previous.tick / 1800) && !state.cause)
          announce(
            state.tick >= 3600
              ? text('最后 30 秒 · 极其极其极其！', 'Last 30 seconds · Extremely extreme!')
              : text('第二幕 · 八股浓度开始升高', 'Act II · More phrases incoming'),
            false,
            2.5,
          );

        if (state.cause && !previous.cause)
          live.current!.textContent =
            text('本局结束。分数 ', 'Game over. Score ') +
            state.score +
            text('，接住 ', ', caught ') +
            state.caught;
      }
      previous = state;
      if (activeTime > toastUntil) toast.current!.classList.remove('visible');
      if (events.length)
        events.forEach((event, index) =>
          renderer.paint(state ?? null, !!session?.active, index === 0 ? dt : 0, event),
        );
      else renderer.paint(state ?? null, !!session?.active, dt);
    }
    frame = requestAnimationFrame(paint);
    return () => {
      clear();
      cancelAnimationFrame(frame);
      observer.disconnect();
      renderer.destroy();
      off?.();
      node.removeEventListener('pointerdown', clear);
      window.removeEventListener('keydown', keydown);
      window.removeEventListener('keyup', keyup);
      window.removeEventListener('blur', clear);
    };
  }, [session, phrases, english, audio, onCatch]);
  return (
    <>
      <canvas
        ref={canvas}
        tabIndex={0}
        aria-label={t(
          '游戏区域。左右箭头或 A D 移动；鼠标在场内移动，手机按住滑动。空格释放护场，P 暂停。',
          'Game field. Move with arrows or A/D, mouse, or touch. Space for shield; P to pause.',
        )}
        onPointerDown={(e) => {
          if (!session?.active) return;
          e.currentTarget.focus({ preventScroll: true });
          e.currentTarget.setPointerCapture(e.pointerId);
          const r = e.currentTarget.getBoundingClientRect();
          session.aim(((e.clientX - r.left) / r.width) * 600000);
        }}
        onPointerMove={(e) => {
          if (
            !session?.active ||
            keys.current.size > 0 ||
            (e.pointerType !== 'mouse' && !e.currentTarget.hasPointerCapture(e.pointerId))
          )
            return;
          const r = e.currentTarget.getBoundingClientRect();
          session.aim(((e.clientX - r.left) / r.width) * 600000);
        }}
      />
      <div ref={toast} className="toast" aria-live="polite" />
      <div ref={live} className="sr-only" role="status" />
    </>
  );
}
