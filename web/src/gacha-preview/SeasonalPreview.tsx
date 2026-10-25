import { useEffect, useRef, useState } from 'react';
import { ArtworkImage } from '@shared/artwork/ArtworkImage';
import { useArtworkPolicy } from '@shared/artwork/context';
import { Button } from '@shared/components/ui/Button';
import { Field } from '@shared/components/ui/Field';
import { Toggle } from '@shared/components/ui/Toggle';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { cardById } from '../user/activities/gacha/catalog';
import { CardTile } from '../user/activities/gacha/CardTile';
import { highResolutionArt } from '../user/activities/gacha/art-resolution';
import { renderSSR } from '../user/activities/gacha/ssr-renderer';
import '../user/activities/gacha/gacha.css';
import './seasonal-preview.css';

function MotionCard({
  id,
  time,
  paused,
  high,
}: {
  id: string;
  time?: number;
  paused: boolean;
  high: boolean;
}) {
  const card = cardById[id];
  const policy = useArtworkPolicy();
  const allowed = policy.allowed('card:' + id);
  const surface = useRef<HTMLCanvasElement>(null);
  const pause = useRef(paused);
  const [ready, setReady] = useState(false);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    pause.current = paused;
  }, [paused]);
  useEffect(() => {
    if (!allowed || !surface.current) return;
    const controller = new AbortController();
    let cleanup: (() => void) | undefined;
    void renderSSR(surface.current, id, time, () => pause.current, high, {
      signal: controller.signal,
    })
      .then((stop) => {
        if (controller.signal.aborted) stop();
        else {
          cleanup = stop;
          setReady(true);
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => {
      controller.abort();
      cleanup?.();
      setReady(false);
      setFailed(false);
    };
  }, [allowed, id, time, high]);
  return (
    <figure className="seasonal-review__card" data-card={id}>
      <div className="seasonal-review__art">
        <ArtworkImage
          artworkKey={'card:' + id}
          src={high ? highResolutionArt(card.art) : card.art}
          alt={card.name}
        />
        {allowed && <canvas ref={surface} className={ready ? 'is-ready' : ''} aria-hidden="true" />}
      </div>
      <figcaption>
        {card.name}
        {failed && ' · 动效未加载，当前显示静态图'}
      </figcaption>
    </figure>
  );
}

export function SeasonalPreview({
  adult,
  setAdult,
}: {
  adult: boolean;
  setAdult: (next: boolean) => void;
}) {
  const [time, setTime] = useState<number>();
  const [paused, setPaused] = useState(false);
  const [high, setHigh] = useState(false);
  return (
    <main className="seasonal-review gacha-app">
      <header className="seasonal-review__header">
        <div>
          <h1>糖夜迷藏</h1>
          <p>两张单卡动效、主题插画与独立封面 · 本地美术验收</p>
        </div>
        <ThemeToggle />
      </header>
      <section className="seasonal-review__controls" aria-label="预览控制">
        <Toggle
          label="显示 R-18 素材"
          description="仅影响当前本地预览。"
          checked={adult}
          onChange={setAdult}
        />
        <Toggle label="高清资源" checked={high} onChange={setHigh} />
        <div className="nb-actions">
          <Button
            onClick={() => {
              setTime(undefined);
              setPaused(false);
            }}
          >
            自然播放
          </Button>
          <Button onClick={() => setPaused((value) => !value)}>
            {paused ? '继续播放' : '暂停动作'}
          </Button>
          <Button onClick={() => setTime(0)}>睁眼</Button>
          <Button onClick={() => setTime(2.265)}>半闭</Button>
          <Button onClick={() => setTime(2.35)}>闭眼</Button>
          <Button onClick={() => setTime(2.48)}>重新睁开</Button>
        </div>
        <Field
          label="检查循环时刻"
          help={time === undefined ? '正在按正常速度播放。' : `${time.toFixed(3)} 秒`}
        >
          {(props) => (
            <input
              {...props}
              type="range"
              min="0"
              max="17.6"
              step="0.005"
              value={time ?? 0}
              onChange={(event) => setTime(Number(event.target.value))}
            />
          )}
        </Field>
        <div className="nb-actions">
          <a className="nb-btn nb-btn--secondary" href={'?r18=' + Number(adult)}>
            查看抽卡、图鉴与心动时刻
          </a>
          <a className="nb-btn nb-btn--secondary" href={'?run=seasonal&r18=' + Number(adult)}>
            查看两张卡的相遇演出
          </a>
        </div>
      </section>
      <section className="seasonal-review__cards" aria-label="动态卡面">
        {['s-chatgpt', 's-claude'].map((id) => (
          <MotionCard key={id} id={id} time={time} paused={paused} high={high} />
        ))}
      </section>
      <section className="seasonal-review__tiles" aria-label="图鉴取景">
        {['s-chatgpt', 's-claude'].map((id) => (
          <CardTile
            key={id}
            card={cardById[id]}
            owned
            onClick={() =>
              document
                .querySelector(`[data-card="${id}"]`)
                ?.scrollIntoView({ behavior: 'smooth', block: 'start' })
            }
          />
        ))}
      </section>
      <section className="seasonal-review__landscapes" aria-label="静态插画与封面">
        {(['groups', 'covers'] as const).map((kind) => (
          <figure key={kind}>
            <ArtworkImage
              artworkKey={kind === 'groups' ? 'illustration:s' : 'cover:s'}
              src={`/assets/gacha/${high ? 'hires/' : ''}${kind}/s.webp`}
              alt={kind === 'groups' ? '糖夜迷藏 · 主题插画' : '糖夜迷藏 · 独立封面'}
            />
            <figcaption>{kind === 'groups' ? '主题插画' : '独立封面'}</figcaption>
          </figure>
        ))}
      </section>
    </main>
  );
}
