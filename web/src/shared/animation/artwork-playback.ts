/** Playback belongs to a single background. Card reveals and exports retain their own schedules. */
export class ArtworkPlayback extends EventTarget {
  paused = false;
  failed = false;
  offscreen = false;

  setOffscreen(offscreen: boolean) {
    if (this.offscreen === offscreen) return;
    this.offscreen = offscreen;
    this.dispatchEvent(new Event('change'));
  }

  readonly getStatus = () => (this.failed ? 'failed' : this.paused ? 'paused' : 'playing');
  readonly subscribe = (notify: () => void) => {
    this.addEventListener('status', notify);
    return () => this.removeEventListener('status', notify);
  };

  constructor(
    readonly highest: boolean,
    readonly sceneKey = '',
  ) {
    super();
  }

  setPaused(paused: boolean) {
    if (this.paused === paused) return;
    this.paused = paused;
    this.dispatchEvent(new Event('status'));
    this.dispatchEvent(new Event('change'));
  }

  setFailed(failed: boolean) {
    if (this.failed === failed) return;
    this.failed = failed;
    this.dispatchEvent(new Event('status'));
  }
}

type Size = readonly [number, number];

export function graphicsLimit(gl: WebGLRenderingContext): Size {
  const viewport = gl.getParameter(gl.MAX_VIEWPORT_DIMS) as Int32Array;
  const edge = Math.min(
    gl.getParameter(gl.MAX_TEXTURE_SIZE),
    gl.getParameter(gl.MAX_RENDERBUFFER_SIZE),
  );
  return [Math.min(viewport[0], edge), Math.min(viewport[1], edge)];
}

/** Native texels per CSS pixel of a projected image plane. */
export function layerDensity(
  image: Pick<HTMLImageElement, 'naturalWidth' | 'naturalHeight'> | null,
  width: number,
  height: number,
) {
  return image ? Math.min(image.naturalWidth / width, image.naturalHeight / height) : 0;
}

export function displayDensity(
  width: number,
  height: number,
  native: number,
  limits?: Size,
  ratio = window.devicePixelRatio || 1,
) {
  return Math.min(
    ratio,
    native,
    limits ? limits[0] / width : Infinity,
    limits ? limits[1] / height : Infinity,
  );
}

/** Pair browser visibility with the background viewer's pause control. */
export function artworkActivity(playback?: ArtworkPlayback) {
  return {
    get hidden() {
      return document.hidden || !!playback?.paused || !!playback?.offscreen;
    },
    // The appearance host already resolves the user's system/full/static preference.
    systemMotion: playback ? undefined : window.matchMedia('(prefers-reduced-motion: reduce)'),
    listen(change: () => void) {
      document.addEventListener('visibilitychange', change);
      playback?.addEventListener('change', change);
    },
    unlisten(change: () => void) {
      document.removeEventListener('visibilitychange', change);
      playback?.removeEventListener('change', change);
    },
  };
}
