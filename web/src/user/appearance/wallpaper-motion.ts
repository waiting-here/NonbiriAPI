import { portraitFlex } from './wallpaper-portrait';
import { decorationGlints, meteorAccent } from './wallpaper-accents';
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
import { eyelidProfiles, eyelidProfileWidth } from './wallpaper-eyelids';
import { wallpaperFragment, wallpaperVertex } from './wallpaper-shaders';

export type { WallpaperScene, WallpaperTheme } from './wallpaper-animation';
export type WallpaperQuality = 'standard' | 'high';

function grid(columns: number, rows: number, flex?: (p: [number, number]) => [number, number]) {
  const vertices: number[] = [];
  for (let y = 0; y < rows; y++) {
    for (let x = 0; x < columns; x++) {
      const left = x / columns,
        right = (x + 1) / columns;
      const top = y / rows,
        bottom = (y + 1) / rows;
      for (const p of [
        [left, top],
        [right, top],
        [left, bottom],
        [left, bottom],
        [right, top],
        [right, bottom],
      ] as [number, number][])
        vertices.push(...p, ...(flex?.(p) ?? [0, 0]));
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
  if (signal.aborted) return () => {};
  const prefix = '/assets/gacha/appearances/layers/' + (quality === 'high' ? 'hires/' : '');
  const id = `${scene}-${theme}` as const;
  const names = [
    id + '-background',
    id + '-hero',
    id + '-closed',
    'cloud',
    ...(scene === 'wandering'
      ? ['whale']
      : ['moon', 'earth', id + '-foreground', ...(theme === 'light' ? [id + '-clouds'] : [])]),
  ];
  const images = await Promise.all(
    names.map(async (name) => {
      const image = new Image();
      const revision = name.endsWith('-hero') || name.endsWith('-closed') ? '?v=4' : '';
      const directory =
        name.endsWith('-foreground') || name.endsWith('-clouds')
          ? '/assets/gacha/appearances/layers/'
          : prefix;
      image.src = directory + name + '.webp' + revision;
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
  // Wallpapers are always page backgrounds.
  const cadence = new PaintCadence(undefined, true);
  let frameID = 0,
    stopped = false,
    elapsed = 0,
    previous = performance.now();
  let quad: WebGLBuffer,
    mesh: WebGLBuffer,
    portraitMesh: WebGLBuffer,
    meshCount = 0;
  let bound: WebGLBuffer | undefined,
    point = 0,
    flex = 0;
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
    const geometry = kind === 1 ? portraitMesh : kind === 3 ? mesh : quad;
    if (bound !== geometry) {
      gl!.bindBuffer(gl!.ARRAY_BUFFER, geometry);
      gl!.vertexAttribPointer(point, 2, gl!.FLOAT, false, 16, 0);
      gl!.vertexAttribPointer(flex, 2, gl!.FLOAT, false, 16, 8);
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
    gl!.drawArrays(gl!.TRIANGLES, 0, geometry === quad ? 6 : meshCount);
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
      gl!.enable(gl!.BLEND);
      if (night) draw(0, 5, [0, 0, 1, 1], 0.7);
      if (scene === 'celestial') {
        const meteor = night ? meteorAccent(t) : undefined;
        if (meteor) draw(0, 8, meteor.rect, meteor.opacity);
        const [moon, earth] = celestialRects(t);
        draw(4, 4, moon);
        draw(5, 4, earth);
        if (!night) draw(0, 11, [0, 0, 1, 1]);
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
      if (scene === 'celestial') {
        draw(0, 7, [0, 0, 1, 1]);
        for (const accent of decorationGlints(t, theme)) draw(0, 9, accent.rect, accent.opacity);
      }
      draw(1, 1, portraits[scene][theme].rect);
      if (scene === 'celestial') draw(0, 10, [0, 0, 1, 1]);
      canvas.dataset.scene = id;
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
    const vertices = grid(128, 72);
    mesh = upload(vertices);
    meshCount = vertices.length / 4;
    portraitMesh = upload(grid(128, 72, (p) => portraitFlex(id, p)));
    point = gl.getAttribLocation(program, 'point');
    gl.enableVertexAttribArray(point);
    flex = gl.getAttribLocation(program, 'flex');
    gl.enableVertexAttribArray(flex);
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
    const profiles = eyelidProfiles[id];
    gl.uniform4f(uniform('eyeBox1'), ...profiles[0].bounds);
    gl.uniform4f(uniform('eyeBox2'), ...profiles[1].bounds);
    const contours = gl.createTexture();
    if (!contours) throw new Error('wallpaper-texture-unavailable');
    textures.push(contours);
    gl.activeTexture(gl.TEXTURE2);
    gl.bindTexture(gl.TEXTURE_2D, contours);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texImage2D(
      gl.TEXTURE_2D,
      0,
      gl.RGBA,
      eyelidProfileWidth,
      4,
      0,
      gl.RGBA,
      gl.UNSIGNED_BYTE,
      new Uint8Array(profiles.flatMap((profile) => profile.rows.flat())),
    );
    gl.uniform1i(uniform('lids'), 2);
    gl.activeTexture(gl.TEXTURE3);
    gl.bindTexture(gl.TEXTURE_2D, textures[scene === 'celestial' ? 6 : 0]);
    gl.uniform1i(uniform('foreground'), 3);
    gl.activeTexture(gl.TEXTURE4);
    gl.bindTexture(gl.TEXTURE_2D, textures[scene === 'celestial' && theme === 'light' ? 7 : 0]);
    gl.uniform1i(uniform('cloudMatte'), 4);
    gl.uniform1f(uniform('celestial'), scene === 'celestial' ? 1 : 0);
    gl.uniform1f(uniform('dark'), theme === 'dark' ? 1 : 0);
    // Flow is part of the painted scenery, so it shares that scenery's depth.
    const water = waterfalls[scene][theme];
    gl.uniform1i(uniform('waterfallCount'), water.length);
    gl.uniform4fv(
      uniform('waterfallRects[0]'),
      new Float32Array([...water.flat(), ...Array((4 - water.length) * 4).fill(0)]),
    );
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
