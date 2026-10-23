import { eyelidShader } from '@shared/animation/illustration';

export const refinedShader = `
precision highp float;
varying vec2 uv;
uniform sampler2D scenery, art, closed, lids, companion, rig, environment, pose;
uniform vec4 view, heroBox, cloudBox, leftEye, rightEye;
uniform float time, blink, night, characterOnly, backgroundOnly, characterPlate, cloudFront;
${eyelidShader}
vec4 over(vec4 base, vec4 top){
  float a=top.a+base.a*(1.-top.a);
  return vec4((top.rgb*top.a+base.rgb*base.a*(1.-top.a))/max(.00001,a),a);
}
float breeze(float seconds){
  float t=mod(seconds+48.,24.);
  float gust=smoothstep(4.5,7.,t)*(1.-smoothstep(9.,12.,t));
  float returnWind=smoothstep(16.,18.,t)*(1.-smoothstep(20.,24.,t));
  return .24*sin(t*6.2831853/8.)+.82*gust-.48*returnWind;
}
float breath(float seconds){
  float t=mod(seconds,5.8);
  return smoothstep(0.,2.1,t)*(1.-smoothstep(2.1,5.8,t));
}
vec2 flex(vec2 q){
  vec2 p=q*vec2(1024.,1536.);
  vec4 weights=texture2D(rig,q);
  vec4 binding=texture2D(pose,q);
  vec2 delta=(binding.rg*255.-128.)/16.*breath(time);
  float left=breeze(time-.35), right=breeze(time-.85);
  delta+=vec2(10.5*left,3.1*breeze(time-.65))*weights.r*smoothstep(220.,610.,p.y);
  delta+=vec2(12.*right,3.8*breeze(time-1.15))*weights.g*smoothstep(180.,670.,p.y);
  delta+=vec2(7.5*breeze(time-1.05),2.6*breeze(time-1.4))*weights.b*smoothstep(540.,850.,p.y);
  delta+=vec2(8.5*breeze(time-1.45),2.8*breeze(time-1.8))*weights.a*smoothstep(725.,1040.,p.y);
  float tail=binding.b*smoothstep(804.,858.,p.x);
  delta+=vec2(1.5,5.)*breeze(time-1.5)*tail;
  return q-delta/vec2(1024.,1536.);
}
vec4 heroColor(vec2 q){
  if(q.x<0. || q.x>1. || q.y<0. || q.y>1.)return vec4(0.);
  q=flex(q);
  vec4 color=texture2D(art,q);
  color=eyelid(q,leftEye,.125,color);
  color=eyelid(q,rightEye,.625,color);
  // A brief smile after the first gust, followed by a long resting pose.
  float t=mod(time,24.);
  float smile=smoothstep(7.5,8.8,t)*(1.-smoothstep(10.8,12.6,t));
  vec2 mouth=(q*vec2(1024.,1536.)-vec2(478.,225.))/vec2(23.,14.);
  float mouthMask=1.-smoothstep(.67,1.,length(mouth));
  color=mix(color,texture2D(closed,q),smile*mouthMask);
  // Leaf shade stays on the lower hair and cloth, away from the face and hands.
  vec4 material=texture2D(rig,q);
  float shade=smoothstep(.3,.85,sin(q.x*24.+q.y*7.+breeze(time)*.25));
  float cloth=clamp(material.r+material.g+material.b+material.a,0.,1.);
  color.rgb*=1.-.022*shade*cloth;
  return color;
}
void main(){
  vec2 p=view.xy+uv*view.zw;
  vec4 motion=texture2D(environment,p);
  vec2 sceneUV=p;
  sceneUV.x+=.0023*sin(time*6.2831853/43.)*motion.r;
  sceneUV+=vec2(.0018,.0008)*breeze(time)*motion.g;
  sceneUV+=vec2(.0022,.0009)*breeze(time-.5)*motion.b;
  vec4 color=characterOnly>.5?vec4(0.):texture2D(scenery,sceneUV);
  if(characterOnly<.5){
    float ground=smoothstep(.66,.78,p.y);
    float shade=sin(p.x*37.+p.y*29.+breeze(time)*.32)+sin(p.x*71.-p.y*52.+breeze(time-.6)*.23);
    color.rgb*=1.+shade*.004*ground;
    // Distant leaves cross the open path behind the figure.
    for(int i=0;i<5;i++){
      float n=float(i), t=fract(time/21.+n*.197);
      vec2 center=vec2(.22+n*.13+.025*sin(t*6.283+n),.19+t*.58);
      vec2 v=(p-center)*vec2(1672.,941.);
      float angle=t*6.283+n;
      v=mat2(cos(angle),-sin(angle),sin(angle),cos(angle))*v;
      float leaf=(1.-smoothstep(.65,1.,length(v/vec2(2.2,4.2))))*sin(t*3.14159);
      color.rgb=mix(color.rgb,vec3(.79,.48,.14),leaf*.65);
    }
  }
  vec2 q=(uv-heroBox.xy)/heroBox.zw;
  if(backgroundOnly<.5){
    if(characterOnly<.5){
      float front=exp(-dot((q-vec2(.528,.976))/vec2(.065,.007),(q-vec2(.528,.976))/vec2(.065,.007)));
      float rear=exp(-dot((q-vec2(.463,.887))/vec2(.048,.006),(q-vec2(.463,.887))/vec2(.048,.006)));
      color.rgb*=1.-.43*max(front,rear);
    }
    vec4 cloudLayer=vec4(0.);
    if(characterPlate<.5){
      vec2 floatBy=vec2(.003*sin(time*.55+.8),.005*sin(time*.8));
      vec2 cloudUV=(uv-cloudBox.xy-floatBy)/cloudBox.zw;
      if(cloudUV.x>=0. && cloudUV.x<=1. && cloudUV.y>=0. && cloudUV.y<=1.)
        cloudLayer=texture2D(companion,cloudUV);
    }
    if(cloudFront<.5)color=over(color,cloudLayer);
    color=over(color,heroColor(q));
    if(cloudFront>.5)color=over(color,cloudLayer);
  }
  gl_FragColor=color;
}
`;
