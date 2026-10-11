import { eyelidProfileWidth } from './wallpaper-eyelids';

const geometry = `
float field(vec2 p,vec2 center,vec2 radius) {
  vec2 q=(p-center)/radius;
  return exp(-2.*dot(q,q));
}
vec2 turn(vec2 p,vec2 pivot,float angle) {
  vec2 q=(p-pivot)*vec2(16./9.,1.);
  return vec2(q.x*cos(angle)-q.y*sin(angle),q.x*sin(angle)+q.y*cos(angle))
    /vec2(16./9.,1.)+pivot;
}
`;

export const wallpaperVertex = `
attribute vec2 point;
varying vec2 uv;
uniform vec2 crop, offset;
uniform vec4 rect, eyes;
uniform float time, kind, screenSpace;
${geometry}
void main() {
  uv=point;
  vec2 p=point;
  if(kind>.5 && kind<1.5) {
    vec2 face=(eyes.xy+eyes.zw)*.5;
    vec2 neck=face+vec2(0.,.12);
    float head=1.-smoothstep(neck.y-.045,neck.y+.12,p.y);
    p=turn(p,neck,.0075*sin(time*.71)*head);
    float torso=field(point,neck+vec2(0.,.17),vec2(.15,.25));
    p.y-=.0024*sin(time*1.48)*torso;
    // Delayed sway grows towards the free ends, leaving the face and hands intact.
    float faceMask=1.-field(point,face,vec2(.072,.10));
    float hair=smoothstep(face.y+.04,face.y+.29,point.y)
      *(1.-smoothstep(face.y+.42,face.y+.64,point.y));
    float side=smoothstep(face.x+.018,face.x+.10,point.x);
    float hand=1.-field(point,face+vec2(-.03,.37),vec2(.06,.08));
    p.x+=.010*sin(time*.88-point.y*4.6)*hair*side*faceMask*hand;
    p.y+=.0035*sin(time*.88-point.y*4.6-.8)*hair*side*faceMask*hand;
    float hem=smoothstep(.62,.97,point.y)*smoothstep(.54,.81,point.x);
    p.x+=.008*sin(time*.73-point.y*5.2)*hem;
  } else if(kind>2.5 && kind<3.5) {
    float stroke=time*1.08;
    // Tail-stock bending leads the flukes; fins trail the power stroke.
    float tail=smoothstep(.48,.87,point.x);
    p=turn(p,vec2(.60,.52),.14*sin(stroke-.9)*tail);
    float fluke=smoothstep(.76,.91,point.x);
    p.y+=(point.y-.43)*.11*sin(stroke-1.35)*fluke;
    float frontFin=smoothstep(.55,.85,point.y)
      *smoothstep(.29,.40,point.x)*(1.-smoothstep(.71,.77,point.x));
    p=mix(p,turn(p,vec2(.376,.548),.24*sin(stroke+.65)),frontFin);
    float backFin=smoothstep(.62,.83,point.y)
      *(1.-smoothstep(.34,.40,point.x));
    p=mix(p,turn(p,vec2(.245,.63),-.16*sin(stroke+.2)),backFin);
    p.y+=.013*sin(stroke-.3);
    p=turn(p,vec2(.30,.48),.014*sin(stroke+.55));
  }
  // Leave room outside the solid disk for a freely fading light bloom.
  if(kind>3.5 && kind<4.5) { p=(p-.5)*1.5+.5; uv=p; }
  vec2 position=rect.xy+p*rect.zw;
  if(screenSpace<.5) position=(position-offset)/crop;
  gl_Position=vec4(position.x*2.-1.,1.-position.y*2.,0.,1.);
}
`;

export const wallpaperFragment = `precision highp float;
varying vec2 uv;
uniform sampler2D art, closed, lids, foreground, cloudMatte;
uniform vec2 crop, offset;
uniform vec4 eyeBox1, eyeBox2;
uniform vec4 rect, eyes;
uniform vec3 tint;
uniform float time, kind, opacity, dark, screenSpace, blink, planet, celestial;
${geometry}
float hash(vec2 p) { return fract(sin(dot(p,vec2(127.1,311.7)))*43758.5453); }

// Only the two close flower beds move. Their bases and the masonry stay anchored.
float plants(vec2 p) {
  float left=field(p,vec2(.055,.94),vec2(.14,.15));
  float right=field(p,vec2(.965,.965),vec2(.145,.16));
  return max(left,right)*smoothstep(.72,.86,p.y);
}
vec2 plantUV(vec2 p) {
  float tips=plants(p)*(1.-smoothstep(.975,1.015,p.y));
  float breeze=sin(time*.68+p.y*4.2);
  return p+vec2(.00165*breeze,.0007*sin(time*.68+p.x*3.1-.7))*tips;
}

// Each eye follows its painted contour. Only the lash ribbon is remapped;
// iris, highlights and all surrounding skin retain their original coordinates.
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
    *smoothstep(0.,.035,q.x)*(1.-smoothstep(.965,1.,q.x));
  coverage*=shut.z*smoothstep(0.,.075,blink);
  if(coverage<=0.) return original;
  float upper=mix(opened.b,shut.x,blink);
  float lower=mix(opened.a,shut.y,blink);
  float bottom=mix(opened.g,shut.y,blink*blink);
  float aperture=smoothstep(lower-edge,lower+edge,q.y)
    *(1.-smoothstep(bottom-edge,bottom,q.y))*(1.-smoothstep(.98,1.,blink));
  vec4 resting=texture2D(closed,p);
  // The final lash is drawn by the moving ribbon below. Keep it out of the
  // fixed skin fill, otherwise two lash lines appear during partial closure.
  vec2 skinAbove=vec2(p.x,box.y+max(0.,shut.x-.055)*box.w);
  vec2 skinBelow=vec2(p.x,box.y+min(1.,shut.y+.055)*box.w);
  float finalRibbon=smoothstep(shut.x-.025,shut.x,q.y)
    *(1.-smoothstep(shut.y,shut.y+.025,q.y));
  vec4 lidSkin=mix(texture2D(closed,skinAbove),texture2D(closed,skinBelow),
    clamp((q.y-shut.x)/max(.005,shut.y-shut.x),0.,1.));
  resting=mix(resting,lidSkin,finalRibbon);
  // Fill the authored eye opening at fixed coordinates; colour similarity
  // cannot distinguish an iris highlight from skin at full closure.
  vec4 color=mix(original,resting,1.-aperture);
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

void main() {
  vec2 world=rect.xy+uv*rect.zw;
  if(kind<.5) {
    // The sky and architecture stay rigid; only the near flower beds sway.
    vec2 sampleAt=celestial>.5 ? plantUV(world) : world;
    gl_FragColor=vec4(texture2D(art,sampleAt).rgb,1.); return;
  }
  if(kind>4.5 && kind<5.5) {
    vec2 stars=world*vec2(90.,50.)+vec2(time*.055,-time*.013);
    vec2 cell=floor(stars), local=fract(stars)-.5;
    float seed=hash(cell);
    float point=exp(-dot(local,local)*1900.)*step(.97,seed);
    float halo=exp(-dot(local,local)*110.)*step(.992,seed)*.11;
    float sky=(1.-smoothstep(.40,.64,world.y))
      *smoothstep(.07,.17,world.x)*(1.-smoothstep(.84,.94,world.x));
    gl_FragColor=vec4(vec3(.73,.86,1.),(point+halo)*sky
      *(.65+.22*sin(time*.65+seed*45.))*opacity);
    return;
  }
  if(kind>5.5 && kind<6.5) {
    vec3 original=texture2D(art,world).rgb;
    float edge=smoothstep(0.,.16,uv.x)*(1.-smoothstep(.84,1.,uv.x))
      *smoothstep(0.,.08,uv.y)*(1.-smoothstep(.85,1.,uv.y));
    // Thin illuminated streaks travel down each isolated waterfall face.
    float streak=sin(uv.y*31.-time*5.5+sin(uv.x*27.)*1.7);
    float fine=sin(uv.y*71.-time*11.3+uv.x*43.);
    float strength=(streak*.65+fine*.35)*.13*edge;
    gl_FragColor=vec4(original*(1.+strength)+vec3(.015,.024,.03)*strength,1.); return;
  }
  if((kind>6.5 && kind<7.5) || (kind>9.5 && kind<10.5)) {
    vec2 sampleAt=plantUV(world);
    // Use the registered extraction only as a depth matte. The original plate
    // supplies all colour/detail, avoiding duplicate outlines from repainting.
    float alpha=texture2D(foreground,sampleAt).a;
    if(kind>9.5) alpha*=smoothstep(.03,.28,plants(world));
    gl_FragColor=vec4(texture2D(art,sampleAt).rgb,alpha); return;
  }
  if(kind>10.5) {
    // Painted atmospheric clouds must also occlude the distant planets.
    gl_FragColor=vec4(texture2D(art,world).rgb,texture2D(cloudMatte,world).a); return;
  }
  if(kind>7.5 && kind<8.5) {
    // A tapered, diagonal trail with a quiet blue-white head.
    vec2 q=uv-vec2(.18,.82);
    vec2 direction=normalize(vec2(.78,-.65));
    float along=dot(q,direction);
    float across=abs(q.x*direction.y-q.y*direction.x);
    float trail=exp(-across*175.)*exp(-max(0.,along)*5.)
      *smoothstep(-.015,.015,along)*(1.-smoothstep(.85,1.,along));
    float head=exp(-dot(q,q)*2300.);
    gl_FragColor=vec4(.78,.87,1.,(trail+head)*opacity); return;
  }
  if(kind>8.5 && kind<9.5) {
    vec2 q=(uv-.5)*2.;
    float light=exp(-abs(q.x)*105.-abs(q.y)*7.)
      +exp(-abs(q.y)*105.-abs(q.x)*7.)+exp(-dot(q,q)*13.)*.12;
    gl_FragColor=vec4(1.,.9,.65,clamp(light,0.,1.)*opacity); return;
  }
  if(kind<1.5) {
    vec4 color=texture2D(art,uv);
    color=eyelid(uv,eyeBox1,.125,color);
    color=eyelid(uv,eyeBox2,.625,color);
    gl_FragColor=color*vec4(tint,opacity); return;
  }
  vec4 color=texture2D(art,uv);
  if(kind>3.5) {
    vec2 q=(uv-.5)*2./.95;
    float radius=length(q);
    float z=sqrt(max(0.,1.-dot(q,q)));
    vec3 normal=vec3(q,z);
    vec3 sun=normalize(vec3(-.91,-.34,mix(.65,-.30,dark)));
    float incidence=dot(normal,sun);
    float day=smoothstep(-.07,.09,incidence);
    float diffuse=max(0.,incidence);
    // The source includes a painted halo. Sample the surface inside it, then
    // light the sphere independently so the halo cannot become a solid shell.
    color=texture2D(art,.5+(uv-.5)*mix(.84,.91,planet));
    if(planet<.5) {
      vec3 night=color.rgb*vec3(.075,.105,.16)+vec3(.008,.012,.025);
      vec3 silver=color.rgb*(.72+.48*sqrt(diffuse));
      color.rgb=mix(night,silver,day);
    } else {
      color.rgb*=mix(.16,.55+.45*diffuse,day);
    }
    color.rgb=mix(color.rgb,vec3(.64,.76,.88),(1.-dark)*(.22+.38*(1.-day)));
    float disk=1.-smoothstep(.997,1.003,radius);
    float outside=max(0.,radius-1.);
    float litRim=pow(max(0.,dot(q/max(radius,.001),normalize(sun.xy))),1.5);
    float halo=(.30*exp(-outside*20.)+.10*exp(-outside*6.5))
      *litRim*(1.-smoothstep(1.35,1.55,radius));
    vec3 glow=mix(vec3(1.,.86,.62),vec3(.46,.75,1.),planet);
    float alpha=disk+halo*(1.-disk);
    color.rgb=(color.rgb*disk+glow*halo*(1.-disk))/max(.0001,alpha);
    color.a=alpha;
  }
  gl_FragColor=color*vec4(tint,opacity);
}
`;
