export type SPScene = 'celestial' | 'wandering';

export const spScenes: Record<string, SPScene> = {
  'l-chatgpt': 'celestial',
  'm-deepseek': 'wandering',
};

// Broad, smooth depth fields keep the original painted details intact.
export const backdropShader = `
vec4 backdrop(vec2 uv) {
 vec2 p=(uv-.5)*.956+.5;
 p+=vec2(.006*sin(phase),.003*(cos(phase)-1.));
 if(scene<1.5) {
  float sky=bump(p,vec2(.54,.19),vec2(.43,.25));
  p+=vec2(.008*sin(phase),.004*sin(phase+.7))*sky;
 } else {
  float whale=bump(p,vec2(.52,.21),vec2(.75,.21));
  p+=vec2(.010*sin(phase+.4),.009*sin(phase))*whale;
  float current=smoothstep(.35,.8,p.y);
  p.x+=.004*sin(phase-p.y*5.)*current;
 }
 return texture2D(tex3,p);
}`;

export function paintSPAtmosphere(
  ctx: CanvasRenderingContext2D,
  scene: SPScene,
  phase: number,
  width: number,
  height: number,
) {
  ctx.save();
  ctx.globalCompositeOperation = 'screen';
  const celestial = scene === 'celestial';
  const points = celestial
    ? [
        [0.32, 0.13],
        [0.51, 0.05],
        [0.63, 0.16],
        [0.87, 0.22],
        [0.2, 0.35],
        [0.81, 0.65],
        [0.19, 0.85],
      ]
    : [
        [0.14, 0.17],
        [0.86, 0.27],
        [0.14, 0.5],
        [0.75, 0.6],
        [0.14, 0.73],
        [0.83, 0.87],
      ];
  points.forEach(([x, y], index) => {
    const wave = phase + index * 1.7;
    const px = (x + Math.sin(wave) * (celestial ? 0.012 : 0.021)) * width;
    const py = (y + Math.cos(wave) * 0.009) * height;
    const radius = width * (celestial ? 0.025 : 0.043);
    const light = ctx.createRadialGradient(px, py, 0, px, py, radius);
    light.addColorStop(0, celestial ? 'rgba(255,228,172,.7)' : 'rgba(179,248,255,.45)');
    light.addColorStop(0.25, celestial ? 'rgba(225,217,255,.3)' : 'rgba(163,235,255,.18)');
    light.addColorStop(1, 'rgba(170,218,255,0)');
    ctx.globalAlpha = 0.38 + 0.38 * (0.5 + 0.5 * Math.sin(wave));
    ctx.fillStyle = light;
    ctx.fillRect(px - radius, py - radius, radius * 2, radius * 2);
  });
  // Light follows the painting's curved currents, behind the character.
  for (let i = 0; i < 3; i++) {
    const wave = phase + i * 2.1;
    ctx.globalAlpha = 0.07 + 0.045 * Math.sin(wave);
    ctx.strokeStyle = celestial ? '#ffe3a9' : '#d5ffff';
    ctx.lineWidth = width * 0.0013;
    ctx.beginPath();
    const y = 0.32 + i * 0.25 + Math.sin(wave) * 0.016;
    ctx.moveTo(-width * 0.05, height * y);
    ctx.bezierCurveTo(
      width * 0.22,
      height * (y + 0.13),
      width * 0.63,
      height * (y - 0.12),
      width * 1.05,
      height * (y + 0.04),
    );
    ctx.stroke();
  }
  ctx.restore();
}
