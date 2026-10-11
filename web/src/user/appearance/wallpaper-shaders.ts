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
  vec2 position=rect.xy+p*rect.zw;
  if(screenSpace<.5) position=(position-offset)/crop;
  gl_Position=vec4(position.x*2.-1.,1.-position.y*2.,0.,1.);
}
`;

export const wallpaperFragment = `precision highp float;
varying vec2 uv;
uniform sampler2D art, closed;
uniform vec2 crop, offset, eyeSize;
uniform vec4 rect, eyes;
uniform vec3 tint;
uniform float time, kind, opacity, dark, screenSpace, blink, planet;
${geometry}
float hash(vec2 p) { return fract(sin(dot(p,vec2(127.1,311.7)))*43758.5453); }

// Geometrically compress the eye opening; never dissolve the open pupil away.
vec4 eyelid(vec2 p,vec2 center,vec4 original) {
  float slope=(eyes.w-eyes.y)/max(.02,eyes.z-eyes.x);
  vec2 q=p-center;
  q.y-=q.x*slope;
  float across=abs(q.x)/eyeSize.x;
  float patch=(1.-smoothstep(1.,1.4,across))
    *(1.-smoothstep(2.,2.6,abs(q.y)/eyeSize.y));
  if(patch<=0. || blink<=0.) return original;
  float shape=sqrt(max(.04,1.-min(.96,across*across)));
  float top=-eyeSize.y*shape, bottom=eyeSize.y*.72*shape;
  float line=eyeSize.y*.1*shape;
  float amount=blink*(1.-smoothstep(.8,1.4,across));
  float upper=mix(top,line,amount), lower=mix(bottom,line,amount);
  float edge=eyeSize.y*2.7;
  float sourceY;
  if(q.y<upper) sourceY=mix(-edge,top,(q.y+edge)/(upper+edge));
  else if(q.y>lower) sourceY=mix(bottom,edge,(q.y-lower)/(edge-lower));
  else sourceY=mix(top,bottom,(q.y-upper)/max(.00001,lower-upper));
  vec2 source=p+vec2(0.,sourceY-q.y);
  vec4 moving=texture2D(art,source);
  moving=mix(moving,texture2D(closed,p),smoothstep(.82,1.,amount));
  return mix(original,moving,patch);
}

void main() {
  vec2 world=rect.xy+uv*rect.zw;
  if(kind<.5) {
    // The complete plate is rigid: no warp can reach architecture or mountains.
    gl_FragColor=vec4(texture2D(art,world).rgb,1.); return;
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
  if(kind>5.5) {
    vec3 original=texture2D(art,world).rgb;
    float edge=smoothstep(0.,.16,uv.x)*(1.-smoothstep(.84,1.,uv.x))
      *smoothstep(0.,.08,uv.y)*(1.-smoothstep(.85,1.,uv.y));
    // Thin illuminated streaks travel down each isolated waterfall face.
    float streak=sin(uv.y*31.-time*5.5+sin(uv.x*27.)*1.7);
    float fine=sin(uv.y*71.-time*11.3+uv.x*43.);
    float strength=(streak*.65+fine*.35)*.13*edge;
    gl_FragColor=vec4(original*(1.+strength)+vec3(.015,.024,.03)*strength,1.); return;
  }
  if(kind<1.5) {
    vec4 color=texture2D(art,uv);
    color=eyelid(uv,eyes.xy,color);
    color=eyelid(uv,eyes.zw,color);
    gl_FragColor=color*vec4(tint,opacity); return;
  }
  vec4 color=texture2D(art,uv);
  if(kind>3.5) {
    // Sample inside the painted atmosphere; the smooth rim is rendered separately.
    color=texture2D(art,.5+(uv-.5)*mix(1.,.91,planet));
    vec2 q=(uv-.5)*2./.95;
    float radius=length(q);
    float z=sqrt(max(0.,1.-dot(q,q)));
    vec3 normal=vec3(q,z);
    // The night-side is an opaque hemisphere, never a transparent crescent cutout.
    vec3 sun=normalize(vec3(-.91,-.34,mix(.65,-.30,dark)));
    float incidence=dot(normal,sun);
    float day=smoothstep(-.035,.035,incidence);
    float diffuse=max(0.,incidence);
    if(planet<.5) {
      color.rgb*=mix(vec3(.052,.066,.088),vec3(.23+.77*diffuse),day);
    } else {
      color.rgb*=mix(.16,.55+.45*diffuse,day);
    }
    // Daytime atmospheric light softens distant bodies without making them transparent.
    color.rgb=mix(color.rgb,vec3(.64,.76,.88),(1.-dark)*(.22+.38*(1.-day)));
    float disk=1.-smoothstep(.996,1.006,radius);
    float halo=exp(-max(0.,radius-1.)*65.)*(.10+.08*day)
      *(1.-smoothstep(1.025,1.052,radius));
    vec3 glow=mix(vec3(.86,.83,.74),vec3(.46,.75,1.),planet);
    color.rgb=mix(glow,color.rgb,disk);
    color.a=max(disk,halo);
  }
  gl_FragColor=color*vec4(tint,opacity);
}
`;
