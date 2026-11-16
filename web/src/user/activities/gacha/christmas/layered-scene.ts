import { eyelidProfileWidth, eyelidShader } from '@shared/animation/illustration';
import { highResolutionArt } from '../art-resolution';
import { motionDrivers } from '../sweet-hide-and-seek/motion';
import { createMotionMesh, vertexStride, meshColumns, meshStep } from './motion';
import type { ChristmasCard, MotionMesh, Point } from './motion';
import { christmasRigs } from './rigs';
import { christmasEyelids } from './eyelids';
import { renderLayers } from './layers';
import type { ArtLayer } from './layers';
import { createChristmasEnvironment, paintChristmasDetail } from './environment';

export type SceneOptions = {
  highResolution?: boolean;
  characterOnly?: boolean;
  signal?: AbortSignal;
};
const vertexShader = `
attribute vec2 position,ribs,follow;
attribute vec4 primary,secondary;
uniform vec4 flow,artRect;
uniform float breath,settle,rotation;
uniform vec2 pivot,translation;
varying vec2 uv,localUV;
void main(){
 uv=position/vec2(1024.,1536.);localUV=(position-artRect.xy)/artRect.zw;
 vec2 offset=primary.xy*flow.x+primary.zw*flow.y+secondary.xy*flow.z+secondary.zw*flow.w+ribs*breath+follow*settle;
 float c=cos(rotation),s=sin(rotation);vec2 q=position-pivot;
 vec2 p=(pivot+vec2(c*q.x-s*q.y,s*q.x+c*q.y)+offset+translation)/vec2(1024.,1536.);
 gl_Position=vec4(p.x*2.-1.,1.-p.y*2.,0.,1.);
}`;
const fragmentShader = `precision highp float;
varying vec2 uv,localUV;
uniform sampler2D art,closed,lids;
uniform float blink,time,metal;
uniform vec4 leftEye,rightEye;
${eyelidShader}
void main(){
 if(localUV.x<0.||localUV.y<0.||localUV.x>1.||localUV.y>1.)discard;
 vec4 color=texture2D(art,localUV);
 color=eyelid(uv,leftEye,.125,color);color=eyelid(uv,rightEye,.625,color);
 vec3 rgb=color.rgb/max(color.a,.001);
 float gold=smoothstep(.08,.20,rgb.g-rgb.b)*smoothstep(.43,.72,rgb.g);
 float silver=metal*(1.-smoothstep(.03,.14,max(rgb.r,max(rgb.g,rgb.b))-min(rgb.r,min(rgb.g,rgb.b))))*smoothstep(.55,.85,rgb.g);
 float sweep=pow(max(0.,sin(uv.x*21.+uv.y*7.-time*.65)),36.);
 color.rgb+=vec3(.042,.035,.022)*max(gold,silver)*sweep*color.a;
 gl_FragColor=color;
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
function sample(mesh: MotionMesh, [x, y]: Point) {
  const col = Math.min(meshColumns - 2, Math.max(0, Math.floor(x / meshStep))),
    row = Math.min(1536 / meshStep - 1, Math.max(0, Math.floor(y / meshStep)));
  const u = x / meshStep - col,
    v = y / meshStep - row,
    a = row * meshColumns + col,
    b = a + 1,
    c = a + meshColumns,
    d = c + 1;
  const ids = u + v <= 1 ? [a, b, c] : [c, b, d],
    weights = u + v <= 1 ? [1 - u - v, u, v] : [1 - u, 1 - v, u + v - 1];
  return Array.from({ length: vertexStride - 2 }, (_, k) =>
    ids.reduce(
      (sum, index, i) => sum + mesh.vertices[index * vertexStride + k + 2] * weights[i],
      0,
    ),
  );
}

export async function createChristmasScene(
  canvas: HTMLCanvasElement,
  id: ChristmasCard,
  options: SceneOptions = {},
  failed?: () => void,
) {
  const controller = new AbortController(),
    abort = () => controller.abort();
  options.signal?.addEventListener('abort', abort, { once: true });
  if (options.signal?.aborted) abort();
  const loaded: HTMLImageElement[] = [],
    layers = renderLayers(id);
  const source = async (name: string) => {
    const path = '/assets/gacha/motion/' + id + '/' + name + '.webp';
    const image = await loadImage(
      options.highResolution ? highResolutionArt(path) : path,
      controller.signal,
    );
    loaded.push(image);
    return image;
  };
  let images: HTMLImageElement[];
  try {
    images = await Promise.all([
      ...(!options.characterOnly ? [source('background')] : []),
      source('hero'),
      source('closed'),
      ...layers.map((layer) => source('parts/' + layer.name)),
      ...(!options.characterOnly ? [source('parts/window-mask'), source('parts/lamp-mask')] : []),
    ]);
    if (options.signal?.aborted) throw new DOMException('Aborted', 'AbortError');
  } catch (error) {
    controller.abort();
    loaded.forEach((image) => {
      image.src = '';
    });
    throw error;
  } finally {
    options.signal?.removeEventListener('abort', abort);
  }
  const background = options.characterOnly ? null : images.shift()!;
  const hero = images.shift()!,
    closed = images.shift()!,
    sprites = images.splice(0, layers.length);
  if (hero.naturalWidth !== closed.naturalWidth || hero.naturalHeight !== closed.naturalHeight) {
    loaded.forEach((image) => {
      image.src = '';
    });
    throw new Error('eye-registration-unavailable');
  }
  const output = canvas.getContext('2d'),
    surface = document.createElement('canvas');
  const gl = surface.getContext('webgl', {
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
  const environment = background ? createChristmasEnvironment(images[0], images[1]) : null;
  const textures: WebGLTexture[] = [],
    shaders: WebGLShader[] = [],
    buffers: WebGLBuffer[] = [];
  const program = gl.createProgram();
  let disposed = false;
  type DrawLayer = {
    definition: ArtLayer;
    vertices: WebGLBuffer;
    indices: WebGLBuffer;
    count: number;
    texture: WebGLTexture;
  };
  const drawLayers: DrawLayer[] = [];
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    surface.removeEventListener('webglcontextlost', lost);
    environment?.dispose();
    textures.forEach((t) => gl.deleteTexture(t));
    shaders.forEach((s) => gl.deleteShader(s));
    buffers.forEach((b) => gl.deleteBuffer(b));
    gl.deleteProgram(program);
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    surface.width = surface.height = 0;
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
  const uniform = (name: string) => gl.getUniformLocation(program!, name);
  try {
    if (!program) throw new Error('renderer-unavailable');
    for (const [type, code] of [
      [gl.VERTEX_SHADER, vertexShader],
      [gl.FRAGMENT_SHADER, fragmentShader],
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
      for (const [p, v] of [
        [gl.TEXTURE_MIN_FILTER, gl.LINEAR],
        [gl.TEXTURE_MAG_FILTER, gl.LINEAR],
        [gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE],
        [gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE],
      ])
        gl.texParameteri(gl.TEXTURE_2D, p, v);
      if (image) gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
      return t;
    };
    texture(1, closed);
    gl.uniform1i(uniform('closed'), 1);
    closed.src = '';
    const profiles = christmasEyelids[id];
    texture(2);
    // Profile channels encode geometry, not RGBA color. Never premultiply them.
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
    gl.texImage2D(
      gl.TEXTURE_2D,
      0,
      gl.RGBA,
      eyelidProfileWidth,
      4,
      0,
      gl.RGBA,
      gl.UNSIGNED_BYTE,
      new Uint8Array(profiles.flatMap((p) => p.rows.flat())),
    );
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    gl.uniform1i(uniform('lids'), 2);
    gl.uniform4f(uniform('leftEye'), ...profiles[0].bounds);
    gl.uniform4f(uniform('rightEye'), ...profiles[1].bounds);
    gl.uniform1i(uniform('art'), 0);
    for (let i = 0; i < layers.length; i++) {
      const definition = layers[i],
        mesh = createMotionMesh(id, {
          region: definition.region,
          rect: definition.rect,
          fixed: definition.fixed,
        });
      if (definition.rigid) {
        const parent = christmasRigs[id].materials.find((r) => r.name === definition.rigid!.parent);
        const weights = sample(createMotionMesh(id, { region: parent }), definition.rigid.pivot);
        for (let p = 0; p < mesh.vertices.length; p += vertexStride)
          mesh.vertices.set(weights, p + 2);
      }
      const vertices = gl.createBuffer();
      if (!vertices) throw new Error('renderer-unavailable');
      buffers.push(vertices);
      const indices = gl.createBuffer();
      if (!indices) throw new Error('renderer-unavailable');
      buffers.push(indices);
      gl.bindBuffer(gl.ARRAY_BUFFER, vertices);
      gl.bufferData(gl.ARRAY_BUFFER, mesh.vertices, gl.STATIC_DRAW);
      gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, indices);
      gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, mesh.indices, gl.STATIC_DRAW);
      drawLayers.push({
        definition,
        vertices,
        indices,
        count: mesh.indices.length,
        texture: texture(0, sprites[i]),
      });
      sprites[i].src = '';
    }
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
  } catch (error) {
    dispose();
    throw error;
  }
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
  const uniforms = Object.fromEntries(
    [
      'flow',
      'breath',
      'settle',
      'blink',
      'artRect',
      'pivot',
      'rotation',
      'translation',
      'time',
      'metal',
    ].map((name) => [name, uniform(name)]),
  );
  const drivers = new Float32Array(6);
  surface.addEventListener('webglcontextlost', lost);
  return {
    resize() {
      if (disposed) return;
      surface.width = canvas.width;
      surface.height = canvas.height;
      gl.viewport(0, 0, surface.width, surface.height);
    },
    paint(time: number, blink: number, motion: boolean) {
      if (disposed) throw new Error('renderer-unavailable');
      output.clearRect(0, 0, canvas.width, canvas.height);
      if (background) output.drawImage(background, 0, 0, canvas.width, canvas.height);
      if (!motion) {
        output.drawImage(hero, 0, 0, canvas.width, canvas.height);
        return;
      }
      environment?.paint(output, time, canvas.width, canvas.height);
      motionDrivers(time, drivers);
      gl.uniform4f(uniforms.flow, drivers[0], drivers[1], drivers[2], drivers[3]);
      gl.uniform1f(uniforms.breath, drivers[4]);
      gl.uniform1f(uniforms.settle, drivers[5]);
      gl.uniform1f(uniforms.time, time);
      gl.clear(gl.COLOR_BUFFER_BIT);
      for (const layer of drawLayers) {
        const d = layer.definition;
        gl.bindBuffer(gl.ARRAY_BUFFER, layer.vertices);
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
        gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, layer.indices);
        gl.activeTexture(gl.TEXTURE0);
        gl.bindTexture(gl.TEXTURE_2D, layer.texture);
        gl.uniform4f(uniforms.artRect, ...d.rect);
        gl.uniform1f(uniforms.blink, d.name === 'body' ? blink : 0);
        const p = d.rigid?.pivot ?? [0, 0];
        gl.uniform2f(uniforms.pivot, p[0], p[1]);
        gl.uniform1f(
          uniforms.rotation,
          d.rigid ? d.rigid.radians * Math.sin((time * Math.PI * 2) / d.rigid.period) : 0,
        );
        gl.uniform2f(
          uniforms.translation,
          d.floating ? d.floating[0] * Math.sin(time * 0.61) : 0,
          d.floating ? d.floating[1] * Math.cos(time * 0.73) : 0,
        );
        gl.uniform1f(uniforms.metal, d.rigid ? 1 : 0);
        gl.drawElements(gl.TRIANGLES, layer.count, gl.UNSIGNED_SHORT, 0);
      }
      output.drawImage(surface, 0, 0);
      paintChristmasDetail(output, id, time, canvas.width, canvas.height);
    },
    dispose,
  };
}
