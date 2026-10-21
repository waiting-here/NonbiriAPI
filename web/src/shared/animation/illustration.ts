import { userBusy } from './activity';

export type EyelidProfile = {
  bounds: [number, number, number, number];
  rows: [number[], number[]];
};
export const eyelidProfileWidth = 48;

const smooth = (value: number) => {
  const t = Math.max(0, Math.min(1, value));
  return t * t * (3 - 2 * t);
};

/** A quick closure and slower release, with enough intermediate eyelid poses. */
export function blinkAt(seconds: number): number {
  const cycle = seconds % 17.6;
  for (const start of [2.2, 7.4, 13.1]) {
    const t = cycle - start;
    if (t >= 0 && t < 0.13) return smooth(t / 0.13);
    if (t >= 0.13 && t < 0.175) return 1;
    if (t >= 0.175 && t < 0.415) return 1 - smooth((t - 0.175) / 0.24);
  }
  return 0;
}

/**
 * Keep the fractional frame budget instead of rounding every frame up to a vsync.
 * Ambient backgrounds paint at a quarter rate while the user is typing or scrolling.
 */
export class PaintCadence {
  private next = -Infinity;
  constructor(
    private readonly interval = 1000 / 60,
    private readonly ambient = false,
  ) {}
  reset() {
    this.next = -Infinity;
  }
  due(now: number): boolean {
    if (now + 0.5 < this.next) return false;
    const step = this.ambient && userBusy(now) ? this.interval * 4 : this.interval;
    if (!Number.isFinite(this.next) || now - this.next > step * 2) this.next = now + step;
    else this.next += step;
    return true;
  }
}

export const eyelidShader = `
// Each eye follows its painted contour. Only the lash ribbon is remapped;
// iris, highlights and all surrounding skin retain their original coordinates.
vec4 eyelid(vec2 p,vec4 box,float row,vec4 original) {
  if(blink<=0.) return original;
  vec2 q=(p-box.xy)/box.zw;
  if(q.x<=0. || q.x>=1. || q.y<=0. || q.y>=1.) return original;
  float sampleX=(q.x*${eyelidProfileWidth - 1}.0+.5)/${eyelidProfileWidth}.0;
  vec4 opened=texture2D(lids,vec2(sampleX,row));
  vec3 shut=texture2D(lids,vec2(sampleX,row+.25)).rgb;
  float edge=.012;
  float coverage=smoothstep(opened.r,opened.r+edge,q.y)
    *(1.-smoothstep(opened.g-edge,opened.g,q.y))
    *smoothstep(0.,.035,q.x)*(1.-smoothstep(.965,1.,q.x));
  coverage*=shut.z*smoothstep(0.,.075,blink);
  if(coverage<=0.) return original;
  float upper=mix(opened.b,shut.x,blink);
  float lower=mix(opened.a,shut.y,blink);
  float bottom=mix(opened.g,shut.y,blink*blink);
  float aperture=smoothstep(lower-edge,lower+edge,q.y)
    *(1.-smoothstep(bottom-edge,bottom,q.y))*(1.-smoothstep(.98,1.,blink));
  vec4 resting=texture2D(closed,p);
  // The final lash is drawn by the moving ribbon below. Keep it out of the
  // fixed skin fill, otherwise two lash lines appear during partial closure.
  vec2 skinAbove=vec2(p.x,box.y+max(0.,shut.x-.055)*box.w);
  vec2 skinBelow=vec2(p.x,box.y+min(1.,shut.y+.055)*box.w);
  float finalRibbon=smoothstep(shut.x-.025,shut.x,q.y)
    *(1.-smoothstep(shut.y,shut.y+.025,q.y));
  vec4 lidSkin=mix(texture2D(closed,skinAbove),texture2D(closed,skinBelow),
    clamp((q.y-shut.x)/max(.005,shut.y-shut.x),0.,1.));
  resting=mix(resting,lidSkin,finalRibbon);
  // Fill the authored eye opening at fixed coordinates; colour similarity
  // cannot distinguish an iris highlight from skin at full closure.
  vec4 color=mix(original,resting,1.-aperture);
  float ribbon=smoothstep(upper-edge,upper+edge,q.y)
    *(1.-smoothstep(lower-edge,lower+edge,q.y));
  float along=clamp((q.y-upper)/max(.005,lower-upper),0.,1.);
  vec2 fromOpen=vec2(p.x,box.y+mix(opened.b,opened.a,along)*box.w);
  vec2 fromClosed=vec2(p.x,box.y+mix(shut.x,shut.y,along)*box.w);
  vec4 lash=mix(texture2D(art,fromOpen),texture2D(closed,fromClosed),blink);
  color=mix(color,lash,ribbon);
  color=mix(color,texture2D(closed,p),smoothstep(.96,1.,blink));
  return mix(original,color,coverage);
}

`;
