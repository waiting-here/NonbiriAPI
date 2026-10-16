import { makeArmLayers } from './heart-thief-arm.js';
import { restPose, inspectRestWeight } from './heart-thief-rest.js';
export { restPose, inspectRestWeight } from './heart-thief-rest.js';
export const ART_WIDTH = 1086,
  ART_HEIGHT = 1448;
const clamp = (x) => Math.max(0, Math.min(1, x));
const smooth = (a, b, x) => {
  const q = clamp((x - a) / (b - a));
  return q * q * (3 - 2 * q);
};
const mix = (a, b, t) => a + (b - a) * t;
function regionDistance(x, y, poly) {
  let inside = false,
    distance = Infinity;
  for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) {
    const [ax, ay] = poly[j],
      [bx, by] = poly[i];
    if (ay > y !== by > y && x < ((bx - ax) * (y - ay)) / (by - ay) + ax) inside = !inside;
    const dx = bx - ax,
      dy = by - ay,
      q = clamp(((x - ax) * dx + (y - ay) * dy) / (dx * dx + dy * dy));
    distance = Math.min(distance, Math.hypot(x - ax - q * dx, y - ay - q * dy));
  }
  return { inside, distance };
}
function regionWeight(x, y, poly, feather = 18) {
  const r = regionDistance(x, y, poly);
  return r.inside ? smooth(0, feather, r.distance) : 0;
}
function outsideWeight(x, y, poly) {
  const r = regionDistance(x, y, poly);
  return r.inside ? 0 : smooth(10, 26, r.distance);
}

const leftHair = [
  [360, 325],
  [392, 370],
  [381, 430],
  [375, 484],
  [364, 568],
  [326, 654],
  [196, 739],
  [66, 727],
  [54, 561],
  [98, 446],
  [200, 395],
];
const rightHair = [
  [666, 279],
  [766, 310],
  [906, 390],
  [1035, 481],
  [1060, 634],
  [1014, 728],
  [874, 757],
  [797, 708],
  [746, 633],
  [826, 615],
  [841, 510],
  [790, 447],
  [729, 406],
];
const leftTail = [
  [352, 608],
  [387, 691],
  [323, 771],
  [258, 866],
  [245, 958],
  [179, 936],
  [137, 865],
  [154, 731],
  [235, 659],
];
const rightTail = [
  [670, 649],
  [765, 681],
  [886, 767],
  [1027, 904],
  [1055, 1053],
  [1011, 1162],
  [945, 1129],
  [947, 1007],
  [858, 916],
  [759, 878],
  [701, 825],
];
const raisedArm = [
  [289, 297],
  [332, 300],
  [353, 340],
  [378, 389],
  [386, 446],
  [383, 488],
  [365, 514],
  [338, 505],
  [320, 468],
  [306, 394],
  [290, 337],
];
const bodyAnchor = [
  [373, 316],
  [426, 297],
  [607, 281],
  [658, 393],
  [630, 526],
  [663, 630],
  [630, 761],
  [567, 859],
  [562, 940],
  [423, 950],
  [388, 665],
  [388, 537],
  [369, 459],
];
const leftCoat = [
  [426, 500],
  [406, 552],
  [350, 586],
  [286, 617],
  [230, 651],
  [200, 692],
  [172, 745],
  [145, 801],
  [141, 868],
  [188, 929],
  [276, 961],
  [259, 912],
  [256, 869],
  [289, 813],
  [337, 755],
  [379, 697],
  [406, 663],
  [420, 655],
];
const rightCoat = [
  [569, 485],
  [632, 531],
  [690, 587],
  [731, 624],
  [803, 676],
  [872, 739],
  [927, 775],
  [986, 826],
  [1047, 934],
  [1058, 1053],
  [1016, 1140],
  [946, 1165],
  [929, 1071],
  [950, 998],
  [904, 914],
  [861, 882],
  [764, 883],
  [699, 824],
  [678, 755],
  [645, 704],
  [633, 651],
  [611, 603],
];
export function inspectMotionWeight(x, y) {
  const bodyClear = Math.min(outsideWeight(x, y, raisedArm), outsideWeight(x, y, bodyAnchor));
  const hairClear = Math.min(
    bodyClear,
    outsideWeight(x, y, leftCoat),
    outsideWeight(x, y, rightCoat),
  );
  return {
    hair:
      Math.max(regionWeight(x, y, leftHair, 24), regionWeight(x, y, rightHair, 24)) *
      smooth(280, 730, y) *
      hairClear,
    tail:
      Math.max(regionWeight(x, y, leftTail, 24), regionWeight(x, y, rightTail, 24)) *
      smooth(630, 1050, y) *
      bodyClear,
  };
}
const curtainLeft = [
  [332, 450],
  [360, 450],
  [375, 513],
  [398, 617],
  [421, 706],
  [437, 741],
  [409, 761],
  [320, 780],
  [301, 771],
  [306, 711],
  [313, 627],
  [316, 540],
];
const curtainRight = [
  [1505, 454],
  [1535, 454],
  [1535, 965],
  [1423, 922],
  [1359, 850],
  [1368, 810],
  [1399, 728],
  [1438, 639],
  [1474, 546],
];
export function inspectBackgroundWeight(x, y) {
  return {
    left: regionWeight(x, y, curtainLeft, 16) * smooth(450, 770, y),
    right: regionWeight(x, y, curtainRight, 18) * smooth(455, 930, y),
  };
}

const shoulder = [620, 310],
  elbow = [625, 402],
  wrist = [683, 453];
export function invitationPose(t) {
  const upper = smooth(4.45, 5.65, t),
    forearm = smooth(4.65, 5.96, t),
    hand = smooth(4.9, 6.18, t);
  return {
    upper: 0.052 * (1 - upper),
    forearm: 0.12 * (1 - forearm),
    wrist: -0.042 * (1 - hand),
    progress: forearm,
  };
}
function compile(gl, type, text) {
  const s = gl.createShader(type);
  gl.shaderSource(s, text);
  gl.compileShader(s);
  if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw Error(gl.getShaderInfoLog(s));
  return s;
}
const vertex = String.raw`
attribute vec2 point;attribute vec3 weight;attribute vec4 restWeight;varying vec2 uv;
uniform float phase;uniform vec3 angles;uniform float layerPass;uniform vec4 restMotion;
vec2 turn(vec2 p,float a){float c=cos(a),s=sin(a);return vec2(c*p.x-s*p.y,s*p.x+c*p.y);}
void main(){
 uv=point/vec2(1086.,1448.);vec2 q=point;
 vec2 shoulder=vec2(620.,310.),elbow=vec2(625.,402.),wrist=vec2(683.,453.);
 float fore=smoothstep(-4.,62.,dot(point-elbow,normalize(vec2(58.,51.))));
 float hand=smoothstep(-14.,9.,dot(point-wrist,normalize(vec2(28.,56.))));
 vec2 wristTurn=wrist+turn(point-wrist,angles.z);
 q=mix(point,wristTurn,hand);
 vec2 foreTurn=elbow+turn(q-elbow,angles.y);q=mix(q,foreTurn,fore);
 q=shoulder+turn(q-shoulder,angles.x);q=mix(point,q,weight.x*layerPass);
 float rear=1.-layerPass;
 q.x+=rear*weight.y*3.4*sin(phase*1.35-point.y*.005)+rear*weight.z*(1.-restMotion.z)*2.2*sin(phase*1.12-point.y*.004);
 q.y+=rear*weight.y*.9*sin(phase*1.35-point.y*.004)+rear*weight.z*(1.-restMotion.z)*.55*sin(phase*1.12-point.y*.003);
 // Cloth panels rotate from their sewn roots; the outer edge travels with the fabric.
 float leftAngle=(-.055+.023*sin(restMotion.w*1.08-(point.y-590.)*.0024))*restMotion.z;
 float rightAngle=(.068+.024*sin(restMotion.w*.94-(point.y-620.)*.0020+.6))*restMotion.z;
 q+=rear*restWeight.z*(vec2(397.,595.)+turn(point-vec2(397.,595.),leftAngle)-point);
 q+=rear*restWeight.w*(vec2(600.,570.)+turn(point-vec2(600.,570.),rightAngle)-point);
 // One ribcage lift; hands, shoulder anchors, waist and support legs are excluded.
 q+=rear*restWeight.y*restMotion.y*vec2((point.x-477.)*.006,-3.2);

 // Keep the head at the approved original pose.
 gl_Position=vec4(q.x/543.-1.,1.-q.y/724.,0.,1.);
}`;
const fragment = String.raw`
precision highp float;varying vec2 uv;uniform sampler2D art;uniform sampler2D profiles;uniform float blink;
uniform vec4 leftEye;uniform vec4 rightEye;uniform sampler2D armMask;uniform float layerPass;
const vec2 size=vec2(1086.,1448.);
const vec2 along=vec2(.973190247,-.23),down=vec2(.23,.973190247),origin=vec2(500.,160.);
vec2 local(vec2 p){vec2 d=p-origin;return vec2(dot(d,along),dot(d,down));}
vec2 source(vec2 p){return (origin+along*p.x+down*p.y)/size;}
vec4 lid(vec4 original,vec2 p,vec4 box,float row){
 if(blink<.0001)return original;
 vec2 q=(p-box.xy)/box.zw;if(q.x<=0.||q.x>=1.||q.y<=0.||q.y>=1.)return original;
 vec4 profile=texture2D(profiles,vec2((q.x*127.+.5)/128.,row));
 float outer=box.y+profile.r*box.w,inner=box.y+profile.g*box.w,bottom=box.y+profile.b*box.w;
 float edge=profile.a;
 // Closed lashes follow the face tilt, with a shallow arch for each eye.
 float bow=4.*q.x*(1.-q.x);
 float target=row<.5?mix(14.,11.,q.x)+5.3*bow:mix(5.,0.,q.x)+7.*bow;
 float closure=max(inner,target);
 float finalWidth=max(1.,(inner-outer)*.68);
 float movedInner=mix(inner,closure,blink),movedOuter=mix(outer,closure-finalWidth,blink);
 float e=.65;
 vec2 sa=row<.5?vec2(465.,189.):vec2(520.,174.);
 vec2 sb=row<.5?vec2(477.,188.):vec2(540.,171.);
 vec4 skin=mix(texture2D(art,sa/size),texture2D(art,sb/size),q.x);

 float uncovered=smoothstep(outer-1.3,outer-.2,p.y)*(1.-smoothstep(movedOuter-e,movedOuter+e,p.y));
 vec4 c=mix(original,skin,uncovered);
 // Only the few pixels below the new meeting line are covered by the lower lid.
 float floor=mix(bottom,min(bottom,closure),smoothstep(.3,1.,blink));
 float lowerCover=smoothstep(floor-.55,floor+.55,p.y)*(1.-smoothstep(bottom+.25,bottom+.85,p.y));
 c=mix(c,skin,lowerCover*smoothstep(.3,.65,blink));
 float fraction=clamp((p.y-movedOuter)/max(.5,movedInner-movedOuter),0.,1.);
 vec4 lash=texture2D(art,source(vec2(p.x,mix(outer,inner,fraction))));
 float ribbon=smoothstep(movedOuter-e,movedOuter+e,p.y)*(1.-smoothstep(movedInner-e,movedInner+e,p.y));
 float ink=(1.-smoothstep(.20,.40,dot(lash.rgb,vec3(.299,.587,.114))))*(1.-smoothstep(.035,.13,lash.b-lash.r));
 c=mix(c,mix(skin,lash,ink),ribbon);
 // The pupil stays fixed while the upper lid and a small lower-lid lift meet.
 float tone=dot(original.rgb,vec3(.299,.587,.114));
 float side=row>.5?smoothstep(50.,56.,p.x):0.;
 float hair=side*smoothstep(.035,.085,tone)*(1.-smoothstep(.45,.65,tone));
 return mix(original,c,edge*(1.-hair)*smoothstep(0.,.035,blink));
}
void main(){vec4 c=texture2D(art,uv);vec2 p=local(uv*size);
 if(layerPass>.5){c.a*=texture2D(armMask,uv).a;}else{c=lid(c,p,leftEye,.25);c=lid(c,p,rightEye,.75);}gl_FragColor=vec4(c.rgb*c.a,c.a);}
`;
function texture(gl, unit, source) {
  const t = gl.createTexture();
  gl.activeTexture(gl.TEXTURE0 + unit);
  gl.bindTexture(gl.TEXTURE_2D, t);
  for (const p of [gl.TEXTURE_WRAP_S, gl.TEXTURE_WRAP_T])
    gl.texParameteri(gl.TEXTURE_2D, p, gl.CLAMP_TO_EDGE);
  for (const p of [gl.TEXTURE_MIN_FILTER, gl.TEXTURE_MAG_FILTER])
    gl.texParameteri(gl.TEXTURE_2D, p, gl.LINEAR);
  gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, source);
  return t;
}
export function makeCharacterRenderer(image, repairImage) {
  const surface = document.createElement('canvas');
  surface.width = ART_WIDTH;
  surface.height = ART_HEIGHT;
  const gl = surface.getContext('webgl', {
    alpha: true,
    premultipliedAlpha: true,
    antialias: false,
    preserveDrawingBuffer: true,
  });
  if (!gl) return { hasWebGL: false, draw: () => image };
  const program = gl.createProgram();
  gl.attachShader(program, compile(gl, gl.VERTEX_SHADER, vertex));
  gl.attachShader(program, compile(gl, gl.FRAGMENT_SHADER, fragment));
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw Error(gl.getProgramInfoLog(program));
  gl.useProgram(program);
  const layers = makeArmLayers(image, repairImage);
  const data = [],
    xs = [...Array.from({ length: 182 }, (_, i) => Math.min(1086, i * 6))],
    ys = [...Array.from({ length: 243 }, (_, i) => Math.min(1448, i * 6))];
  const weightCache = new Map();
  const add = (x, y) => {
    const arm = smooth(315, 357, y),
      key = y * 1087 + x;
    let w = weightCache.get(key);
    if (!w) {
      w = { ...inspectMotionWeight(x, y), rest: inspectRestWeight(x, y) };
      weightCache.set(key, w);
    }
    const rest = w.rest;
    data.push(x, y, arm, w.hair, w.tail, rest.head, rest.ribs, rest.left, rest.right);
  };
  for (let r = 0; r < ys.length - 1; r++)
    for (let c = 0; c < xs.length - 1; c++) {
      const x = xs[c],
        y = ys[r],
        xx = xs[c + 1],
        yy = ys[r + 1];
      [
        [x, y],
        [xx, y],
        [x, yy],
        [xx, y],
        [xx, yy],
        [x, yy],
      ].forEach((p) => add(...p));
    }
  const buf = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, buf);
  gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(data), gl.STATIC_DRAW);
  for (const [name, size, offset] of [
    ['point', 2, 0],
    ['weight', 3, 8],
    ['restWeight', 4, 20],
  ]) {
    const a = gl.getAttribLocation(program, name);
    gl.enableVertexAttribArray(a);
    gl.vertexAttribPointer(a, size, gl.FLOAT, false, 36, offset);
  }
  const artTexture = texture(gl, 0, image);
  gl.uniform1i(gl.getUniformLocation(program, 'art'), 0);
  const baseTexture = texture(gl, 2, layers.backing);
  texture(gl, 3, layers.mask);
  gl.uniform1i(gl.getUniformLocation(program, 'armMask'), 3);
  const layerPass = gl.getUniformLocation(program, 'layerPass');
  gl.enable(gl.BLEND);
  gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
  const eyePaths = [
    {
      outer: [
        [444, 173],
        [448, 169],
        [451, 165],
        [456, 162],
        [462, 162],
        [469, 163],
        [475, 166],
        [480, 171],
        [485, 177],
      ],
      inner: [
        [444, 176],
        [448, 177],
        [451, 176],
        [456, 170],
        [462, 168],
        [469, 168],
        [475, 171],
        [480, 175],
        [485, 178],
      ],
      bottom: [
        [444, 177],
        [448, 179],
        [453, 182],
        [458, 184.5],
        [467, 185.5],
        [475, 184],
        [480, 181],
        [485, 178],
      ],
    },
    {
      outer: [
        [510, 154],
        [514, 147],
        [519, 143],
        [525, 140],
        [534, 137],
        [542, 137],
        [547, 139],
        [551, 142],
        [554, 141],
        [558, 140],
        [560, 139.5],
        [562, 140.5],
      ],
      inner: [
        [510, 155],
        [514, 154],
        [519, 150],
        [525, 148],
        [534, 146],
        [542, 146],
        [547, 148],
        [551, 147],
        [554, 147],
        [558, 143],
        [560, 140.5],
        [562, 140.5],
      ],
      bottom: [
        [510, 155],
        [514, 159],
        [519, 163],
        [526, 165.5],
        [533, 164.5],
        [540, 162],
        [546, 156],
        [551, 147],
        [555, 147],
        [559, 143],
        [562, 140.5],
      ],
    },
  ];
  const axis = [0.973190247, -0.23],
    normal = [0.23, 0.973190247];
  const project = ([x, y]) => [
    (x - 500) * axis[0] + (y - 160) * axis[1],
    (x - 500) * normal[0] + (y - 160) * normal[1],
  ];
  const interp = (a, x) => {
    if (x < a[0][0]) return a[0][1];
    for (let i = 1; i < a.length; i++)
      if (x <= a[i][0])
        return mix(a[i - 1][1], a[i][1], clamp((x - a[i - 1][0]) / (a[i][0] - a[i - 1][0])));
    return a.at(-1)[1];
  };
  const values = [];
  for (const [ei, e] of eyePaths.entries()) {
    e.outer = e.outer.map(project);
    e.inner = e.inner.map(project);
    e.bottom = e.bottom.map(project);
    const all = [...e.outer, ...e.inner, ...e.bottom],
      x = Math.min(...all.map((p) => p[0])),
      right = Math.max(...all.map((p) => p[0])),
      y = Math.min(...all.map((p) => p[1])) - 3,
      bottom = Math.max(...all.map((p) => p[1])) + 2,
      w = right - x,
      h = bottom - y;
    e.box = [x, y, w, h];
    gl.uniform4f(gl.getUniformLocation(program, ei ? 'rightEye' : 'leftEye'), x, y, w, h);
    for (let i = 0; i < 128; i++) {
      const q = i / 127,
        u = x + q * w;
      const outer = interp(e.outer, u),
        inner = interp(e.inner, u),
        bottom = Math.max(inner, interp(e.bottom, u));
      values.push(
        ...[outer, inner, bottom].map((v) => Math.round(clamp((v - y) / h) * 255)),
        Math.round(smooth(0, 0.09, q) * (1 - smooth(0.9, 1, q)) * 255),
      );
    }
  }
  const ptex = gl.createTexture();
  gl.activeTexture(gl.TEXTURE1);
  gl.bindTexture(gl.TEXTURE_2D, ptex);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
  gl.texImage2D(
    gl.TEXTURE_2D,
    0,
    gl.RGBA,
    128,
    2,
    0,
    gl.RGBA,
    gl.UNSIGNED_BYTE,
    new Uint8Array(values),
  );
  gl.uniform1i(gl.getUniformLocation(program, 'profiles'), 1);
  const phase = gl.getUniformLocation(program, 'phase'),
    angles = gl.getUniformLocation(program, 'angles'),
    blink = gl.getUniformLocation(program, 'blink'),
    restMotion = gl.getUniformLocation(program, 'restMotion');
  return {
    hasWebGL: true,
    dispose() {
      gl.getExtension('WEBGL_lose_context')?.loseContext();
    },
    draw(t, pose, amount = 0, restOverride = null) {
      gl.useProgram(program);
      const r = restOverride ?? restPose(t);
      gl.uniform4f(restMotion, r.nod, r.breath, r.cloth, r.elapsed);
      gl.uniform1f(phase, t);
      gl.uniform3f(angles, pose.upper, pose.forearm, pose.wrist);
      gl.uniform1f(blink, amount);
      gl.viewport(0, 0, ART_WIDTH, ART_HEIGHT);
      gl.clearColor(0, 0, 0, 0);
      gl.clear(gl.COLOR_BUFFER_BIT);
      gl.activeTexture(gl.TEXTURE0);
      gl.bindTexture(gl.TEXTURE_2D, baseTexture);
      gl.uniform1f(layerPass, 0);
      gl.drawArrays(gl.TRIANGLES, 0, data.length / 9);
      if (layers.hasRepair) {
        gl.bindTexture(gl.TEXTURE_2D, artTexture);
        gl.uniform1f(layerPass, 1);
        gl.drawArrays(gl.TRIANGLES, 0, data.length / 9);
      }
      return surface;
    },
    joints: { shoulder, elbow, wrist },
    eyePaths,
    layers,
  };
}
export function makeBackgroundRenderer(image) {
  const surface = document.createElement('canvas');
  surface.width = 1536;
  surface.height = 1024;
  const gl = surface.getContext('webgl', {
    alpha: false,
    antialias: false,
    preserveDrawingBuffer: true,
  });
  if (!gl) return { draw: () => image };
  const v = compile(
    gl,
    gl.VERTEX_SHADER,
    'attribute vec2 p;varying vec2 uv;void main(){uv=vec2((p.x+1.)*.5,(1.-p.y)*.5);gl_Position=vec4(p,0.,1.);}',
  );
  const f = compile(
    gl,
    gl.FRAGMENT_SHADER,
    `precision highp float;varying vec2 uv;uniform sampler2D art;uniform sampler2D windMap;uniform float phase;
 void main(){vec2 q=uv;vec2 weight=texture2D(windMap,uv).rg;
 q.x+=(weight.r*2.4+weight.g*1.7)/1536.*sin(phase*.87-uv.y*4.3);gl_FragColor=texture2D(art,q);}`,
  );
  const pr = gl.createProgram();
  gl.attachShader(pr, v);
  gl.attachShader(pr, f);
  gl.linkProgram(pr);
  if (!gl.getProgramParameter(pr, gl.LINK_STATUS)) throw Error(gl.getProgramInfoLog(pr));
  gl.useProgram(pr);
  const b = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, b);
  gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW);
  const p = gl.getAttribLocation(pr, 'p');
  gl.enableVertexAttribArray(p);
  gl.vertexAttribPointer(p, 2, gl.FLOAT, false, 0, 0);
  texture(gl, 0, image);
  gl.uniform1i(gl.getUniformLocation(pr, 'art'), 0);
  const wind = document.createElement('canvas');
  wind.width = 1536;
  wind.height = 1024;
  const wc = wind.getContext('2d'),
    pixels = wc.createImageData(1536, 1024);
  for (const [x0, x1, y0, y1] of [
    [290, 448, 440, 790],
    [1348, 1536, 444, 975],
  ])
    for (let y = y0; y < y1; y++)
      for (let x = x0; x < x1; x++) {
        const w = inspectBackgroundWeight(x, y),
          n = (y * 1536 + x) * 4;
        pixels.data[n] = Math.round(w.left * 255);
        pixels.data[n + 1] = Math.round(w.right * 255);
        pixels.data[n + 3] = 255;
      }
  wc.putImageData(pixels, 0, 0);
  texture(gl, 1, wind);
  gl.uniform1i(gl.getUniformLocation(pr, 'windMap'), 1);
  const phase = gl.getUniformLocation(pr, 'phase');
  return {
    dispose() {
      gl.getExtension('WEBGL_lose_context')?.loseContext();
    },
    draw(t) {
      gl.useProgram(pr);
      gl.uniform1f(phase, t);
      gl.viewport(0, 0, 1536, 1024);
      gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
      return surface;
    },
  };
}
/* global document */
