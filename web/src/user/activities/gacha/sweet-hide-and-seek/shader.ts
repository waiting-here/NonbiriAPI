import { eyelidShader } from './eyelid';
import { motionShader } from './motion';
import type { SweetCard } from './motion';

export const vertexShader = `
attribute vec2 position;varying vec2 uv;
void main(){uv=position;gl_Position=vec4(position.x*2.-1.,1.-position.y*2.,0.,1.);}
`;

export function fragmentShader(id: SweetCard): string {
  return `precision highp float;
varying vec2 uv;
uniform sampler2D hero,eyeAtlas,masks,profiles;
uniform float time,blink;
uniform vec4 face,basis0,basis1,box0,box1,placement0,placement1;
uniform vec4 openHalfRange0,openHalfRange1;
uniform vec2 span0,span1,closedRange0,closedRange1;
const vec2 size=vec2(1024.,1536.);
${motionShader(id)}
${eyelidShader}
void main(){
  vec2 p=motion(uv*size);
  vec4 color=texture2D(hero,p/size);
  color=eye(p,color,basis0,span0,openHalfRange0,closedRange0,box0,placement0,1./16.);
  gl_FragColor=eye(p,color,basis1,span1,openHalfRange1,closedRange1,box1,placement1,9./16.);
}`;
}
