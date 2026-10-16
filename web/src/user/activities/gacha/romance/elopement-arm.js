const W = 1024,
  H = 1536;
const armOutline = new Path2D(
  'M347 398 C360 388 385 393 397 409 C408 426 397 447 384 468 C384 481 389 496 386 514 C391 519 399 508 406 510 C412 511 414 516 408 520 L387 534 L376 537 C381 543 388 549 384 555 C381 558 375 551 369 548 C369 552 366 559 361 558 C352 553 342 543 335 534 L328 510 C321 513 314 521 305 530 C294 540 282 544 277 539 C274 535 287 524 292 516 L308 490 C316 479 316 469 315 465 C305 472 294 470 283 463 C278 459 280 454 286 453 C298 456 308 451 319 446 L333 440 C334 427 339 407 347 398 Z',
);

const shoulder = [405, 277],
  elbow = [372, 403];
const upperRegion = [
  [350, 237],
  [412, 239],
  [447, 267],
  [460, 312],
  [455, 362],
  [443, 423],
  [422, 461],
  [307, 467],
  [286, 406],
  [310, 350],
  [320, 289],
  [335, 251],
];
const smooth = (a, b, x) => {
  const t = Math.max(0, Math.min(1, (x - a) / (b - a)));
  return t * t * (3 - 2 * t);
};
function upperWeight(x, y) {
  let inside = false,
    distance = Infinity;
  for (let i = 0, j = upperRegion.length - 1; i < upperRegion.length; j = i++) {
    const [ax, ay] = upperRegion[j],
      [bx, by] = upperRegion[i];
    if (ay > y !== by > y && x < ((bx - ax) * (y - ay)) / (by - ay) + ax) inside = !inside;
    const dx = bx - ax,
      dy = by - ay,
      t = Math.max(0, Math.min(1, ((x - ax) * dx + (y - ay) * dy) / (dx * dx + dy * dy)));
    distance = Math.min(distance, Math.hypot(x - ax - t * dx, y - ay - t * dy));
  }
  const along = ((x - 405) * -33 + (y - 277) * 126) / Math.hypot(33, 126);
  return inside ? smooth(0, 28, distance) * smooth(-18, 50, along) : 0;
}
function shader(gl, type, text) {
  const s = gl.createShader(type);
  gl.shaderSource(s, text);
  gl.compileShader(s);
  if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s));
  return s;
}
export function makeArmRig(images) {
  const base = document.createElement('canvas');
  base.width = W;
  base.height = H;
  const b = base.getContext('2d');
  b.drawImage(images.offer, 0, 0);
  const repair = document.createElement('canvas');
  repair.width = W;
  repair.height = H;
  const r = repair.getContext('2d');
  r.drawImage(images.clean, 0, 0);
  const repairMask = document.createElement('canvas');
  repairMask.width = W;
  repairMask.height = H;
  const m = repairMask.getContext('2d');
  m.filter = 'blur(5px)';
  m.fillStyle = 'white';
  m.beginPath();
  m.moveTo(326, 397);
  m.lineTo(409, 396);
  m.lineTo(422, 487);
  m.lineTo(424, 570);
  m.lineTo(270, 577);
  m.lineTo(262, 438);
  m.closePath();
  m.fill();
  r.globalCompositeOperation = 'destination-in';
  r.drawImage(repairMask, 0, 0);
  b.drawImage(repair, 0, 0);

  const plate = document.createElement('canvas');
  plate.width = W;
  plate.height = H;
  const p = plate.getContext('2d');
  p.save();
  p.clip(armOutline);
  p.drawImage(images.offer, 0, 0);
  p.restore();
  const surface = document.createElement('canvas');
  surface.width = W;
  surface.height = H;
  const gl = surface.getContext('webgl', {
    alpha: true,
    premultipliedAlpha: true,
    antialias: false,
    preserveDrawingBuffer: true,
  });
  if (!gl)
    return {
      base: images.offer,
      draw() {
        return images.offer;
      },
      supported: false,
    };
  const v = shader(
    gl,
    gl.VERTEX_SHADER,
    `
    attribute vec2 pos;attribute float upperWeight;varying vec2 uv;
    uniform float extension;uniform float wrist;uniform float upper;uniform float armPass;
    vec2 turn(vec2 p,float a){float c=cos(a),s=sin(a);return vec2(c*p.x-s*p.y,s*p.x+c*p.y);}
    void main(){
      uv=pos/vec2(1024.,1536.);
      vec2 shoulder=vec2(405.,277.),elbow=vec2(372.,403.),handJoint=vec2(352.,456.);
      float upperAngle=-.085*(1.-upper);vec2 target;
      if(armPass<.5){target=mix(pos,shoulder+turn(pos-shoulder,upperAngle),upperWeight);}
      else{
        float forearmAngle=-.30*(1.-extension);
        vec2 forearm=elbow+turn(pos-elbow,forearmAngle);
        vec2 movedWrist=elbow+turn(handJoint-elbow,forearmAngle);
        vec2 hand=movedWrist+turn(forearm-movedWrist,wrist);
        float handWeight=smoothstep(-10.,13.,dot(pos-handJoint,normalize(vec2(-28.,51.))));
        vec2 bent=mix(forearm,hand,handWeight);
        float cuffWeight=smoothstep(0.,32.,dot(pos-elbow,normalize(vec2(-11.,50.))));
        vec2 local=mix(pos,bent,cuffWeight);target=shoulder+turn(local-shoulder,upperAngle);
      }
      gl_Position=vec4(target.x/512.-1.,1.-target.y/768.,0.,1.);
    }`,
  );
  const f = shader(
    gl,
    gl.FRAGMENT_SHADER,
    `
    precision mediump float;
    varying vec2 uv;uniform sampler2D art;
    void main(){vec4 c=texture2D(art,uv);gl_FragColor=vec4(c.rgb*c.a,c.a);}
  `,
  );
  const program = gl.createProgram();
  gl.attachShader(program, v);
  gl.attachShader(program, f);
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS))
    throw new Error(gl.getProgramInfoLog(program));
  gl.useProgram(program);
  const baseVertices = [],
    armVertices = [];
  const xs = [
    0,
    160,
    240,
    270,
    ...Array.from({ length: 41 }, (_, i) => 280 + i * 5),
    512,
    640,
    768,
    1024,
  ];
  const ys = [
    0,
    160,
    210,
    230,
    ...Array.from({ length: 49 }, (_, i) => 240 + i * 5),
    512,
    768,
    1024,
    1536,
  ];
  const add = (list, x, y) => list.push(x, y, upperWeight(x, y));
  for (let row = 0; row < ys.length - 1; row++)
    for (let col = 0; col < xs.length - 1; col++) {
      const x = xs[col],
        y = ys[row],
        xx = xs[col + 1],
        yy = ys[row + 1];
      [
        [x, y],
        [xx, y],
        [x, yy],
        [xx, y],
        [xx, yy],
        [x, yy],
      ].forEach((p) => add(baseVertices, ...p));
    }
  for (let row = 0; row < 28; row++)
    for (let col = 0; col < 24; col++) {
      const x = 267 + (col * 153) / 24,
        y = 386 + (row * 180) / 28,
        dx = 153 / 24,
        dy = 180 / 28;
      [
        [x, y],
        [x + dx, y],
        [x, y + dy],
        [x + dx, y],
        [x + dx, y + dy],
        [x, y + dy],
      ].forEach((p) => add(armVertices, ...p));
    }
  const makeBuffer = (data) => {
    const b = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, b);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(data), gl.STATIC_DRAW);
    return b;
  };
  const baseBuffer = makeBuffer(baseVertices),
    armBuffer = makeBuffer(armVertices);
  const pos = gl.getAttribLocation(program, 'pos'),
    weight = gl.getAttribLocation(program, 'upperWeight');
  gl.enableVertexAttribArray(pos);
  gl.enableVertexAttribArray(weight);
  const bindBuffer = (b) => {
    gl.bindBuffer(gl.ARRAY_BUFFER, b);
    gl.vertexAttribPointer(pos, 2, gl.FLOAT, false, 12, 0);
    gl.vertexAttribPointer(weight, 1, gl.FLOAT, false, 12, 8);
  };
  const makeTexture = (source) => {
    const tex = gl.createTexture();
    gl.bindTexture(gl.TEXTURE_2D, tex);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, source);
    return tex;
  };
  const baseTexture = makeTexture(base),
    armTexture = makeTexture(plate);
  gl.uniform1i(gl.getUniformLocation(program, 'art'), 0);
  const extension = gl.getUniformLocation(program, 'extension'),
    wrist = gl.getUniformLocation(program, 'wrist'),
    upper = gl.getUniformLocation(program, 'upper'),
    armPass = gl.getUniformLocation(program, 'armPass');
  gl.enable(gl.BLEND);
  gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
  let last = -1,
    lastWrist = NaN,
    lastUpper = NaN;
  const weightMap = document.createElement('canvas');
  weightMap.width = W;
  weightMap.height = H;
  const wc = weightMap.getContext('2d'),
    pixels = wc.createImageData(W, H);
  for (let y = 230; y < 480; y++)
    for (let x = 280; x < 480; x++) {
      const at = (y * W + x) * 4;
      pixels.data[at] = 40;
      pixels.data[at + 1] = 230;
      pixels.data[at + 2] = 180;
      pixels.data[at + 3] = Math.round(upperWeight(x, y) * 180);
    }
  wc.putImageData(pixels, 0, 0);
  return {
    base,
    plate,
    weightMap,
    supported: true,
    dispose() {
      gl.getExtension('WEBGL_lose_context')?.loseContext();
    },
    joints(amount) {
      const a = -0.085 * (1 - amount),
        c = Math.cos(a),
        s = Math.sin(a),
        x = elbow[0] - shoulder[0],
        y = elbow[1] - shoulder[1];
      return { shoulder, elbow: [shoulder[0] + c * x - s * y, shoulder[1] + s * x + c * y] };
    },
    draw(amount, wristAmount = 0, upperAmount = 1) {
      if (amount !== last || wristAmount !== lastWrist || upperAmount !== lastUpper) {
        gl.useProgram(program);
        gl.uniform1f(extension, amount);
        gl.uniform1f(wrist, wristAmount);
        gl.uniform1f(upper, upperAmount);
        gl.viewport(0, 0, W, H);
        gl.clearColor(0, 0, 0, 0);
        gl.clear(gl.COLOR_BUFFER_BIT);
        gl.uniform1f(armPass, 0);
        gl.bindTexture(gl.TEXTURE_2D, baseTexture);
        bindBuffer(baseBuffer);
        gl.drawArrays(gl.TRIANGLES, 0, baseVertices.length / 3);
        gl.uniform1f(armPass, 1);
        gl.bindTexture(gl.TEXTURE_2D, armTexture);
        bindBuffer(armBuffer);
        gl.drawArrays(gl.TRIANGLES, 0, armVertices.length / 3);
        last = amount;
        lastWrist = wristAmount;
        lastUpper = upperAmount;
      }
      return surface;
    },
  };
}
/* global document, Path2D */
