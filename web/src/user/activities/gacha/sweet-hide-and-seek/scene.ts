import type { MomentExpansion } from '../moment-frame';
import { highResolutionArt } from '../art-resolution';
import { eyeTextures, faceCrops, profileWidth } from './eyelid';
import { fragmentShader, vertexShader } from './shader';
import { createMotionMesh, motionDrivers, vertexStride } from './motion';
import type { SweetCard } from './motion';
import { sweetLayers, layerRect } from './layers';
import type { SweetLayer, LayerRect } from './layers';
import { createSweetEnvironment } from './environment';

export type SceneOptions = {
  highResolution?: boolean;
  characterOnly?: boolean;
  momentFrame?: MomentExpansion;
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
  const frame =
    options.characterOnly && options.momentFrame?.mode === 'expanded-layer'
      ? options.momentFrame
      : undefined;
  if (frame && (!frame.hero || !frame.half || !frame.closed))
    throw new Error('eye-registration-unavailable');
  const layers = sweetLayers(id, frame);
  const loaded: HTMLImageElement[] = [];
  const loading = new AbortController();
  const abortLoading = () => loading.abort();
  options.signal?.addEventListener('abort', abortLoading, { once: true });
  if (options.signal?.aborted) loading.abort();
  const source = (name: string) => {
    const registered =
      name === 'hero'
        ? frame?.hero
        : name === 'half'
          ? frame?.half
          : name === 'closed'
            ? frame?.closed
            : undefined;
    const path = registered
      ? '/assets/gacha/' + registered
      : '/assets/gacha/' +
        (frame && name.startsWith('parts/') ? 'moment/' : 'motion/') +
        id +
        '/' +
        name +
        '.webp';
    return loadImage(options.highResolution ? highResolutionArt(path) : path, loading.signal).then(
      (image) => {
        loaded.push(image);
        return image;
      },
    );
  };
  let images: (HTMLImageElement | null)[];
  try {
    images = await Promise.all([
      options.characterOnly ? null : source('background'),
      source('hero'),
      source('half'),
      source('closed'),
      ...layers.map((layer) => source('parts/' + layer.name)),
    ]);
  } catch (error) {
    loading.abort();
    loaded.forEach((image) => {
      image.src = '';
    });
    throw error;
  } finally {
    options.signal?.removeEventListener('abort', abortLoading);
  }
  if (options.signal?.aborted) {
    loaded.forEach((image) => {
      image.src = '';
    });
    throw new DOMException('Aborted', 'AbortError');
  }
  const background = images[0],
    hero = images[1]!,
    half = images[2]!,
    closed = images[3]!;
  if (
    [half, closed].some(
      (image) =>
        image.naturalWidth !== hero.naturalWidth || image.naturalHeight !== hero.naturalHeight,
    )
  ) {
    loaded.forEach((image) => {
      image.src = '';
    });
    throw new Error('eye-registration-unavailable');
  }
  const output = canvas.getContext('2d');
  const layer = document.createElement('canvas');
  const gl = layer.getContext('webgl', {
    alpha: true,
    premultipliedAlpha: true,
    antialias: false,
  });
  if (!output || !gl) {
    gl?.getExtension('WEBGL_lose_context')?.loseContext();
    loaded.forEach((image) => {
      image.src = '';
    });
    throw new Error('renderer-unavailable');
  }
  const textures: WebGLTexture[] = [],
    shaders: WebGLShader[] = [];
  const program = gl.createProgram(),
    buffer = gl.createBuffer(),
    elements = gl.createBuffer();
  const buffers: WebGLBuffer[] = [];
  if (buffer) buffers.push(buffer);
  if (elements) buffers.push(elements);
  const drawLayers: {
    definition: SweetLayer;
    rect: LayerRect;
    buffer: WebGLBuffer;
    elements: WebGLBuffer;
    texture: WebGLTexture;
    count: number;
  }[] = [];
  let environment: ReturnType<typeof createSweetEnvironment> | null = null;
  let disposed = false;
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    layer.removeEventListener('webglcontextlost', lost);
    textures.forEach((texture) => gl.deleteTexture(texture));
    shaders.forEach((shader) => gl.deleteShader(shader));
    buffers.forEach((value) => gl.deleteBuffer(value));
    gl.deleteProgram(program);
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    layer.width = layer.height = 0;
    environment?.dispose();
    loaded.forEach((image) => {
      image.src = '';
    });
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
    environment = background ? createSweetEnvironment(background, id) : null;
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
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
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
      return texture;
    };
    texture('hero', 0);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, hero);
    const face = faceCrops[id],
      scale = hero.naturalWidth / (frame?.canvas[0] ?? 1024);
    atlas.width = Math.round(face[2] * scale);
    atlas.height = Math.round(face[3] * scale) * 2;
    const context = atlas.getContext('2d');
    if (!context) throw new Error('renderer-unavailable');
    [half, closed].forEach((image, state) =>
      context.drawImage(
        image,
        (face[0] + (frame?.originalRegion.x ?? 0)) * scale,
        (face[1] + (frame?.originalRegion.y ?? 0)) * scale,
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
    gl.uniform2f(
      gl.getUniformLocation(program, 'sourceOffset'),
      frame?.originalRegion.x ?? 0,
      frame?.originalRegion.y ?? 0,
    );
    gl.uniform2f(
      gl.getUniformLocation(program, 'sourceCanvas'),
      frame?.canvas[0] ?? 1024,
      frame?.canvas[1] ?? 1536,
    );
    const data = eyeTextures(id);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
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
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    layers.forEach((definition, i) => {
      const rect = layerRect(definition, frame);
      const mesh = createMotionMesh(id, frame, {
        rect,
        materials: definition.material ? [definition.material] : [],
        fixed: definition.rest || definition.protectAnatomy ? undefined : [],
        breathing: !definition.fixed,
      });
      if (definition.rigid) {
        const pivot = definition.rigid.pivot;
        const parent = createMotionMesh(id, frame, {
          rect: [pivot[0], pivot[1], 1, 1],
          materials: [],
        });
        for (let p = 0; p < mesh.vertices.length; p += vertexStride) {
          mesh.vertices.fill(0, p + 2, p + vertexStride);
          mesh.vertices.set(parent.vertices.subarray(10, 14), p + 10);
        }
      }
      const vertexBuffer = i === 0 ? buffer : gl.createBuffer(),
        indexBuffer = i === 0 ? elements : gl.createBuffer();
      if (i > 0) {
        if (vertexBuffer) buffers.push(vertexBuffer);
        if (indexBuffer) buffers.push(indexBuffer);
      }
      if (!vertexBuffer || !indexBuffer) throw new Error('renderer-unavailable');
      gl.bindBuffer(gl.ARRAY_BUFFER, vertexBuffer);
      gl.bufferData(gl.ARRAY_BUFFER, mesh.vertices, gl.STATIC_DRAW);
      gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, indexBuffer);
      gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, mesh.indices, gl.STATIC_DRAW);
      const image = images[i + 4]!;
      const partTexture = texture('piece', 4);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
      image.src = '';
      drawLayers.push({
        definition,
        rect,
        buffer: vertexBuffer,
        elements: indexBuffer,
        texture: partTexture,
        count: mesh.indices.length,
      });
    });
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
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
  const settleUniform = gl.getUniformLocation(program!, 'settle');
  const drivers = new Float32Array(6);
  const blinkUniform = gl.getUniformLocation(program!, 'blink');
  const artRectUniform = gl.getUniformLocation(program!, 'artRect'),
    pivotUniform = gl.getUniformLocation(program!, 'pivot'),
    rotationUniform = gl.getUniformLocation(program!, 'rotation'),
    translationUniform = gl.getUniformLocation(program!, 'translation');
  const attributes = (
    [
      ['position', 2, 0],
      ['primary', 4, 2],
      ['secondary', 4, 6],
      ['ribs', 2, 10],
      ['follow', 2, 12],
    ] as const
  ).map(([name, size, offset]) => ({
    location: gl.getAttribLocation(program!, name),
    size,
    offset,
  }));
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
        environment?.paint(output, time, canvas.width, canvas.height);
        motionDrivers(time, drivers);
        gl.uniform4f(flowUniform, drivers[0], drivers[1], drivers[2], drivers[3]);
        gl.uniform1f(breathUniform, drivers[4]);
        gl.uniform1f(settleUniform, drivers[5]);
        gl.clear(gl.COLOR_BUFFER_BIT);
        for (const part of drawLayers) {
          gl.bindBuffer(gl.ARRAY_BUFFER, part.buffer);
          for (const a of attributes) {
            gl.enableVertexAttribArray(a.location);
            gl.vertexAttribPointer(
              a.location,
              a.size,
              gl.FLOAT,
              false,
              vertexStride * 4,
              a.offset * 4,
            );
          }
          gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, part.elements);
          gl.activeTexture(gl.TEXTURE4);
          gl.bindTexture(gl.TEXTURE_2D, part.texture);
          gl.uniform4f(artRectUniform, ...part.rect);
          gl.uniform1f(blinkUniform, part.definition.rest ? blink : 0);
          gl.uniform2f(pivotUniform, ...(part.definition.rigid?.pivot ?? [0, 0]));
          const rigid = part.definition.rigid;
          gl.uniform1f(
            rotationUniform,
            rigid
              ? rigid.angle *
                  (Math.sin((time * Math.PI * 2) / rigid.period) * 0.74 +
                    Math.sin(((time * Math.PI * 2) / rigid.period) * 0.47) * 0.26)
              : 0,
          );
          gl.uniform2f(translationUniform, 0, 0);
          gl.drawElements(gl.TRIANGLES, part.count, gl.UNSIGNED_SHORT, 0);
        }
        output.drawImage(layer, 0, 0);
      } else output.drawImage(hero, 0, 0, canvas.width, canvas.height);
    },
    dispose,
  };
}
