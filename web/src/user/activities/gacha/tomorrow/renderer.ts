import { blinkAt, eyelidProfileWidth, PaintCadence } from '@shared/animation/illustration';
import { tomorrowAssets } from './assets';
import { tomorrowEyelids } from './eyelids';
import { tomorrowLayout, type TomorrowVariant } from './layout';
import { tomorrowShader } from './shader';
import { motionMasks } from './motion';

export type TomorrowOptions = {
  variant?: TomorrowVariant;
  dark?: boolean;
  landscape?: boolean;
  highResolution?: boolean;
  characterOnly?: boolean;
  characterPlate?: boolean;
  backgroundOnly?: boolean;
  frozenTime?: number;
  blink?: number;
  paused?: () => boolean;
  signal?: AbortSignal;
  ambient?: boolean;
  exportSize?: boolean;
};

function loadImage(src: string, signal?: AbortSignal) {
  return new Promise<HTMLImageElement>((resolve, reject) => {
    const image = new Image();
    const finish = () => signal?.removeEventListener('abort', abort);
    const abort = () => {
      image.onload = image.onerror = null;
      image.src = '';
      finish();
      reject(new DOMException('Cancelled', 'AbortError'));
    };
    image.onload = () => {
      finish();
      resolve(image);
    };
    image.onerror = () => {
      finish();
      reject(new Error('image-unavailable'));
    };
    if (signal?.aborted) {
      abort();
      return;
    }
    signal?.addEventListener('abort', abort, { once: true });
    image.src = src;
  });
}

export async function renderTomorrow(canvas: HTMLCanvasElement, options: TomorrowOptions = {}) {
  if (options.signal?.aborted) return () => {};
  const [background, hero, closed, cloud] = await Promise.all([
    loadImage(
      options.dark ? tomorrowAssets.backgroundDark : tomorrowAssets.backgroundLight,
      options.signal,
    ),
    loadImage(tomorrowAssets.hero, options.signal),
    loadImage(tomorrowAssets.closed, options.signal),
    loadImage(tomorrowAssets.cloud, options.signal),
  ]);
  if (options.signal?.aborted) return () => {};
  const context = canvas.getContext('2d');
  const layer = document.createElement('canvas');
  const gl = layer.getContext('webgl', {
    alpha: true,
    premultipliedAlpha: false,
    antialias: false,
  });
  if (!context || !gl) throw new Error('renderer-unavailable');
  const shaders: WebGLShader[] = [],
    textures: WebGLTexture[] = [];
  let program: WebGLProgram | null = null,
    buffer: WebGLBuffer | null = null;
  let request = 0,
    elapsed = options.frozenTime ?? 0,
    previous = 0,
    stopped = false,
    painted = false;
  const cadence = new PaintCadence(undefined, options.ambient);
  const observer = new ResizeObserver(() => {
    measure();
    paint();
  });
  function dispose() {
    if (stopped) return;
    stopped = true;
    cancelAnimationFrame(request);
    observer.disconnect();
    document.removeEventListener('visibilitychange', visibility);
    options.signal?.removeEventListener('abort', dispose);
    textures.forEach((value) => gl!.deleteTexture(value));
    shaders.forEach((value) => gl!.deleteShader(value));
    gl!.deleteProgram(program);
    gl!.deleteBuffer(buffer);
    gl!.getExtension('WEBGL_lose_context')?.loseContext();
    layer.width = layer.height = 0;
    canvas.dataset.ready = 'false';
  }
  const uniform = (name: string) => gl!.getUniformLocation(program!, name);
  try {
    program = gl.createProgram();
    buffer = gl.createBuffer();
    if (!program || !buffer) throw new Error('renderer-unavailable');
    for (const [type, source] of [
      [
        gl.VERTEX_SHADER,
        'attribute vec2 a;varying vec2 uv;void main(){uv=a;gl_Position=vec4(a.x*2.-1.,1.-a.y*2.,0.,1.);}',
      ],
      [gl.FRAGMENT_SHADER, tomorrowShader],
    ] as const) {
      const shader = gl.createShader(type);
      if (!shader) throw new Error('renderer-unavailable');
      shaders.push(shader);
      gl.shaderSource(shader, source);
      gl.compileShader(shader);
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS))
        throw new Error(gl.getShaderInfoLog(shader) ?? 'shader-unavailable');
      gl.attachShader(program, shader);
    }
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS))
      throw new Error(gl.getProgramInfoLog(program) ?? 'shader-unavailable');
    gl.useProgram(program);
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(
      gl.ARRAY_BUFFER,
      new Float32Array([0, 0, 1, 0, 0, 1, 0, 1, 1, 0, 1, 1]),
      gl.STATIC_DRAW,
    );
    const attribute = gl.getAttribLocation(program, 'a');
    gl.enableVertexAttribArray(attribute);
    gl.vertexAttribPointer(attribute, 2, gl.FLOAT, false, 0, 0);
    for (const [index, image] of [background, hero, closed, null, cloud].entries()) {
      const texture = gl.createTexture();
      if (!texture) throw new Error('renderer-unavailable');
      textures.push(texture);
      gl.activeTexture(gl.TEXTURE0 + index);
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      if (image) gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
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
          new Uint8Array(tomorrowEyelids.flatMap((p) => p.rows.flat())),
        );
      gl.uniform1i(uniform(['scenery', 'art', 'closed', 'lids', 'companion'][index]), index);
    }
    const masks = motionMasks();
    for (const [index, [name, mask]] of [
      ['rig', masks.hero],
      ['environment', masks.environment],
    ].entries()) {
      const texture = gl.createTexture();
      if (!texture || typeof mask === 'string') throw new Error('renderer-unavailable');
      textures.push(texture);
      gl.activeTexture(gl.TEXTURE5 + index);
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      gl.texImage2D(
        gl.TEXTURE_2D,
        0,
        gl.RGBA,
        mask.width,
        mask.height,
        0,
        gl.RGBA,
        gl.UNSIGNED_BYTE,
        mask.pixels,
      );
      gl.uniform1i(uniform(String(name)), 5 + index);
    }
    gl.uniform4fv(uniform('leftEye'), tomorrowEyelids[0].bounds);
    gl.uniform1f(uniform('cloudFront'), (options.variant ?? 'card') === 'card' ? 1 : 0);
    gl.uniform4fv(uniform('rightEye'), tomorrowEyelids[1].bounds);
    for (const [key, value] of Object.entries({
      night: options.dark,
      characterOnly: options.characterOnly,
      characterPlate: options.characterPlate,
      backgroundOnly: options.backgroundOnly,
    }))
      gl.uniform1f(uniform(key), value ? 1 : 0);
  } catch (error) {
    dispose();
    throw error;
  }

  function measure() {
    if (!options.exportSize) {
      const rect = canvas.getBoundingClientRect();
      const width = Math.max(1, rect.width || 768),
        height = Math.max(1, rect.height || width * 1.5);
      const cap = options.highResolution ? (options.landscape ? 3840 : 2560) : 1600;
      const density = Math.min(window.devicePixelRatio || 1, cap / width);
      canvas.width = Math.round(width * density);
      canvas.height = Math.round(height * density);
    }
    layer.width = canvas.width;
    layer.height = canvas.height;
    gl!.viewport(0, 0, layer.width, layer.height);
    const layout = options.characterPlate
      ? { view: [0, 0, 1, 1], hero: [0, 0, 1, 1], cloud: [0, 0, 0, 0] }
      : tomorrowLayout(canvas.width, canvas.height, options.variant ?? 'card');
    gl!.uniform4fv(uniform('view'), layout.view);
    gl!.uniform4fv(uniform('heroBox'), layout.hero);
    gl!.uniform4fv(uniform('cloudBox'), layout.cloud);
    cadence.reset();
  }
  function paint() {
    if (stopped) return;
    const blink = options.blink ?? blinkAt(elapsed);
    gl!.uniform1f(uniform('time'), elapsed);
    gl!.uniform1f(uniform('blink'), blink);
    gl!.drawArrays(gl!.TRIANGLES, 0, 6);
    context!.clearRect(0, 0, canvas.width, canvas.height);
    context!.drawImage(layer, 0, 0);
    canvas.dataset.ready = 'true';
    canvas.dataset.time = elapsed.toFixed(4);
    canvas.dataset.blink = blink.toFixed(4);
    painted = true;
  }
  function tick(now: number) {
    if (stopped || options.signal?.aborted) return;
    if (!options.paused?.()) {
      elapsed += previous ? Math.min((now - previous) / 1000, 0.1) : 0;
      if (cadence.due(now)) paint();
    } else if (!painted) paint();
    previous = now;
    if (!document.hidden) request = requestAnimationFrame(tick);
  }
  function visibility() {
    cancelAnimationFrame(request);
    previous = 0;
    cadence.reset();
    if (!document.hidden && !stopped && options.frozenTime === undefined)
      request = requestAnimationFrame(tick);
  }
  measure();
  paint();
  observer.observe(canvas);
  options.signal?.addEventListener('abort', dispose, { once: true });
  document.addEventListener('visibilitychange', visibility);
  if (options.frozenTime === undefined && !document.hidden) request = requestAnimationFrame(tick);
  return dispose;
}
