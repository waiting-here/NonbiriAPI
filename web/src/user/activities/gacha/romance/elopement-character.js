import { makeArmRig } from './elopement-arm.js';
const clamp = (x, a = 0, b = 1) => Math.max(a, Math.min(b, x));
const mix = (a, b, t) => a + (b - a) * t;
// Eye openings are individually registered to the 1024 × 1536 source.
const eyes = [
  { cx: 431, cy: 174, width: 47, height: 21, angle: -0.35 },
  { cx: 508, cy: 148, width: 46, height: 20, angle: -0.25 },
];

export function makeCharacterRenderer(images) {
  const armRig = makeArmRig(images);
  let cachedExtension = -1,
    cachedSmile = -1,
    cachedWrist = NaN,
    cachedUpper = NaN;
  const source = document.createElement('canvas');
  source.width = 1024;
  source.height = 1536;
  const painter = source.getContext('2d');
  const surface = document.createElement('canvas');
  surface.width = 1024;
  surface.height = 1536;
  const gl = surface.getContext('webgl', {
    alpha: true,
    premultipliedAlpha: true,
    antialias: false,
    preserveDrawingBuffer: true,
  });
  const mask = document.createElement('canvas');
  mask.width = 1024;
  mask.height = 1536;
  const m = mask.getContext('2d');
  m.fillStyle = '#000';
  m.fillRect(0, 0, 1024, 1536);
  const polygons = [
    [
      [242, 315],
      [265, 345],
      [258, 415],
      [263, 484],
      [272, 552],
      [296, 616],
      [283, 666],
      [201, 641],
      [154, 579],
      [158, 465],
      [188, 370],
    ],
    [
      [705, 294],
      [766, 323],
      [827, 371],
      [887, 442],
      [899, 524],
      [851, 604],
      [781, 657],
      [736, 630],
      [749, 575],
      [730, 503],
      [713, 423],
    ],
  ];
  for (const points of polygons) {
    m.beginPath();
    points.forEach(([x, y], i) => (i ? m.lineTo(x, y) : m.moveTo(x, y)));
    m.closePath();
    const g = m.createLinearGradient(0, 300, 0, 670);
    g.addColorStop(0, '#000');
    g.addColorStop(0.3, '#333');
    g.addColorStop(1, '#ddd');
    m.fillStyle = g;
    m.fill();
  }
  // Explicit body exclusions keep the reaching hand and the waist rigid.
  m.fillStyle = '#000';
  m.beginPath();
  [
    [270, 400],
    [450, 390],
    [550, 465],
    [573, 598],
    [355, 610],
    [270, 566],
  ].forEach(([x, y], i) => (i ? m.lineTo(x, y) : m.moveTo(x, y)));
  m.closePath();
  m.fill();
  let program, texture, phaseUniform, blinkUniform, attributes;
  if (gl) {
    const compile = (type, code) => {
      const shader = gl.createShader(type);
      gl.shaderSource(shader, code);
      gl.compileShader(shader);
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS))
        throw new Error(gl.getShaderInfoLog(shader));
      return shader;
    };
    const vertex = compile(
      gl.VERTEX_SHADER,
      'attribute vec2 p; varying vec2 uv; void main(){uv=vec2((p.x+1.)*.5,(1.-p.y)*.5);gl_Position=vec4(p,0.,1.);}',
    );
    const fragment = compile(
      gl.FRAGMENT_SHADER,
      [
        'precision mediump float; varying vec2 uv;',
        'uniform sampler2D art; uniform sampler2D weights; uniform sampler2D closed; uniform sampler2D lids;',
        'uniform float phase; uniform float blink; uniform vec4 leftEye; uniform vec4 rightEye;',
        'vec4 eyelid(vec2 p,vec4 box,float row,vec4 original){',
        'if(blink<=0.)return original;vec2 q=(p-box.xy)/box.zw;if(q.x<=0.||q.x>=1.||q.y<=0.||q.y>=1.)return original;',
        'float sx=(q.x*47.+.5)/48.;vec4 o=texture2D(lids,vec2(sx,row));vec3 s=texture2D(lids,vec2(sx,row+.25)).rgb;float e=.015;',
        'float coverage=smoothstep(o.r,o.r+e,q.y)*(1.-smoothstep(o.g-e,o.g,q.y))*smoothstep(0.,.035,q.x)*(1.-smoothstep(.965,1.,q.x))*s.z*smoothstep(0.,.075,blink);',
        'float top=mix(o.b,s.x,blink);float low=mix(o.a,s.y,blink);float bottom=mix(o.g,s.y,blink*blink);',
        'float aperture=smoothstep(low-e,low+e,q.y)*(1.-smoothstep(bottom-e,bottom,q.y))*(1.-smoothstep(.98,1.,blink));',
        'vec4 rest=texture2D(closed,p);vec2 above=vec2(p.x,box.y+max(0.,s.x-.07)*box.w);vec2 below=vec2(p.x,box.y+min(1.,s.y+.07)*box.w);',
        'float finalLash=smoothstep(s.x-.03,s.x,q.y)*(1.-smoothstep(s.y,s.y+.03,q.y));vec4 skin=mix(texture2D(closed,above),texture2D(closed,below),clamp((q.y-s.x)/max(.005,s.y-s.x),0.,1.));rest=mix(rest,skin,finalLash);',
        'vec4 color=mix(original,rest,1.-aperture);float ribbon=smoothstep(top-e,top+e,q.y)*(1.-smoothstep(low-e,low+e,q.y));float along=clamp((q.y-top)/max(.005,low-top),0.,1.);',
        'vec2 fromOpen=vec2(p.x,box.y+mix(o.b,o.a,along)*box.w);vec2 fromClosed=vec2(p.x,box.y+mix(s.x,s.y,along)*box.w);',
        'color=mix(color,mix(texture2D(art,fromOpen),texture2D(closed,fromClosed),blink),ribbon);color=mix(color,texture2D(closed,p),smoothstep(.96,1.,blink));return mix(original,color,coverage);}',
        'void main(){float w=texture2D(weights,uv).r;vec2 q=uv;q.x+=w*.0044*sin(phase*1.6-uv.y*4.);q.y+=w*.0014*sin(phase*1.6-uv.y*3.6);vec4 c=texture2D(art,q);c=eyelid(q,leftEye,.125,c);c=eyelid(q,rightEye,.625,c);gl_FragColor=vec4(c.rgb*c.a,c.a);}',
      ].join('\n'),
    );
    program = gl.createProgram();
    gl.attachShader(program, vertex);
    gl.attachShader(program, fragment);
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS))
      throw new Error(gl.getProgramInfoLog(program));
    gl.useProgram(program);
    const buffer = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW);
    attributes = gl.getAttribLocation(program, 'p');
    gl.enableVertexAttribArray(attributes);
    gl.vertexAttribPointer(attributes, 2, gl.FLOAT, false, 0, 0);
    const makeTexture = (unit, sourceImage) => {
      const tex = gl.createTexture();
      gl.activeTexture(gl.TEXTURE0 + unit);
      gl.bindTexture(gl.TEXTURE_2D, tex);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, sourceImage);
      return tex;
    };
    texture = makeTexture(0, source);
    makeTexture(1, mask);
    gl.uniform1i(gl.getUniformLocation(program, 'art'), 0);
    gl.uniform1i(gl.getUniformLocation(program, 'weights'), 1);
    makeTexture(2, images.closed);
    gl.uniform1i(gl.getUniformLocation(program, 'closed'), 2);
    const eyeProfiles = [
      {
        box: [402, 151, 51, 40],
        open: [
          [402, 181],
          [408, 178],
          [411, 171],
          [414, 167],
          [417, 165],
          [420, 163],
          [426, 161],
          [432, 160],
          [438, 161],
          [444, 164],
          [448, 168],
          [453, 170],
        ],
        shut: [
          [402, 177],
          [408, 178],
          [411, 179],
          [414, 179],
          [417, 175],
          [420, 175],
          [426, 174],
          [432, 172],
          [438, 170],
          [444, 169],
          [448, 168],
          [453, 168],
        ],
      },
      {
        box: [483, 125, 52, 40],
        open: [
          [483, 149],
          [489, 141],
          [492, 138],
          [495, 137],
          [501, 134],
          [507, 133],
          [513, 133],
          [519, 134],
          [525, 133],
          [528, 139],
          [535, 145],
        ],
        shut: [
          [483, 153],
          [489, 153],
          [492, 149],
          [498, 148],
          [504, 147],
          [510, 145],
          [516, 142],
          [519, 141],
          [525, 143],
          [528, 143],
          [535, 145],
        ],
      },
    ];
    const interp = (points, x) => {
      for (let i = 1; i < points.length; i++)
        if (x <= points[i][0])
          return mix(
            points[i - 1][1],
            points[i][1],
            clamp((x - points[i - 1][0]) / (points[i][0] - points[i - 1][0])),
          );
      return points.at(-1)[1];
    };
    const rows = [];
    for (const profile of eyeProfiles) {
      const opened = [],
        shut = [];
      for (let i = 0; i < 48; i++) {
        const [bx, by, bw, bh] = profile.box,
          x = bx + (i / 47) * bw,
          f = Math.sin((i / 47) * Math.PI);
        const a = interp(profile.open, x),
          b = interp(profile.shut, x),
          thick = 2 + 4 * f;
        const norm = (y) => Math.round(clamp((y - by) / bh) * 255);
        opened.push(
          norm(Math.min(a, b) - 3),
          norm(Math.max(a + 15 * f, b + thick + 3)),
          norm(a - 1),
          norm(a + thick),
        );
        shut.push(norm(b - 1), norm(b + thick), 255, 255);
      }
      rows.push(...opened, ...shut);
    }
    const profileTexture = gl.createTexture();
    gl.activeTexture(gl.TEXTURE3);
    gl.bindTexture(gl.TEXTURE_2D, profileTexture);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texImage2D(
      gl.TEXTURE_2D,
      0,
      gl.RGBA,
      48,
      4,
      0,
      gl.RGBA,
      gl.UNSIGNED_BYTE,
      new Uint8Array(rows),
    );
    gl.uniform1i(gl.getUniformLocation(program, 'lids'), 3);
    eyeProfiles.forEach((p, i) =>
      gl.uniform4f(
        gl.getUniformLocation(program, i ? 'rightEye' : 'leftEye'),
        p.box[0] / 1024,
        p.box[1] / 1536,
        p.box[2] / 1024,
        p.box[3] / 1536,
      ),
    );
    phaseUniform = gl.getUniformLocation(program, 'phase');
    blinkUniform = gl.getUniformLocation(program, 'blink');
  }
  function eyePatch(eye, amount) {
    if (amount <= 0.001) return;
    painter.save();
    painter.translate(eye.cx, eye.cy);
    painter.rotate(eye.angle);
    const w = eye.width / 2,
      h = eye.height / 2;
    painter.beginPath();
    painter.moveTo(-w, -h * 0.05);
    painter.bezierCurveTo(-w * 0.55, -h * 1.2, w * 0.45, -h * 1.2, w, -h * 0.05);
    painter.bezierCurveTo(w * 0.58, h * 0.95, -w * 0.5, h * 1.05, -w, -h * 0.05);
    painter.closePath();
    painter.clip();
    // Reveal the registered closed-eye plate as the upper lid travels down.
    painter.beginPath();
    painter.rect(-w - 3, -h * 1.4, 2 * w + 6, 2.7 * h * amount);
    painter.clip();
    painter.rotate(-eye.angle);
    painter.translate(-eye.cx, -eye.cy);
    painter.drawImage(images.closed, 0, 0);
    painter.restore();
  }
  function build(extension, smile, blink, wrist, upper) {
    if (
      gl &&
      extension === cachedExtension &&
      smile === cachedSmile &&
      wrist === cachedWrist &&
      upper === cachedUpper
    )
      return;
    painter.clearRect(0, 0, 1024, 1536);
    painter.drawImage(armRig.draw(extension, wrist, upper), 0, 0);
    if (smile < 1) {
      painter.save();
      painter.globalAlpha = 1 - smile;
      painter.beginPath();
      painter.ellipse(482, 203, 21, 11, -0.28, 0, Math.PI * 2);
      painter.clip();
      painter.drawImage(images.arrival, 0, 0);
      painter.restore();
    }
    if (!gl && blink > 0.75)
      eyes.forEach((eye) =>
        eyePatch({ ...eye, width: eye.width + 14, height: eye.height + 18 }, 1),
      );
    if (gl) {
      gl.activeTexture(gl.TEXTURE0);
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, source);
    }
    cachedExtension = extension;
    cachedSmile = smile;
    cachedWrist = wrist;
    cachedUpper = upper;
  }
  return {
    draw(time, extension, smile, blink, wrist = 0, upper = 1) {
      build(extension, smile, blink, wrist, upper);
      if (!gl) return source;
      gl.useProgram(program);
      gl.uniform1f(phaseUniform, time);
      gl.uniform1f(blinkUniform, blink);
      gl.viewport(0, 0, 1024, 1536);
      gl.clearColor(0, 0, 0, 0);
      gl.clear(gl.COLOR_BUFFER_BIT);
      gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
      return surface;
    },
    source,
    mask,
    hasWebGL: !!gl,
    dispose() {
      armRig.dispose?.();
      gl?.getExtension('WEBGL_lose_context')?.loseContext();
    },
  };
}

/* global document */
