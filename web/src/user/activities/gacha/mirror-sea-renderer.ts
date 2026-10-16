import { blinkAt, PaintCadence, eyelidProfileWidth } from '@shared/animation/illustration';
import { highResolutionArt } from './art-resolution';
import { mirrorSea } from './mirror-sea-config';
import { mirrorSeaEyelids } from './mirror-sea-eyelids';
import { mirrorSeaShader } from './mirror-sea-shader';
import { portraitView } from './portrait';

type Options = {
  polished?: boolean;
  mirrorFlow?: boolean;
  exportSize?: boolean;
  blink?: number;
  landscape?: boolean;
  dark?: boolean;
  highResolution?: boolean;
  characterOnly?: boolean;
  backgroundOnly?: boolean;
  frozenTime?: number;
  paused?: () => boolean;
  signal?: AbortSignal;
};

function image(src: string, signal?: AbortSignal) {
  return new Promise<HTMLImageElement>((resolve, reject) => {
    const img = new Image();
    const abort = () => {
      img.src = '';
      reject(new DOMException('Aborted', 'AbortError'));
    };
    const finish = () => signal?.removeEventListener('abort', abort);
    img.onload = () => {
      finish();
      resolve(img);
    };
    img.onerror = () => {
      finish();
      reject(new Error('image-unavailable'));
    };
    if (signal?.aborted) {
      abort();
      return;
    }
    signal?.addEventListener('abort', abort, { once: true });
    img.src = src;
  });
}

export async function renderMirrorSea(canvas: HTMLCanvasElement, options: Options = {}) {
  const frozenTime =
    options.frozenTime ??
    (window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : undefined);
  const base = '/assets/gacha/motion/n-gemini/';
  const source = (name: string) => {
    const url = base + name + '.webp';
    return image(options.highResolution ? highResolutionArt(url) : url, options.signal);
  };
  const [background, hero, closed, shadow] = await Promise.all([
    options.characterOnly ? null : source(options.dark ? 'background-dark' : 'background-light'),
    source('portrait'),
    source('closed'),
    source('shadow'),
  ]);
  if (options.signal?.aborted) return () => {};
  const context = canvas.getContext('2d');
  const layer = document.createElement('canvas');
  const gl = layer.getContext('webgl', {
    alpha: !!options.characterOnly,
    premultipliedAlpha: false,
    antialias: false,
  });
  if (!gl || !context) throw new Error('renderer-unavailable');
  const shaders: WebGLShader[] = [],
    textures: WebGLTexture[] = [];
  const program = gl.createProgram(),
    buffer = gl.createBuffer();
  if (!program || !buffer) throw new Error('renderer-unavailable');
  let raf = 0,
    stopped = false,
    time = 0,
    last = performance.now(),
    painted = false;
  const observer = new ResizeObserver(() => resize());
  const cadence = new PaintCadence();
  const dispose = () => {
    if (stopped) return;
    stopped = true;
    cancelAnimationFrame(raf);
    observer.disconnect();
    document.removeEventListener('visibilitychange', visibility);
    options.signal?.removeEventListener('abort', dispose);
    textures.forEach((texture) => gl.deleteTexture(texture));
    shaders.forEach((shader) => gl.deleteShader(shader));
    gl.deleteBuffer(buffer);
    gl.deleteProgram(program);
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    layer.width = 0;
    layer.height = 0;
  };
  try {
    for (const [type, code] of [
      [
        gl.VERTEX_SHADER,
        'attribute vec2 a;varying vec2 uv;void main(){uv=a;gl_Position=vec4(a.x*2.-1.,1.-a.y*2.,0.,1.);}',
      ],
      [gl.FRAGMENT_SHADER, mirrorSeaShader],
    ] as const) {
      const shader = gl.createShader(type);
      if (!shader) throw new Error('renderer-unavailable');
      shaders.push(shader);
      gl.shaderSource(shader, code);
      gl.compileShader(shader);
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error('shader-unavailable');
      gl.attachShader(program, shader);
    }
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error('shader-unavailable');
    gl.useProgram(program);
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(
      gl.ARRAY_BUFFER,
      new Float32Array([0, 0, 1, 0, 0, 1, 0, 1, 1, 0, 1, 1]),
      gl.STATIC_DRAW,
    );
    const attr = gl.getAttribLocation(program, 'a');
    gl.enableVertexAttribArray(attr);
    gl.vertexAttribPointer(attr, 2, gl.FLOAT, false, 0, 0);
    for (const [i, img] of [background ?? hero, hero, closed, null, shadow].entries()) {
      const texture = gl.createTexture();
      if (!texture) throw new Error('renderer-unavailable');
      textures.push(texture);
      gl.activeTexture(gl.TEXTURE0 + i);
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      if (img) gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, img);
      else
        gl.texImage2D(
          gl.TEXTURE_2D,
          0,
          gl.RGBA,
          eyelidProfileWidth,
          4,
          0,
          gl.RGBA,
          gl.UNSIGNED_BYTE,
          new Uint8Array(mirrorSeaEyelids.flatMap((profile) => profile.rows.flat())),
        );
      gl.uniform1i(
        gl.getUniformLocation(program, ['scenery', 'art', 'closed', 'lids', 'shadow'][i]),
        i,
      );
    }
    gl.uniform4f(gl.getUniformLocation(program, 'leftEye'), ...mirrorSeaEyelids[0].bounds);
    gl.uniform4f(gl.getUniformLocation(program, 'rightEye'), ...mirrorSeaEyelids[1].bounds);
    gl.uniform1f(gl.getUniformLocation(program, 'night'), options.dark ? 1 : 0);
    gl.uniform1f(gl.getUniformLocation(program, 'polish'), options.polished ? 1 : 0);
    gl.uniform1f(
      gl.getUniformLocation(program, 'mirrorFlow'),
      options.polished && options.mirrorFlow !== false ? 1 : 0,
    );
    gl.uniform1f(gl.getUniformLocation(program, 'characterOnly'), options.characterOnly ? 1 : 0);
    gl.uniform1f(gl.getUniformLocation(program, 'backgroundOnly'), options.backgroundOnly ? 1 : 0);
  } catch (error) {
    dispose();
    throw error;
  }
  const uniform = (name: string) => gl.getUniformLocation(program, name);
  const timeUniform = uniform('time'),
    blinkUniform = uniform('blink');
  const resize = () => {
    const rect = canvas.getBoundingClientRect();
    const width = Math.max(1, rect.width || canvas.width),
      height = options.landscape ? Math.max(1, rect.height || canvas.height) : width * 1.5;
    const density = options.exportSize
      ? 1
      : Math.min(window.devicePixelRatio || 1, (options.landscape ? 3840 : 2560) / width);
    canvas.width = Math.round(width * density);
    canvas.height = Math.round(height * density);
    const sampling = options.polished && !options.exportSize ? Math.min(2, 2048 / canvas.width) : 1;
    layer.width = Math.round(canvas.width * sampling);
    layer.height = Math.round(canvas.height * sampling);
    gl.viewport(0, 0, layer.width, layer.height);
    if (options.landscape) {
      const aspect = width / height,
        original = 1672 / 941;
      const vw = Math.min(1, aspect / original),
        vh = Math.min(1, original / aspect);
      // Keep the same top edge as the static painting, including on ultrawide screens.
      gl.uniform4f(uniform('view'), (1 - vw) * mirrorSea.cropPosition, 0, vw, vh);
      gl.uniform4f(uniform('heroBox'), ...mirrorSea.landscapeHero);
    } else {
      gl.uniform4fv(
        uniform('view'),
        options.polished
          ? portraitView(mirrorSea.portraitCrop, 'mirrorsea')
          : mirrorSea.portraitCrop,
      );
      const [x, y, w, h] = mirrorSea.portraitHero,
        [cx, cy, cw, ch] = mirrorSea.portraitCrop;
      gl.uniform4f(uniform('heroBox'), cx + x * cw, cy + y * ch, w * cw, h * ch);
    }
    painted = false;
    cadence.reset();
    if (frozenTime !== undefined) frame(performance.now());
  };
  function frame(at: number) {
    if (stopped || options.signal?.aborted) return;
    const paused = options.paused?.() ?? false;
    if (!paused) time += Math.min(at - last, 100) / 1000;
    last = at;
    if ((!painted || !paused) && (frozenTime !== undefined || cadence.due(at))) {
      const t = frozenTime ?? time;
      gl!.uniform1f(timeUniform, t);
      gl!.uniform1f(blinkUniform, options.blink ?? blinkAt(t));
      gl!.drawArrays(gl!.TRIANGLES, 0, 6);
      painted = true;
      context!.clearRect(0, 0, canvas.width, canvas.height);
      context!.imageSmoothingQuality = 'high';
      context!.drawImage(layer, 0, 0, canvas.width, canvas.height);
      canvas.dataset.ready = 'true';
      canvas.dataset.time = t.toFixed(3);
      canvas.dataset.blink = (options.blink ?? blinkAt(t)).toFixed(3);
    }
    if (frozenTime === undefined && !document.hidden) raf = requestAnimationFrame(frame);
  }
  function visibility() {
    cancelAnimationFrame(raf);
    if (!document.hidden && !stopped && frozenTime === undefined) {
      last = performance.now();
      cadence.reset();
      raf = requestAnimationFrame(frame);
    }
  }
  observer.observe(canvas);
  document.addEventListener('visibilitychange', visibility);
  options.signal?.addEventListener('abort', dispose, { once: true });
  resize();
  if (frozenTime === undefined) frame(last);
  return dispose;
}
