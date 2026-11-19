import type { ArtworkPlayback } from '@shared/animation/artwork-playback';

export function startBackground(
  canvas: HTMLCanvasElement,
  playback: ArtworkPlayback,
  render: (signal: AbortSignal) => Promise<() => void>,
  markReady = false,
) {
  const controller = new AbortController();
  let release: (() => void) | undefined;
  canvas.dataset.ready = 'false';
  canvas.style.removeProperty('visibility');
  playback.setFailed(false);
  void render(controller.signal)
    .then((stop) => {
      if (controller.signal.aborted) stop();
      else {
        release = stop;
        if (markReady) canvas.dataset.ready = 'true';
      }
    })
    .catch(() => {
      if (!controller.signal.aborted) {
        canvas.dataset.ready = 'false';
        playback.setFailed(true);
      }
    });
  return () => {
    controller.abort();
    release?.();
    canvas.dataset.ready = 'false';
  };
}
