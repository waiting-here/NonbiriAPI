export type WallpaperScene = 'celestial' | 'wandering';
export type WallpaperTheme = 'light' | 'dark';
export type WallpaperQuality = 'standard' | 'high';
type Rect = [number, number, number, number];
type Portrait = { rect: Rect; eyes: Rect };

const portraits: Record<WallpaperScene, Record<WallpaperTheme, Portrait>> = {
  wandering: {
    light: { rect: [0.245, 0.25, 0.8, 0.8], eyes: [0.739, 0.2, 0.775, 0.224] },
    dark: { rect: [0.35, 0.254, 0.75, 0.75], eyes: [0.663, 0.243, 0.714, 0.191] },
  },
  celestial: {
    light: { rect: [0.295, 0.277, 0.72, 0.72], eyes: [0.763, 0.16, 0.798, 0.181] },
    dark: { rect: [0.357, 0.338, 0.675, 0.675], eyes: [0.752, 0.154, 0.796, 0.185] },
  },
};

const fragment = `precision highp float;
varying vec2 uv;
uniform sampler2D art, closed;
uniform vec2 crop, offset;
uniform vec4 rect, eyes;
uniform vec3 tint;
uniform float time, kind, opacity, dark, screenSpace, blink;
float field(vec2 p,vec2 center,vec2 radius) {
  vec2 q=(p-center)/radius;
  return exp(-2.*dot(q,q));
}
float hash(vec2 p) { return fract(sin(dot(p,vec2(127.1,311.7)))*43758.5453); }
void main() {
  vec2 world=uv*crop+offset;
  if(kind<.5) {
    vec2 p=world;
    // Broad water currents move independently of buildings and the portrait.
    float water=field(p,vec2(.37,.76),vec2(.31,.23));
    p.x+=.0018*sin(p.y*56.-time*1.5)*water;
    p.y+=.0007*cos(p.x*45.+time*.7)*water;
    vec3 color=texture2D(art,p).rgb;
    float ripple=sin(p.y*210.-time*2.4+sin(p.x*35.));
    color+=vec3(.011,.021,.027)*ripple*water*(.5+.5*dark);
    gl_FragColor=vec4(color,1.); return;
  }
  if(kind>4.5) {
    vec2 stars=world*vec2(80.,45.)+vec2(time*.11,-time*.024);
    vec2 cell=floor(stars), local=fract(stars)-.5;
    float seed=hash(cell);
    float point=exp(-dot(local,local)*2400.)*step(.965,seed);
    float halo=exp(-dot(local,local)*85.)*step(.991,seed)*.14;
    float visible=(1.-smoothstep(.37,.67,world.y));
    gl_FragColor=vec4(vec3(.66,.84,1.),(point+halo)*visible*(.55+.30*sin(time*.7+seed*45.))*opacity);
    return;
  }
  vec2 p=((screenSpace>.5?uv:world)-rect.xy)/rect.zw;
  if(p.x<0.||p.y<0.||p.x>1.||p.y>1.) discard;
  if(kind<1.5) {
    vec2 face=(eyes.xy+eyes.zw)*.5, pivot=face+vec2(0.,.13);
    float upper=1.-smoothstep(pivot.y-.08,pivot.y+.17,p.y);
    float angle=.006*sin(time*.64)*upper;
    vec2 q=p-pivot;
    p=vec2(q.x*cos(angle)-q.y*sin(angle),q.x*sin(angle)+q.y*cos(angle))+pivot;
    float body=field(p,pivot+vec2(0.,.16),vec2(.13,.27));
    float stillFace=field(p,face,vec2(.050,.080));
    p.y+=.0035*sin(time*1.45)*body*(1.-stillFace);
    float hair=field(p,face+vec2(.09,.16),vec2(.10,.23))
      +field(p,face+vec2(-.07,.16),vec2(.06,.22));
    float fabric=smoothstep(pivot.y+.18,pivot.y+.45,p.y);
    p.x+=(.009*sin(time*.8-p.y*5.)*hair+.008*sin(time*.7-p.y*6.)*fabric)*(1.-stillFace);
    p.y+=.003*cos(time*.8-p.y*5.)*hair*(1.-stillFace);
    vec4 a=texture2D(art,p), b=texture2D(closed,p);
    float eye=max(field(p,eyes.xy,vec2(.026,.024)),field(p,eyes.zw,vec2(.026,.024)));
    float mask=smoothstep(.1,.4,eye);
    gl_FragColor=mix(a,b,blink*mask)*vec4(tint,opacity); return;
  }
  if(kind<2.5) {
    p.y+=.013*sin(time*.3+p.x*6.);
  } else if(kind<3.5) {
    p.y+=.035*sin(time*.85-p.x*5.)*pow(p.x,2.);
    p.x+=.009*sin(time*.85-p.y*5.)*smoothstep(.48,.85,p.y);
  } else {
    vec2 q=p-.5; float angle=.018*sin(time*.07);
    p=vec2(q.x*cos(angle)-q.y*sin(angle),q.x*sin(angle)+q.y*cos(angle))+.5;
  }
  if(p.x<0.||p.y<0.||p.x>1.||p.y>1.) discard;
  gl_FragColor=texture2D(art,p)*vec4(tint,opacity);
}`;

export function wallpaperQuality(): WallpaperQuality {
  return Math.max(window.innerWidth, window.innerHeight) * (window.devicePixelRatio || 1) > 1920
    ? 'high'
    : 'standard';
}

export async function renderWallpaper(
  canvas: HTMLCanvasElement,
  scene: WallpaperScene,
  theme: WallpaperTheme,
  signal: AbortSignal,
  quality: WallpaperQuality = wallpaperQuality(),
): Promise<() => void> {
  const prefix = '/assets/gacha/appearances/layers/' + (quality === 'high' ? 'hires/' : '');
  const id = scene + '-' + theme;
  const names = [
    id + '-background',
    id + '-hero',
    id + '-closed',
    'cloud',
    ...(scene === 'wandering' ? ['whale'] : [theme === 'dark' ? 'crescent' : 'moon', 'earth']),
  ];
  const images = await Promise.all(
    names.map(async (name) => {
      const image = new Image();
      image.src = prefix + name + '.webp';
      await image.decode();
      return image;
    }),
  );
  if (signal.aborted) return () => {};
  const gl = canvas.getContext('webgl', {
    alpha: false,
    antialias: false,
    depth: false,
    stencil: false,
  });
  if (!gl) throw new Error('wallpaper-renderer-unavailable');
  const shaders: WebGLShader[] = [],
    textures: WebGLTexture[] = [];
  const program = gl.createProgram(),
    buffer = gl.createBuffer();
  let frameID = 0,
    stopped = false,
    elapsed = 0,
    previous = performance.now(),
    painted = -Infinity;
  const locations = new Map<string, WebGLUniformLocation | null>();
  const uniform = (name: string) => {
    if (!locations.has(name)) locations.set(name, gl.getUniformLocation(program!, name));
    return locations.get(name)!;
  };
  const stop = () => {
    if (stopped) return;
    stopped = true;
    cancelAnimationFrame(frameID);
    window.removeEventListener('resize', resize);
    document.removeEventListener('visibilitychange', visibility);
    canvas.removeEventListener('webglcontextlost', lost);
    signal.removeEventListener('abort', stop);
    canvas.dataset.ready = 'false';
    textures.forEach((texture) => gl.deleteTexture(texture));
    gl.deleteBuffer(buffer);
    gl.deleteProgram(program);
    shaders.forEach((shader) => gl.deleteShader(shader));
  };
  function lost(event: Event) {
    event.preventDefault();
    stop();
  }
  function visibility() {
    cancelAnimationFrame(frameID);
    previous = performance.now();
    if (!document.hidden && !stopped) frameID = requestAnimationFrame(frame);
  }
  function resize() {
    const width = window.innerWidth,
      height = window.innerHeight;
    const density = Math.min(
      window.devicePixelRatio || 1,
      3840 / Math.max(width, height),
      Math.sqrt(8_294_400 / (width * height)),
    );
    canvas.width = Math.max(1, Math.round(width * density));
    canvas.height = Math.max(1, Math.round(height * density));
    gl!.viewport(0, 0, canvas.width, canvas.height);
    const scale = Math.max(width / 16, height / 9);
    const cropX = width / (scale * 16),
      cropY = height / (scale * 9);
    gl!.uniform2f(uniform('crop'), cropX, cropY);
    gl!.uniform2f(uniform('offset'), (1 - cropX) * (width < 640 ? 1 : 0.65), (1 - cropY) * 0.5);
    painted = -Infinity;
  }
  function draw(
    index: number,
    kind: number,
    rect: Rect,
    opacity = 1,
    tint: [number, number, number] = [1, 1, 1],
    screen = false,
  ) {
    gl!.activeTexture(gl!.TEXTURE0);
    gl!.bindTexture(gl!.TEXTURE_2D, textures[index]);
    gl!.uniform1f(uniform('kind'), kind);
    gl!.uniform1f(uniform('opacity'), opacity);
    gl!.uniform1f(uniform('screenSpace'), screen ? 1 : 0);
    gl!.uniform4f(uniform('rect'), ...rect);
    gl!.uniform3f(uniform('tint'), ...tint);
    gl!.drawArrays(gl!.TRIANGLES, 0, 6);
  }
  function blinkAt(t: number) {
    const cycle = t % 11.3;
    for (const start of [2.1, 7.7]) {
      const d = cycle - start;
      if (d >= 0 && d < 0.1) return Math.sin(((d / 0.1) * Math.PI) / 2);
      if (d >= 0.1 && d < 0.16) return 1;
      if (d >= 0.16 && d < 0.34) return Math.cos((((d - 0.16) / 0.18) * Math.PI) / 2);
    }
    return 0;
  }
  function frame(now: number) {
    if (stopped || document.hidden) return;
    elapsed += Math.min(now - previous, 100);
    previous = now;
    if (now - painted >= 1000 / 30) {
      const t = elapsed / 1000,
        night = theme === 'dark';
      gl!.uniform1f(uniform('time'), t);
      gl!.uniform1f(uniform('blink'), blinkAt(t));
      gl!.disable(gl!.BLEND);
      draw(0, 0, [0, 0, 1, 1]);
      gl!.enable(gl!.BLEND);
      if (night) draw(0, 5, [0, 0, 1, 1], 0.8);
      if (scene === 'celestial') {
        draw(
          4,
          4,
          [0.64 + 0.009 * Math.sin(t * 0.075), -0.065 + 0.012 * Math.sin(t * 0.09), 0.31, 0.55],
          0.92,
        );
        draw(
          5,
          4,
          [0.83 + 0.025 * Math.cos(t * 0.06), 0.05 + 0.028 * Math.sin(t * 0.06), 0.105, 0.187],
          0.95,
        );
      }
      const cloudTint: [number, number, number] = night ? [0.27, 0.4, 0.61] : [1, 1, 0.98];
      const far = ((t * 0.0025 + 0.08) % 1.15) - 1.15;
      for (const x of [far, far + 1.15]) draw(3, 2, [x, 0.38, 1.2, 0.53], 0.3, cloudTint);
      if (scene === 'wandering') {
        // Wrap only after the complete creature has travelled outside both edges.
        const width = window.innerWidth < 640 ? 1.55 : 0.78;
        const x = 1.1 - ((t / 84 + 0.19) % 1) * (2.2 + width);
        const h = (((width * window.innerWidth) / window.innerHeight) * 9) / 16;
        draw(
          4,
          3,
          [x, 0.025 + 0.025 * Math.sin(t * 0.15), width, h],
          0.89,
          night ? [0.55, 0.76, 1] : [1, 1, 1],
          true,
        );
        canvas.dataset.whaleX = x.toFixed(3);
      }
      const near = ((t * 0.005 + 0.39) % 1.35) - 1.35;
      for (const x of [near, near + 1.35]) draw(3, 2, [x, 0.6, 1.4, 0.4], 0.22, cloudTint);
      gl!.activeTexture(gl!.TEXTURE1);
      gl!.bindTexture(gl!.TEXTURE_2D, textures[2]);
      draw(1, 1, portraits[scene][theme].rect);
      canvas.dataset.ready = 'true';
      painted = now;
    }
    frameID = requestAnimationFrame(frame);
  }
  try {
    if (!program || !buffer) throw new Error('wallpaper-renderer-unavailable');
    for (const [kind, source] of [
      [
        gl.VERTEX_SHADER,
        'attribute vec2 point;varying vec2 uv;void main(){uv=point;gl_Position=vec4(point.x*2.-1.,1.-point.y*2.,0.,1.);}',
      ],
      [gl.FRAGMENT_SHADER, fragment],
    ] as const) {
      const shader = gl.createShader(kind);
      if (!shader) throw new Error('wallpaper-shader-unavailable');
      shaders.push(shader);
      gl.shaderSource(shader, source);
      gl.compileShader(shader);
      gl.attachShader(program, shader);
    }
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS))
      throw new Error('wallpaper-shader-unavailable');
    gl.useProgram(program);
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(
      gl.ARRAY_BUFFER,
      new Float32Array([0, 0, 1, 0, 0, 1, 0, 1, 1, 0, 1, 1]),
      gl.STATIC_DRAW,
    );
    const point = gl.getAttribLocation(program, 'point');
    gl.enableVertexAttribArray(point);
    gl.vertexAttribPointer(point, 2, gl.FLOAT, false, 0, 0);
    gl.activeTexture(gl.TEXTURE0);
    for (const image of images) {
      const texture = gl.createTexture();
      if (!texture) throw new Error('wallpaper-texture-unavailable');
      textures.push(texture);
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
    }
    gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
    gl.uniform1i(uniform('art'), 0);
    gl.uniform1i(uniform('closed'), 1);
    gl.activeTexture(gl.TEXTURE1);
    gl.bindTexture(gl.TEXTURE_2D, textures[2]);
    gl.uniform4f(uniform('eyes'), ...portraits[scene][theme].eyes);
    gl.uniform1f(uniform('dark'), theme === 'dark' ? 1 : 0);
    resize();
    window.addEventListener('resize', resize);
    document.addEventListener('visibilitychange', visibility);
    canvas.addEventListener('webglcontextlost', lost);
    signal.addEventListener('abort', stop, { once: true });
    if (!document.hidden) frameID = requestAnimationFrame(frame);
    return stop;
  } catch (error) {
    stop();
    throw error;
  }
}
