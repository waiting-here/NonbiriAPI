import { eyelidProfileWidth, eyelidShader } from '@shared/animation/illustration';
import type { EyelidProfile } from '@shared/animation/illustration';
import { characterRigs, registeredParts, partWeight, partMotion, smooth } from './layers';
import type { MythicID, Point, RegisteredPart } from './layers';
export async function loadMythicImage(src: string, signal?: AbortSignal) {
  return new Promise<HTMLImageElement>((resolve, reject) => {
    const image = new Image();
    const clean = () => {
      image.onload = image.onerror = null;
      signal?.removeEventListener('abort', abort);
    };
    const abort = () => {
      clean();
      image.src = '';
      reject(new DOMException('Aborted', 'AbortError'));
    };
    image.onload = () => {
      clean();
      resolve(image);
    };
    image.onerror = () => {
      clean();
      image.src = '';
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
const vertex = `
attribute vec2 point;
attribute float weight,pose;
varying vec2 uv,localUV;
uniform vec2 size,shift,pelvis;
uniform vec4 rect;
uniform float breath,floatY,turn;
void main(){
 uv=point/size;localUV=(point-rect.xy)/rect.zw;
 vec2 p=point+shift*weight;
 // The upper body follows a single pelvis constraint; there is no limb wave.
 vec2 q=p-pelvis;float a=turn*pose;
 p=pelvis+vec2(q.x*cos(a)-q.y*sin(a),q.x*sin(a)+q.y*cos(a));
 p.y+=breath*pose+floatY;
 gl_Position=vec4(p.x/size.x*2.-1.,1.-p.y/size.y*2.,0.,1.);
}`;
const fragment = `precision highp float;
varying vec2 uv,localUV;
uniform sampler2D art,closed,lids;
uniform vec4 leftEye,rightEye;
uniform float blink,time,metal;
${eyelidShader}
void main(){
 if(localUV.x<0.||localUV.y<0.||localUV.x>1.||localUV.y>1.)discard;
 vec4 color=texture2D(art,localUV);
 color=eyelid(uv,leftEye,.125,color);color=eyelid(uv,rightEye,.625,color);
 float sweep=pow(max(0.,sin(uv.x*27.+uv.y*9.-time*.65)),48.);
 color.rgb+=vec3(.035,.027,.012)*metal*sweep*color.a;
 gl_FragColor=color;
}`;
export async function createMythicCharacter(
  id: MythicID,
  profiles: [EyelidProfile, EyelidProfile],
  options: { highResolution?: boolean; signal?: AbortSignal } = {},
) {
  const card = id === 'l-chatgpt' || id === 'm-deepseek',
    rig = characterRigs[id];
  const prefix =
    '/assets/gacha/' +
    (card
      ? (options.highResolution ? 'hires/motion/' : 'motion/') + id + '/'
      : 'appearances/layers/' + (options.highResolution ? 'hires/' : '') + id + '/');
  const closedPath = card
    ? prefix + 'closed.webp'
    : '/assets/gacha/appearances/layers/' +
      (options.highResolution ? 'hires/' : '') +
      id +
      '-closed.webp';
  const controller = new AbortController(),
    abort = () => controller.abort();
  options.signal?.addEventListener('abort', abort, { once: true });
  if (options.signal?.aborted) abort();
  const definitions = registeredParts(id),
    loaded: HTMLImageElement[] = [];
  let images: HTMLImageElement[];
  try {
    images = await Promise.all(
      [
        closedPath,
        ...definitions.map((p) => prefix + 'parts/' + p.name + '.webp'),
        prefix + 'parts/still.webp',
      ].map(async (path) => {
        const image = await loadMythicImage(path, controller.signal);
        loaded.push(image);
        return image;
      }),
    );
  } catch (error) {
    controller.abort();
    loaded.forEach((image) => (image.src = ''));
    throw error;
  } finally {
    options.signal?.removeEventListener('abort', abort);
  }
  const surface = document.createElement('canvas'),
    gl = surface.getContext('webgl', { alpha: true, premultipliedAlpha: true, antialias: false });
  if (!gl) {
    loaded.forEach((i) => (i.src = ''));
    surface.width = surface.height = 0;
    throw new Error('renderer-unavailable');
  }
  const textures: WebGLTexture[] = [],
    buffers: WebGLBuffer[] = [],
    shaders: WebGLShader[] = [],
    program = gl.createProgram();
  let disposed = false;
  const drawParts: {
    definition: RegisteredPart;
    buffer: WebGLBuffer;
    texture: WebGLTexture;
    count: number;
  }[] = [];
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    textures.forEach((t) => gl.deleteTexture(t));
    buffers.forEach((b) => gl.deleteBuffer(b));
    shaders.forEach((s) => gl.deleteShader(s));
    gl.deleteProgram(program);
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    loaded.forEach((i) => (i.src = ''));
    surface.width = surface.height = 0;
  };
  const uniform = (name: string) => gl.getUniformLocation(program!, name);
  const uniforms = new Map<string, WebGLUniformLocation | null>();
  try {
    if (!program) throw new Error('renderer-unavailable');
    for (const [type, code] of [
      [gl.VERTEX_SHADER, vertex],
      [gl.FRAGMENT_SHADER, fragment],
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
    const texture = (unit: number, image?: TexImageSource) => {
      const value = gl.createTexture();
      if (!value) throw new Error('renderer-unavailable');
      textures.push(value);
      gl.activeTexture(gl.TEXTURE0 + unit);
      gl.bindTexture(gl.TEXTURE_2D, value);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      if (image) gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
      return value;
    };
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    texture(1, images[0]);
    gl.uniform1i(uniform('closed'), 1);
    texture(2);
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
    gl.uniform1i(uniform('lids'), 2);
    gl.uniform4f(uniform('leftEye'), ...profiles[0].bounds);
    gl.uniform4f(uniform('rightEye'), ...profiles[1].bounds);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    gl.uniform1i(uniform('art'), 0);
    gl.uniform2f(uniform('size'), ...rig.size);
    gl.uniform2f(uniform('pelvis'), ...rig.pelvis);
    for (let i = 0; i < definitions.length; i++) {
      const definition = definitions[i],
        [x, y, w, h] = definition.rect,
        columns = Math.max(1, Math.ceil(w / 20)),
        rows = Math.max(1, Math.ceil(h / 20)),
        vertices: number[] = [];
      const p = definition.part;
      const weight = (point: Point) => (p ? partWeight(p, point) : 0);
      for (let iy = 0; iy < rows; iy++)
        for (let ix = 0; ix < columns; ix++) {
          const left = x + (ix * w) / columns,
            right = x + ((ix + 1) * w) / columns,
            top = y + (iy * h) / rows,
            bottom = y + ((iy + 1) * h) / rows;
          for (const point of [
            [left, top],
            [right, top],
            [left, bottom],
            [left, bottom],
            [right, top],
            [right, bottom],
          ] as Point[]) {
            const pose =
              1 -
              smooth(
                (point[1] - rig.shoulder[1] - rig.size[1] * 0.16) /
                  (rig.pelvis[1] - rig.shoulder[1]),
              );
            vertices.push(...point, weight(point), pose);
          }
        }
      const buffer = gl.createBuffer();
      if (!buffer) throw new Error('renderer-unavailable');
      buffers.push(buffer);
      gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
      gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(vertices), gl.STATIC_DRAW);
      drawParts.push({
        definition,
        buffer,
        count: vertices.length / 4,
        texture: texture(0, images[i + 1]),
      });
    }
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
    for (const name of ['rect', 'shift', 'breath', 'floatY', 'turn', 'blink', 'time', 'metal'])
      uniforms.set(name, uniform(name));
  } catch (error) {
    dispose();
    throw error;
  }
  images.slice(0, -1).forEach((image) => {
    image.src = '';
  });
  const attributes = [
    ['point', 2, 0],
    ['weight', 1, 8],
    ['pose', 1, 12],
  ] as const;
  const locations = attributes.map(([name, size, offset]) => ({
    location: gl.getAttribLocation(program!, name),
    size,
    offset,
  }));
  return {
    surface,
    still: images[images.length - 1],
    resize(width: number, height: number) {
      if (disposed) return;
      if (surface.width === width && surface.height === height) return;
      surface.width = width;
      surface.height = height;
      gl.viewport(0, 0, width, height);
    },
    paint(time: number, blink: number, motion = true) {
      if (disposed || gl.isContextLost()) throw new Error('renderer-unavailable');
      gl.clear(gl.COLOR_BUFFER_BIT);
      gl.uniform1f(uniforms.get('time')!, time);
      gl.uniform1f(uniforms.get('breath')!, motion ? -1.15 * Math.sin(time * 1.17) : 0);
      gl.uniform1f(uniforms.get('floatY')!, motion ? rig.floating * Math.sin(time * 0.51) : 0);
      gl.uniform1f(uniforms.get('turn')!, motion ? 0.0012 * Math.sin(time * 0.43) : 0);
      for (const entry of drawParts) {
        const { definition } = entry,
          p = definition.part;
        let shift: Point = motion && p ? partMotion(p, time) : [0, 0];
        if (motion && p?.parent) {
          const parent = [...rig.parts, ...rig.gauze].find((x) => x.name === p.parent);
          if (parent) {
            const d = partMotion(parent, time),
              weight = partWeight(parent, p.root);
            shift = [shift[0] + d[0] * weight, shift[1] + d[1] * weight];
          }
        }
        gl.bindBuffer(gl.ARRAY_BUFFER, entry.buffer);
        for (const a of locations) {
          gl.enableVertexAttribArray(a.location);
          gl.vertexAttribPointer(a.location, a.size, gl.FLOAT, false, 16, a.offset);
        }
        gl.activeTexture(gl.TEXTURE0);
        gl.bindTexture(gl.TEXTURE_2D, entry.texture);
        gl.uniform4f(uniforms.get('rect')!, ...definition.rect);
        gl.uniform2f(uniforms.get('shift')!, ...shift);
        gl.uniform1f(uniforms.get('blink')!, definition.eyes ? blink : 0);
        gl.uniform1f(uniforms.get('metal')!, motion && p?.material === 'jewel' ? 1 : 0);
        gl.drawArrays(gl.TRIANGLES, 0, entry.count);
      }
    },
    dispose,
  };
}
