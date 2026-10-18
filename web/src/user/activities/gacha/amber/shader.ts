import { eyelidShader } from '@shared/animation/illustration';
import { environmentLayers } from './environment';

const sceneEffects = `
float hash(vec2 p){return fract(sin(dot(p,vec2(127.1,311.7)))*43758.5453);}
vec3 goldCloud(vec2 p,vec2 center,vec2 size,float phase,float depth){
  center+=vec2(.012*sin(phase+depth),.009*cos(phase+depth*2.));
  vec2 q=(p-center)/size;
  float swirl=sin(q.x*5.+phase*.5+sin(q.y*4.));
  float envelope=exp(-dot(q,q)*1.4)*pow(.5+.5*sin(q.y*7.+swirl*2.),3.);
  vec2 cells=p*(950.+depth*180.);
  vec2 local=fract(cells)-.5;
  float seed=hash(floor(cells));
  float mote=exp(-dot(local,local)*mix(45.,12.,depth/3.))*step(.88,seed);
  return vec3(1.,.60,.16)*envelope*(mote*.9+.015)*(.7+.3*sin(phase+seed*6.));
}
vec4 book(vec2 p,vec2 center,vec2 size,float index,float phase){
  center+=vec2(.004*sin(phase+index*1.9),.012*sin(phase+index*2.1));
  vec2 q=(p-center)/size;
  float angle=.07*sin(phase+index*1.7);
  q=mat2(cos(angle),-sin(angle),sin(angle),cos(angle))*q+.5;
  if(q.x<0. || q.x>1. || q.y<0. || q.y>1.)return vec4(0.);
  // A shallow page fold pivots at the spine; the cover remains rigid.
  float fold=sin(phase+index*2.3);
  vec2 page=q;
  if(q.x>.5 && q.y<.6)page.x=.5+(q.x-.5)*(1.+.12*fold);
  vec4 color=texture2D(books,vec2((page.x+index)/3.,page.y));
  color.rgb*=1.+.045*fold*smoothstep(.5,.65,q.x)*(1.-smoothstep(.5,.7,q.y));
  color.rgb*=mix(vec3(1.),vec3(.69,.61,.51),night);
  return color;
}
`;

export const amberShader = `
precision highp float;
varying vec2 uv;
uniform sampler2D scenery, books, flags, lamps;
uniform vec4 view, heroBox;
uniform float time, night, characterOnly, backgroundOnly, polish, characterPlate;
vec4 over(vec4 base,vec4 top){
  float a=top.a+base.a*(1.-top.a);
  return vec4((top.rgb*top.a+base.rgb*base.a*(1.-top.a))/max(a,.0001),a);
}
${sceneEffects}
vec4 banner(vec2 p,vec4 source,vec4 target,float phase,float index){
  vec2 q=(p-target.xy)/target.zw;
  q.x-=.028*q.y*q.y*sin(phase+index*.8-q.y*1.5);
  if(q.x<0. || q.x>1. || q.y<0. || q.y>1.)return vec4(0.);
  vec4 color=texture2D(flags,source.xy+q*source.zw);
  color.rgb*=mix(vec3(1.),vec3(.56,.58,.57),night);
  color.rgb*=1.+.045*q.y*sin(phase+index*.8-q.y*1.5);
  return color;
}
vec4 chandelier(vec2 p,vec4 source,vec4 target,float phase,float index){
  vec2 pivot=target.xy+vec2(target.z*.5,0.);
  float angle=.009*sin(phase+index*.7);
  vec2 d=(p-pivot)*vec2(1672./941.,1.);
  d=mat2(cos(angle),-sin(angle),sin(angle),cos(angle))*d;
  vec2 q=(pivot+d/vec2(1672./941.,1.)-target.xy)/target.zw;
  vec4 color=vec4(0.);
  if(q.x>=0. && q.x<=1. && q.y>=0. && q.y<=1.){
    color=texture2D(lamps,source.xy+q*source.zw);
    color.rgb*=mix(vec3(1.),vec3(.76,.64,.49),night);
  }
  float flicker=.85+.10*sin(phase*7.+index)+.05*sin(phase*13.+index*2.);
  float halo=exp(-dot((q-vec2(.5,.66))/vec2(.7,.55),(q-vec2(.5,.66))/vec2(.7,.55)))*.11*night*flicker;
  color.rgb=mix(color.rgb,vec3(1.,.46,.09),halo);
  color.a=max(color.a,halo*.35);
  for(int i=0;i<4;i++){
    float n=float(i);
    vec2 flame=(q-vec2(.19+n*.205,.61+abs(n-1.5)*.045))/vec2(.014,.032*flicker);
    float glow=exp(-dot(flame,flame));
    color=over(color,vec4(1.,.71,.27,glow*mix(.28,.85,night)));
  }
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
    float rays=pow(.5+.5*sin((p.x-.5-.004*sin(phase))/max(.16,p.y-.1)*28.),14.)*height;
    float light=(.012+.006*sin(phase))*(rose+rays*.4);
    color.rgb+=mix(vec3(1.,.59,.18),vec3(.28,.38,.57),night)*light;
    ${environmentLayers}
    color.rgb+=goldCloud(p,vec2(.49,.52),vec2(.15,.11),phase,0.);
    color.rgb+=goldCloud(p,vec2(.28,.65),vec2(.14,.13),phase,1.);
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
  }
  if((characterOnly<.5 && backgroundOnly<.5) || characterPlate>.5){
    // Each sole has its own stationary floor contact in the character plate.
    vec2 front=(q-vec2(.487,.984))/vec2(.048,.007);
    vec2 rear=(q-vec2(.573,.951))/vec2(.039,.006);
    float contact=max(exp(-dot(front,front)),exp(-dot(rear,rear)));
    vec2 frontCast=(q-vec2(.528,.997))/vec2(.095,.016);
    vec2 rearCast=(q-vec2(.611,.963))/vec2(.078,.013);
    float castShadow=max(exp(-dot(frontCast,frontCast)),exp(-dot(rearCast,rearCast)));
    float shadow=contact*.62+castShadow*.24;
    if(characterPlate>.5)color=vec4(0.,0.,0.,shadow);
    else color.rgb*=1.-shadow;
  }
  gl_FragColor=color;
}
`;

export const foregroundShader = `
precision highp float;
varying vec2 uv;
uniform sampler2D books;
uniform vec4 view;
uniform float time, night, aspect, landscape;
${sceneEffects}
void main(){
  float phase=time*6.28318530718/17.6;
  vec2 p=mix(uv,view.xy+uv*view.zw,landscape);
  float ratio=mix(aspect,1672./941.,landscape);
  vec3 dust=goldCloud(p,vec2(.09,.85),vec2(.18,.13),phase,2.);
  float alpha=min(.4,max(dust.r,max(dust.g,dust.b)));
  vec4 color=vec4(dust/max(alpha,.001),alpha);
  // Near books stay at opposite edges, clear of the face, hands and navigation.
  vec4 nearBook=book(p,vec2(.025,.86),vec2(.24,.12*ratio),0.,phase);
  color=mix(color,nearBook,nearBook.a*.76);
  vec4 farBook=book(p,vec2(.985,.16),vec2(.15,.075*ratio),2.,phase+.8);
  color=mix(color,farBook,farBook.a*.65);
  gl_FragColor=color;
}
`;

export const characterShader = `
precision highp float;
varying vec2 uv;
uniform sampler2D art, closed, lids, boots;
uniform vec4 leftEye, rightEye;
uniform float blink, night, polish;
${eyelidShader}
void main(){
  // Eyes are evaluated in source coordinates, before joint deformation.
  vec4 hero=texture2D(art,uv);
  hero=eyelid(uv,leftEye,.125,hero);
  hero=eyelid(uv,rightEye,.625,hero);
  if(polish>.5){
    // The footwear patch preserves the original face, hands, costume and eye plates.
    vec2 bootUV=(uv-vec2(440./1037.,1300./1516.))/vec2(210./1037.,216./1516.);
    if(bootUV.x>=0. && bootUV.x<=1. && bootUV.y>=0. && bootUV.y<=1.)
      hero=mix(hero,texture2D(boots,bootUV),smoothstep(.864,.883,uv.y));
    float rim=max(0.,hero.a-texture2D(art,uv+vec2(-.002,.001)).a);
    // Relight both eye states together, in undeformed character coordinates.
    float windowSide=1.-smoothstep(.15,.8,uv.x);
    float face=exp(-dot((uv-vec2(.5,.16))/vec2(.2,.18),(uv-vec2(.5,.16))/vec2(.2,.18)));
    vec3 daylight=hero.rgb*vec3(.91,.84,.74)+vec3(.075,.043,.012)*(windowSide*.65+.35);
    vec3 candlelight=hero.rgb*vec3(.64,.51,.41)+vec3(.09,.042,.012)*windowSide+hero.rgb*face*.10;
    hero.rgb=mix(daylight,candlelight,night);
    hero.rgb+=mix(vec3(.11,.065,.022),vec3(.08,.05,.025),night)*rim;
    float floorBounce=smoothstep(.86,1.,uv.y);
    hero.rgb=mix(hero.rgb,hero.rgb*vec3(1.,.91,.75)+vec3(.025,.012,0.),floorBounce*.45);
  }else{
    hero.rgb*=mix(vec3(1.),vec3(.79,.72,.64),night);
  }
  gl_FragColor=hero;
}
`;
