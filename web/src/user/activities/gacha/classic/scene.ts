import { eyelidProfileWidth, eyelidShader } from '@shared/animation/illustration';
import { highResolutionArt } from '../art-resolution';
import { cardEyelids } from '../card-eyelids';
import type { MomentExpansion } from '../moment-frame';
import { parts } from './parts';
import { createMesh } from './mesh';
import { placementAt } from './placement';
import { createScenery, environments } from './scenery';
import type { ClassicOptions } from './renderer';
import type { ClassicCard, Layer, RigidMotion } from './types';

const vertexSource = `
attribute vec4 vertex;
uniform vec4 artRect, frameRect;
uniform vec2 outputSize, amplitude, pivot, displacement;
uniform float mode, phase, lag, turn, breath;
varying vec2 uv, original;
varying float ribbonDistance;
void main() {
  vec2 point = vertex.xy;
  ribbonDistance = vertex.w;
  uv = (point - artRect.xy) / artRect.zw;
  original = point / vec2(1024.0, 1536.0);
  if (mode > 3.5) {
    float trailing = 1.0 - uv.x;
    float tail = max(0.0, (trailing - 0.33) / 0.67);
    float acrossFin = trailing - 0.48;
    float fins = sin(phase - 0.32) * exp(-(acrossFin * acrossFin) / 0.041) * 0.044;
    point.y += sin(phase - tail * 2.2) * tail * tail * 12.0
        + fins * artRect.w * (uv.y - 0.28);
    point = mat2(cos(turn), sin(turn), -sin(turn), cos(turn)) * point + displacement;
  } else if (mode > 2.5) {
    vec2 relative = point - pivot;
    point = pivot + mat2(cos(turn), sin(turn), -sin(turn), cos(turn)) * relative + displacement;
  } else if (mode > 1.5) {
    point += vec2(amplitude.x * vertex.w, amplitude.y) * vertex.z * breath;
  } else if (mode > 0.5) {
    float follow = phase - lag * vertex.w;
    point += vertex.z * amplitude * vec2(sin(follow), sin(follow + 0.57));
  }
  vec2 clip = (point * frameRect.zw + frameRect.xy) / outputSize;
  gl_Position = vec4(clip * vec2(2.0, -2.0) + vec2(-1.0, 1.0), 0.0, 1.0);
}
`;
const fragmentSource = `
precision highp float;
varying vec2 uv, original;
varying float ribbonDistance;
uniform sampler2D layerArt, art, closed, lids;
uniform float blink, ribbonPulse, ribbonClock;
uniform vec4 leftEye, rightEye;
${eyelidShader}
void main() {
  vec4 color = texture2D(layerArt, uv);
  float ripple = .32 + .68 * pow(.5 + .5 * sin(ribbonDistance * 31.4 - ribbonClock), 5.0);
  color.rgb = min(color.rgb * (1.0 + ripple * ribbonPulse), vec3(color.a));
  color = eyelid(original, leftEye, .125, color);
  gl_FragColor = eyelid(original, rightEye, .625, color);
}
`;

function available<T>(resource: T | null): T {
  if (resource === null) throw new Error('renderer-unavailable');
  return resource;
}

function decode(src: string, signal: AbortSignal): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image();
    const clear = () => {
      image.onload = image.onerror = null;
      signal.removeEventListener('abort', abort);
    };
    const abort = () => {
      clear();
      image.src = '';
      reject(signal.reason);
    };
    image.onload = () => {
      clear();
      resolve(image);
    };
    image.onerror = () => {
      clear();
      reject(new Error('image-unavailable'));
    };
    signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) abort();
    else image.src = src;
  });
}

type PropLight = {
  center: readonly [number, number];
  radius: readonly [number, number];
  period: number;
  color: string;
};

// Glints stay in the visible glass or stone, outside the finger silhouettes.
const propLights: Partial<Record<ClassicCard, PropLight>> = {
  'g-deepseek': { center: [31, 380], radius: [28, 59], period: 6.4, color: '#bfecff' },
  'g-gemini': { center: [265, 525], radius: [16, 34], period: 6.9, color: '#fff3c0' },
  'i-glm': { center: [240, 472], radius: [51, 49], period: 7.5, color: '#e6f2ff' },
  'i-deepseek': { center: [432, 361], radius: [50, 45], period: 5.7, color: '#caeaff' },
  'i-gemini': { center: [862, 343], radius: [27, 29], period: 6.4, color: '#fff1cc' },
  'j-glm': { center: [370, 299], radius: [31, 37], period: 7.2, color: '#c3d6ff' },
  'j-deepseek': { center: [161, 334], radius: [24, 24], period: 4.8, color: '#e4d8ff' },
  'j-gemini': { center: [271, 393], radius: [33, 42], period: 7.5, color: '#fff2c8' },
};

function paintPropLight(
  context: CanvasRenderingContext2D,
  card: ClassicCard,
  time: number,
  frame?: MomentExpansion,
  attachment?: RigidMotion,
) {
  const light = propLights[card];
  if (!light) return;
  const { center, radius, period } = light;
  const phase = (time * Math.PI * 2) / period;
  const placement = attachment ? placementAt(attachment, time) : undefined;
  context.save();
  context.scale(
    context.canvas.width / (frame?.canvas[0] ?? 1024),
    context.canvas.height / (frame?.canvas[1] ?? 1536),
  );
  context.translate(frame?.originalRegion.x ?? 0, frame?.originalRegion.y ?? 0);
  if (placement) {
    context.translate(...placement.translation);
    context.rotate(placement.angle);
  }
  context.translate(center[0], center[1]);
  context.scale(radius[0], radius[1]);
  context.beginPath();
  context.arc(0, 0, 1, 0, Math.PI * 2);
  context.clip();
  context.globalCompositeOperation = 'screen';
  context.globalAlpha = 0.17 + 0.18 * (0.5 + Math.sin(phase) * 0.5);
  const offset = 0.47 * Math.sin(phase);
  const glint = context.createRadialGradient(offset, -0.22, 0, offset, -0.22, 0.72);
  glint.addColorStop(0, light.color);
  glint.addColorStop(0.25, light.color);
  glint.addColorStop(1, 'transparent');
  context.fillStyle = glint;
  context.fillRect(-1, -1, 2, 2);
  context.restore();
}
export async function createClassicScene(
  canvas: HTMLCanvasElement,
  id: ClassicCard,
  options: ClassicOptions,
  frame: MomentExpansion | undefined,
  failed: () => void,
) {
  const group = parts[id];
  const layers = frame ? group?.expanded : group?.portrait;
  const environment = options.characterOnly ? undefined : environments[id];
  if (!layers?.length || (frame && !frame.hero)) throw new Error('layers-unavailable');
  const charge = new AbortController();
  const stop = () => charge.abort(options.signal?.reason);
  options.signal?.addEventListener('abort', stop, { once: true });
  if (options.signal?.aborted) stop();
  const loaded: HTMLImageElement[] = [];
  const load = async (path: string) => {
    const url = '/assets/gacha/' + path;
    const image = await decode(
      options.highResolution ? highResolutionArt(url) : url,
      charge.signal,
    );
    loaded.push(image);
    return image;
  };
  const standard = 'motion/' + id + '/';
  const partPath = standard + 'parts/' + (frame ? 'expanded-' : '');
  const portrait = options.characterOnly
    ? (frame?.hero ?? standard + 'hero.webp')
    : 'cards/' + id + '.webp';
  let sources;
  try {
    sources = await Promise.all([
      load(standard + 'hero.webp'),
      load(standard + 'closed.webp'),
      load(portrait),
      Promise.all(layers.map((layer) => load(partPath + layer.name + '.webp'))),
      environment
        ? Promise.all([load(environment.base), ...environment.parts.map((part) => load(part.path))])
        : Promise.resolve(undefined),
    ] as const);
    charge.signal.throwIfAborted();
  } catch (error) {
    charge.abort();
    loaded.forEach((image) => (image.src = ''));
    throw error;
  } finally {
    options.signal?.removeEventListener('abort', stop);
  }
  const [eyeOpen, eyeClosed, fallback, layerArt, sceneryArt] = sources;
  const surface = document.createElement('canvas');
  const gl = surface.getContext('webgl', {
    alpha: true,
    premultipliedAlpha: true,
    antialias: false,
  });
  const out = canvas.getContext('2d');
  if (!gl || !out) {
    gl?.getExtension('WEBGL_lose_context')?.loseContext();
    loaded.forEach((image) => (image.src = ''));
    throw new Error('renderer-unavailable');
  }
  const program = gl.createProgram();
  const textures: WebGLTexture[] = [];
  const shaders: WebGLShader[] = [];
  const buffers: WebGLBuffer[] = [];
  let scenery: ReturnType<typeof createScenery> | undefined;
  let disposed = false;
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    surface.removeEventListener('webglcontextlost', lost);
    scenery?.dispose();
    for (const graphic of textures) gl.deleteTexture(graphic);
    for (const shader of shaders) gl.deleteShader(shader);
    for (const buffer of buffers) gl.deleteBuffer(buffer);
    gl.deleteProgram(program);
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    surface.width = surface.height = 0;
    loaded.forEach((image) => (image.src = ''));
  };
  const lost = () => {
    out.clearRect(0, 0, canvas.width, canvas.height);
    out.drawImage(fallback, 0, 0, canvas.width, canvas.height);
    failed();
  };
  const uniform = (key: string) => gl.getUniformLocation(available(program), key);
  const buffer = (target: number, data: Float32Array | Uint16Array) => {
    const object = available(gl.createBuffer());
    buffers.push(object);
    gl.bindBuffer(target, object);
    gl.bufferData(target, data, gl.STATIC_DRAW);
    return object;
  };
  const texture = (unit: number, image?: HTMLImageElement) => {
    const object = available(gl.createTexture());
    textures.push(object);
    gl.activeTexture(gl.TEXTURE0 + unit);
    gl.bindTexture(gl.TEXTURE_2D, object);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    if (image) gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
    return object;
  };
  const draws: {
    layer: Layer;
    vertices: WebGLBuffer;
    indices: WebGLBuffer;
    count: number;
    art: WebGLTexture;
  }[] = [];
  let swimmer: (typeof draws)[number] | undefined;
  try {
    scenery = environment && sceneryArt ? createScenery(environment, sceneryArt) : undefined;
    const shaderProgram = available(program);
    for (const [type, code] of [
      [gl.VERTEX_SHADER, vertexSource],
      [gl.FRAGMENT_SHADER, fragmentSource],
    ] as const) {
      const shader = available(gl.createShader(type));
      shaders.push(shader);
      gl.shaderSource(shader, code);
      gl.compileShader(shader);
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error('shader-unavailable');
      gl.attachShader(shaderProgram, shader);
    }
    gl.linkProgram(shaderProgram);
    if (!gl.getProgramParameter(shaderProgram, gl.LINK_STATUS))
      throw new Error('shader-unavailable');
    gl.useProgram(shaderProgram);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    texture(1, eyeOpen);
    texture(2, eyeClosed);
    gl.uniform1i(uniform('layerArt'), 0);
    gl.uniform1i(uniform('art'), 1);
    gl.uniform1i(uniform('closed'), 2);
    gl.uniform1i(uniform('lids'), 3);
    texture(3);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
    const profiles = cardEyelids[id]!;
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
    gl.uniform4f(uniform('leftEye'), ...profiles[0].bounds);
    gl.uniform4f(uniform('rightEye'), ...profiles[1].bounds);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    eyeOpen.src = eyeClosed.src = '';
    const upload = (layer: Layer, image: HTMLImageElement) => {
      const mesh = createMesh(layer);
      const draw = {
        layer,
        vertices: buffer(gl.ARRAY_BUFFER, mesh.vertices),
        indices: buffer(gl.ELEMENT_ARRAY_BUFFER, mesh.indices),
        count: mesh.indices.length,
        art: texture(0, image),
      };
      image.src = '';
      return draw;
    };
    for (const [index, layer] of layers.entries()) draws.push(upload(layer, layerArt[index]!));
    const swimmerIndex = environment?.parts.findIndex((part) => part.swim) ?? -1;
    if (environment && sceneryArt && swimmerIndex >= 0) {
      const part = environment.parts[swimmerIndex]!;
      swimmer = upload(
        { name: 'whale', rect: part.rect, swim: part.motion },
        sceneryArt[swimmerIndex + 1]!,
      );
    }
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
  } catch (error) {
    dispose();
    throw error;
  }

  const vertex = gl.getAttribLocation(available(program), 'vertex');
  gl.enableVertexAttribArray(vertex);
  const u = {
    outputSize: uniform('outputSize'),
    frameRect: uniform('frameRect'),
    artRect: uniform('artRect'),
    mode: uniform('mode'),
    phase: uniform('phase'),
    lag: uniform('lag'),
    turn: uniform('turn'),
    breath: uniform('breath'),
    displacement: uniform('displacement'),
    amplitude: uniform('amplitude'),
    pivot: uniform('pivot'),
    blink: uniform('blink'),
    ribbonPulse: uniform('ribbonPulse'),
    ribbonClock: uniform('ribbonClock'),
  };
  const lightLayers: Partial<Record<ClassicCard, string>> = {
    'i-glm': 'lantern-shell',
    'i-gemini': 'star-light-core',
    'j-gemini': 'lantern',
    'g-gemini': 'palm-star',
  };
  const lightBinding = layers.find((part) => part.name === lightLayers[id])?.motion;
  const pilot = id === 'g-chatgpt' && !options.characterOnly;
  gl.uniform4f(
    u.frameRect,
    frame?.originalRegion.x ?? (pilot ? (1024 * 10) / 864 : 0),
    frame?.originalRegion.y ?? (pilot ? (1536 * 72) / 1296 : 0),
    pilot ? 844 / 864 : 1,
    pilot ? 1266 / 1296 : 1,
  );
  gl.uniform2f(u.outputSize, frame?.canvas[0] ?? 1024, frame?.canvas[1] ?? 1536);
  surface.addEventListener('webglcontextlost', lost, { once: true });

  function paintSwimmer(time: number) {
    if (!swimmer || !swimmer.layer.swim || !gl || !out) return;
    const { layer, vertices, indices, count, art } = swimmer;
    const placement = placementAt(swimmer.layer.swim, time);
    gl.clear(gl.COLOR_BUFFER_BIT);
    gl.bindBuffer(gl.ARRAY_BUFFER, vertices);
    gl.vertexAttribPointer(vertex, 4, gl.FLOAT, false, 16, 0);
    gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, indices);
    gl.activeTexture(gl.TEXTURE0);
    gl.bindTexture(gl.TEXTURE_2D, art);
    gl.uniform4f(u.artRect, ...layer.rect);
    gl.uniform1f(u.blink, 0);
    gl.uniform1f(u.ribbonPulse, 0);
    gl.uniform1f(u.mode, 4);
    gl.uniform1f(u.phase, time * 1.72);
    gl.uniform1f(u.turn, placement.angle);
    gl.uniform2f(u.displacement, ...placement.translation);
    gl.drawElements(gl.TRIANGLES, count, gl.UNSIGNED_SHORT, 0);
    // The scenery callback runs inside its native 1024×1536 transform.
    out.drawImage(surface, 0, 0, 1024, 1536);
  }

  return {
    resize() {
      if (surface.width !== canvas.width || surface.height !== canvas.height) {
        surface.width = canvas.width;
        surface.height = canvas.height;
      }
      gl.viewport(0, 0, surface.width, surface.height);
    },
    paint(time: number, blink: number, animate: boolean) {
      out.clearRect(0, 0, canvas.width, canvas.height);
      if (!animate) {
        out.drawImage(fallback, 0, 0, canvas.width, canvas.height);
        return;
      }
      scenery?.paint(out, time, false, () => paintSwimmer(time));
      gl.clear(gl.COLOR_BUFFER_BIT);
      const inhale = (time % 6.4) / 6.4;
      const breath =
        (1 - Math.cos(Math.PI * (inhale < 0.38 ? inhale / 0.38 : (1 - inhale) / 0.62))) / 2;
      gl.uniform1f(u.breath, breath);
      for (const { layer, vertices, indices, count, art } of draws) {
        gl.bindBuffer(gl.ARRAY_BUFFER, vertices);
        gl.vertexAttribPointer(vertex, 4, gl.FLOAT, false, 16, 0);
        gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, indices);
        gl.activeTexture(gl.TEXTURE0);
        gl.bindTexture(gl.TEXTURE_2D, art);
        gl.uniform4f(u.artRect, ...layer.rect);
        gl.uniform1f(u.blink, layer.eyes ? blink : 0);
        const binding = layer.motion;
        const ribbon = binding?.kind === 'mesh' ? binding.ribbon : undefined;
        gl.uniform1f(u.ribbonPulse, ribbon ? 0.29 : 0);
        gl.uniform1f(u.ribbonClock, ribbon ? time * ribbon.frequency : 0);
        if (binding?.kind === 'rigid') {
          const placement = placementAt(binding, time);
          gl.uniform1f(u.mode, 3);
          gl.uniform2f(u.pivot, 0, 0);
          gl.uniform1f(u.turn, placement.angle);
          gl.uniform2f(u.displacement, ...placement.translation);
        } else if (binding?.kind === 'breath') {
          gl.uniform1f(u.mode, 2);
          gl.uniform2f(u.amplitude, binding.amount * 0.3, -binding.amount);
        } else if (binding?.kind === 'mesh') {
          gl.uniform1f(u.mode, 1);
          gl.uniform1f(u.phase, (time * 2 * Math.PI) / binding.period);
          gl.uniform1f(u.lag, binding.delay);
          gl.uniform2f(u.amplitude, ...binding.amplitude);
        } else {
          gl.uniform1f(u.mode, 0);
        }
        gl.drawElements(gl.TRIANGLES, count, gl.UNSIGNED_SHORT, 0);
      }
      out.drawImage(surface, 0, 0);
      paintPropLight(
        out,
        id,
        time,
        frame,
        lightBinding?.kind === 'rigid' ? lightBinding : undefined,
      );
      scenery?.paint(out, time, true);
    },
    dispose,
  };
}
