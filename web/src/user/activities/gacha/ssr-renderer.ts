import { backdropShader, paintSPAtmosphere, spScenes } from './sp-motion';
import { highResolutionArt } from './art-resolution';

type MotionConfig = {
  background: string;
  hero: string;
  half?: string;
  closed?: string;
  cloud?: string;
  eyes?: [number, number, number, number];
  pilot?: boolean;
};
const base = '/assets/gacha/motion/';
const eyeCenters: Record<string, [number, number, number, number]> = {
  'l-chatgpt': [0.402, 0.161, 0.485, 0.139],
  'm-deepseek': [0.501, 0.131, 0.571, 0.111],
  'g-claude': [0.408, 0.169, 0.505, 0.146],
  'g-glm': [0.506, 0.195, 0.602, 0.165],
  'g-deepseek': [0.291, 0.233, 0.402, 0.253],
  'g-gemini': [0.4, 0.18, 0.501, 0.147],
  'i-chatgpt': [0.464, 0.13, 0.543, 0.111],
  'i-claude': [0.455, 0.111, 0.535, 0.099],
  'i-glm': [0.464, 0.132, 0.535, 0.121],
  'i-deepseek': [0.437, 0.139, 0.515, 0.126],
  'i-gemini': [0.466, 0.112, 0.534, 0.1],
  'j-chatgpt': [0.441, 0.128, 0.52, 0.114],
  'j-claude': [0.454, 0.11, 0.527, 0.1],
  'j-glm': [0.439, 0.112, 0.506, 0.104],
  'j-deepseek': [0.431, 0.161, 0.507, 0.149],
  'j-gemini': [0.448, 0.111, 0.52, 0.1],
};
function configFor(id: string): MotionConfig {
  if (id === 'g-chatgpt')
    return {
      background: base + id + '/garden.webp',
      hero: base + id + '/hero.webp',
      half: base + id + '/half.webp',
      closed: base + id + '/closed.webp',
      cloud: base + id + '/cloud.webp',
      eyes: [0.429, 0.14, 0.522, 0.127],
      pilot: true,
    };
  return {
    background: base + id + '/background.webp',
    hero: base + id + '/hero.webp',
    closed: base + id + '/closed.webp',
    eyes: eyeCenters[id],
  };
}
function load(src: string) {
  return new Promise<HTMLImageElement>((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve(image);
    image.onerror = reject;
    image.src = src;
  });
}
export async function renderSSR(
  canvas: HTMLCanvasElement,
  id: string,
  frozenTime?: number,
  paused?: () => boolean,
  highResolution = false,
) {
  const cfg = configFor(id);
  const scene = spScenes[id];
  const image = (src: string) => load(highResolution ? highResolutionArt(src) : src);
  const [background, hero, half, closed, cloud] = await Promise.all([
    image(cfg.background),
    image(cfg.hero),
    cfg.half ? image(cfg.half) : null,
    cfg.closed ? image(cfg.closed) : null,
    cfg.cloud ? image(cfg.cloud) : null,
  ]);
  const ctx = canvas.getContext('2d', { alpha: false });
  const layer = document.createElement('canvas');
  layer.width = canvas.width;
  layer.height = canvas.height;
  const gl = layer.getContext('webgl', {
    alpha: true,
    premultipliedAlpha: false,
    antialias: false,
  });
  if (!ctx || !gl) throw new Error('renderer-unavailable');
  const program = gl.createProgram();
  if (!program) throw new Error('renderer-unavailable');
  const shaders: WebGLShader[] = [];
  const compile = (type: number, source: string) => {
    const shader = gl.createShader(type);
    if (!shader) throw new Error('renderer-unavailable');
    shaders.push(shader);
    gl.shaderSource(shader, source);
    gl.compileShader(shader);
    if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error('shader-unavailable');
    gl.attachShader(program, shader);
  };
  compile(
    gl.VERTEX_SHADER,
    'attribute vec2 a;varying vec2 uv;void main(){uv=a;gl_Position=vec4(a.x*2.-1.,1.-a.y*2.,0.,1.);}',
  );
  compile(
    gl.FRAGMENT_SHADER,
    `precision highp float;
varying vec2 uv;uniform sampler2D tex0,tex1,tex2,tex3;uniform float phase,blink,hasHalf,strength,scene,backgroundPass;uniform vec4 eyes;uniform vec2 pivot,eyeSize;
float bump(vec2 p,vec2 c,vec2 r){return exp(-dot((p-c)/r,(p-c)/r)*2.);}
float eye(vec2 p,vec2 c){return 1.-smoothstep(.70,1.,length((p-c)/eyeSize));}
${backdropShader}
void main(){
 if(backgroundPass>.5){gl_FragColor=backdrop(uv);return;}
 vec2 p=uv;float s=strength;
 if(scene>.5)p+=vec2(.0035*sin(phase),.0025*sin(phase+.4));float upper=1.-smoothstep(pivot.y-.09,pivot.y+.16,p.y);float angle=.0028*sin(phase)*upper*s;
 vec2 q=p-pivot;p=vec2(q.x*cos(angle)-q.y*sin(angle),q.x*sin(angle)+q.y*cos(angle))+pivot;
 float body=bump(p,pivot+vec2(0.,.08),vec2(.27,.28));p.y+=.0015*sin(phase*2.)*body*s;
 p.x-=(p.x-pivot.x)*.006*sin(phase*2.)*body*s;
 float side=smoothstep(.08,.23,abs(p.x-.50));
 float hair=side*smoothstep(.20,.38,p.y)*(1.-smoothstep(.69,.84,p.y));
 float hand=1.-bump(p,vec2(.87,.33),vec2(.20,.10));
 p.x+=.0058*sin(phase-p.y*6.)*hair*hand*s;p.y+=.0016*cos(phase-p.y*4.)*hair*hand*s;
 p.x+=.004*sin(phase*2.-p.y*5.)*side*smoothstep(.54,.86,p.y)*s;
 vec4 a=texture2D(tex0,p),b=texture2D(tex1,p),c=texture2D(tex2,p);
 float em=max(eye(p,eyes.xy),eye(p,eyes.zw));
 if(hasHalf<.5)b=mix(a,c,.5);
 gl_FragColor=mix(a,blink<.5?mix(a,b,blink*2.):mix(b,c,(blink-.5)*2.),em);
}`,
  );
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error('shader-unavailable');
  gl.useProgram(program);
  const buffer = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
  gl.bufferData(
    gl.ARRAY_BUFFER,
    new Float32Array([0, 0, 1, 0, 0, 1, 0, 1, 1, 0, 1, 1]),
    gl.STATIC_DRAW,
  );
  const attr = gl.getAttribLocation(program, 'a');
  gl.enableVertexAttribArray(attr);
  gl.vertexAttribPointer(attr, 2, gl.FLOAT, false, 0, 0);
  const textures = [hero, half ?? hero, closed ?? hero, background].map((image, i) => {
    const texture = gl.createTexture();
    gl.activeTexture(gl.TEXTURE0 + i);
    gl.bindTexture(gl.TEXTURE_2D, texture);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
    gl.uniform1i(gl.getUniformLocation(program, 'tex' + i), i);
    return texture;
  });
  gl.uniform4f(
    gl.getUniformLocation(program, 'eyes'),
    ...(cfg.eyes ?? ([-1, -1, -1, -1] as [number, number, number, number])),
  );
  const eyeData = cfg.eyes ?? [0.49, 0.14, 0.49, 0.14];
  gl.uniform2f(
    gl.getUniformLocation(program, 'pivot'),
    (eyeData[0] + eyeData[2]) / 2,
    (eyeData[1] + eyeData[3]) / 2 + 0.16,
  );
  gl.uniform2f(
    gl.getUniformLocation(program, 'eyeSize'),
    id.startsWith('g-') ? 0.05 : 0.038,
    id.startsWith('g-') ? 0.025 : 0.021,
  );
  gl.uniform1f(gl.getUniformLocation(program, 'hasHalf'), cfg.half ? 1 : 0);
  gl.uniform1f(
    gl.getUniformLocation(program, 'strength'),
    scene === 'wandering' ? 2.25 : scene ? 2.05 : 1.3,
  );
  gl.uniform1f(gl.getUniformLocation(program, 'scene'), scene === 'celestial' ? 1 : scene ? 2 : 0);
  const backgroundPass = gl.getUniformLocation(program, 'backgroundPass');
  const phase = gl.getUniformLocation(program, 'phase'),
    blink = gl.getUniformLocation(program, 'blink');
  let stopped = false,
    raf = 0,
    time = 0,
    last = performance.now(),
    painted = -Infinity,
    initialized = false;
  const resize = () => {
    const rect = canvas.getBoundingClientRect();
    const width = Math.max(1, rect.width || canvas.width);
    const density = Math.min(window.devicePixelRatio || 1, 2560 / width);
    const nextWidth = Math.round(width * density);
    const nextHeight = Math.round(nextWidth * 1.5);
    if (canvas.width === nextWidth && canvas.height === nextHeight) return;
    canvas.width = nextWidth;
    canvas.height = nextHeight;
    layer.width = canvas.width;
    layer.height = canvas.height;
    gl.viewport(0, 0, layer.width, layer.height);
    painted = -Infinity;
    if (initialized && frozenTime !== undefined) frame(performance.now());
  };
  const observer = new ResizeObserver(resize);
  observer.observe(canvas);
  window.addEventListener('resize', resize);
  resize();
  const smooth = (x: number) => {
    x = Math.max(0, Math.min(1, x));
    return x * x * (3 - 2 * x);
  };
  const blinkAt = (t: number) => {
    let value = 0;
    for (const start of [2.15, 6.55]) {
      const d = t - start;
      if (d >= 0 && d < 0.1) value = Math.max(value, smooth(d / 0.1));
      else if (d >= 0.1 && d < 0.15) value = 1;
      else if (d >= 0.15 && d < 0.32) value = Math.max(value, 1 - smooth((d - 0.15) / 0.17));
    }
    return value;
  };
  function frame(at: number) {
    if (stopped) return;
    const isPaused = paused?.() && frozenTime === undefined;
    if (isPaused && painted !== -Infinity) {
      last = at;
      raf = requestAnimationFrame(frame);
      return;
    }
    time = (frozenTime ?? (isPaused ? time : time + Math.min(at - last, 100) / 1000)) % 10;
    last = at;
    if (frozenTime === undefined && at - painted < 1000 / 30) {
      raf = requestAnimationFrame(frame);
      return;
    }
    painted = at;
    const p = (time / 10) * Math.PI * 2,
      w = canvas.width,
      h = canvas.height;
    gl!.uniform1f(phase, p);
    if (scene) {
      gl!.uniform1f(backgroundPass, 1);
      gl!.drawArrays(gl!.TRIANGLES, 0, 6);
      ctx!.drawImage(layer, 0, 0);
      paintSPAtmosphere(ctx!, scene, p, w, h);
    } else
      ctx!.drawImage(
        background,
        -w * 0.008 + Math.sin(p) * w * 0.003,
        -h * 0.008,
        w * 1.016,
        h * 1.016,
      );
    gl!.uniform1f(phase, p);
    gl!.uniform1f(backgroundPass, 0);
    gl!.uniform1f(blink, blinkAt(time));
    gl!.drawArrays(gl!.TRIANGLES, 0, 6);
    if (cfg.pilot)
      ctx!.drawImage(layer, (w * 10) / 864, (h * 72) / 1296, (w * 844) / 864, (h * 1266) / 1296);
    else ctx!.drawImage(layer, 0, 0);
    if (cloud) {
      for (const [x, y, size, offset] of [
        [0.16, 0.25, 0.13, 0],
        [0.87, 0.285, 0.21, 0.5],
        [0.15, 0.63, 0.2, 2],
      ]) {
        ctx!.save();
        ctx!.translate(
          (x + Math.sin(p + offset) * 0.014) * w,
          (y + Math.cos(p + offset) * 0.009) * h,
        );
        ctx!.rotate(Math.sin(p + offset) * 0.055);
        ctx!.drawImage(cloud, (-size * w) / 2, (-size * w) / 2, size * w, size * w);
        ctx!.restore();
      }
    }
    if (frozenTime === undefined) raf = requestAnimationFrame(frame);
  }
  const visibility = () => {
    cancelAnimationFrame(raf);
    if (!document.hidden && !stopped) {
      last = performance.now();
      if (frozenTime === undefined) raf = requestAnimationFrame(frame);
    }
  };
  document.addEventListener('visibilitychange', visibility);
  initialized = true;
  frame(last);
  return () => {
    stopped = true;
    cancelAnimationFrame(raf);
    document.removeEventListener('visibilitychange', visibility);
    observer.disconnect();
    window.removeEventListener('resize', resize);
    textures.forEach((t) => gl.deleteTexture(t));
    gl.deleteBuffer(buffer);
    gl.deleteProgram(program);
    shaders.forEach((s) => gl.deleteShader(s));
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    layer.width = 0;
    layer.height = 0;
  };
}
