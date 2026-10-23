import { eyelidShader } from '@shared/animation/illustration';
import { mirrorSeaOptics } from './mirror-sea-optics';

export const mirrorSeaShader = `precision highp float;
varying vec2 uv;
uniform sampler2D scenery,art,closed,lids,shadow;
uniform vec4 view,heroBox,leftEye,rightEye;
uniform float time,blink,night,characterOnly,backgroundOnly,polish,mirrorFlow;
uniform float characterPlate,reflectionPlate,lightPlate,independentBackground;
const float TAU=6.28318530718;
float feather(vec2 p,vec2 c,vec2 r){return 1.-smoothstep(.45,1.,length((p-c)/r));}
float noiseHash(vec2 p){
  vec3 q=fract(vec3(p.xyx)*.1031);
  q+=dot(q,q.yzx+33.33);
  return fract((q.x+q.y)*q.z);
}
float waterNoise(vec2 p){
  vec2 i=floor(p),f=fract(p);
  f=f*f*(3.-2.*f);
  return mix(mix(noiseHash(i),noiseHash(i+vec2(1.,0.)),f.x),
             mix(noiseHash(i+vec2(0.,1.)),noiseHash(i+vec2(1.)),f.x),f.y);
}
float waterMask(vec2 p){
  // The shore follows the marble platform; the hanging mirror frame stays rigid.
  float shore=mix(.557,.525,smoothstep(.73,.80,p.y))+.18*smoothstep(.86,1.,p.y);
  float outside=smoothstep(.515,.575,p.y)*(1.-smoothstep(shore-.009,shore,p.x));
  float inside=feather(p,vec2(.698,.58),vec2(.085,.15))*smoothstep(.55,.60,p.y);
  inside*=1.-feather(p,vec2(.752,.711),vec2(.048,.036));
  float right=feather(p,vec2(.863,.591),vec2(.026,.032));
  float sunset=feather(p,vec2(.525,.407),vec2(.014,.026));
  // Foreground lotus petals and their stems belong above the moving reflections.
  float lotus=max(feather(p,vec2(.502,.778),vec2(.022,.033)),
                  feather(p,vec2(.492,.908),vec2(.023,.027)));
  return clamp(outside+inside+right+sunset,0.,1.)*(1.-lotus);
}
${eyelidShader}
${mirrorSeaOptics}
vec3 touchLight(vec2 offset){
  float phase=TAU*time/17.6;
  vec2 d=offset*vec2(1.77778,1.);
  float distanceFromHand=length(d);
  vec3 light=vec3(0.);
  for(int i=0;i<2;i++){
    float age=fract(time/8.8+float(i)*.5);
    float ring=(1.-smoothstep(.0005,.0018,abs(distanceFromHand-(.003+age*.052))))*pow(sin(age*3.14159),2.)*.26;
    light+=vec3(.34,.21,.45)*ring;
  }
  if(polish>.5)light+=vec3(.17,.12,.24)*exp(-dot(d,d)/.002)*(.42+.08*sin(phase));
  return light;
}
vec4 sea(vec2 p){
  float phase=TAU*time/17.6;
  float water=waterMask(p);
  vec2 refraction=mirrorFlow>.5 ? mirrorCurrent(p) : vec2(0.);
  // Cloud and garden motion stays within the existing distant glass interiors.
  refraction+=vec2(.0012*sin(phase*.5),.0004*cos(phase*.5))*glassMask(p,cloudGlass);
  refraction+=vec2(.0008*sin(phase+.7),0.)*glassMask(p,gardenGlass)
    *feather(p,vec2(.079,.245),vec2(.026,.084));
  vec4 color;
  if(water>0.){
    float depth=clamp((p.y-.50)*2.,0.,1.);
    // Advect the painted reflections with two irregular, overlapping currents.
    // Wavelength and displacement grow toward the viewer; the orbit closes smoothly.
    vec2 plane=vec2((p.x-.4)*mix(38.,12.,depth),log(1.+depth*5.)*16.);
    vec2 orbit=vec2(cos(phase),sin(phase));
    vec2 broad=vec2(waterNoise(plane+orbit*.75),
                    waterNoise(plane*.81-orbit.yx*.62+7.3))-.5;
    vec2 detail=vec2(waterNoise(plane*2.7-orbit*1.2+13.1),
                     waterNoise(plane*2.1+orbit.yx+3.8))-.5;
    vec2 current=broad*.74+detail*.26;
    vec2 displacement=current*vec2(.0006+.0050*depth,.0003+.0021*depth)*water;
    color=texture2D(scenery,p+displacement+refraction);
    // Light breathes only on highlights already present in the painting.
    float reflection=smoothstep(.28,.85,dot(color.rgb,vec3(.2126,.7152,.0722)));
    float glint=waterNoise(plane*1.8+orbit*.85+21.);
    color.rgb*=1.+water*reflection*(glint-.5)*mix(.04,.18,depth);
  }else color=texture2D(scenery,p+refraction);
  float rain=feather(p,vec2(.173,.413),vec2(.026,.104));
  vec2 drops=vec2(p.x*990.,p.y*135.-phase*7.);
  float streak=pow(max(0.,sin(drops.x)),26.)*pow(max(0.,sin(drops.y+sin(floor(drops.x))*8.)),12.);
  color.rgb+=vec3(.35,.45,.62)*streak*rain*.22;
  float stone=feather(p,vec2(.738,.883),vec2(.16,.11));
  float reflectedLight=smoothstep(.45,.9,dot(color.rgb,vec3(.2126,.7152,.0722)));
  color.rgb+=prism(.15-phase/TAU*.5)*stone*reflectedLight*.035*(.5+.5*sin(phase*.5+.5));
  vec2 touch=heroBox.xy+heroBox.zw*vec2(.145,.128);
  if(polish>.5){
    // Keep the mirror and sea readable while reserving the brightest values for her face.
    float value=dot(color.rgb,vec3(.2126,.7152,.0722));
    color.rgb=mix(vec3(value),color.rgb,.87)*.89;
  }
  if(mirrorFlow>.5)color.rgb=mirrorOptics(color.rgb,p);
  if(independentBackground<.5){
    if(mirrorFlow>.5)color.rgb=centralEcho(color.rgb,p);
    color.rgb+=touchLight(p-touch);
  }
  return color;
}
void main(){
  if(reflectionPlate>.5){gl_FragColor=centralEchoLayer(uv);return;}
  if(lightPlate>.5){gl_FragColor=vec4(touchLight((uv-.5)*vec2(.16,.2844448)),1.);return;}
  vec2 p=view.xy+uv*view.zw;
  vec4 background=max(characterOnly,characterPlate)>.5 ? vec4(0.) : sea(p);
  if(backgroundOnly>.5){gl_FragColor=background;return;}
  vec2 q=(p-heroBox.xy)/heroBox.zw;
  if(q.x<0. || q.x>1. || q.y<0. || q.y>1.){gl_FragColor=background;return;}
  vec4 contact=texture2D(shadow,q);
  background.rgb=mix(background.rgb,contact.rgb,contact.a);
  float phase=TAU*time/17.6;
  // Finite fields on detached hair ends, hanging sleeve and skirt edges.
  // Roots, face, shoulders, torso, hands and legs retain their painted coordinates.
  float hairRight=feather(q,vec2(.90,.42),vec2(.10,.15));
  float hairLeft=feather(q,vec2(.10,.49),vec2(.10,.13));
  float ribbon=feather(q,vec2(.75,.16),vec2(.10,.055));
  float sleeve=feather(q,vec2(.245,.50),vec2(.075,.12));
  float hemLeft=feather(q,vec2(.19,.81),vec2(.14,.12));
  float hemRight=feather(q,vec2(.85,.875),vec2(.13,.095));
  q.x+=.006*sin(phase-q.y*4.)*hairRight+.0045*sin(phase+.7-q.y*4.)*hairLeft;
  q.y+=.0025*sin(phase)*ribbon+.0018*sin(phase+.5)*sleeve;
  q.x+=.0035*sin(phase-1.)*sleeve+.004*sin(phase-q.y*3.)*(hemLeft+hemRight);
  vec4 hero=texture2D(art,q);
  hero=eyelid(q,leftEye,.125,hero);
  hero=eyelid(q,rightEye,.625,hero);
  if(polish>.5){
    float focus=feather(q,vec2(.46,.14),vec2(.30,.25));
    float value=dot(hero.rgb,vec3(.2126,.7152,.0722));
    hero.rgb=mix(vec3(value),hero.rgb,.92+.08*focus);
    float rim=max(0.,hero.a-texture2D(art,q+vec2(-.002,.001)).a);
    hero.rgb+=vec3(.16,.12,.20)*rim*.45;
    hero.rgb*=1.+focus*.025;
    // Specular response stays on the small star clasp, away from eyes and skin.
    float star=feather(q,vec2(.515,.267),vec2(.025,.02));
    hero.rgb+=vec3(.10,.08,.045)*star*pow(.5+.5*sin(phase-.7),5.);
  }
  hero.rgb*=mix(vec3(1.),vec3(.80,.82,.95),night);
  if(characterOnly>.5){gl_FragColor=hero;return;}
  if(characterPlate>.5){
    float alpha=hero.a+contact.a*(1.-hero.a);
    vec3 color=hero.rgb*hero.a+contact.rgb*contact.a*(1.-hero.a);
    gl_FragColor=vec4(alpha>0. ? color/alpha : vec3(0.),alpha);return;
  }
  gl_FragColor=vec4(mix(background.rgb,hero.rgb,hero.a),1.);
}`;
