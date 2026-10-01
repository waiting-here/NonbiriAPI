import { useEffect, useRef, type CSSProperties, type ReactNode } from 'react';
import { barHeight, equipmentEffects, fish, periodAt, weatherForDay, type Profile } from './rules';
import { catalogText, useLakeCopy } from './copy';
import { LakeController } from './controller';

export function LakeScene({
  controller,
  profile,
  controls,
}: {
  controller: LakeController;
  profile: Profile;
  controls: ReactNode;
}) {
  const { t: text } = useLakeCopy();
  const root = useRef<HTMLDivElement>(null),
    copyRef = useRef(text),
    fallback = useRef(profile);
  useEffect(() => {
    copyRef.current = text;
    fallback.current = profile;
  }, [text, profile]);
  useEffect(() => {
    const stage = root.current;
    if (!stage) return;
    const track = stage.querySelector<HTMLElement>('.track')!,
      hold = stage.querySelector<HTMLButtonElement>('.hold')!;
    const bar = stage.querySelector<HTMLElement>('.catch-bar')!,
      fishElement = stage.querySelector<HTMLElement>('.fish')!;
    const progress = stage.querySelector<HTMLElement>('.progress-track')!,
      fill = stage.querySelector<HTMLElement>('.progress-fill')!,
      percent = stage.querySelector<HTMLElement>('.progress-value')!;
    const scenery = stage.querySelector<HTMLElement>('.scenery')!,
      time = stage.querySelector<HTMLElement>('.scene-time')!,
      message = stage.querySelector<HTMLElement>('.scene-message')!;
    const name = stage.querySelector<HTMLElement>('.fish-name')!,
      phase = stage.querySelector<HTMLElement>('.phase-badge')!;
    const treasure = stage.querySelector<HTMLElement>('.treasure-marker')!,
      treasureStatus = stage.querySelector<HTMLElement>('.treasure-status')!,
      treasureLabel = stage.querySelector<HTMLElement>('.treasure-label')!,
      treasureFill = stage.querySelector<HTMLElement>('.treasure-meter i')!;
    const rod = stage.querySelector<SVGGElement>('.rod-body')!,
      line = stage.querySelector<SVGPathElement>('.fishing-line')!,
      bobber = stage.querySelector<HTMLElement>('.bobber')!;
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
    let frame = 0,
      previous = performance.now(),
      accumulator = 0,
      space = false;
    const pointers = new Set<number>();
    const held = () => controller.setHeld(space || pointers.size > 0);
    const clear = () => {
      space = false;
      pointers.clear();
      held();
    };
    const down = (event: PointerEvent) => {
      if (event.button !== 0) return;
      event.preventDefault();
      pointers.add(event.pointerId);
      (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
      held();
    };
    const up = (event: PointerEvent) => {
      pointers.delete(event.pointerId);
      held();
    };
    const keydown = (event: KeyboardEvent) => {
      if (event.code !== 'Space' || event.repeat) return;
      const target = document.activeElement;
      if (
        !(target instanceof HTMLElement) ||
        !stage.contains(target) ||
        (target !== hold && target !== track)
      )
        return;
      event.preventDefault();
      space = true;
      held();
    };
    const keyup = (event: KeyboardEvent) => {
      if (event.code === 'Space') {
        space = false;
        held();
      }
    };
    const pause = () => {
      clear();
      accumulator = 0;
      void controller.pause().catch(() => undefined);
    };
    const hidden = () => {
      if (document.hidden) pause();
    };
    const online = () => {
      if (controller.snapshot().status === 'unknown')
        void controller.retry().catch(() => undefined);
    };
    for (const element of [track, hold]) {
      element.addEventListener('pointerdown', down);
      element.addEventListener('lostpointercapture', up);
    }
    window.addEventListener('pointerup', up);
    window.addEventListener('pointercancel', up);
    window.addEventListener('keydown', keydown);
    window.addEventListener('keyup', keyup);
    window.addEventListener('blur', pause);
    window.addEventListener('offline', pause);
    window.addEventListener('online', online);
    document.addEventListener('visibilitychange', hidden);
    const paint = (now: number) => {
      const dt = Math.min(0.05, Math.max(0, (now - previous) / 1000));
      previous = now;
      if (!document.hidden) {
        accumulator = Math.min(0.1, accumulator + dt);
        while (accumulator >= 1 / 60) {
          controller.tick();
          accumulator -= 1 / 60;
        }
      } else accumulator = 0;
      const projection = controller.projection(),
        p = projection?.profile ?? fallback.current,
        c = projection?.cast;
      const translate = copyRef.current,
        period = periodAt(p.clockMinutes),
        weather = weatherForDay(p.day);
      scenery.dataset.location = p.location;
      scenery.dataset.period = period;
      scenery.dataset.weather = weather;
      scenery.classList.toggle('scene-bite', c?.phase === 'playing');
      scenery.classList.toggle('scene-success', c?.phase === 'success');
      const minutes = Math.floor(p.clockMinutes / 10) * 10;
      time.textContent = translate('clock', {
        day: p.day,
        time:
          String(Math.floor(minutes / 60)).padStart(2, '0') +
          ':' +
          String(minutes % 60).padStart(2, '0'),
        period: catalogText(translate, 'periods', period, ''),
        weather: catalogText(translate, 'weathers', weather, ''),
      });
      message.textContent = catalogText(translate, 'locations', p.location, 'scene');
      const height =
          c?.snapshot.barHeight ??
          barHeight(p, equipmentEffects(p.equipped), p.equipped.rod, p.selectedBait),
        y = c?.barY ?? 0.65;
      bar.style.top = (y - height / 2) * 100 + '%';
      bar.style.height = height * 100 + '%';
      bar.classList.toggle(
        'hit',
        Boolean(c?.fish && c.phase === 'playing' && Math.abs(c.fish.y - y) <= height / 2),
      );
      fishElement.style.top = (c?.fish?.y ?? y) * 100 + '%';
      fishElement.style.opacity = c?.fish ? '1' : '0';
      const type = c?.plan.fishKind ? fish(c.plan.fishKind) : undefined;
      fishElement.dataset.kind = type?.style ?? 'gold';
      const reveal =
        c &&
        c.phase !== 'waiting' &&
        type &&
        (c.snapshot.effects.sonar || c.phase === 'success' || c.phase === 'failed');
      name.textContent = reveal
        ? catalogText(translate, 'fish', type.kind)
        : translate('unknownFish');
      const controllerState = controller.snapshot();
      phase.textContent =
        c &&
        (c.phase === 'success' || c.phase === 'failed') &&
        controllerState.status !== 'terminal'
          ? translate('confirming')
          : controllerState.status === 'paused' || controllerState.status === 'unknown'
            ? translate('paused')
            : c
              ? translate(c.phase as 'waiting' | 'playing' | 'success' | 'failed')
              : translate('idle');
      const amount = Math.floor((c?.progress ?? 0.3) * 100 + 0.5);
      fill.style.height = amount + '%';
      percent.textContent = amount + '%';
      if (progress.getAttribute('aria-valuenow') !== String(amount))
        progress.setAttribute('aria-valuenow', String(amount));
      const showTreasure = Boolean(c?.treasure && c.phase === 'playing' && c.elapsed >= 2.2);
      treasure.hidden = treasureStatus.hidden = !showTreasure;
      if (c?.treasure) {
        treasure.style.top = c.treasure.y * 100 + '%';
        treasure.classList.toggle('secured', c.treasure.secured);
        treasureLabel.textContent = translate(
          c.treasure.secured ? 'treasureSecured' : 'treasureCollect',
        );
        treasureFill.style.width = c.treasure.progress * 100 + '%';
      }
      hold.disabled = controllerState.status !== 'running' && controllerState.status !== 'saving';
      hold.classList.toggle('pressed', space || pointers.size > 0);
      const seconds = now / 1000,
        hooked = c?.phase === 'playing',
        waiting = c?.phase === 'waiting';
      const angle = reduced.matches
        ? 0
        : hooked
          ? 1.45 * Math.sin(seconds * 11) + 0.55 * Math.sin(seconds * 19)
          : waiting
            ? 0.55 * Math.sin(seconds * 3.3)
            : 0.2 * Math.sin(seconds * 1.8);
      const radians = (angle * Math.PI) / 180,
        tipX = 20 + 37 * Math.cos(radians) + 63 * Math.sin(radians),
        tipY = 94 + 37 * Math.sin(radians) - 63 * Math.cos(radians);
      rod.setAttribute('transform', 'rotate(' + angle + ' 20 94)');
      const sceneRect = scenery.getBoundingClientRect(),
        bobRect = bobber.getBoundingClientRect();
      if (sceneRect.width && sceneRect.height) {
        const ex = ((bobRect.left + bobRect.width / 2 - sceneRect.left) / sceneRect.width) * 100,
          ey = ((bobRect.top + 2 - sceneRect.top) / sceneRect.height) * 100,
          bend = hooked ? 4.5 : 2.5;
        line.setAttribute(
          'd',
          `M${tipX} ${tipY} C${tipX + bend} ${tipY + 8} ${ex + 3} ${ey - 16} ${ex} ${ey}`,
        );
      }
      frame = requestAnimationFrame(paint);
    };
    frame = requestAnimationFrame(paint);
    return () => {
      cancelAnimationFrame(frame);
      clear();
      for (const element of [track, hold]) {
        element.removeEventListener('pointerdown', down);
        element.removeEventListener('lostpointercapture', up);
      }
      window.removeEventListener('pointerup', up);
      window.removeEventListener('pointercancel', up);
      window.removeEventListener('keydown', keydown);
      window.removeEventListener('keyup', keyup);
      window.removeEventListener('blur', pause);
      window.removeEventListener('offline', pause);
      window.removeEventListener('online', online);
      document.removeEventListener('visibilitychange', hidden);
    };
  }, [controller]);
  return (
    <div className="game-grid" ref={root}>
      <section
        className="scenery"
        data-location={profile.location}
        aria-label={catalogText(text, 'locations', profile.location)}
      >
        <div className="sky" />
        <div className="sun" />
        <div className="cloud one" />
        <div className="cloud two" />
        <div className="hill one" />
        <div className="hill two" />
        <div className="treeline" />
        <div className="water" />
        <div className="shore" />
        <div className="pier" />
        <div className="reeds" />
        <div className="weather-rain" aria-hidden="true">
          {Array.from({ length: 28 }, (_, i) => (
            <i
              key={i}
              className="rain-drop"
              style={
                {
                  '--x': 5 + (((i * 73 + 19) % 101) / 100) * 109 + '%',
                  '--width': (i % 3 === 0 ? 1 : 1.6) + 'px',
                  '--length': 24 + (i % 6) * 2 + 'px',
                  '--opacity': i % 3 === 0 ? 0.3 : 0.5,
                  '--duration': 1.05 + ((i * 17) % 13) * 0.065 + 's',
                  '--delay': -i * 0.379 + 's',
                } as CSSProperties
              }
            />
          ))}
        </div>
        <svg
          className="fishing-rig"
          viewBox="0 0 100 100"
          preserveAspectRatio="none"
          aria-hidden="true"
        >
          <g className="rod-body">
            <path className="rod-outline" d="M20 94 C30 77 43 51 57 31" />
            <path className="rod-highlight" d="M20 94 C30 77 43 51 57 31" />
            <path className="rod-light" d="M22 91 C33 72 45 47 57 31" />
            <path className="rod-grip" d="M20 94 Q24 87 27 82" />
            <path className="rod-guide" d="M34 69 l2 1.7 M45 47 l2 1.3 M56 31 l1.8 .9" />
            <circle className="rod-reel" cx="28" cy="81" r="2.1" />
            <circle className="rod-reel-core" cx="28" cy="81" r=".9" />
          </g>
          <path className="fishing-line" d="M57 31 Q64 40 61 60" />
        </svg>
        <div className="ripple" />
        <div className="ripple two" />
        <div className="bobber" />
        <div className="scene-card">
          <small className="scene-time" />
          <strong className="scene-message" />
        </div>
      </section>
      <section className="panel" aria-label={text('title')}>
        <div className="panel-heading">
          <div>
            <h2>{text('title')}</h2>
            <p className="muted fish-name" />
          </div>
          <span className="phase-badge" />
        </div>
        <div className="arena">
          <div className="track" tabIndex={0} role="button" aria-label={text('track')}>
            <div className="catch-bar" />
            <div className="treasure-marker" hidden aria-hidden="true">
              <img src="/assets/lake-notes/item-treasure.webp" alt="" loading="lazy" />
            </div>
            <div className="fish" aria-hidden="true">
              <svg viewBox="0 0 50 34">
                <path className="fish-fin" d="M15 12 8 4l-1 12-5 8 15-3M25 10l5-8 5 11" />
                <path
                  className="fish-body"
                  d="M47 17c-6-9-15-12-24-10-8 1-14 6-17 10 3 5 9 9 17 10 9 2 18-1 24-10Z"
                />
                <circle className="fish-eye" cx="36" cy="14" r="2.3" />
              </svg>
            </div>
          </div>
          <div className="progress-wrap">
            <span className="progress-label">{text('progress')}</span>
            <div
              className="progress-track"
              role="progressbar"
              aria-label={text('progress')}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={30}
            >
              <div className="progress-fill" />
            </div>
            <span className="progress-value">30%</span>
          </div>
        </div>
        <p className="hint-line">{text('intro')}</p>
        <div className="treasure-status" hidden>
          <span className="treasure-label" />
          <span className="treasure-meter">
            <i />
          </span>
        </div>
        <div className="controls">
          <button className="hold" type="button" disabled>
            {text('hold')}
          </button>
        </div>
        {controls}
      </section>
    </div>
  );
}
