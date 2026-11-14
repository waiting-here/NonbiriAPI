import { eyelidShader } from './eyelid';

export const vertexShader = `
attribute vec2 position,ribs,follow;
attribute vec4 primary,secondary;
uniform vec4 flow,artRect;uniform vec2 sourceOffset,sourceCanvas,pivot,translation;
uniform float breath,settle,rotation;
varying vec2 uv,localUV;
void main(){
  uv=position/vec2(1024.,1536.);
  localUV=(position-artRect.xy)/artRect.zw;
  vec2 offset=primary.xy*flow.x+primary.zw*flow.y+
    secondary.xy*flow.z+secondary.zw*flow.w+ribs*breath+follow*settle;
  float c=cos(rotation),s=sin(rotation);vec2 q=position-pivot;
  vec2 p=(pivot+vec2(c*q.x-s*q.y,s*q.x+c*q.y)+offset+translation+sourceOffset)/sourceCanvas;
  gl_Position=vec4(p.x*2.-1.,1.-p.y*2.,0.,1.);
}
`;

export function fragmentShader(): string {
  return `precision highp float;
varying vec2 uv,localUV;
uniform sampler2D hero,piece,eyeAtlas,masks,profiles;
uniform float blink;
uniform vec4 face,basis0,basis1,box0,box1,placement0,placement1;
uniform vec4 openHalfRange0,openHalfRange1;
uniform vec2 span0,span1,closedRange0,closedRange1;
uniform vec2 sourceOffset,sourceCanvas;
const vec2 size=vec2(1024.,1536.);
vec2 sourceUV(vec2 p){return (p+sourceOffset)/sourceCanvas;}
${eyelidShader}
void main(){
  vec2 p=uv*size;
  if(localUV.x<0.||localUV.y<0.||localUV.x>1.||localUV.y>1.)discard;
  vec4 color=texture2D(piece,localUV);
  color=eye(p,color,basis0,span0,openHalfRange0,closedRange0,box0,placement0,1./16.);
  gl_FragColor=eye(p,color,basis1,span1,openHalfRange1,closedRange1,box1,placement1,9./16.);
}`;
}
