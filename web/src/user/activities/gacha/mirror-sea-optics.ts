// Mirror-space effects are composited before the opaque character layer.
export const mirrorSeaOptics = `
vec3 prism(float hue){
  return .64+.36*cos(TAU*(hue+vec3(0.,.33,.67)));
}
float glassMask(vec2 p,vec4 box){
  return 1.-smoothstep(.89,.985,length((p-box.xy)/box.zw));
}
vec2 glassCurrent(vec2 p,vec4 box,float lag){
  vec2 q=(p-box.xy)/box.zw;
  float phase=TAU*time/17.6-lag;
  float mask=glassMask(p,box);
  return vec2(sin(q.y*9.-phase)+.35*sin(q.y*17.+phase*2.),
              .42*sin(q.x*6.+q.y*4.-phase))*box.zw*.018*mask;
}
const vec4 mainGlass=vec4(.704,.430,.108,.353);
const vec4 gardenGlass=vec4(.079,.277,.034,.122);
const vec4 rainGlass=vec4(.172,.417,.028,.099);
const vec4 sunsetGlass=vec4(.529,.357,.025,.085);
const vec4 cloudGlass=vec4(.885,.231,.026,.087);
vec2 mirrorCurrent(vec2 p){
  return glassCurrent(p,mainGlass,0.)+glassCurrent(p,gardenGlass,2.4)
    +glassCurrent(p,rainGlass,1.8)+glassCurrent(p,sunsetGlass,.9)
    +glassCurrent(p,cloudGlass,1.3);
}
vec3 glassLight(vec2 p,vec4 box,float lag,float strength){
  vec2 q=(p-box.xy)/box.zw;
  float radius=length(q);
  float phase=TAU*time/17.6-lag;
  float angle=atan(q.y,q.x);
  // Two soft arcs follow the mirror contour. The frame itself never deforms.
  float traveler=pow(.5+.5*cos(angle-phase+2.4),10.);
  float answer=pow(.5+.5*cos(angle+phase-2.1),16.);
  float edge=exp(-pow((radius-.998)/.041,2.));
  float glow=exp(-pow((radius-.97)/.13,2.));
  float interior=glassMask(p,box);
  float silk=pow(.5+.5*sin(q.y*6.6+q.x*2.5-phase),8.);
  float crossing=pow(.5+.5*sin(q.y*8.2-q.x*3.2+phase+1.7),12.);
  vec3 tint=prism(angle/TAU-phase/TAU*.5+.13);
  return strength*(tint*((.06+.45*traveler+.23*answer)*edge
                  +(.05+.13*traveler)*glow)
      +prism(q.y*.15-phase/TAU*.5+.7)*interior*(silk*.10+crossing*.045));
}
vec3 mirrorEcho(vec3 color,vec2 p,vec4 box,float lag){
  float mask=glassMask(p,box);
  if(mask<.001)return color;
  vec2 q=(p-box.xy)/box.zw;
  float phase=TAU*time/17.6-lag;
  // The distant reflected figure stays confined to the glass and softly disperses.
  vec2 reflected=vec2(.5-q.x*.46,.5+q.y*.51);
  reflected.x+=.018*sin(q.y*8.-phase)+.007*sin(q.y*18.+phase);
  reflected.y+=.007*sin(q.x*5.+phase);
  if(reflected.x<0. || reflected.x>1. || reflected.y<0. || reflected.y>1.)return color;
  vec4 silhouette=texture2D(art,reflected);
  float wave=.5+.5*sin(q.y*5.-phase);
  float presence=.12+.17*pow(.5+.5*cos(phase),2.);
  float value=dot(silhouette.rgb,vec3(.2126,.7152,.0722));
  vec3 spectral=mix(vec3(value),silhouette.rgb,.36)*.80+prism(.3-phase/TAU*.5)*.11;
  return mix(color,spectral,silhouette.a*mask*presence*(.62+.38*wave));
}
vec3 centralEcho(vec3 color,vec2 p){
  float mask=glassMask(p,mainGlass);
  if(mask<.001)return color;
  vec2 q=(p-heroBox.xy)/heroBox.zw;
  float phase=TAU*time/17.6;
  q.x=1.-q.x+.026*sin(q.y*7.-phase);
  q.y+=.004*sin(q.x*6.+phase);
  if(q.x<0. || q.x>1. || q.y<0. || q.y>1.)return color;
  vec4 echo=texture2D(art,q);
  float value=dot(echo.rgb,vec3(.2126,.7152,.0722));
  vec3 spectral=mix(vec3(value),echo.rgb,.25)*.8+prism(q.y*.2-phase/TAU*.5)*.08;
  // The opaque foreground figure covers the reflected face and torso.
  return mix(color,spectral,echo.a*mask*(.07+.065*pow(.5+.5*sin(phase-q.y*4.),2.)));
}
vec3 mirrorOptics(vec3 color,vec2 p){
  color=centralEcho(color,p);
  color=mirrorEcho(color,p,gardenGlass,2.4);
  color=mirrorEcho(color,p,rainGlass,1.8);
  color=mirrorEcho(color,p,sunsetGlass,.9);
  color=mirrorEcho(color,p,cloudGlass,1.3);
  // A common phase links the central invitation to the four distant answers.
  float light=mix(.86,.66,night);
  color+=light*(glassLight(p,mainGlass,0.,1.)
    +glassLight(p,gardenGlass,2.4,.60)+glassLight(p,rainGlass,1.8,.65)
    +glassLight(p,sunsetGlass,.9,.74)+glassLight(p,cloudGlass,1.3,.70));
  return color;
}
`;
