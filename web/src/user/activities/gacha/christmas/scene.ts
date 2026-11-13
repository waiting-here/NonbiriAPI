import { eyelidProfileWidth, eyelidShader } from '@shared/animation/illustration';
import { highResolutionArt } from '../art-resolution';
import { motionDrivers } from '../sweet-hide-and-seek/motion';
import { createMotionMesh, vertexStride } from './motion';
import type { ChristmasCard } from './motion';
import { christmasEyelids } from './eyelids';

export type SceneOptions = {
  highResolution?: boolean;
  characterOnly?: boolean;
  signal?: AbortSignal;
};
type ChristmasScene = {
  paint(time: number, blink: number, motion: boolean): void;
  resize(): void;
  dispose(): void;
};

const vertexShader = `
attribute vec2 position,ribs,follow;
attribute vec4 primary,secondary;
uniform vec4 flow;
uniform float breath,settle;
varying vec2 uv;
void main(){
  uv=position/vec2(1024.,1536.);
  vec2 offset=primary.xy*flow.x+primary.zw*flow.y+
    secondary.xy*flow.z+secondary.zw*flow.w+ribs*breath+follow*settle;
  vec2 p=(position+offset)/vec2(1024.,1536.);
  gl_Position=vec4(p.x*2.-1.,1.-p.y*2.,0.,1.);
}
`;
const fragmentShader = `precision highp float;
varying vec2 uv;
uniform sampler2D art,closed,lids;
uniform float blink;
uniform vec4 leftEye,rightEye;
${eyelidShader}
void main(){
  vec4 color=texture2D(art,uv);
  color=eyelid(uv,leftEye,.125,color);
  gl_FragColor=eyelid(uv,rightEye,.625,color);
}`;

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
      image.src = '';
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

export async function createChristmasScene(
  canvas: HTMLCanvasElement,
  id: ChristmasCard,
  options: SceneOptions = {},
  failed?: () => void,
): Promise<ChristmasScene> {
  const loading = new AbortController();
  const abortLoading = () => loading.abort();
  options.signal?.addEventListener('abort', abortLoading, { once: true });
  if (options.signal?.aborted) loading.abort();
  const loaded: HTMLImageElement[] = [];
  const source = async (name: string) => {
    const path = '/assets/gacha/motion/' + id + '/' + name + '.webp';
    const image = await loadImage(
      options.highResolution ? highResolutionArt(path) : path,
      loading.signal,
    );
    loaded.push(image);
    return image;
  };
  let images: [HTMLImageElement | null, HTMLImageElement, HTMLImageElement];
  try {
    images = await Promise.all([
      options.characterOnly ? null : source('background'),
      source('hero'),
      source('closed'),
    ]);
    if (options.signal?.aborted) throw new DOMException('Aborted', 'AbortError');
  } catch (error) {
    loading.abort();
    loaded.forEach((image) => {
      image.src = '';
    });
    throw error;
  } finally {
    options.signal?.removeEventListener('abort', abortLoading);
  }
  const [background, hero, closed] = images;
  if (closed.naturalWidth !== hero.naturalWidth || closed.naturalHeight !== hero.naturalHeight) {
    loaded.forEach((image) => {
      image.src = '';
    });
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
  let elementCount = 0,
    disposed = false;
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
    loaded.forEach((image) => {
      image.src = '';
    });
  };
  const lost = () => {
    output.clearRect(0, 0, canvas.width, canvas.height);
    canvas.style.visibility = 'hidden';
    canvas.dataset.ready = 'false';
    dispose();
    failed?.();
  };
  try {
    if (!program || !buffer || !elements) throw new Error('renderer-unavailable');
    for (const [type, code] of [
      [gl.VERTEX_SHADER, vertexShader],
      [gl.FRAGMENT_SHADER, fragmentShader],
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
      ['follow', 2, 12],
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
    texture('art', 0);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, hero);
    texture('closed', 1);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, closed);
    const profiles = christmasEyelids[id];
    texture('lids', 2);
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
    gl.uniform4f(gl.getUniformLocation(program, 'leftEye'), ...profiles[0].bounds);
    gl.uniform4f(gl.getUniformLocation(program, 'rightEye'), ...profiles[1].bounds);
  } catch (error) {
    dispose();
    throw error;
  } finally {
    closed.src = '';
  }
  const flow = gl.getUniformLocation(program!, 'flow'),
    breath = gl.getUniformLocation(program!, 'breath'),
    settle = gl.getUniformLocation(program!, 'settle'),
    blinkUniform = gl.getUniformLocation(program!, 'blink');
  const drivers = new Float32Array(6);
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
      if (!motion) {
        output.drawImage(hero, 0, 0, canvas.width, canvas.height);
        return;
      }
      motionDrivers(time, drivers);
      gl.uniform4f(flow, drivers[0], drivers[1], drivers[2], drivers[3]);
      gl.uniform1f(breath, drivers[4]);
      gl.uniform1f(settle, drivers[5]);
      gl.uniform1f(blinkUniform, blink);
      gl.clear(gl.COLOR_BUFFER_BIT);
      gl.drawElements(gl.TRIANGLES, elementCount, gl.UNSIGNED_SHORT, 0);
      output.drawImage(layer, 0, 0);
    },
    dispose,
  };
}
