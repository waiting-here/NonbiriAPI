import bodyUrl from './assets/claude-chiffon/body.webp';
import dressUrl from './assets/claude-chiffon/dress.webp';
import frontUrl from './assets/claude-chiffon/front.webp';

export const studyLayers = { body: bodyUrl, dress: dressUrl, front: frontUrl };
export type StudyLayer = 'composite' | keyof typeof studyLayers;
export type StudyBackdrop = 'room' | 'checker' | 'light' | 'dark';

const vertexSource = `
attribute vec2 point;
uniform float time,moving;
varying vec2 uv;
void main(){
  uv=point/vec2(1024.,1536.);
  float left=(1.-smoothstep(390.,540.,point.x))*smoothstep(770.,1100.,point.y);
  float right=smoothstep(690.,840.,point.x)*smoothstep(810.,1150.,point.y);
  float hem=smoothstep(1110.,1380.,point.y);
  float phase=time*6.2831853/9.6;
  vec2 offset=vec2(
    15.*left*sin(phase-point.y*.002)+12.*right*sin(phase-point.y*.002+.5)+
      6.*hem*sin(phase-point.x*.002),
    3.*left*sin(phase-.5)+2.*right*sin(phase-.9)+2.*hem*sin(phase-1.1)
  )*moving;
  vec2 p=(point+offset)/vec2(1024.,1536.);
  gl_Position=vec4(p.x*2.-1.,1.-p.y*2.,0.,1.);
}`;
const fragmentSource = `precision mediump float;
varying vec2 uv;
uniform sampler2D fabric;
void main(){gl_FragColor=texture2D(fabric,uv);}`;

export async function createClothStudy(canvas: HTMLCanvasElement, signal: AbortSignal) {
  const images = await Promise.all(
    ['/assets/gacha/motion/t-claude/background.webp', bodyUrl, dressUrl, frontUrl].map(
      async (src) => {
        const image = new Image();
        image.src = src;
        await image.decode();
        return image;
      },
    ),
  );
  if (signal.aborted) throw new DOMException('Aborted', 'AbortError');
  const [background, body, dress, front] = images;
  const output = canvas.getContext('2d');
  const layer = document.createElement('canvas');
  const gl = layer.getContext('webgl', { alpha: true, premultipliedAlpha: false });
  if (!output || !gl) throw new Error('renderer-unavailable');
  const shaders: WebGLShader[] = [];
  const program = gl.createProgram();
  const buffer = gl.createBuffer();
  const elements = gl.createBuffer();
  const texture = gl.createTexture();
  const dispose = () => {
    shaders.forEach((shader) => gl.deleteShader(shader));
    gl.deleteProgram(program);
    gl.deleteBuffer(buffer);
    gl.deleteBuffer(elements);
    gl.deleteTexture(texture);
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    layer.width = layer.height = 0;
    images.forEach((image) => {
      image.src = '';
    });
  };
  try {
    if (!program || !buffer || !elements || !texture) throw new Error('renderer-unavailable');
    for (const [type, source] of [
      [gl.VERTEX_SHADER, vertexSource],
      [gl.FRAGMENT_SHADER, fragmentSource],
    ] as const) {
      const shader = gl.createShader(type);
      if (!shader) throw new Error('shader-unavailable');
      shaders.push(shader);
      gl.shaderSource(shader, source);
      gl.compileShader(shader);
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error('shader-unavailable');
      gl.attachShader(program, shader);
    }
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error('shader-unavailable');
    gl.useProgram(program);
    const vertices: number[] = [],
      indices: number[] = [];
    const columns = 32,
      rows = 48;
    for (let y = 0; y <= rows; y++)
      for (let x = 0; x <= columns; x++) vertices.push(x * 32, y * 32);
    for (let y = 0; y < rows; y++)
      for (let x = 0; x < columns; x++) {
        const a = y * (columns + 1) + x,
          b = a + columns + 1;
        indices.push(a, b, a + 1, a + 1, b, b + 1);
      }
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(vertices), gl.STATIC_DRAW);
    const point = gl.getAttribLocation(program, 'point');
    gl.enableVertexAttribArray(point);
    gl.vertexAttribPointer(point, 2, gl.FLOAT, false, 0, 0);
    gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, elements);
    gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, new Uint16Array(indices), gl.STATIC_DRAW);
    gl.bindTexture(gl.TEXTURE_2D, texture);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, dress);
    gl.uniform1i(gl.getUniformLocation(program, 'fabric'), 0);
    const timeUniform = gl.getUniformLocation(program, 'time');
    const movingUniform = gl.getUniformLocation(program, 'moving');
    return {
      resize() {
        const width = Math.min(
          1024,
          Math.max(1, Math.round(canvas.clientWidth * Math.min(devicePixelRatio || 1, 2))),
        );
        if (width === canvas.width && width === layer.width) return;
        canvas.width = layer.width = width;
        canvas.height = layer.height = Math.round(width * 1.5);
        gl.viewport(0, 0, layer.width, layer.height);
      },
      paint(time: number, mode: StudyLayer, backdrop: StudyBackdrop, moving: boolean) {
        output.clearRect(0, 0, canvas.width, canvas.height);
        const draw = (image: CanvasImageSource) =>
          output.drawImage(image, 0, 0, canvas.width, canvas.height);
        if (backdrop === 'room') draw(background);
        // Anatomy, chair and contact points are never inputs to the cloth mesh.
        if (mode === 'composite' || mode === 'body') draw(body);
        if (mode === 'composite' || mode === 'dress') {
          gl.clear(gl.COLOR_BUFFER_BIT);
          gl.uniform1f(timeUniform, time);
          gl.uniform1f(movingUniform, moving ? 1 : 0);
          gl.drawElements(gl.TRIANGLES, indices.length, gl.UNSIGNED_SHORT, 0);
          draw(layer);
        }
        if (mode === 'composite' || mode === 'front') draw(front);
      },
      dispose,
    };
  } catch (error) {
    dispose();
    throw error;
  }
}
