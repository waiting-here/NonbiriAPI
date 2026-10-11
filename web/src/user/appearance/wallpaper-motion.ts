import {
  blinkAt,
  celestialRects,
  PaintCadence,
  portraits,
  waterfalls,
  whaleRect,
  type Rect,
  type WallpaperScene,
  type WallpaperTheme,
} from './wallpaper-animation';
import { wallpaperFragment, wallpaperVertex } from './wallpaper-shaders';

export type { WallpaperScene, WallpaperTheme } from './wallpaper-animation';
export type WallpaperQuality = 'standard' | 'high';

function grid(columns: number, rows: number) {
  const vertices: number[] = [];
  for (let y = 0; y < rows; y++) {
    for (let x = 0; x < columns; x++) {
      const left = x / columns,
        right = (x + 1) / columns;
      const top = y / rows,
        bottom = (y + 1) / rows;
      vertices.push(left, top, right, top, left, bottom, left, bottom, right, top, right, bottom);
    }
  }
  return new Float32Array(vertices);
}

export function wallpaperQuality(): WallpaperQuality {
  return Math.max(window.innerWidth, window.innerHeight) * (window.devicePixelRatio || 1) > 1920
    ? 'high'
    : 'standard';
}

export async function renderWallpaper(
  canvas: HTMLCanvasElement,
  scene: WallpaperScene,
  theme: WallpaperTheme,
  signal: AbortSignal,
  quality: WallpaperQuality = wallpaperQuality(),
): Promise<() => void> {
  const prefix = '/assets/gacha/appearances/layers/' + (quality === 'high' ? 'hires/' : '');
  const id = scene + '-' + theme;
  const names = [
    id + '-background',
    id + '-hero',
    id + '-closed',
    'cloud',
    ...(scene === 'wandering' ? ['whale'] : ['moon', 'earth']),
  ];
  const images = await Promise.all(
    names.map(async (name) => {
      const image = new Image();
      image.src = prefix + name + '.webp';
      await image.decode();
      return image;
    }),
  );
  if (signal.aborted) return () => {};
  const gl = canvas.getContext('webgl', {
    alpha: false,
    antialias: false,
    depth: false,
    stencil: false,
  });
  if (!gl) throw new Error('wallpaper-renderer-unavailable');
  const shaders: WebGLShader[] = [],
    textures: WebGLTexture[] = [],
    buffers: WebGLBuffer[] = [];
  const program = gl.createProgram();
  const cadence = new PaintCadence();
  let frameID = 0,
    stopped = false,
    elapsed = 0,
    previous = performance.now();
  let quad: WebGLBuffer,
    mesh: WebGLBuffer,
    meshCount = 0;
  let bound: WebGLBuffer | undefined,
    point = 0;
  let layoutWidth = 0,
    layoutHeight = 0,
    layoutDensity = 0;
  let reportAt = 0,
    paints = 0;
  const locations = new Map<string, WebGLUniformLocation | null>();
  const uniform = (name: string) => {
    if (!locations.has(name)) locations.set(name, gl.getUniformLocation(program!, name));
    return locations.get(name)!;
  };
  const stop = () => {
    if (stopped) return;
    stopped = true;
    cancelAnimationFrame(frameID);
    window.removeEventListener('resize', resize);
    document.removeEventListener('visibilitychange', visibility);
    canvas.removeEventListener('webglcontextlost', lost);
    signal.removeEventListener('abort', stop);
    canvas.dataset.ready = 'false';
    delete canvas.dataset.frameRate;
    textures.forEach((texture) => gl.deleteTexture(texture));
    buffers.forEach((buffer) => gl.deleteBuffer(buffer));
    gl.deleteProgram(program);
    shaders.forEach((shader) => gl.deleteShader(shader));
  };
  function lost(event: Event) {
    event.preventDefault();
    stop();
  }
  function visibility() {
    cancelAnimationFrame(frameID);
    previous = performance.now();
    reportAt = previous;
    paints = 0;
    cadence.reset();
    if (!document.hidden && !stopped) frameID = requestAnimationFrame(frame);
  }
  function resize() {
    const width = window.innerWidth,
      height = window.innerHeight;
    const density = Math.min(
      window.devicePixelRatio || 1,
      3840 / Math.max(width, height),
      Math.sqrt(8_294_400 / (width * height)),
    );
    if (width === layoutWidth && height === layoutHeight && density === layoutDensity) return;
    layoutWidth = width;
    layoutHeight = height;
    layoutDensity = density;
    canvas.width = Math.max(1, Math.round(width * density));
    canvas.height = Math.max(1, Math.round(height * density));
    gl!.viewport(0, 0, canvas.width, canvas.height);
    const scale = Math.max(width / 16, height / 9);
    const cropX = width / (scale * 16),
      cropY = height / (scale * 9);
    gl!.uniform2f(uniform('crop'), cropX, cropY);
    gl!.uniform2f(uniform('offset'), (1 - cropX) * (width < 640 ? 1 : 0.65), (1 - cropY) * 0.5);
    cadence.reset();
  }
  function draw(
    index: number,
    kind: number,
    rect: Rect,
    opacity = 1,
    tint: [number, number, number] = [1, 1, 1],
    screen = false,
  ) {
    const geometry = kind === 1 || kind === 3 ? mesh : quad;
    if (bound !== geometry) {
      gl!.bindBuffer(gl!.ARRAY_BUFFER, geometry);
      gl!.vertexAttribPointer(point, 2, gl!.FLOAT, false, 0, 0);
      bound = geometry;
    }
    gl!.activeTexture(gl!.TEXTURE0);
    gl!.bindTexture(gl!.TEXTURE_2D, textures[index]);
    gl!.uniform1f(uniform('kind'), kind);
    gl!.uniform1f(uniform('opacity'), opacity);
    gl!.uniform1f(uniform('screenSpace'), screen ? 1 : 0);
    gl!.uniform1f(uniform('planet'), index === 5 ? 1 : 0);
    gl!.uniform4f(uniform('rect'), ...rect);
    gl!.uniform3f(uniform('tint'), ...tint);
    gl!.drawArrays(gl!.TRIANGLES, 0, geometry === mesh ? meshCount : 6);
  }
  function frame(now: number) {
    if (stopped || document.hidden) return;
    elapsed += Math.max(0, Math.min(now - previous, 100));
    previous = now;
    if (cadence.due(now)) {
      const t = elapsed / 1000,
        night = theme === 'dark';
      gl!.uniform1f(uniform('time'), t);
      gl!.uniform1f(uniform('blink'), blinkAt(t));
      gl!.disable(gl!.BLEND);
      draw(0, 0, [0, 0, 1, 1]);
      for (const rect of waterfalls[scene][theme]) draw(0, 6, rect);
      gl!.enable(gl!.BLEND);
      if (night) draw(0, 5, [0, 0, 1, 1], 0.7);
      if (scene === 'celestial') {
        const [moon, earth] = celestialRects(t);
        draw(4, 4, moon);
        draw(5, 4, earth);
      }
      const cloudTint: [number, number, number] = night ? [0.24, 0.38, 0.58] : [1, 1, 0.98];
      // Clouds translate at different depths; their painted contours do not ripple.
      const far = ((t * 0.0028 + 0.08) % 1.15) - 1.15;
      for (const x of [far, far + 1.15]) draw(3, 2, [x, 0.35, 1.2, 0.44], 0.27, cloudTint);
      if (scene === 'wandering') {
        const rect = whaleRect(t, window.innerWidth, window.innerHeight);
        draw(4, 3, rect, 1, night ? [0.55, 0.76, 1] : [1, 1, 1], true);
        canvas.dataset.whaleX = rect[0].toFixed(3);
      }
      const near = ((t * 0.006 + 0.39) % 1.35) - 1.35;
      for (const x of [near, near + 1.35]) draw(3, 2, [x, 0.56, 1.4, 0.42], 0.2, cloudTint);
      draw(1, 1, portraits[scene][theme].rect);
      canvas.dataset.ready = 'true';
      paints++;
      if (now - reportAt >= 2000) {
        canvas.dataset.frameRate = ((paints * 1000) / (now - reportAt)).toFixed(1);
        reportAt = now;
        paints = 0;
      }
    }
    frameID = requestAnimationFrame(frame);
  }
  try {
    if (!program) throw new Error('wallpaper-renderer-unavailable');
    for (const [kind, source] of [
      [gl.VERTEX_SHADER, wallpaperVertex],
      [gl.FRAGMENT_SHADER, wallpaperFragment],
    ] as const) {
      const shader = gl.createShader(kind);
      if (!shader) throw new Error('wallpaper-shader-unavailable');
      shaders.push(shader);
      gl.shaderSource(shader, source);
      gl.compileShader(shader);
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS))
        throw new Error('wallpaper-shader-unavailable');
      gl.attachShader(program, shader);
    }
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS))
      throw new Error('wallpaper-shader-unavailable');
    gl.useProgram(program);
    function upload(vertices: Float32Array) {
      const buffer = gl!.createBuffer();
      if (!buffer) throw new Error('wallpaper-buffer-unavailable');
      buffers.push(buffer);
      gl!.bindBuffer(gl!.ARRAY_BUFFER, buffer);
      gl!.bufferData(gl!.ARRAY_BUFFER, vertices, gl!.STATIC_DRAW);
      return buffer;
    }
    quad = upload(grid(1, 1));
    const vertices = grid(96, 54);
    mesh = upload(vertices);
    meshCount = vertices.length / 2;
    point = gl.getAttribLocation(program, 'point');
    gl.enableVertexAttribArray(point);
    gl.activeTexture(gl.TEXTURE0);
    for (const image of images) {
      const texture = gl.createTexture();
      if (!texture) throw new Error('wallpaper-texture-unavailable');
      textures.push(texture);
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
    }
    gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
    gl.uniform1i(uniform('art'), 0);
    gl.uniform1i(uniform('closed'), 1);
    gl.activeTexture(gl.TEXTURE1);
    gl.bindTexture(gl.TEXTURE_2D, textures[2]);
    gl.uniform4f(uniform('eyes'), ...portraits[scene][theme].eyes);
    gl.uniform2f(uniform('eyeSize'), ...portraits[scene][theme].eyeSize);
    gl.uniform1f(uniform('dark'), theme === 'dark' ? 1 : 0);
    resize();
    window.addEventListener('resize', resize);
    document.addEventListener('visibilitychange', visibility);
    canvas.addEventListener('webglcontextlost', lost);
    signal.addEventListener('abort', stop, { once: true });
    reportAt = previous = performance.now();
    if (!document.hidden) frameID = requestAnimationFrame(frame);
    return stop;
  } catch (error) {
    stop();
    throw error;
  }
}
