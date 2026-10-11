export type WallpaperScene = 'celestial' | 'wandering';
export type WallpaperTheme = 'light' | 'dark';

const fragment = `precision highp float;
varying vec2 uv;
uniform sampler2D art;
uniform vec2 crop, offset, face;
uniform float phase, scene, dark;
float field(vec2 p,vec2 center,vec2 radius) {
  vec2 q=(p-center)/radius;
  return exp(-2.*dot(q,q));
}
void main() {
  vec2 original=uv*crop+offset;
  vec2 p=original;
  float hero=field(p,face+vec2(.015,.16),vec2(.15,.36));
  float portrait=field(p,face,vec2(.047,.066));
  float body=field(p,face+vec2(0.,.20),vec2(.095,.20));
  float hair=field(p,face+vec2(.054,.13),vec2(.07,.17));
  hair+=field(p,face+vec2(-.047,.13),vec2(.043,.15));
  float cloth=field(p,face+vec2(.005,.36),vec2(.145,.19));
  // Keep facial features together while the shoulders breathe and hair follows.
  p+=vec2(.0009*sin(phase),.0011*sin(phase*2.))*hero;
  p.y+=.0022*sin(phase*2.)*body*(1.-portrait);
  p.x+=(.0042*sin(phase-original.y*5.)*hair+
        .0034*sin(phase-original.y*4.+.6)*cloth)*(1.-portrait);
  p.y+=.0018*cos(phase-original.y*4.)*hair*(1.-portrait);
  float clear=1.-smoothstep(.60,.81,original.x);
  float clouds=field(original,vec2(.39,.71),vec2(.57,.16));
  p.x+=.006*sin(phase-original.y*4.)*clouds*clear;
  p.y+=.002*sin(phase+.8)*clouds*clear;
  if(scene>.5) {
    float whale=field(original,vec2(.66,.14),vec2(.32,.105));
    p+=vec2(.0055*sin(phase+.4),.0045*sin(phase))*whale*(1.-hero);
    float water=field(original,vec2(.38,.91),vec2(.48,.10));
    p.x+=.003*sin(phase*2.-original.y*9.)*water*clear;
  }
  vec3 color=texture2D(art,p).rgb;
  float glow=scene<.5
    ? field(original,vec2(.64,.14),vec2(.23,.22))
    : field(original,vec2(.58,.21),vec2(.34,.11));
  color+=vec3(.028,.030,.039)*glow*(.5+.5*sin(phase+.4))*(.55+.45*dark);
  gl_FragColor=vec4(color,1.);
}`;

const faces: Record<WallpaperScene, Record<WallpaperTheme, [number, number]>> = {
  celestial: { light: [0.864, 0.397], dark: [0.877, 0.454] },
  wandering: { light: [0.852, 0.424], dark: [0.868, 0.417] },
};

// One small surface; pause in hidden tabs and release it on theme/account changes.
export async function renderWallpaper(
  canvas: HTMLCanvasElement,
  scene: WallpaperScene,
  theme: WallpaperTheme,
  signal: AbortSignal,
): Promise<() => void> {
  const image = new Image();
  image.src = `/assets/gacha/appearances/${scene}-${theme}.webp`;
  await image.decode();
  if (signal.aborted) return () => {};
  const gl = canvas.getContext('webgl', {
    alpha: false,
    antialias: false,
    depth: false,
    stencil: false,
  });
  if (!gl) throw new Error('wallpaper-renderer-unavailable');
  const shaders: WebGLShader[] = [];
  const program = gl.createProgram();
  const buffer = gl.createBuffer();
  const texture = gl.createTexture();
  let frameID = 0;
  let stopped = false;
  let elapsed = 0;
  let previous = performance.now();
  let painted = -Infinity;
  const stop = () => {
    if (stopped) return;
    stopped = true;
    cancelAnimationFrame(frameID);
    window.removeEventListener('resize', resize);
    document.removeEventListener('visibilitychange', visibility);
    canvas.removeEventListener('webglcontextlost', lost);
    signal.removeEventListener('abort', stop);
    canvas.dataset.ready = 'false';
    gl.deleteTexture(texture);
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
    const density = Math.min(window.devicePixelRatio || 1, 1.5, 1920 / Math.max(width, height));
    canvas.width = Math.round(width * density);
    canvas.height = Math.round(height * density);
    gl!.viewport(0, 0, canvas.width, canvas.height);
    const scale = Math.max(width / image.width, height / image.height);
    const cropX = width / (scale * image.width),
      cropY = height / (scale * image.height);
    gl!.uniform2f(gl!.getUniformLocation(program!, 'crop'), cropX, cropY);
    gl!.uniform2f(
      gl!.getUniformLocation(program!, 'offset'),
      (1 - cropX) * (width < 640 ? 1 : 0.65),
      (1 - cropY) * 0.5,
    );
    painted = -Infinity;
  }
  function frame(now: number) {
    if (stopped || document.hidden) return;
    elapsed += Math.min(now - previous, 100);
    previous = now;
    if (now - painted >= 1000 / 30) {
      gl!.uniform1f(
        gl!.getUniformLocation(program!, 'phase'),
        ((elapsed % 12000) / 12000) * Math.PI * 2,
      );
      gl!.drawArrays(gl!.TRIANGLES, 0, 6);
      canvas.dataset.ready = 'true';
      painted = now;
    }
    frameID = requestAnimationFrame(frame);
  }
  try {
    if (!program || !buffer || !texture) throw new Error('wallpaper-renderer-unavailable');
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
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS))
        throw new Error('wallpaper-shader-unavailable');
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
    gl.bindTexture(gl.TEXTURE_2D, texture);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
    gl.uniform1i(gl.getUniformLocation(program, 'art'), 0);
    gl.uniform2f(gl.getUniformLocation(program, 'face'), ...faces[scene][theme]);
    gl.uniform1f(gl.getUniformLocation(program, 'scene'), scene === 'wandering' ? 1 : 0);
    gl.uniform1f(gl.getUniformLocation(program, 'dark'), theme === 'dark' ? 1 : 0);
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
