import { blinkAt, PaintCadence, eyelidProfileWidth } from '@shared/animation/illustration';
import { highResolutionArt } from '../art-resolution';
import { amberEyelids } from './eyelids';
import { amberShader, characterShader, foregroundShader } from './shader';
import { characterVertex, createRigMesh, poseAt } from './rig';
import { polishedAmberEyelids } from './polished-eyelids';
import { amberLayout } from './layout';

type Options = {
  polished?: boolean;
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
  variant?: 'card' | 'cover' | 'illustration' | 'appearance';
  characterPlate?: boolean;
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

export async function renderAmber(canvas: HTMLCanvasElement, options: Options = {}) {
  const eyelids = options.polished ? polishedAmberEyelids : amberEyelids;
  const base = '/assets/gacha/motion/o-claude/';
  const source = (name: string) => {
    if (options.polished && (name === 'hero-original' || name === 'hero-closed')) {
      return image(
        '/assets/gacha/motion/o-claude/' + (name === 'hero-original' ? 'hero' : 'closed') + '.png',
        options.signal,
      );
    }
    const url = base + name + '.webp';
    return image(options.highResolution ? highResolutionArt(url) : url, options.signal);
  };
  const [background, hero, closed, books, boots, flags, lamps] = await Promise.all([
    options.characterOnly ? null : source(options.dark ? 'background-dark' : 'background-light'),
    source('hero-original'),
    source('hero-closed'),
    options.characterOnly ? null : source('books'),
    image(base + 'boots.webp', options.signal),
    options.characterOnly ? null : source('flags'),
    options.characterOnly ? null : source('lamps'),
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
  const programs: WebGLProgram[] = [],
    buffers: WebGLBuffer[] = [];
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
    buffers.forEach((buffer) => gl.deleteBuffer(buffer));
    programs.forEach((program) => gl.deleteProgram(program));
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    layer.width = 0;
    layer.height = 0;
  };
  function compile(vertex: string, fragment: string) {
    const program = gl!.createProgram();
    if (!program) throw new Error('renderer-unavailable');
    programs.push(program);
    for (const [type, code] of [
      [gl!.VERTEX_SHADER, vertex],
      [gl!.FRAGMENT_SHADER, fragment],
    ] as const) {
      const shader = gl!.createShader(type);
      if (!shader) throw new Error('renderer-unavailable');
      shaders.push(shader);
      gl!.shaderSource(shader, code);
      gl!.compileShader(shader);
      if (!gl!.getShaderParameter(shader, gl!.COMPILE_STATUS))
        throw new Error('shader-unavailable: ' + gl!.getShaderInfoLog(shader));
      gl!.attachShader(program, shader);
    }
    gl!.linkProgram(program);
    if (!gl!.getProgramParameter(program, gl!.LINK_STATUS))
      throw new Error('shader-unavailable: ' + gl!.getProgramInfoLog(program));
    return program;
  }
  function makeBuffer(data: Float32Array) {
    const buffer = gl!.createBuffer();
    if (!buffer) throw new Error('renderer-unavailable');
    buffers.push(buffer);
    gl!.bindBuffer(gl!.ARRAY_BUFFER, buffer);
    gl!.bufferData(gl!.ARRAY_BUFFER, data, gl!.STATIC_DRAW);
    return buffer;
  }
  let sceneProgram: WebGLProgram, heroProgram: WebGLProgram, foregroundProgram: WebGLProgram;
  let sceneBuffer: WebGLBuffer, heroBuffer: WebGLBuffer;
  let vertexCount: number;
  try {
    sceneProgram = compile(
      'attribute vec2 a;varying vec2 uv;void main(){uv=a;gl_Position=vec4(a.x*2.-1.,1.-a.y*2.,0.,1.);}',
      amberShader,
    );
    heroProgram = compile(characterVertex, characterShader);
    foregroundProgram = compile(
      'attribute vec2 a;varying vec2 uv;void main(){uv=a;gl_Position=vec4(a.x*2.-1.,1.-a.y*2.,0.,1.);}',
      foregroundShader,
    );
    sceneBuffer = makeBuffer(new Float32Array([0, 0, 1, 0, 0, 1, 0, 1, 1, 0, 1, 1]));
    const mesh = createRigMesh();
    heroBuffer = makeBuffer(mesh);
    vertexCount = mesh.length / 10;
    for (const [i, img] of [
      background ?? hero,
      hero,
      closed,
      null,
      books,
      boots,
      flags,
      lamps,
    ].entries()) {
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
      else if (i === 3)
        gl.texImage2D(
          gl.TEXTURE_2D,
          0,
          gl.RGBA,
          eyelidProfileWidth,
          4,
          0,
          gl.RGBA,
          gl.UNSIGNED_BYTE,
          new Uint8Array(eyelids.flatMap((profile) => profile.rows.flat())),
        );
      else
        gl.texImage2D(
          gl.TEXTURE_2D,
          0,
          gl.RGBA,
          1,
          1,
          0,
          gl.RGBA,
          gl.UNSIGNED_BYTE,
          new Uint8Array(4),
        );
      for (const program of programs) {
        gl.useProgram(program);
        gl.uniform1i(
          gl.getUniformLocation(
            program,
            ['scenery', 'art', 'closed', 'lids', 'books', 'boots', 'flags', 'lamps'][i],
          ),
          i,
        );
      }
    }
    for (const program of programs) {
      gl.useProgram(program);
      gl.uniform4f(gl.getUniformLocation(program, 'leftEye'), ...eyelids[0].bounds);
      gl.uniform4f(gl.getUniformLocation(program, 'rightEye'), ...eyelids[1].bounds);
      gl.uniform1f(gl.getUniformLocation(program, 'night'), options.dark ? 1 : 0);
      gl.uniform1f(gl.getUniformLocation(program, 'polish'), options.polished ? 1 : 0);
      gl.uniform1f(gl.getUniformLocation(program, 'landscape'), options.landscape ? 1 : 0);
      gl.uniform1f(gl.getUniformLocation(program, 'characterOnly'), options.characterOnly ? 1 : 0);
      gl.uniform1f(
        gl.getUniformLocation(program, 'characterPlate'),
        options.characterPlate ? 1 : 0,
      );
      gl.uniform1f(
        gl.getUniformLocation(program, 'backgroundOnly'),
        options.backgroundOnly ? 1 : 0,
      );
    }
  } catch (error) {
    dispose();
    throw error;
  }
  const timeUniform = gl.getUniformLocation(sceneProgram, 'time'),
    blinkUniform = gl.getUniformLocation(heroProgram, 'blink'),
    bonesUniform = gl.getUniformLocation(heroProgram, 'bones');
  const sceneAttribute = gl.getAttribLocation(sceneProgram, 'a');
  const foregroundAttribute = gl.getAttribLocation(foregroundProgram, 'a');
  const foregroundTime = gl.getUniformLocation(foregroundProgram, 'time');
  const heroAttributes = ['position', 'joints', 'weights'].map((name) =>
    gl.getAttribLocation(heroProgram, name),
  );
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
    const { view, heroBox } = options.characterPlate
      ? { view: [0, 0, 1, 1], heroBox: [0, 0, 1, 1516 / 1560] }
      : amberLayout(
          width,
          height,
          !!options.landscape,
          !!options.polished,
          options.variant === 'appearance',
        );
    for (const program of programs) {
      gl.useProgram(program);
      gl.uniform4fv(gl.getUniformLocation(program, 'view'), view);
      gl.uniform4fv(gl.getUniformLocation(program, 'heroBox'), heroBox);
      gl.uniform1f(gl.getUniformLocation(program, 'aspect'), width / height);
    }
    painted = false;
    cadence.reset();
    if (options.frozenTime !== undefined) frame(performance.now());
  };
  function frame(at: number) {
    if (stopped || options.signal?.aborted) return;
    const paused = options.paused?.() ?? false;
    if (!paused) time += Math.min(at - last, 100) / 1000;
    last = at;
    if ((!painted || !paused) && (options.frozenTime !== undefined || cadence.due(at))) {
      const t = options.frozenTime ?? time;
      heroAttributes.forEach((attribute) => gl!.disableVertexAttribArray(attribute));
      gl!.useProgram(sceneProgram);
      gl!.bindBuffer(gl!.ARRAY_BUFFER, sceneBuffer);
      gl!.enableVertexAttribArray(sceneAttribute);
      gl!.vertexAttribPointer(sceneAttribute, 2, gl!.FLOAT, false, 0, 0);
      gl!.disable(gl!.BLEND);
      gl!.uniform1f(timeUniform, t);
      gl!.drawArrays(gl!.TRIANGLES, 0, 6);
      gl!.disableVertexAttribArray(sceneAttribute);
      gl!.useProgram(heroProgram);
      gl!.bindBuffer(gl!.ARRAY_BUFFER, heroBuffer);
      for (const [i, attribute] of heroAttributes.entries()) {
        gl!.enableVertexAttribArray(attribute);
        gl!.vertexAttribPointer(attribute, i === 0 ? 2 : 4, gl!.FLOAT, false, 40, [0, 8, 24][i]);
      }
      gl!.uniform1f(blinkUniform, options.blink ?? blinkAt(t));
      gl!.uniformMatrix3fv(bonesUniform, false, poseAt(t));
      gl!.enable(gl!.BLEND);
      gl!.blendFuncSeparate(
        gl!.SRC_ALPHA,
        gl!.ONE_MINUS_SRC_ALPHA,
        gl!.ONE,
        gl!.ONE_MINUS_SRC_ALPHA,
      );
      if (!options.backgroundOnly) gl!.drawArrays(gl!.TRIANGLES, 0, vertexCount);
      if (!options.characterOnly) {
        heroAttributes.forEach((attribute) => gl!.disableVertexAttribArray(attribute));
        gl!.useProgram(foregroundProgram);
        gl!.bindBuffer(gl!.ARRAY_BUFFER, sceneBuffer);
        gl!.enableVertexAttribArray(foregroundAttribute);
        gl!.vertexAttribPointer(foregroundAttribute, 2, gl!.FLOAT, false, 0, 0);
        gl!.uniform1f(foregroundTime, t);
        gl!.drawArrays(gl!.TRIANGLES, 0, 6);
        gl!.disableVertexAttribArray(foregroundAttribute);
      }
      painted = true;
      context!.clearRect(0, 0, canvas.width, canvas.height);
      context!.imageSmoothingQuality = 'high';
      context!.drawImage(layer, 0, 0, canvas.width, canvas.height);
      canvas.dataset.ready = 'true';
      canvas.dataset.time = t.toFixed(3);
      canvas.dataset.blink = (options.blink ?? blinkAt(t)).toFixed(3);
    }
    if (options.frozenTime === undefined && !document.hidden) raf = requestAnimationFrame(frame);
  }
  function visibility() {
    cancelAnimationFrame(raf);
    if (!document.hidden && !stopped && options.frozenTime === undefined) {
      last = performance.now();
      cadence.reset();
      raf = requestAnimationFrame(frame);
    }
  }
  observer.observe(canvas);
  document.addEventListener('visibilitychange', visibility);
  options.signal?.addEventListener('abort', dispose, { once: true });
  resize();
  if (options.frozenTime === undefined) frame(last);
  return dispose;
}
