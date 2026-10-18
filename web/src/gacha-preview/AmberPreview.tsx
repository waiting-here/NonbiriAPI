import { useEffect, useRef, useState } from 'react';
import { renderAmber } from '../user/activities/gacha/amber/renderer';

export function AmberPreview() {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [dark, setDark] = useState(false);
  const [landscape, setLandscape] = useState(true);
  const [paused, setPaused] = useState(
    () => matchMedia('(prefers-reduced-motion: reduce)').matches,
  );
  const [blink, setBlink] = useState(false);
  const [error, setError] = useState(false);
  const pause = useRef(paused);
  useEffect(() => {
    pause.current = paused;
  }, [paused]);
  useEffect(() => {
    const surface = canvas.current;
    if (!surface) return;
    const controller = new AbortController();
    let dispose: (() => void) | undefined;
    void renderAmber(surface, {
      polished: true,
      landscape,
      dark,
      blink: blink ? 1 : undefined,
      paused: () => pause.current,
      signal: controller.signal,
    })
      .then((stop) => {
        if (controller.signal.aborted) stop();
        else dispose = stop;
      })
      .catch(() => {
        if (!controller.signal.aborted) setError(true);
      });
    return () => {
      controller.abort();
      dispose?.();
    };
  }, [dark, landscape, blink]);
  return (
    <main className="amber-preview">
      <header>
        <h1>琥珀誓章 · 分层动效</h1>
        <p>桌面装扮独立靠右，旗帜、吊灯与前景分别运动。可切换明暗和横竖构图。</p>
        <nav aria-label="美术预览控制">
          <button aria-pressed={dark} onClick={() => setDark((v) => !v)}>
            {dark ? '切换亮版' : '切换暗版'}
          </button>
          <button aria-pressed={!landscape} onClick={() => setLandscape((v) => !v)}>
            {landscape ? '查看竖版卡面' : '查看横版插画'}
          </button>
          <button aria-pressed={paused} onClick={() => setPaused((v) => !v)}>
            {paused ? '继续播放' : '暂停动作'}
          </button>
          <button aria-pressed={blink} onClick={() => setBlink((v) => !v)}>
            {blink ? '恢复自然眨眼' : '检查闭眼'}
          </button>
          <a href="?view=appearance&appearance=amber">装扮可读性预览</a>
          <a href="?">抽卡预览</a>
        </nav>
      </header>
      {error && <p role="alert">画面加载失败，请刷新重试。</p>}
      <figure className={landscape ? 'amber-preview__wide' : 'amber-preview__portrait'}>
        <canvas ref={canvas} aria-label={dark ? '琥珀夜景' : '琥珀亮景'} />
        <figcaption>{dark ? '星光与烛光' : '琥珀窗光'} · 保留原有角色动作</figcaption>
      </figure>
      <details>
        <summary>展开静态场景</summary>
        <img
          src={'/assets/gacha/appearances/amber-' + (dark ? 'dark' : 'light') + '.webp'}
          alt="琥珀静态场景"
        />
      </details>
    </main>
  );
}
