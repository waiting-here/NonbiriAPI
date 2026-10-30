import { eyeMeasurements } from './eyelids';
import type { SweetCard } from './motion';

export type EyeMeasurement = {
  origin: [number, number];
  axis: [number, number];
  span: [number, number];
  /** Actual ink extents in each state; the far eye's upper lash is shorter than its corner axis. */
  lashSpans: [number, number][];
  maskBox: [number, number, number, number];
  maskRLE: string;
  /** Open, half, closed; each entry is an upper/lower lash normal in original pixels. */
  ribbons: [number, number][][];
  /** Per-column lower support of the original eye opening, including its feather. */
  lowerBounds: number[];
  originalInkBottom: number[];
};
export const profileWidth = 64;
export const faceCrops: Record<SweetCard, [number, number, number, number]> = {
  's-chatgpt': [300, 190, 360, 360],
  's-claude': [480, 60, 220, 220],
};

export function decodeMask(eye: EyeMeasurement): Uint8Array {
  const [x0, y0, x1, y1] = eye.maskBox;
  const output = new Uint8Array((x1 - x0) * (y1 - y0));
  const bytes = atob(eye.maskRLE);
  let cursor = 0;
  for (let i = 0; i < bytes.length; i += 2) {
    const end = cursor + bytes.charCodeAt(i);
    output.fill(bytes.charCodeAt(i + 1), cursor, end);
    cursor = end;
  }
  return output;
}

export function eyeTextures(id: SweetCard) {
  const eyes = eyeMeasurements[id];
  const width = Math.max(...eyes.map((e) => e.maskBox[2] - e.maskBox[0]));
  const height = eyes.reduce((n, e) => n + e.maskBox[3] - e.maskBox[1], 0);
  const mask = new Uint8Array(width * height);
  const placements: [number, number, number, number][] = [];
  const profiles = new Uint8Array(profileWidth * 8 * 4);
  let row = 0;
  eyes.forEach((eye, index) => {
    const w = eye.maskBox[2] - eye.maskBox[0],
      h = eye.maskBox[3] - eye.maskBox[1];
    const pixels = decodeMask(eye);
    for (let y = 0; y < h; y++) mask.set(pixels.subarray(y * w, (y + 1) * w), (row + y) * width);
    placements.push([0, row / height, w / width, h / height]);
    row += h;
    [...eye.ribbons, eye.lowerBounds.map((n, x) => [n, eye.originalInkBottom[x]])].forEach(
      (ribbon, state) =>
        ribbon.forEach((band, x) => {
          band.forEach((normal, channel) => {
            const n = Math.round(((normal + 64) / 128) * 65535);
            const i = ((index * 4 + state) * profileWidth + x) * 4 + channel * 2;
            profiles[i] = n >> 8;
            profiles[i + 1] = n & 255;
          });
        }),
    );
  });
  return { eyes, mask, width, height, placements, profiles };
}

export const eyelidShader = `
vec2 band(float x,float row){
  vec4 q=texture2D(profiles,vec2((clamp(x,0.,1.)*63.+.5)/64.,row));
  return vec2(q.r*65280.+q.g*255.,q.b*65280.+q.a*255.)/65535.*128.-64.;
}
vec4 eyeArt(vec2 p,float state){
  vec2 q=(p-face.xy)/face.zw;
  return texture2D(eyeAtlas,vec2(q.x,(q.y+state)*.5));
}
vec2 eyePoint(vec2 origin,vec2 axis,float tangent,float normal){
  return origin+axis*tangent+vec2(-axis.y,axis.x)*normal;
}
vec4 eye(vec2 p,vec4 original,vec4 basis,vec2 span,vec4 openHalfRange,vec2 closedRange,vec4 box,vec4 placement,float row){
  if(blink<=0.)return original;
  vec2 local=(p-box.xy)/box.zw;
  if(local.x<=0.||local.y<=0.||local.x>=1.||local.y>=1.)return original;
  float coverage=texture2D(masks,placement.xy+local*placement.zw).r;
  if(coverage<=0.)return original;
  vec2 delta=p-basis.xy,axis=basis.zw;
  float tangent=dot(delta,axis),normal=dot(delta,vec2(-axis.y,axis.x));
  float along=(tangent-span.x)/(span.y-span.x);
  float segment=blink<.5?blink*2.:(blink-.5)*2.;
  vec2 currentRange=blink<.5?mix(openHalfRange.xy,openHalfRange.zw,segment):mix(openHalfRange.zw,closedRange,segment);
  float ribbonAlong=(tangent-currentRange.x)/(currentRange.y-currentRange.x);
  float ribbonPosition=clamp(ribbonAlong,0.,1.);
  vec3 tangents=vec3(mix(openHalfRange.x,openHalfRange.y,ribbonPosition),mix(openHalfRange.z,openHalfRange.w,ribbonPosition),mix(closedRange.x,closedRange.y,ribbonPosition));
  vec3 sourceAlong=(tangents-vec3(span.x))/(span.y-span.x);
  vec2 opened=band(sourceAlong.x,row),halfway=band(sourceAlong.y,row+1./8.),closed=band(sourceAlong.z,row+2./8.);
  vec2 current=blink<.5?mix(opened,halfway,segment):mix(halfway,closed,segment);
  // A narrow ribbon transports the authored lash. The iris is never remapped.
  float edge=.6;
  float ribbon=smoothstep(current.x-edge,current.x+edge,normal)*(1.-smoothstep(current.y-edge,current.y+edge,normal));
  ribbon*=smoothstep(-.02,.02,ribbonAlong)*(1.-smoothstep(.98,1.02,ribbonAlong));
  float vertical=clamp((normal-current.x)/max(.5,current.y-current.x),0.,1.);
  vec2 fromOpen=eyePoint(basis.xy,axis,tangents.x,mix(opened.x,opened.y,vertical));
  vec2 fromHalf=eyePoint(basis.xy,axis,tangents.y,mix(halfway.x,halfway.y,vertical));
  vec2 fromClosed=eyePoint(basis.xy,axis,tangents.z,mix(closed.x,closed.y,vertical));
  vec4 lash=blink<.5?mix(texture2D(hero,sourceUV(fromOpen)),eyeArt(fromHalf,0.),segment):mix(eyeArt(fromHalf,0.),eyeArt(fromClosed,1.),segment);
  vec4 resting=eyeArt(p,1.);
  // Remove the stationary final lash from the skin fill while the moving lash
  // approaches it. Skin samples are local to this eye, below the protected brow.
  vec2 fixedClosed=band(along,row+2./8.);
  vec4 above=eyeArt(eyePoint(basis.xy,axis,tangent,fixedClosed.x-3.),1.);
  vec4 below=eyeArt(eyePoint(basis.xy,axis,tangent,fixedClosed.y+4.),1.);
  float finalRibbon=smoothstep(fixedClosed.x-1.5,fixedClosed.x,normal)*(1.-smoothstep(fixedClosed.y,fixedClosed.y+3.,normal));
  resting=mix(resting,mix(above,below,clamp((normal-fixedClosed.x)/max(1.,fixedClosed.y-fixedClosed.x),0.,1.)),finalRibbon);
  float bottom=mix(band(along,row+3./8.).x,closed.y,blink*blink);
  float aperture=smoothstep(current.y-edge,current.y+edge,normal)*(1.-smoothstep(bottom-edge,bottom+edge,normal));
  // Outer-corner mascara belongs to the upper lash, not to the iris aperture.
  // Its measured lower edge prevents the original triangular tip remaining
  // under the moving ribbon. The iris-bearing middle keeps its original RGB.
  float corner=row<.5?1.-smoothstep(.26,.38,along):smoothstep(.62,.74,along);
  float inkBottom=max(opened.y,band(along,row+3./8.).y);
  aperture*=mix(1.,smoothstep(inkBottom-edge,inkBottom+edge,normal),corner*smoothstep(0.,.35,blink));
  vec4 color=mix(resting,original,aperture);
  color=mix(color,lash,ribbon);
  color=mix(color,eyeArt(p,1.),smoothstep(.97,1.,blink));
  // The measured mask includes all of the original upper lash. Clipping again
  // at a fitted curve would leave its outer tips behind during partial closure.
  coverage*=smoothstep(0.,.035,blink);
  return mix(original,color,coverage);
}
`;
