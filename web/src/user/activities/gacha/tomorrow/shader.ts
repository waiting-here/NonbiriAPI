import { eyelidShader } from '@shared/animation/illustration';

export const tomorrowShader = `
precision highp float;
varying vec2 uv;
uniform sampler2D scenery, art, closed, lids, companion, rig, environment;
uniform vec4 view, heroBox, cloudBox, leftEye, rightEye;
uniform float time, blink, night, characterOnly, backgroundOnly, characterPlate, cloudFront;
${eyelidShader}
vec4 over(vec4 base, vec4 top){
  float a=top.a+base.a*(1.-top.a);
  return vec4((top.rgb*top.a+base.rgb*base.a*(1.-top.a))/max(.00001,a),a);
}
vec2 flex(vec2 q,float phase){
  vec2 p=q*vec2(1024.,1536.);
  vec4 weights=texture2D(rig,q);
  float left=weights.r, right=weights.g, coatLeft=weights.b, coatRight=weights.a;
  vec2 delta=vec2(0.);
  delta+=vec2(5.0*sin(phase-p.y*.003),1.8*sin(phase-.5))*left*smoothstep(300.,650.,p.y);
  delta+=vec2(6.5*sin(phase+.8-p.y*.0025),2.1*sin(phase+.2))*right*smoothstep(355.,710.,p.y);
  delta+=vec2(3.5*sin(phase-.9),1.4*sin(phase-1.3))*coatLeft*smoothstep(542.,840.,p.y);
  delta+=vec2(4.2*sin(phase-1.3),1.7*sin(phase-1.7))*coatRight*smoothstep(727.,1030.,p.y);
  return q-delta/vec2(1024.,1536.);
}
vec4 heroColor(vec2 q,float phase){
  if(q.x<0. || q.x>1. || q.y<0. || q.y>1.)return vec4(0.);
  q=flex(q,phase);
  vec4 color=texture2D(art,q);
  color=eyelid(q,leftEye,.125,color);
  color=eyelid(q,rightEye,.625,color);
  // Both eye states receive the same environmental light in source coordinates.
  color.rgb*=mix(vec3(1.),vec3(.64,.71,.84),night);
  float rim=max(0.,color.a-texture2D(art,q+vec2(-.0015,.001)).a);
  color.rgb+=vec3(.08,.047,.019)*rim*night;
  return color;
}
void main(){
  float phase=time*6.28318530718/17.6;
  vec2 p=view.xy+uv*view.zw;
  vec2 sceneUV=p;
  // Bounded sky-only advection leaves the horizon and all masonry stationary.
  vec4 motion=texture2D(environment,p);
  float sky=motion.r;
  sceneUV.x+=.0018*sin(phase*.5)*sky;
  float canopyLeft=motion.g;
  float canopyRight=motion.b;
  sceneUV+=vec2(.0009*sin(phase),.0006*sin(phase+.7))*canopyLeft;
  sceneUV+=vec2(.0011*sin(phase-.8),.0005*sin(phase))*canopyRight;
  vec4 color=characterOnly>.5?vec4(0.):texture2D(scenery,sceneUV);
  if(characterOnly<.5){
    float ground=smoothstep(.65,.77,p.y)*(1.-smoothstep(.96,1.,p.y));
    float shade=(sin(p.x*37.+p.y*29.+sin(phase)*.3)+sin(p.x*71.-p.y*52.+sin(phase-.7)*.25))*.004;
    color.rgb*=1.+shade*ground*(1.-night*.6);
    // Four distant leaves stay behind the figure and outside the title area.
    for(int i=0;i<4;i++){
      float n=float(i), t=fract(time/24.+n*.247);
      vec2 center=vec2(.19+n*.205+.035*sin(t*6.283+n),.21+t*.50);
      vec2 v=(p-center)*vec2(1672.,941.);
      float angle=t*6.283+n;
      v=mat2(cos(angle),-sin(angle),sin(angle),cos(angle))*v;
      float leaf=(1.-smoothstep(.7,1.,length(v/vec2(2.3,4.5))))*sin(t*3.14159);
      color.rgb=mix(color.rgb,mix(vec3(.77,.45,.12),vec3(.38,.35,.25),night),leaf*.65);
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
      vec2 floatBy=vec2(.003*sin(phase+.8),.005*sin(phase));
      vec2 cloudUV=(uv-cloudBox.xy-floatBy)/cloudBox.zw;
      if(cloudUV.x>=0. && cloudUV.x<=1. && cloudUV.y>=0. && cloudUV.y<=1.){
        vec4 cloud=texture2D(companion,cloudUV);cloud.rgb*=mix(vec3(1.),vec3(.79,.88,.98),night);
        cloudLayer=cloud;
      }
    }
    if(cloudFront<.5)color=over(color,cloudLayer);
    color=over(color,heroColor(q,phase));
    if(cloudFront>.5)color=over(color,cloudLayer);
  }
  gl_FragColor=color;
}
`;
