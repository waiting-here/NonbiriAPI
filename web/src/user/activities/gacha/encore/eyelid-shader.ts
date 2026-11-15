import { eyelidProfileWidth } from '@shared/animation/illustration';

// Separate skin backing prevents the final lash from showing behind a moving lid.
// Only the lash ribbon moves; the iris and surrounding face keep their coordinates.
export const eyelidShader = `
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
    *smoothstep(0.,.025,q.x)*(1.-smoothstep(.975,1.,q.x));
  coverage*=shut.z*smoothstep(0.,.075,blink);
  if(coverage<=0.) return original;
  float upper=mix(opened.b,shut.x,blink);
  float lower=mix(opened.a,shut.y,blink);
  float bottom=mix(opened.g,shut.y,blink*blink);
  float aperture=smoothstep(lower-edge,lower+edge,q.y)
    *(1.-smoothstep(bottom-edge,bottom,q.y))*(1.-smoothstep(.98,1.,blink));
  vec4 color=mix(original,texture2D(eyeSkin,p),1.-aperture);
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
