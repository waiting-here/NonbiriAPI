import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createSweetScene } from './scene';

let imageMode: 'load' | 'pending' | 'fail-half';
let images: MockImage[];
class MockImage {
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  naturalWidth = 2560;
  naturalHeight = 3840;
  url = '';
  requested = '';
  constructor() {
    images.push(this);
  }
  set src(value: string) {
    this.url = value;
    if (!value) return;
    this.requested = value;
    queueMicrotask(() => {
      if (imageMode === 'load') this.onload?.();
      else if (imageMode === 'fail-half' && value.endsWith('/half.webp')) this.onerror?.();
    });
  }
  get src() {
    return this.url;
  }
}

let gl: WebGLRenderingContext;
let uploads: { source?: string; width?: number; height?: number }[];
let context: CanvasRenderingContext2D;
let lose: ReturnType<typeof vi.fn>;
const cleanups: (() => void)[] = [];
beforeEach(() => {
  imageMode = 'load';
  images = [];
  uploads = [];
  vi.stubGlobal('Image', MockImage);
  lose = vi.fn();
  const api: Record<string, unknown> = { getExtension: vi.fn(() => ({ loseContext: lose })) };
  [
    'VERTEX_SHADER',
    'FRAGMENT_SHADER',
    'COMPILE_STATUS',
    'LINK_STATUS',
    'ARRAY_BUFFER',
    'STATIC_DRAW',
    'FLOAT',
    'TEXTURE0',
    'TEXTURE_2D',
    'TEXTURE_MIN_FILTER',
    'TEXTURE_MAG_FILTER',
    'LINEAR',
    'TEXTURE_WRAP_S',
    'TEXTURE_WRAP_T',
    'CLAMP_TO_EDGE',
    'RGBA',
    'UNSIGNED_BYTE',
    'UNPACK_ALIGNMENT',
    'LUMINANCE',
    'TRIANGLES',
  ].forEach((name, i) => {
    api[name] = i + 1;
  });
  ['createProgram', 'createBuffer', 'createShader', 'createTexture', 'getUniformLocation'].forEach(
    (name) => {
      api[name] = vi.fn(() => ({}));
    },
  );
  [
    'shaderSource',
    'compileShader',
    'attachShader',
    'linkProgram',
    'useProgram',
    'bindBuffer',
    'bufferData',
    'enableVertexAttribArray',
    'vertexAttribPointer',
    'activeTexture',
    'bindTexture',
    'texParameteri',
    'uniform1i',
    'uniform4f',
    'uniform2f',
    'uniform1f',
    'pixelStorei',
    'viewport',
    'drawArrays',
    'deleteTexture',
    'deleteShader',
    'deleteBuffer',
    'deleteProgram',
  ].forEach((name) => {
    api[name] = vi.fn();
  });
  api.getShaderParameter = vi.fn(() => true);
  api.getProgramParameter = vi.fn(() => true);
  api.getAttribLocation = vi.fn(() => 0);
  api.texImage2D = vi.fn((...args: unknown[]) => {
    const source = args[5];
    if (source instanceof MockImage) uploads.push({ source: source.requested });
    else if (source instanceof HTMLCanvasElement)
      uploads.push({ width: source.width, height: source.height });
    else uploads.push({ width: Number(args[3]), height: Number(args[4]) });
  });
  gl = api as unknown as WebGLRenderingContext;
  context = { drawImage: vi.fn(), clearRect: vi.fn() } as unknown as CanvasRenderingContext2D;
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(function (
    this: HTMLCanvasElement,
    type: string,
  ) {
    if (type === 'webgl') {
      return gl;
    }
    return context;
  } as typeof HTMLCanvasElement.prototype.getContext);
});
afterEach(() => {
  cleanups.splice(0).forEach((dispose) => dispose());
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it('uploads only one full character and a cropped two-state eye atlas, and releases every GL resource', async () => {
  const canvas = document.createElement('canvas');
  const scene = await createSweetScene(canvas, 's-chatgpt', {
    characterOnly: true,
    highResolution: true,
  });
  cleanups.push(scene.dispose);
  expect(images.map((image) => image.requested)).toEqual(
    ['hero', 'half', 'closed'].map((name) => `/assets/gacha/hires/motion/s-chatgpt/${name}.webp`),
  );
  expect(uploads).toHaveLength(4);
  expect(uploads[0].source).toContain('/hero.webp');
  expect(uploads[1]).toEqual({ width: 900, height: 1800 });
  expect(images.slice(1).every((image) => image.src === '')).toBe(true);
  scene.resize();
  scene.paint(2.35, 1, true);
  expect(gl.drawArrays).toHaveBeenCalledOnce();
  scene.dispose();
  scene.dispose();
  expect(gl.deleteTexture).toHaveBeenCalledTimes(4);
  expect(gl.deleteShader).toHaveBeenCalledTimes(2);
  expect(gl.deleteProgram).toHaveBeenCalledOnce();
  expect(gl.deleteBuffer).toHaveBeenCalledOnce();
  expect(lose).toHaveBeenCalledOnce();
  expect(images.every((image) => image.src === '')).toBe(true);
});

it('cancels pending sibling loads when one eye state fails, leaving the outer canvas untouched', async () => {
  imageMode = 'fail-half';
  await expect(createSweetScene(document.createElement('canvas'), 's-claude')).rejects.toThrow(
    'image-unavailable',
  );
  expect(
    images
      .filter((image) => !image.requested.endsWith('/half.webp'))
      .every((image) => image.src === ''),
  ).toBe(true);
  expect(HTMLCanvasElement.prototype.getContext).not.toHaveBeenCalled();
});

it('aborts all image requests before constructing a canvas or GL context', async () => {
  imageMode = 'pending';
  const controller = new AbortController();
  const result = createSweetScene(document.createElement('canvas'), 's-claude', {
    signal: controller.signal,
  });
  controller.abort();
  await expect(result).rejects.toMatchObject({ name: 'AbortError' });
  expect(images.every((image) => image.src === '')).toBe(true);
  expect(HTMLCanvasElement.prototype.getContext).not.toHaveBeenCalled();
});

it('cleans partially compiled scenes on shader failure', async () => {
  vi.mocked(gl.getShaderParameter).mockReturnValueOnce(false);
  await expect(createSweetScene(document.createElement('canvas'), 's-claude')).rejects.toThrow(
    'shader-unavailable',
  );
  expect(gl.deleteShader).toHaveBeenCalledOnce();
  expect(gl.deleteProgram).toHaveBeenCalledOnce();
  expect(gl.deleteBuffer).toHaveBeenCalledOnce();
  expect(lose).toHaveBeenCalledOnce();
});

it('clears the animation and stops its owner when the WebGL context is lost', async () => {
  const canvas = document.createElement('canvas'),
    failed = vi.fn();
  const scene = await createSweetScene(canvas, 's-claude', {}, failed);
  cleanups.push(scene.dispose);
  const mock = vi.mocked(HTMLCanvasElement.prototype.getContext).mock;
  const layer = mock.contexts[
    mock.calls.findIndex((args) => args[0] === 'webgl')
  ] as HTMLCanvasElement;
  layer.dispatchEvent(new Event('webglcontextlost'));
  expect(context.clearRect).toHaveBeenCalledWith(0, 0, canvas.width, canvas.height);
  expect(failed).toHaveBeenCalledOnce();
  expect(canvas.dataset.ready).toBe('false');
  expect(canvas.style.visibility).toBe('hidden');
  expect(gl.deleteTexture).toHaveBeenCalledTimes(4);
});
