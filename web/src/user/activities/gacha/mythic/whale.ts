export function createWhalePlane(image: HTMLImageElement) {
  const canvas = document.createElement('canvas'),
    gl = canvas.getContext('webgl', { alpha: true, premultipliedAlpha: true, antialias: false });
  if (!gl) {
    canvas.width = canvas.height = 0;
    throw new Error('renderer-unavailable');
  }
  const program = gl.createProgram(),
    texture = gl.createTexture(),
    buffer = gl.createBuffer(),
    shaders: WebGLShader[] = [];
  let disposed = false;
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    gl.deleteBuffer(buffer);
    gl.deleteTexture(texture);
    shaders.forEach((s) => gl.deleteShader(s));
    gl.deleteProgram(program);
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    canvas.width = canvas.height = 0;
  };
  try {
    if (!program || !texture || !buffer) throw new Error('renderer-unavailable');
    for (const [type, source] of [
      [
        gl.VERTEX_SHADER,
        `attribute vec2 point;varying vec2 uv;uniform float time,motion;void main(){
    uv=point/vec2(1024.,1536.);vec2 p=point;
    float tail=(1.-smoothstep(65.,200.,p.y))*smoothstep(100.,160.,p.x)*(1.-smoothstep(290.,360.,p.x));
    float fin=(1.-smoothstep(0.,430.,p.x))*smoothstep(265.,365.,p.y)*(1.-smoothstep(530.,660.,p.y));
    p+=motion*vec2(8.*sin(time*.17)+tail*5.*sin(time*.86-.5),3.*sin(time*.23)+tail*9.*sin(time*.86)+fin*7.*sin(time*.86-.9));
    gl_Position=vec4(p.x/512.-1.,1.-p.y/768.,0.,1.);}`,
      ],
      [
        gl.FRAGMENT_SHADER,
        `precision highp float;varying vec2 uv;uniform sampler2D art;void main(){gl_FragColor=texture2D(art,uv);}`,
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
    for (let y = 0; y < 672; y += 32)
      for (let x = 0; x < 1024; x += 32)
        vertices.push(x, y, x + 32, y, x, y + 32, x, y + 32, x + 32, y, x + 32, y + 32);
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(vertices), gl.STATIC_DRAW);
    const point = gl.getAttribLocation(program, 'point');
    gl.enableVertexAttribArray(point);
    gl.vertexAttribPointer(point, 2, gl.FLOAT, false, 0, 0);
    gl.bindTexture(gl.TEXTURE_2D, texture);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
    gl.uniform1i(gl.getUniformLocation(program, 'art'), 0);
    const time = gl.getUniformLocation(program, 'time'),
      motion = gl.getUniformLocation(program, 'motion');
    return {
      canvas,
      paint(t: number, active: boolean, width: number, height: number) {
        if (disposed) return;
        if (canvas.width !== width || canvas.height !== height) {
          canvas.width = width;
          canvas.height = height;
          gl.viewport(0, 0, width, height);
        }
        gl.uniform1f(time, t);
        gl.uniform1f(motion, active ? 1 : 0);
        gl.clear(gl.COLOR_BUFFER_BIT);
        gl.drawArrays(gl.TRIANGLES, 0, vertices.length / 2);
      },
      dispose,
    };
  } catch (error) {
    dispose();
    throw error;
  }
}
