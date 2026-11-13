import { useEffect, useRef, useState } from 'react';
import { PaintCadence } from '@shared/animation/illustration';
import { Button } from '@shared/components/ui/Button';
import { Field } from '@shared/components/ui/Field';
import { Toggle } from '@shared/components/ui/Toggle';
import { createClothStudy, studyLayers } from './cloth-study-scene';
import type { StudyBackdrop, StudyLayer } from './cloth-study-scene';
import compositeUrl from './assets/claude-chiffon/composite.webp';

export function ClaudeLayerStudy() {
  const [mode, setMode] = useState<StudyLayer>('composite');
  const [backdrop, setBackdrop] = useState<StudyBackdrop>('room');
  const [paused, setPaused] = useState(false);
  const [still, setStill] = useState(false);
  const [inspect, setInspect] = useState<number>();
  const [failed, setFailed] = useState(false);
  const surface = useRef<HTMLCanvasElement>(null);
  const settings = useRef({ mode, backdrop, paused, still, inspect });
  const dirty = useRef(true);
  useEffect(() => {
    settings.current = { mode, backdrop, paused, still, inspect };
    dirty.current = true;
  }, [mode, backdrop, paused, still, inspect]);
  useEffect(() => {
    const canvas = surface.current;
    if (!canvas) return;
    const controller = new AbortController();
    let stop = () => {};
    void createClothStudy(canvas, controller.signal)
      .then((scene) => {
        if (controller.signal.aborted) {
          scene.dispose();
          return;
        }
        const cadence = new PaintCadence();
        const preference = matchMedia('(prefers-reduced-motion: reduce)');
        let frame = 0,
          elapsed = 0,
          previous: number | undefined,
          visible = true;
        const measure = () => {
          scene.resize();
          dirty.current = true;
        };
        const observer = new ResizeObserver(measure);
        const intersection = new IntersectionObserver(([entry]) => {
          visible = entry.isIntersecting;
          previous = undefined;
        });
        const reset = () => {
          previous = undefined;
          dirty.current = true;
        };
        const tick = (now: number) => {
          const current = settings.current;
          const playing =
            !current.paused &&
            !current.still &&
            current.inspect === undefined &&
            !preference.matches;
          if (visible && !document.hidden) {
            if (playing)
              elapsed += previous === undefined ? 0 : Math.min((now - previous) / 1000, 0.05);
            if (dirty.current || (playing && cadence.due(now))) {
              const time = current.inspect ?? elapsed;
              scene.paint(
                time,
                current.mode,
                current.backdrop,
                !current.still && !preference.matches,
              );
              canvas.dataset.ready = 'true';
              canvas.dataset.time = String(time);
              dirty.current = false;
            }
          }
          previous = now;
          frame = requestAnimationFrame(tick);
        };
        measure();
        observer.observe(canvas);
        intersection.observe(canvas);
        document.addEventListener('visibilitychange', reset);
        preference.addEventListener('change', reset);
        frame = requestAnimationFrame(tick);
        stop = () => {
          cancelAnimationFrame(frame);
          observer.disconnect();
          intersection.disconnect();
          document.removeEventListener('visibilitychange', reset);
          preference.removeEventListener('change', reset);
          scene.dispose();
        };
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => {
      controller.abort();
      stop();
    };
  }, []);
  return (
    <section
      id="claude-study"
      className="seasonal-review__study"
      aria-labelledby="claude-study-title"
    >
      <h2 id="claude-study-title">Claude · 新旧方案对照</h2>
      <p>
        现有丝绒礼裙继续保留。新方案是半透明长裙候选：人物底层穿不透明内衣，裙纱单独绘制。本轮先固定人物，只检验衣料独立摆动，尚未替换正式卡面。
      </p>
      <div className="seasonal-review__study-controls">
        <Field label="候选展示">
          {(props) => (
            <select
              {...props}
              className="nb-select"
              value={mode}
              onChange={(event) => setMode(event.target.value as StudyLayer)}
            >
              <option value="composite">完整新方案</option>
              <option value="body">人物底层</option>
              <option value="dress">独立裙装</option>
              <option value="front">手、书本与前景发丝</option>
            </select>
          )}
        </Field>
        <Field label="候选底色">
          {(props) => (
            <select
              {...props}
              className="nb-select"
              value={backdrop}
              onChange={(event) => setBackdrop(event.target.value as StudyBackdrop)}
            >
              <option value="room">圣诞会客厅</option>
              <option value="checker">棋盘格</option>
              <option value="light">浅色</option>
              <option value="dark">深色</option>
            </select>
          )}
        </Field>
        <Toggle label="候选静态对照" checked={still} onChange={setStill} />
        <div className="nb-actions">
          <Button onClick={() => setPaused((value) => !value)}>
            {paused ? '继续衣料动效' : '暂停衣料动效'}
          </Button>
          <Button
            onClick={() => {
              setInspect(undefined);
              setPaused(false);
              setStill(false);
            }}
          >
            自然摆动
          </Button>
        </div>
        <Field
          label="衣料循环时刻"
          help={
            inspect === undefined
              ? '9.6 秒一个缓慢回环；领口、肩部与腰线固定。'
              : `${inspect.toFixed(2)} 秒`
          }
        >
          {(props) => (
            <input
              {...props}
              type="range"
              min="0"
              max="9.6"
              step="0.05"
              value={inspect ?? 0}
              onChange={(event) => {
                setInspect(Number(event.target.value));
                setStill(false);
              }}
            />
          )}
        </Field>
      </div>
      <div className="seasonal-review__study-pair">
        <figure>
          <img
            src="/assets/gacha/cards/t-claude.webp"
            alt="Claude 现有丝绒礼裙"
            width="1024"
            height="1536"
            loading="lazy"
          />
          <figcaption>现有方案 · 丝绒礼裙</figcaption>
        </figure>
        <figure>
          <div className="seasonal-review__study-stage" data-backdrop={backdrop}>
            {failed ? (
              <img src={compositeUrl} alt="Claude 半透明长裙静态候选" />
            ) : (
              <canvas
                ref={surface}
                width="1024"
                height="1536"
                aria-label="Claude 半透明长裙候选"
                role="img"
              />
            )}
          </div>
          <figcaption>新方案 · 半透明长裙{failed && '（动效未加载，显示静态图）'}</figcaption>
        </figure>
      </div>
      <div className="nb-actions seasonal-review__study-links" aria-label="候选分层原图">
        <a href={compositeUrl} target="_blank" rel="noreferrer">
          新方案合成图 ↗
        </a>
        <a href={studyLayers.body} target="_blank" rel="noreferrer">
          人物底层 ↗
        </a>
        <a href={studyLayers.dress} target="_blank" rel="noreferrer">
          半透明裙装 ↗
        </a>
        <a href={studyLayers.front} target="_blank" rel="noreferrer">
          前景遮挡层 ↗
        </a>
      </div>
    </section>
  );
}
