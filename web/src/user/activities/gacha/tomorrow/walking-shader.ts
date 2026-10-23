import { eyelidShader } from '@shared/animation/illustration';

export const walkingShader = `
precision highp float;
varying vec2 uv;
uniform sampler2D scenery, art, closed, lids, companion, rig, environment, pendant;
uniform vec4 view, heroBox, cloudBox, leftEye, rightEye;
uniform float time, blink, night, characterOnly, backgroundOnly, characterPlate, cloudFront, pendantAngle;
${eyelidShader}
vec4 over(vec4 base,vec4 top){
 float a=top.a+base.a*(1.-top.a);
 return vec4((top.rgb*top.a+base.rgb*base.a*(1.-top.a))/max(.00001,a),a);
}
float wind(float seconds){
 float t=mod(seconds+48.,24.);
 return .12*sin(t*3.14159265/4.)+smoothstep(1.5,4.5,t)*(1.-smoothstep(7.5,12.,t))
   -.5*smoothstep(14.,17.,t)*(1.-smoothstep(19.5,24.,t));
}
float breath(){float t=mod(time,5.8);return smoothstep(0.,2.1,t)*(1.-smoothstep(2.1,5.8,t));}
vec2 displacement(vec2 q){
 vec4 binding=texture2D(rig,q);
 return ((binding.rg*255.-128.)/3.+vec2(-.8,-1.9)*binding.b*breath())/vec2(1024.,1536.);
}
vec4 heroColor(vec2 q){
 if(q.x<0.||q.x>1.||q.y<0.||q.y>1.)return vec4(0.);
 vec2 source=q;
 // Relax the inverse lookup to keep curled free edges stable at gust extremes.
 for(int i=0;i<8;i++)source=mix(source,q-displacement(source),.75);
 vec4 color=texture2D(art,source);
 color=eyelid(source,leftEye,.125,color);
 color=eyelid(source,rightEye,.625,color);
 vec2 pivot=vec2(655.,547.);
 vec2 local=q*vec2(1024.,1536.)-pivot;
 float a=pendantAngle;
 vec2 ornament=(mat2(cos(a),-sin(a),sin(a),cos(a))*local+pivot)/vec2(1024.,1536.);
 color=over(color,texture2D(pendant,ornament));
 color.rgb*=mix(vec3(1.),vec3(.64,.71,.84),night);
 return color;
}
void main(){
 vec2 p=view.xy+uv*view.zw;
 vec4 motion=texture2D(environment,p);
 vec2 sceneUV=p+vec2(.0021*sin(time*6.2831853/43.),0.)*motion.r;
 sceneUV+=vec2(.0016,.0007)*wind(time)*motion.g;
 sceneUV+=vec2(.0019,.0008)*wind(time-.4)*motion.b;
 vec4 color=characterOnly>.5?vec4(0.):texture2D(scenery,sceneUV);
 if(characterOnly<.5){
  float ground=smoothstep(.67,.79,p.y);
  float shade=sin(p.x*37.+p.y*29.+wind(time)*.32)+sin(p.x*71.-p.y*52.+wind(time-.6)*.23);
  color.rgb*=1.+shade*.004*ground*(1.-night*.6);
  for(int i=0;i<4;i++){
   float n=float(i),t=fract(time/24.+n*.247);
   vec2 center=vec2(.19+n*.205+.035*sin(t*6.283+n),.21+t*.50);
   vec2 v=(p-center)*vec2(1672.,941.);
   float angle=t*6.283+n;
   v=mat2(cos(angle),-sin(angle),sin(angle),cos(angle))*v;
   float leaf=(1.-smoothstep(.7,1.,length(v/vec2(2.3,4.5))))*sin(t*3.14159);
   color.rgb=mix(color.rgb,mix(vec3(.77,.45,.12),vec3(.38,.35,.25),night),leaf*.6);
  }
 }
 vec2 q=(uv-heroBox.xy)/heroBox.zw;
 if(backgroundOnly<.5){
  if(characterOnly<.5){
   float support=exp(-dot((q-vec2(.414,.893))/vec2(.051,.008),(q-vec2(.414,.893))/vec2(.051,.008)));
   float lifted=exp(-dot((q-vec2(.651,.989))/vec2(.067,.014),(q-vec2(.651,.989))/vec2(.067,.014)));
   color.rgb*=1.-.36*support-.13*lifted;
  }
  vec4 cloudLayer=vec4(0.);
  if(characterPlate<.5){
   vec2 offset=vec2(.0025*(sin(time*.55+.8)-sin(.8)),.004*sin(time*.8));
   vec2 cloudUV=(uv-cloudBox.xy-offset)/cloudBox.zw;
   if(cloudUV.x>=0.&&cloudUV.x<=1.&&cloudUV.y>=0.&&cloudUV.y<=1.){
    cloudLayer=texture2D(companion,cloudUV);cloudLayer.rgb*=mix(vec3(1.),vec3(.79,.88,.98),night);
   }
  }
  if(cloudFront<.5)color=over(color,cloudLayer);
  color=over(color,heroColor(q));
  if(cloudFront>.5)color=over(color,cloudLayer);
 }
 gl_FragColor=color;
}
`;
