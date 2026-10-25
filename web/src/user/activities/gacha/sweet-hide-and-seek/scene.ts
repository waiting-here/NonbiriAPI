import { highResolutionArt } from '../art-resolution';
import { eyeTextures, faceCrops, profileWidth } from './eyelid';
import { fragmentShader, vertexShader } from './shader';
import { createMotionMesh, motionDrivers, vertexStride } from './motion';
import type { SweetCard } from './motion';

export type SceneOptions = {
  highResolution?: boolean;
  characterOnly?: boolean;
  signal?: AbortSignal;
};
export type SweetScene = {
  paint(time: number, blink: number, motion: boolean): void;
  resize(): void;
  dispose(): void;
};

function loadImage(src: string, signal: AbortSignal): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image();
    const clear = () => {
      signal.removeEventListener('abort', abort);
      image.onload = image.onerror = null;
    };
    const abort = () => {
      clear();
      image.src = '';
      reject(new DOMException('Aborted', 'AbortError'));
    };
    image.onload = () => {
      clear();
      resolve(image);
    };
    image.onerror = () => {
      clear();
      reject(new Error('image-unavailable'));
    };
    if (signal.aborted) {
      abort();
      return;
    }
    signal.addEventListener('abort', abort, { once: true });
    image.src = src;
  });
}

export async function createSweetScene(
  canvas: HTMLCanvasElement,
  id: SweetCard,
  options: SceneOptions = {},
  failed?: () => void,
): Promise<SweetScene> {
  const loading = new AbortController();
  const abortLoading = () => loading.abort();
  options.signal?.addEventListener('abort', abortLoading, { once: true });
  if (options.signal?.aborted) loading.abort();
  const source = (name: string) => {
    const path = `/assets/gacha/motion/${id}/${name}.webp`;
    return loadImage(options.highResolution ? highResolutionArt(path) : path, loading.signal);
  };
  let images: [HTMLImageElement | null, HTMLImageElement, HTMLImageElement, HTMLImageElement];
  try {
    images = await Promise.all([
      options.characterOnly ? null : source('background'),
      source('hero'),
      source('half'),
      source('closed'),
    ]);
  } catch (error) {
    loading.abort();
    throw error;
  } finally {
    options.signal?.removeEventListener('abort', abortLoading);
  }
  if (options.signal?.aborted) throw new DOMException('Aborted', 'AbortError');
  const [background, hero, half, closed] = images;
  if (
    [half, closed].some(
      (image) =>
        image.naturalWidth !== hero.naturalWidth || image.naturalHeight !== hero.naturalHeight,
    )
  ) {
    throw new Error('eye-registration-unavailable');
  }
  const output = canvas.getContext('2d');
  const layer = document.createElement('canvas');
  const gl = layer.getContext('webgl', {
    alpha: true,
    premultipliedAlpha: false,
    antialias: false,
  });
  if (!output || !gl) {
    gl?.getExtension('WEBGL_lose_context')?.loseContext();
    throw new Error('renderer-unavailable');
  }
  const textures: WebGLTexture[] = [],
    shaders: WebGLShader[] = [];
  const program = gl.createProgram(),
    buffer = gl.createBuffer(),
    elements = gl.createBuffer();
  let elementCount = 0;
  let disposed = false;
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    layer.removeEventListener('webglcontextlost', lost);
    textures.forEach((texture) => gl.deleteTexture(texture));
    shaders.forEach((shader) => gl.deleteShader(shader));
    gl.deleteBuffer(buffer);
    gl.deleteBuffer(elements);
    gl.deleteProgram(program);
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    layer.width = layer.height = 0;
    hero.src = '';
    if (background) background.src = '';
  };
  const lost = () => {
    output.clearRect(0, 0, canvas.width, canvas.height);
    // A canvas reused after a generic SSR may have an opaque 2D context.
    // Hide it explicitly so context loss still reveals the outer static art.
    canvas.style.visibility = 'hidden';
    canvas.dataset.ready = 'false';
    dispose();
    failed?.();
  };
  const atlas = document.createElement('canvas');
  try {
    if (!program || !buffer || !elements) throw new Error('renderer-unavailable');
    for (const [type, code] of [
      [gl.VERTEX_SHADER, vertexShader],
      [gl.FRAGMENT_SHADER, fragmentShader()],
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
    const mesh = createMotionMesh(id);
    gl.bufferData(gl.ARRAY_BUFFER, mesh.vertices, gl.STATIC_DRAW);
    for (const [name, size, offset] of [
      ['position', 2, 0],
      ['primary', 4, 2],
      ['secondary', 4, 6],
      ['ribs', 2, 10],
    ] as const) {
      const attribute = gl.getAttribLocation(program, name);
      gl.enableVertexAttribArray(attribute);
      gl.vertexAttribPointer(attribute, size, gl.FLOAT, false, vertexStride * 4, offset * 4);
    }
    gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, elements);
    gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, mesh.indices, gl.STATIC_DRAW);
    elementCount = mesh.indices.length;
    const texture = (name: string, unit: number) => {
      const texture = gl.createTexture();
      if (!texture) throw new Error('renderer-unavailable');
      textures.push(texture);
      gl.activeTexture(gl.TEXTURE0 + unit);
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      gl.uniform1i(gl.getUniformLocation(program, name), unit);
    };
    texture('hero', 0);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, hero);
    const face = faceCrops[id],
      scale = hero.naturalWidth / 1024;
    atlas.width = Math.round(face[2] * scale);
    atlas.height = Math.round(face[3] * scale) * 2;
    const context = atlas.getContext('2d');
    if (!context) throw new Error('renderer-unavailable');
    [half, closed].forEach((image, state) =>
      context.drawImage(
        image,
        face[0] * scale,
        face[1] * scale,
        face[2] * scale,
        face[3] * scale,
        0,
        (atlas.height / 2) * state,
        atlas.width,
        atlas.height / 2,
      ),
    );
    texture('eyeAtlas', 1);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, atlas);
    gl.uniform4f(gl.getUniformLocation(program, 'face'), ...face);
    const data = eyeTextures(id);
    texture('masks', 2);
    gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
    gl.texImage2D(
      gl.TEXTURE_2D,
      0,
      gl.LUMINANCE,
      data.width,
      data.height,
      0,
      gl.LUMINANCE,
      gl.UNSIGNED_BYTE,
      data.mask,
    );
    texture('profiles', 3);
    gl.texImage2D(
      gl.TEXTURE_2D,
      0,
      gl.RGBA,
      profileWidth,
      8,
      0,
      gl.RGBA,
      gl.UNSIGNED_BYTE,
      data.profiles,
    );
    data.eyes.forEach((eye, i) => {
      gl.uniform4f(gl.getUniformLocation(program, `basis${i}`), ...eye.origin, ...eye.axis);
      gl.uniform2f(gl.getUniformLocation(program, `span${i}`), ...eye.span);
      gl.uniform4f(
        gl.getUniformLocation(program, `openHalfRange${i}`),
        ...eye.lashSpans[0],
        ...eye.lashSpans[1],
      );
      gl.uniform2f(gl.getUniformLocation(program, `closedRange${i}`), ...eye.lashSpans[2]);
      const [x0, y0, x1, y1] = eye.maskBox;
      gl.uniform4f(gl.getUniformLocation(program, `box${i}`), x0, y0, x1 - x0, y1 - y0);
      gl.uniform4f(gl.getUniformLocation(program, `placement${i}`), ...data.placements[i]);
    });
  } catch (error) {
    dispose();
    throw error;
  } finally {
    atlas.width = atlas.height = 0;
    // The half/closed bitmaps are needed only for upload, not for animation.
    half.src = closed.src = '';
  }
  const flowUniform = gl.getUniformLocation(program!, 'flow');
  const breathUniform = gl.getUniformLocation(program!, 'breath');
  const drivers = new Float32Array(5);
  const blinkUniform = gl.getUniformLocation(program!, 'blink');
  layer.addEventListener('webglcontextlost', lost);
  return {
    resize() {
      if (disposed) return;
      layer.width = canvas.width;
      layer.height = canvas.height;
      gl.viewport(0, 0, layer.width, layer.height);
    },
    paint(time, blink, motion) {
      if (disposed) throw new Error('renderer-unavailable');
      output.clearRect(0, 0, canvas.width, canvas.height);
      if (background) output.drawImage(background, 0, 0, canvas.width, canvas.height);
      if (motion) {
        motionDrivers(time, drivers);
        gl.uniform4f(flowUniform, drivers[0], drivers[1], drivers[2], drivers[3]);
        gl.uniform1f(breathUniform, drivers[4]);
        gl.uniform1f(blinkUniform, blink);
        gl.clear(gl.COLOR_BUFFER_BIT);
        gl.drawElements(gl.TRIANGLES, elementCount, gl.UNSIGNED_SHORT, 0);
        output.drawImage(layer, 0, 0);
      } else output.drawImage(hero, 0, 0, canvas.width, canvas.height);
    },
    dispose,
  };
}
