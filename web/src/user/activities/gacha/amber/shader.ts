import { eyelidShader } from '@shared/animation/illustration';

export const amberShader = `
precision highp float;
varying vec2 uv;
uniform sampler2D scenery, books;
uniform vec4 view, heroBox;
uniform float time, night, characterOnly, backgroundOnly, polish;
vec4 over(vec4 base,vec4 top){
  float a=top.a+base.a*(1.-top.a);
  return vec4((top.rgb*top.a+base.rgb*base.a*(1.-top.a))/max(a,.0001),a);
}
vec4 book(vec2 p,vec2 center,vec2 size,float index,float phase){
  center+=vec2(.004*sin(phase+index*1.9),.012*sin(phase+index*2.1));
  vec2 q=(p-center)/size;
  float angle=.07*sin(phase+index*1.7);
  q=mat2(cos(angle),-sin(angle),sin(angle),cos(angle))*q+.5;
  if(q.x<0. || q.x>1. || q.y<0. || q.y>1.)return vec4(0.);
  vec4 color=texture2D(books,vec2((q.x+index)/3.,q.y));
  color.rgb*=mix(vec3(1.),vec3(.79,.72,.64),night);
  return color;
}
void main(){
  vec2 p=view.xy+uv*view.zw;
  float phase=time*6.28318530718/17.6;
  vec4 color=characterOnly>.5?vec4(0.):texture2D(scenery,p);
  vec2 q=(p-heroBox.xy)/heroBox.zw;
  if(characterOnly<.5){
    // Stationary architecture; only the intensity of the transmitted light changes.
    float rose=exp(-dot((p-vec2(.5,.26))/vec2(.155,.245),(p-vec2(.5,.26))/vec2(.155,.245))*2.);
    float height=smoothstep(.35,.55,p.y)*(1.-smoothstep(.86,1.,p.y));
    float rays=pow(.5+.5*sin((p.x-.5)/max(.16,p.y-.1)*28.),14.)*height;
    float light=(.018+.012*sin(phase))*(rose+rays*.55);
    color.rgb+=vec3(1.,.59,.18)*light;
    color=over(color,book(p,vec2(.332,.628),vec2(.07,.061),0.,phase));
    color=over(color,book(p,vec2(.376,.597),vec2(.06,.04),1.,phase));
    color=over(color,book(p,vec2(.675,.567),vec2(.045,.041),2.,phase));
    // Small motes follow closed paths in depth, keeping the loop free of jumps.
    vec3 dust=vec3(0.);
    for(int i=0;i<24;i++){
      float n=float(i), seed=fract(sin(n*71.31+7.8)*43758.54);
      float x=.16+fract(seed*9.13)*.68;
      float y=.39+fract(seed*5.71)*.53;
      vec2 center=vec2(x+.024*sin(phase+n*2.3),y+.019*cos(phase+n*1.7));
      vec2 d=(p-center)*vec2(1.777,1.);
      float size=.0007+.0013*seed;
      float core=1.-smoothstep(size*.2,size,length(d));
      float glow=exp(-dot(d,d)/(size*size*10.))*.1;
      float brightness=.18+.36*pow(.5+.5*sin(phase+n),2.);
      dust+=(core+glow)*brightness*vec3(1.,.65,.23);
    }
    color.rgb+=dust;
    float contact=exp(-dot((q-vec2(.487,.993))/vec2(.06,.007),(q-vec2(.487,.993))/vec2(.06,.007)));
    float castShadow=exp(-dot((q-vec2(.51,1.005))/vec2(.1,.025),(q-vec2(.51,1.005))/vec2(.1,.025)));
    if(backgroundOnly<.5)color.rgb*=1.-contact*.28-castShadow*.11;
    if(polish>.5){
      float focus=exp(-dot((q-vec2(.49,.16))/vec2(.6,.5),(q-vec2(.49,.16))/vec2(.6,.5)));
      color.rgb*=.83+.11*focus;
      color.rgb+=vec3(.08,.045,.015)*rose;
    }
  }
  gl_FragColor=color;
}
`;

export const characterShader = `
precision highp float;
varying vec2 uv;
uniform sampler2D art, closed, lids;
uniform vec4 leftEye, rightEye;
uniform float blink, night, polish;
${eyelidShader}
void main(){
  // Eyes are evaluated in source coordinates, before joint deformation.
  vec4 hero=texture2D(art,uv);
  hero=eyelid(uv,leftEye,.125,hero);
  hero=eyelid(uv,rightEye,.625,hero);
  if(polish>.5){
    float rim=max(0.,hero.a-texture2D(art,uv+vec2(-.002,.001)).a);
    hero.rgb+=vec3(.13,.075,.025)*rim;
  }
  hero.rgb*=mix(vec3(1.),vec3(.79,.72,.64),night);
  gl_FragColor=hero;
}
`;
