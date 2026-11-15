import { useEffect, useRef, useState } from 'react';
import { ArtworkImage } from '@shared/artwork/ArtworkImage';
import { useArtworkPolicy } from '@shared/artwork/context';
import { Button } from '@shared/components/ui/Button';
import { Field } from '@shared/components/ui/Field';
import { Toggle } from '@shared/components/ui/Toggle';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { cardById, cards, themes, themeById } from '../user/activities/gacha/catalog';
import { CardTile } from '../user/activities/gacha/CardTile';
import { highResolutionArt } from '../user/activities/gacha/art-resolution';
import { renderSSR } from '../user/activities/gacha/ssr-renderer';
import { ChristmasAssets } from './ChristmasAssets';
import { ClaudeLayerStudy } from './ClaudeLayerStudy';
import { MotionAssets } from './MotionAssets';
import { loadLayerResources } from './layer-resources';
import '../user/activities/gacha/gacha.css';
import './seasonal-preview.css';

function MotionCard({
  id,
  time,
  paused,
  high,
  staticOnly,
}: {
  id: string;
  time?: number;
  paused: boolean;
  high: boolean;
  staticOnly: boolean;
}) {
  const card = cardById[id];
  const policy = useArtworkPolicy();
  const allowed = policy.allowed('card:' + id);
  const surface = useRef<HTMLCanvasElement>(null);
  const pause = useRef(paused || staticOnly);
  const visible = useRef(true);
  const [ready, setReady] = useState(false);
  const [failed, setFailed] = useState(false);
  const [reduced, setReduced] = useState(
    () => matchMedia('(prefers-reduced-motion: reduce)').matches,
  );
  useEffect(() => {
    const preference = matchMedia('(prefers-reduced-motion: reduce)');
    const update = () => setReduced(preference.matches);
    preference.addEventListener('change', update);
    return () => preference.removeEventListener('change', update);
  }, []);
  useEffect(() => {
    pause.current = paused || staticOnly;
  }, [paused, staticOnly]);
  useEffect(() => {
    if (!surface.current) return;
    const observer = new IntersectionObserver(([entry]) => {
      visible.current = entry.isIntersecting;
    });
    observer.observe(surface.current);
    return () => observer.disconnect();
  }, [allowed]);
  useEffect(() => {
    if (!allowed || !surface.current || (reduced && time === undefined)) return;
    const controller = new AbortController();
    let cleanup: (() => void) | undefined;
    void renderSSR(surface.current, id, time, () => pause.current || !visible.current, high, {
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
  }, [allowed, id, time, high, reduced]);
  return (
    <figure className="seasonal-review__card" data-card={id}>
      <div className="seasonal-review__art">
        <ArtworkImage
          artworkKey={'card:' + id}
          src={high ? highResolutionArt(card.art) : card.art}
          alt={card.name}
        />
        {allowed && (
          <canvas
            ref={surface}
            className={ready && !staticOnly ? 'is-ready' : ''}
            aria-hidden="true"
          />
        )}
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
  theme = 's',
}: {
  adult: boolean;
  setAdult: (next: boolean) => void;
  theme?: string;
}) {
  const ids = cards.filter((card) => card.theme === theme).map((card) => card.id);
  const name = themeById[theme].name;
  const [time, setTime] = useState<number>();
  const [paused, setPaused] = useState(false);
  const [high, setHigh] = useState(false);
  const [staticOnly, setStaticOnly] = useState(false);
  const eyeTimes = theme === 'q' ? [1.9575, 2.03, 2.152] : [2.265, 2.35, 2.48];
  const inspect = (seconds: number) => {
    setStaticOnly(false);
    setTime(seconds);
  };
  return (
    <main className="seasonal-review gacha-app">
      <header className="seasonal-review__header">
        <div>
          <h1>{name}</h1>
          <p>单卡动效、主题插画与独立封面 · 本地美术验收</p>
        </div>
        <ThemeToggle />
      </header>
      <nav className="seasonal-review__navigation nb-actions" aria-label="美术资源导航">
        <a className="nb-btn nb-btn--secondary" href="?view=copy">
          文案校对
        </a>
        <a className="nb-btn nb-btn--secondary" href="#motion">
          动态卡面
        </a>
        <a className="nb-btn nb-btn--secondary" href="#tiles">
          图鉴取景
        </a>
        <a className="nb-btn nb-btn--secondary" href="#landscapes">
          插画与封面
        </a>
        {theme === 't' && (
          <a className="nb-btn nb-btn--secondary" href="#claude-study">
            Claude 新旧方案
          </a>
        )}
        <a className="nb-btn nb-btn--secondary" href="#resources">
          分层资源与原图
        </a>
      </nav>
      <section className="seasonal-review__controls" aria-label="预览控制">
        <Field label="卡牌主题">
          {(props) => (
            <select
              {...props}
              value={theme}
              onChange={(event) => {
                location.href = `?view=motion&theme=${event.target.value}&r18=${Number(adult)}`;
              }}
            >
              {themes
                .filter((item) => item.rarity === 'SSR' || item.rarity === 'SP')
                .map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.rarity} · {item.name}
                  </option>
                ))}
            </select>
          )}
        </Field>
        {theme === 's' && (
          <Toggle
            label="显示 R-18 素材"
            description="仅影响当前本地预览。"
            checked={adult}
            onChange={setAdult}
          />
        )}
        <Toggle label="高清资源" checked={high} onChange={setHigh} />
        <Toggle
          label="对比静态原画"
          description="切换查看人物轮廓与动作幅度；返回动态时从原进度继续。"
          checked={staticOnly}
          onChange={setStaticOnly}
        />
        <div className="nb-actions">
          <Button
            onClick={() => {
              setTime(undefined);
              setPaused(false);
              setStaticOnly(false);
            }}
          >
            自然播放
          </Button>
          <Button onClick={() => setPaused((value) => !value)}>
            {paused ? '继续播放' : '暂停动作'}
          </Button>
          <Button onClick={() => inspect(0)}>睁眼</Button>
          <Button onClick={() => inspect(eyeTimes[0])}>半闭</Button>
          <Button onClick={() => inspect(eyeTimes[1])}>闭眼</Button>
          <Button onClick={() => inspect(eyeTimes[2])}>重新睁开</Button>
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
              max={theme === 'm' ? '120' : themeById[theme].rarity === 'SP' ? '180' : '60'}
              step="0.005"
              value={time ?? 0}
              onChange={(event) => inspect(Number(event.target.value))}
            />
          )}
        </Field>
        <div className="nb-actions">
          <a className="nb-btn nb-btn--secondary" href={'?r18=' + Number(adult)}>
            查看抽卡、图鉴与心动时刻
          </a>
          {(theme === 's' || theme === 't') && (
            <a
              className="nb-btn nb-btn--secondary"
              href={`?run=${theme === 't' ? 'christmas' : 'seasonal'}&r18=${Number(adult)}`}
            >
              查看本组卡的相遇演出
            </a>
          )}
          {themeById[theme].rarity === 'SP' && (
            <a
              className="nb-btn nb-btn--secondary"
              href={`?view=appearance&appearance=${cardById[ids[0]].appearance}`}
            >
              查看动态主题装扮
            </a>
          )}
        </div>
      </section>
      <h2 id="motion">动态卡面</h2>
      <section className="seasonal-review__cards" aria-label="动态卡面">
        {ids.map((id) => (
          <MotionCard
            key={id}
            id={id}
            time={time}
            paused={paused}
            high={high}
            staticOnly={staticOnly}
          />
        ))}
      </section>
      <h2 id="tiles">图鉴取景</h2>
      <section className="seasonal-review__tiles" aria-label="图鉴取景">
        {ids.map((id) => (
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
      <h2 id="landscapes">插画与封面</h2>
      <section className="seasonal-review__landscapes" aria-label="静态插画与封面">
        {(['groups', 'covers'] as const).map((kind) => (
          <figure key={kind}>
            <ArtworkImage
              artworkKey={(kind === 'groups' ? 'illustration:' : 'cover:') + theme}
              src={`/assets/gacha/${high ? 'hires/' : ''}${kind}/${theme}.webp`}
              alt={name + (kind === 'groups' ? ' · 主题插画' : ' · 独立封面')}
            />
            <figcaption>
              {kind === 'groups' ? '主题插画' : '独立封面'}
              {' · '}
              <a
                href={`/assets/gacha/${high ? 'hires/' : ''}${kind}/${theme}.webp`}
                target="_blank"
                rel="noreferrer"
              >
                打开原尺寸图片 ↗
              </a>
            </figcaption>
          </figure>
        ))}
      </section>
      {theme === 't' && <ClaudeLayerStudy />}
      {theme === 't' && <ChristmasAssets high={high} />}
      {theme !== 't' && <MotionAssets ids={ids} high={high} load={loadLayerResources} />}
    </main>
  );
}
