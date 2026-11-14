const W = 960;
const H = 352;
const PERIOD = 1216;
const SPEED = 1.7;
const mod = (value: number, period: number) => ((value % period) + period) % period;

// Assets use the native 1536 x 1024 station register. The extra 256px closes
// the cloud conveyor; roof, wires and the distant skyline never travel with it.
export function elopementSkyResources(dark = false, highResolution = false) {
  const tone = dark ? 'dark' : 'light';
  const base = `/assets/gacha/${highResolution ? 'hires/' : ''}motion/p-claude/daylight-sky`;
  const density = highResolution ? 2 : 1;
  return {
    clouds: {
      name: 'Continuous native clouds',
      src: `${base}/clouds-${tone}.webp`,
      rect: [0, 0, 1216 * density, H * density] as const,
    },
    visibility: {
      name: 'Fixed wires, roof and horizon',
      src: `${base}/visibility-${tone}.webp`,
      rect: [0, 0, W * density, H * density] as const,
    },
  };
}

export function makeElopementSky(
  source: HTMLImageElement,
  clouds: HTMLImageElement,
  visibility: HTMLImageElement,
  dark = false,
) {
  const surface = document.createElement('canvas');
  const moving = document.createElement('canvas');
  surface.width = 1536;
  surface.height = 1024;
  const density = clouds.naturalHeight / H;
  moving.width = W * density;
  moving.height = H * density;
  const context = surface.getContext('2d')!;
  const sky = moving.getContext('2d')!;
  let disposed = false;

  function birds(time: number) {
    // A brief paired passage, followed by a long empty sky. Wingbeats settle
    // into glides; both birds remain behind the same real station occluders.
    const passage = mod(time + 20, 83);
    if (passage > 39) return;
    const fade = Math.min(1, passage / 2.5, (39 - passage) / 3);
    sky.save();
    sky.strokeStyle = dark ? '#14243b' : '#394258';
    sky.lineCap = 'round';
    for (let bird = 0; bird < 2; bird++) {
      const x = -30 + passage * 25 - bird * 20;
      const y = 112 + bird * 12 - passage * 0.9 + Math.sin(passage * 0.26 + bird) * 4;
      const beat = Math.sin(passage * 6.4 + bird * 1.1);
      const glide = Math.sin(passage * 0.75 + bird) > 0.35;
      const wing = glide ? -0.6 : beat * 2.2;
      sky.globalAlpha = fade * (dark ? 0.75 : 0.64);
      sky.lineWidth = bird ? 0.8 : 1;
      sky.beginPath();
      sky.moveTo(x - 3.8, y + wing);
      sky.quadraticCurveTo(x - 1.6, y - 1.1, x, y);
      sky.quadraticCurveTo(x + 1.6, y - 1.1, x + 3.8, y + wing);
      sky.stroke();
    }
    sky.restore();
  }

  return {
    surface,
    setResolution(width: number, height: number) {
      if (disposed) return;
      // Match the existing material lifecycle, without reallocating sky plates
      // each frame. Hi-res source paint is retained in the stationary scene.
      const scale = Math.min(2, Math.max(1, width / 1536, height / 1024));
      const nextWidth = Math.round(1536 * scale);
      const nextHeight = Math.round(1024 * scale);
      if (surface.width !== nextWidth || surface.height !== nextHeight) {
        surface.width = nextWidth;
        surface.height = nextHeight;
      }
    },
    draw(time: number) {
      if (disposed) return surface;
      const offset = mod(time * SPEED, PERIOD);
      sky.setTransform(density, 0, 0, density, 0, 0);
      sky.clearRect(0, 0, W, H);
      sky.globalCompositeOperation = 'source-over';
      // Both copies have identical subpixel phase. No reset, reverse motion,
      // accumulating frames, or opacity crossfade over the original clouds.
      sky.drawImage(clouds, offset - PERIOD, 0, PERIOD, H);
      sky.drawImage(clouds, offset, 0, PERIOD, H);
      birds(time);
      sky.globalCompositeOperation = 'destination-in';
      sky.drawImage(visibility, 0, 0, W, H);
      sky.globalCompositeOperation = 'source-over';
      context.setTransform(surface.width / 1536, 0, 0, surface.height / 1024, 0, 0);
      context.clearRect(0, 0, 1536, 1024);
      context.drawImage(source, 0, 0, 1536, 1024);
      context.drawImage(moving, 0, 0, W, H);
      return surface;
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      surface.width = surface.height = moving.width = moving.height = 0;
      // Image ownership belongs to createScene's existing abort/release path.
    },
  };
}
