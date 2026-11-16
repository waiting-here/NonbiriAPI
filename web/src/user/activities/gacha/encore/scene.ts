import { eyelidProfileWidth } from '@shared/animation/illustration';
import { eyelidShader } from './eyelid-shader';
import { highResolutionArt } from '../art-resolution';
import { encoreEyelids } from './eyelids';
import { encoreLayers, layerPhase, layerWeight } from './layers';
import type { EncoreCard, EncoreLayer } from './layers';
import { paintStageLighting, stageLightingAt } from './stage-lighting';
import { bindEncorePoint, encorePoseAt, posedEncorePoint, type RigBinding } from './rig';

export type SceneOptions = {
  highResolution?: boolean;
  characterOnly?: boolean;
  signal?: AbortSignal;
};
const vertexSource = `
attribute vec4 position;
uniform vec4 rect;
varying vec2 uv,local;
void main(){
 uv=position.zw/vec2(1024.,1536.);local=(position.zw-rect.xy)/rect.zw;
 vec2 p=position.xy/vec2(1024.,1536.);
 gl_Position=vec4(p*vec2(2.,-2.)+vec2(-1.,1.),0.,1.);
}`;

const fragmentSource = `precision highp float;
varying vec2 uv,local;
uniform sampler2D layerArt,art,closed,lids,eyeSkin;
uniform vec4 leftEye,rightEye;
uniform float blink;
uniform vec3 stageTint;
uniform float stageStrength;
${eyelidShader}
void main(){
 vec4 color=texture2D(layerArt,local);
 color=eyelid(uv,leftEye,.125,color);
 color=eyelid(uv,rightEye,.625,color);
 gl_FragColor=vec4(color.rgb+(color.a-color.rgb)*stageTint*stageStrength,color.a);
}`;

function loadImage(src: string, signal: AbortSignal): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image();
    const clear = () => {
      image.onload = image.onerror = null;
      signal.removeEventListener('abort', abort);
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
    signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) abort();
    else image.src = src;
  });
}

export async function createEncoreScene(
  canvas: HTMLCanvasElement,
  id: EncoreCard,
  options: SceneOptions = {},
  failed?: () => void,
) {
  const controller = new AbortController();
  const abort = () => controller.abort();
  options.signal?.addEventListener('abort', abort, { once: true });
  if (options.signal?.aborted) abort();
  const loaded: HTMLImageElement[] = [];
  const source = async (name: string) => {
    const path = '/assets/gacha/motion/' + id + '/' + name + '.webp';
    const image = await loadImage(
      options.highResolution ? highResolutionArt(path) : path,
      controller.signal,
    );
    loaded.push(image);
    return image;
  };
  const layers = encoreLayers[id];
  let images: HTMLImageElement[];
  try {
    images = await Promise.all([
      source('hero'),
      source('closed'),
      source('parts/body'),
      ...layers.map((p) => source('parts/' + p.name)),
      ...(!options.characterOnly ? [source('background')] : []),
      source('eye-skin'),
    ]);
    controller.signal.throwIfAborted();
  } catch (error) {
    controller.abort();
    loaded.forEach((im) => {
      im.src = '';
    });
    throw error;
  } finally {
    options.signal?.removeEventListener('abort', abort);
  }
  const [hero, closed, body] = images;
  const background = options.characterOnly ? undefined : images[3 + layers.length];
  const output = canvas.getContext('2d');
  const surface = document.createElement('canvas');
  const gl = surface.getContext('webgl', {
    alpha: true,
    premultipliedAlpha: true,
    antialias: false,
  });
  if (!output || !gl) {
    gl?.getExtension('WEBGL_lose_context')?.loseContext();
    loaded.forEach((im) => {
      im.src = '';
    });
    throw new Error('renderer-unavailable');
  }
  const textures: WebGLTexture[] = [],
    shaders: WebGLShader[] = [],
    buffers: WebGLBuffer[] = [];
  const program = gl.createProgram();
  let disposed = false;
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    surface.removeEventListener('webglcontextlost', lost);
    textures.forEach((t) => gl.deleteTexture(t));
    shaders.forEach((s) => gl.deleteShader(s));
    buffers.forEach((b) => gl.deleteBuffer(b));
    gl.deleteProgram(program);
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    surface.width = surface.height = 0;
    loaded.forEach((im) => {
      im.src = '';
    });
  };
  const lost = () => {
    output.clearRect(0, 0, canvas.width, canvas.height);
    canvas.style.visibility = 'hidden';
    canvas.dataset.ready = 'false';
    dispose();
    failed?.();
  };
  const uniform = (name: string) => gl.getUniformLocation(program!, name);
  type Mesh = {
    buffer: WebGLBuffer;
    indexBuffer: WebGLBuffer;
    data: Float32Array;
    bindings: RigBinding[];
    weights: number[];
    texture: WebGLTexture;
    count: number;
    rect: readonly [number, number, number, number];
    layer?: EncoreLayer;
  };
  const meshes: Mesh[] = [];
  try {
    if (!program) throw new Error('renderer-unavailable');
    for (const [type, code] of [
      [gl.VERTEX_SHADER, vertexSource],
      [gl.FRAGMENT_SHADER, fragmentSource],
    ] as const) {
      const s = gl.createShader(type);
      if (!s) throw new Error('renderer-unavailable');
      shaders.push(s);
      gl.shaderSource(s, code);
      gl.compileShader(s);
      if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error('shader-unavailable');
      gl.attachShader(program, s);
    }
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error('shader-unavailable');
    gl.useProgram(program);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    const texture = (unit: number, image?: HTMLImageElement) => {
      const t = gl.createTexture();
      if (!t) throw new Error('renderer-unavailable');
      textures.push(t);
      gl.activeTexture(gl.TEXTURE0 + unit);
      gl.bindTexture(gl.TEXTURE_2D, t);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      if (image) gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
      return t;
    };
    texture(4, images[images.length - 1]);
    texture(1, hero);
    texture(2, closed);
    texture(3);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
    const eyes = encoreEyelids[id];
    gl.texImage2D(
      gl.TEXTURE_2D,
      0,
      gl.RGBA,
      eyelidProfileWidth,
      4,
      0,
      gl.RGBA,
      gl.UNSIGNED_BYTE,
      new Uint8Array(eyes.flatMap((p) => p.rows.flat())),
    );
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    for (const [name, unit] of [
      ['layerArt', 0],
      ['art', 1],
      ['closed', 2],
      ['lids', 3],
      ['eyeSkin', 4],
    ] as const)
      gl.uniform1i(uniform(name), unit);
    gl.uniform4f(uniform('leftEye'), ...eyes[0].bounds);
    gl.uniform4f(uniform('rightEye'), ...eyes[1].bounds);
    [body, ...images.slice(3, 3 + layers.length)].forEach((image, index) => {
      const layer = index ? layers[index - 1] : undefined;
      const rect = layer?.rect ?? ([0, 0, 1024, 1536] as const);
      const [x, y, w, h] = rect,
        cols = Math.ceil(w / (layer ? 12 : 16)),
        rows = Math.ceil(h / (layer ? 12 : 16));
      const points: number[] = [];
      const bindings: RigBinding[] = [],
        weights: number[] = [],
        indices: number[] = [];
      for (let row = 0; row <= rows; row++)
        for (let col = 0; col <= cols; col++) {
          const px = x + (col * w) / cols,
            py = y + (row * h) / rows;
          points.push(px, py, px, py);
          bindings.push(bindEncorePoint(id, px, py));
          weights.push(layer ? layerWeight(layer, px, py) : 0);
          if (row < rows && col < cols) {
            const a = row * (cols + 1) + col,
              b = a + 1,
              c = a + cols + 1,
              d = c + 1;
            indices.push(a, b, c, c, b, d);
          }
        }
      const buffer = gl.createBuffer();
      const indexBuffer = gl.createBuffer();
      if (!buffer || !indexBuffer) throw new Error('renderer-unavailable');
      buffers.push(buffer, indexBuffer);
      const data = new Float32Array(points);
      gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
      gl.bufferData(gl.ARRAY_BUFFER, data, gl.DYNAMIC_DRAW);
      gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, indexBuffer);
      gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, new Uint16Array(indices), gl.STATIC_DRAW);
      meshes.push({
        buffer,
        indexBuffer,
        data,
        bindings,
        weights,
        texture: texture(0, image),
        count: indices.length,
        rect,
        layer,
      });
    });
    meshes.sort(
      (a, b) =>
        (a.layer?.depth === 'behind' ? -1 : a.layer ? 1 : 0) -
        (b.layer?.depth === 'behind' ? -1 : b.layer ? 1 : 0),
    );
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
  } catch (error) {
    dispose();
    throw error;
  }
  const attribute = gl.getAttribLocation(program!, 'position');
  const locations = {
    rect: uniform('rect'),
    blink: uniform('blink'),
    stageTint: uniform('stageTint'),
    stageStrength: uniform('stageStrength'),
  };
  surface.addEventListener('webglcontextlost', lost);
  return {
    resize() {
      if (!disposed) {
        surface.width = canvas.width;
        surface.height = canvas.height;
        gl.viewport(0, 0, surface.width, surface.height);
      }
    },
    paint(time: number, blink: number, motion: boolean) {
      if (disposed) return;
      output.clearRect(0, 0, canvas.width, canvas.height);
      output.save();
      output.scale(canvas.width / 1024, canvas.height / 1536);
      const lighting = background && motion ? stageLightingAt(time, id) : undefined;
      if (background) {
        output.drawImage(background, 0, 0, 1024, 1536);
        if (lighting) paintStageLighting(output, lighting);
      }
      if (!motion) output.drawImage(hero, 0, 0, 1024, 1536);
      else {
        const pose = encorePoseAt(id, time);
        gl.clear(gl.COLOR_BUFFER_BIT);
        gl.uniform3f(locations.stageTint, ...(lighting?.tint ?? [0, 0, 0]));
        gl.uniform1f(locations.stageStrength, lighting?.tintStrength ?? 0);
        for (const mesh of meshes) {
          const phase = mesh.layer ? layerPhase(mesh.layer, time) : 0;
          for (let i = 0; i < mesh.bindings.length; i++) {
            const p = posedEncorePoint(mesh.bindings[i], pose),
              weight = mesh.weights[i],
              follow = phase - weight * 0.7;
            mesh.data[i * 4] =
              p[0] + (mesh.layer ? weight * mesh.layer.amplitude[0] * Math.sin(follow) : 0);
            mesh.data[i * 4 + 1] =
              p[1] + (mesh.layer ? weight * mesh.layer.amplitude[1] * Math.sin(follow + 0.4) : 0);
          }
          gl.bindBuffer(gl.ARRAY_BUFFER, mesh.buffer);
          gl.bufferSubData(gl.ARRAY_BUFFER, 0, mesh.data);
          gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, mesh.indexBuffer);
          gl.enableVertexAttribArray(attribute);
          gl.vertexAttribPointer(attribute, 4, gl.FLOAT, false, 16, 0);
          gl.activeTexture(gl.TEXTURE0);
          gl.bindTexture(gl.TEXTURE_2D, mesh.texture);
          gl.uniform4f(locations.rect, ...mesh.rect);
          gl.uniform1f(locations.blink, mesh.layer ? 0 : blink);
          gl.drawElements(gl.TRIANGLES, mesh.count, gl.UNSIGNED_SHORT, 0);
        }
        output.drawImage(surface, 0, 0, 1024, 1536);
      }
      output.restore();
    },
    dispose,
  };
}
