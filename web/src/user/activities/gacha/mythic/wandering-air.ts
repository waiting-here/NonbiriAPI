import { loadMythicImage } from './character';
import { wanderingAirLayers, type WanderingAirLayer } from './wandering-air-layers';

/** Clouds and flying birds retain separate material boundaries. Coordinates are 1024 × 1536. */
export async function createWanderingAir(
  options: { highResolution?: boolean; signal?: AbortSignal } = {},
) {
  const prefix =
    '/assets/gacha/' + (options.highResolution ? 'hires/' : '') + 'motion/m-deepseek/parts/';
  const controller = new AbortController();
  const abort = () => controller.abort();
  options.signal?.addEventListener('abort', abort, { once: true });
  if (options.signal?.aborted) abort();
  const layers = [
    ...wanderingAirLayers.filter((l) => l.kind === 'cloud'),
    ...wanderingAirLayers.filter((l) => l.kind === 'bird'),
  ];
  const images: HTMLImageElement[] = [];
  try {
    await Promise.all(
      layers.map(async (layer, i) => {
        try {
          images[i] = await loadMythicImage(prefix + layer.path, controller.signal);
        } catch (error) {
          controller.abort();
          throw error;
        }
      }),
    );
    controller.signal.throwIfAborted();
  } catch (error) {
    controller.abort();
    images.forEach((image) => {
      image.src = '';
    });
    throw error;
  } finally {
    options.signal?.removeEventListener('abort', abort);
  }
  const canvas = document.createElement('canvas');
  const gl = canvas.getContext('webgl', {
    alpha: true,
    premultipliedAlpha: true,
    antialias: false,
  });
  if (!gl) {
    images.forEach((image) => {
      image.src = '';
    });
    canvas.width = canvas.height = 0;
    throw new Error('renderer-unavailable');
  }
  const program = gl.createProgram();
  const buffer = gl.createBuffer();
  const textures: WebGLTexture[] = [];
  const shaders: WebGLShader[] = [];
  let disposed = false;
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    images.forEach((image) => {
      image.src = '';
    });
    textures.forEach((texture) => gl.deleteTexture(texture));
    shaders.forEach((shader) => gl.deleteShader(shader));
    gl.deleteBuffer(buffer);
    gl.deleteProgram(program);
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    canvas.width = canvas.height = 0;
  };
  try {
    if (!program || !buffer) throw new Error('renderer-unavailable');
    for (const [type, source] of [
      [
        gl.VERTEX_SHADER,
        `attribute vec2 point;
varying vec2 uv; uniform vec4 rect; uniform vec2 shift;
uniform float flap,phase;
void main(){
  uv=point; vec2 p=rect.xy+point*rect.zw+shift;
  float wing=smoothstep(.1,.46,abs(point.x-.5))*(1.-smoothstep(.33,.68,point.y));
  p.y+=flap*wing*sin(phase+abs(point.x-.5)*.35);
  gl_Position=vec4(p.x/512.-1.,1.-p.y/768.,0.,1.);
}`,
      ],
      [
        gl.FRAGMENT_SHADER,
        `precision mediump float; varying vec2 uv;
uniform sampler2D art; uniform float opacity;
void main(){gl_FragColor=texture2D(art,uv)*opacity;}`,
      ],
    ] as const) {
      const shader = gl.createShader(type);
      if (!shader) throw new Error('renderer-unavailable');
      shaders.push(shader);
      gl.shaderSource(shader, source);
      gl.compileShader(shader);
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error('shader-unavailable');
      gl.attachShader(program, shader);
    }
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error('shader-unavailable');
    gl.useProgram(program);
    const vertices: number[] = [];
    for (let y = 0; y < 8; y++)
      for (let x = 0; x < 12; x++) {
        const l = x / 12,
          r = (x + 1) / 12,
          a = y / 8,
          b = (y + 1) / 8;
        vertices.push(l, a, r, a, l, b, l, b, r, a, r, b);
      }
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(vertices), gl.STATIC_DRAW);
    const point = gl.getAttribLocation(program, 'point');
    gl.enableVertexAttribArray(point);
    gl.vertexAttribPointer(point, 2, gl.FLOAT, false, 0, 0);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
    for (const image of images) {
      const texture = gl.createTexture();
      if (!texture) throw new Error('renderer-unavailable');
      textures.push(texture);
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
      image.src = '';
    }
    gl.uniform1i(gl.getUniformLocation(program, 'art'), 0);
    const rect = gl.getUniformLocation(program, 'rect');
    const shift = gl.getUniformLocation(program, 'shift');
    const flap = gl.getUniformLocation(program, 'flap');
    const phase = gl.getUniformLocation(program, 'phase');
    const opacity = gl.getUniformLocation(program, 'opacity');
    const draw = (
      layer: WanderingAirLayer,
      i: number,
      dx: number,
      dy: number,
      alpha: number,
      wing: number,
      t: number,
    ) => {
      gl.bindTexture(gl.TEXTURE_2D, textures[i]);
      gl.uniform4f(rect, ...layer.rect);
      gl.uniform2f(shift, dx, dy);
      gl.uniform1f(opacity, alpha);
      gl.uniform1f(flap, wing);
      gl.uniform1f(phase, t * (layer.name.includes('gull') ? 3.6 : 2.4) + i * 0.71);
      gl.drawArrays(gl.TRIANGLES, 0, vertices.length / 2);
    };
    return {
      canvas,
      paint(
        ctx: CanvasRenderingContext2D,
        time: number,
        width: number,
        height: number,
        active = true,
      ) {
        if (disposed) return;
        const scale = Math.min(1, 2048 / Math.max(width, height));
        const w = Math.max(1, Math.round(width * scale)),
          h = Math.max(1, Math.round(height * scale));
        if (canvas.width !== w || canvas.height !== h) {
          canvas.width = w;
          canvas.height = h;
          gl.viewport(0, 0, w, h);
        }
        gl.clear(gl.COLOR_BUFFER_BIT);
        const t = active ? time : 0;
        layers.forEach((layer, i) => {
          if (layer.kind === 'cloud') {
            const q = (t % layer.period!) / layer.period!;
            const dissolve = Math.max(0, (q - 0.84) / 0.16);
            const a = dissolve * dissolve * (3 - 2 * dissolve);
            draw(layer, i, layer.extent! * q, 0, 1 - a, 0, t);
            if (a > 0) {
              gl.blendFunc(gl.ONE, gl.ONE);
              draw(layer, i, 0, 0, a, 0, t);
              gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
            }
          } else {
            const margin = Math.max(160, layer.rect[2] + 10),
              span = 1024 + margin * 2;
            const x =
              ((((layer.rect[0] + layer.speed! * t + margin) % span) + span) % span) - margin;
            const dy = active ? 2.4 * (Math.sin(t * 0.19 + i) - Math.sin(i)) : 0;
            draw(layer, i, x - layer.rect[0], dy, 1, active ? layer.flap! : 0, t);
          }
        });
        ctx.drawImage(canvas, 0, 0, 1024, 1536);
      },
      dispose,
    };
  } catch (error) {
    dispose();
    throw error;
  }
}
