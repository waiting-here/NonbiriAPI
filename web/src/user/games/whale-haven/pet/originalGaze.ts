import { getSceneAssetUrl, getSpriteUrl } from '../game/catalog';
import { createIdleMotion, IDLE_MOTION_PADDING, type OriginalOutfit } from './idleMotion';

interface EyeShape {
  aperture: string;
  iris: string;
  bounds: readonly [x: number, y: number, width: number, height: number];
}

interface GazeArtwork {
  src: () => string;
  // Placement in the full-size idle atlas cell, including the transparent margins.
  left: number;
  top: number;
  scale: number;
  travelX: number;
  travelY: number;
  eyeCenter: readonly [number, number];
  eyes: readonly EyeShape[];
}

const ARTWORK: Readonly<Record<OriginalOutfit, GazeArtwork>> = {
  'whale-maid': {
    src: () => getSpriteUrl('cheerful'),
    left: 21.03,
    top: 27,
    scale: 0.421,
    travelX: 9,
    travelY: 6,
    eyeCenter: [290, 349],
    eyes: [
      {
        aperture:
          'M183 345 C188 331 201 325 216 324 C233 324 246 335 250 350 C254 364 243 377 232 382 C216 388 192 379 185 367 C181 360 181 351 183 345 Z',
        iris: 'M222 325 C239 324 245 339 247 352 C250 368 240 380 224 382 C210 382 201 369 199 354 C198 340 208 325 222 325 Z',
        bounds: [195, 322, 56, 63],
      },
      {
        aperture:
          'M330 338 C333 324 344 314 357 313 C376 312 389 321 395 337 C401 352 390 364 379 370 C367 376 355 376 344 371 C333 367 328 352 330 338 Z',
        iris: 'M357 312 C373 311 381 324 384 339 C387 354 377 369 361 373 C346 374 333 361 331 345 C330 332 339 313 357 312 Z',
        bounds: [328, 309, 60, 66],
      },
    ],
  },
  'whale-winter': {
    src: () => getSceneAssetUrl('sprites/whale-winter.webp'),
    left: 21.59,
    top: 27.88,
    scale: 0.597,
    travelX: 6.5,
    travelY: 4.3,
    eyeCenter: [211, 236],
    eyes: [
      {
        aperture:
          'M139 235 C141 224 152 220 165 218 C175 219 182 224 185 235 C188 248 179 257 168 259 C155 260 143 254 140 245 C138 241 138 238 139 235 Z',
        iris: 'M164 219 C175 219 181 229 182 239 C184 250 176 258 166 258 C155 259 150 250 149 240 C148 229 155 220 164 219 Z',
        bounds: [146, 216, 40, 45],
      },
      {
        aperture:
          'M237 232 C237 220 245 213 256 212 C270 211 279 219 281 229 C285 241 276 250 267 253 C252 257 242 252 238 244 C236 240 236 235 237 232 Z',
        iris: 'M256 212 C267 211 273 220 275 231 C277 243 270 252 258 253 C246 255 238 245 238 234 C237 223 244 213 256 212 Z',
        bounds: [235, 209, 44, 48],
      },
    ],
  },
  'whale-knit': {
    src: () => getSceneAssetUrl('sprites/whale-knit.webp'),
    left: 21.92,
    top: 25.61,
    scale: 0.6042,
    travelX: 6.5,
    travelY: 4.3,
    eyeCenter: [206, 234],
    eyes: [
      {
        aperture:
          'M136 235 C139 225 151 217 162 217 C170 216 177 220 179 228 C182 236 181 248 173 252 C164 257 150 254 141 249 C136 246 134 239 136 235 Z',
        iris: 'M161 217 C171 217 178 227 178 239 C178 249 172 254 163 254 C152 254 145 245 146 234 C146 225 152 218 161 217 Z',
        bounds: [143, 215, 38, 42],
      },
      {
        aperture:
          'M231 230 C231 218 239 211 251 212 C263 213 271 221 273 231 C275 242 267 247 256 249 C243 252 234 247 231 238 C230 235 230 233 231 230 Z',
        iris: 'M249 212 C260 212 266 220 266 232 C267 242 261 250 251 250 C239 251 232 243 231 233 C231 222 238 213 249 212 Z',
        bounds: [229, 210, 40, 43],
      },
    ],
  },
  'whale-hoodie': {
    src: () => getSceneAssetUrl('sprites/whale-hoodie.webp'),
    left: 30.18,
    top: 26.16,
    scale: 0.6026,
    travelX: 6.5,
    travelY: 4.3,
    eyeCenter: [209, 237],
    eyes: [
      {
        aperture:
          'M140 239 C141 228 151 220 164 219 C175 218 182 222 185 230 C188 241 183 252 174 255 C162 259 149 255 143 249 C140 246 139 243 140 239 Z',
        iris: 'M167 219 C177 219 184 227 185 240 C185 249 179 256 169 256 C159 256 151 248 152 238 C151 229 157 220 167 219 Z',
        bounds: [149, 217, 39, 42],
      },
      {
        aperture:
          'M233 237 C233 225 242 218 253 218 C264 218 274 224 276 233 C278 243 274 249 263 254 C250 259 239 254 235 248 C233 245 232 241 233 237 Z',
        iris: 'M249 218 C259 218 267 226 268 237 C270 247 263 255 253 255 C241 256 233 248 233 238 C233 228 240 220 249 218 Z',
        bounds: [231, 216, 40, 43],
      },
    ],
  },
};

const IDLE_FRAME_WIDTH = 348;
const IDLE_FRAME_HEIGHT = 340;

export interface OriginalGaze {
  readonly petId: string;
  readonly centerX: number;
  readonly centerY: number;
  draw(ctx: CanvasRenderingContext2D, x: number, y: number, alpha: number, now: number): void;
}

export async function loadOriginalGaze(petId: string): Promise<OriginalGaze | null> {
  if (!Object.prototype.hasOwnProperty.call(ARTWORK, petId)) return null;
  const outfit = petId as OriginalOutfit;
  const art = ARTWORK[outfit];

  const image = new Image();
  image.src = art.src();
  await image.decode();

  const motion = createIdleMotion(image, outfit);
  const pad = motion ? IDLE_MOTION_PADDING : 0;
  const body = motion?.canvas ?? image;
  const layer = document.createElement('canvas');
  layer.width = image.naturalWidth + 2 * pad;
  layer.height = image.naturalHeight + 2 * pad;
  const context = layer.getContext('2d');
  if (!context) return null;
  context.translate(pad, pad);

  const eyes = art.eyes.map(({ aperture, iris, bounds }) => {
    const [x, y, width, height] = bounds;
    const white = context.createLinearGradient(x, y, x, y + height);
    white.addColorStop(0, '#f1e4e6');
    white.addColorStop(0.45, '#fff9f3');
    white.addColorStop(1, '#fbf0eb');
    return { aperture: new Path2D(aperture), iris: new Path2D(iris), x, y, width, height, white };
  });

  function paintEyes(ctx: CanvasRenderingContext2D, dx: number, dy: number) {
    ctx.clearRect(-pad, -pad, layer.width, layer.height);
    ctx.drawImage(body, -pad, -pad);
    ctx.globalAlpha = Math.min(1, Math.hypot(dx, dy) / 1.5);
    for (const eye of eyes) {
      ctx.save();
      ctx.clip(eye.aperture);
      ctx.fillStyle = eye.white;
      ctx.fill(eye.iris);
      ctx.strokeStyle = eye.white;
      ctx.lineWidth = 3;
      ctx.stroke(eye.iris);
      ctx.translate(dx, dy);
      ctx.clip(eye.iris);
      ctx.drawImage(
        image,
        eye.x,
        eye.y,
        eye.width,
        eye.height,
        eye.x,
        eye.y,
        eye.width,
        eye.height,
      );
      ctx.restore();
    }
    ctx.globalAlpha = 1;
  }

  let paintedX = Number.NaN;
  let paintedY = Number.NaN;
  return {
    petId,
    centerX: (art.left + art.eyeCenter[0] * art.scale) / IDLE_FRAME_WIDTH,
    centerY: (art.top + art.eyeCenter[1] * art.scale) / IDLE_FRAME_HEIGHT,
    draw(ctx, x, y, alpha, now) {
      const motionUpdated = motion?.update(now, x, y);
      const dx = x * art.travelX;
      const dy = y * art.travelY;
      const tracking = Math.hypot(dx, dy) > 0.025;
      if (tracking && (motionUpdated || dx !== paintedX || dy !== paintedY)) {
        paintEyes(context, dx, dy);
        paintedX = dx;
        paintedY = dy;
      }

      const sx = ctx.canvas.width / IDLE_FRAME_WIDTH;
      const sy = ctx.canvas.height / IDLE_FRAME_HEIGHT;
      ctx.save();
      ctx.globalAlpha = alpha;
      // Complement the current frame's alpha during the short handoff from the idle clip.
      if (alpha < 1) ctx.globalCompositeOperation = 'lighter';
      ctx.drawImage(
        tracking ? layer : body,
        (art.left - pad * art.scale) * sx,
        (art.top - pad * art.scale) * sy,
        layer.width * art.scale * sx,
        layer.height * art.scale * sy,
      );
      ctx.restore();
    },
  };
}
