import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createChristmasScene } from './scene';
import { renderLayers } from './layers';

let imageMode: 'load' | 'pending' | 'fail-closed';
let images: MockImage[];
let imageSize: [number, number];
class MockImage {
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  naturalWidth = imageSize[0];
  naturalHeight = imageSize[1];
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
      else if (imageMode === 'fail-closed' && value.endsWith('/closed.webp')) this.onerror?.();
    });
  }
  get src() {
    return this.url;
  }
}

let gl: WebGLRenderingContext;
let uploads: { source?: string; width?: number; height?: number; premultiplied?: boolean }[];
let context: CanvasRenderingContext2D;
let lose: ReturnType<typeof vi.fn>;
const cleanups: (() => void)[] = [];
beforeEach(() => {
  imageMode = 'load';
  imageSize = [2560, 3840];
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
    'ELEMENT_ARRAY_BUFFER',
    'UNSIGNED_SHORT',
    'COLOR_BUFFER_BIT',
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
    'BLEND',
    'ONE',
    'ONE_MINUS_SRC_ALPHA',
    'UNPACK_PREMULTIPLY_ALPHA_WEBGL',
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
    'drawElements',
    'clear',
    'deleteTexture',
    'deleteShader',
    'deleteBuffer',
    'deleteProgram',
    'enable',
    'blendFunc',
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
    else
      uploads.push({
        width: Number(args[3]),
        height: Number(args[4]),
        premultiplied:
          vi
            .mocked(gl.pixelStorei)
            .mock.calls.filter(([flag]) => flag === gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL)
            .at(-1)?.[1] === true,
      });
  });
  gl = api as unknown as WebGLRenderingContext;
  context = Object.fromEntries([
    ...[
      'drawImage',
      'clearRect',
      'save',
      'restore',
      'scale',
      'beginPath',
      'ellipse',
      'arc',
      'clip',
      'fill',
      'fillRect',
      'moveTo',
      'lineTo',
      'stroke',
    ].map((name) => [name, vi.fn()]),
    ...['createRadialGradient', 'createLinearGradient'].map((name) => [
      name,
      vi.fn(() => ({ addColorStop: vi.fn() })),
    ]),
  ]) as unknown as CanvasRenderingContext2D;
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

it('selects high-resolution character assets without loading the background for moments', async () => {
  const scene = await createChristmasScene(document.createElement('canvas'), 't-chatgpt', {
    characterOnly: true,
    highResolution: true,
  });
  cleanups.push(scene.dispose);
  expect(images.map((image) => image.requested)).toEqual(
    ['hero', 'closed', ...renderLayers('t-chatgpt').map((layer) => 'parts/' + layer.name)].map(
      (name) => '/assets/gacha/hires/motion/t-chatgpt/' + name + '.webp',
    ),
  );
  expect(uploads).toHaveLength(renderLayers('t-chatgpt').length + 2);
  expect(uploads.find((upload) => upload.height === 4)?.premultiplied).toBe(false);
  expect(images[1].src).toBe('');
  scene.resize();
  scene.paint(2.35, 1, true);
  const uploadsBeforePaint = vi.mocked(gl.bufferData).mock.calls.length;
  scene.paint(4.7, 0, true);
  expect(gl.bufferData).toHaveBeenCalledTimes(uploadsBeforePaint);
  expect(gl.clear).toHaveBeenCalledWith(gl.COLOR_BUFFER_BIT);
  scene.dispose();
  scene.dispose();
  expect(gl.deleteTexture).toHaveBeenCalledTimes(vi.mocked(gl.createTexture).mock.calls.length);
  expect(gl.deleteShader).toHaveBeenCalledTimes(2);
  expect(gl.deleteBuffer).toHaveBeenCalledTimes(vi.mocked(gl.createBuffer).mock.calls.length);
  expect(gl.deleteProgram).toHaveBeenCalledOnce();
  expect(lose).toHaveBeenCalledOnce();
  expect(images.every((image) => image.src === '')).toBe(true);
});

it('keeps room architecture unwarped and uses the complete flat artwork for reduced motion', async () => {
  const canvas = document.createElement('canvas');
  const scene = await createChristmasScene(canvas, 't-glm');
  cleanups.push(scene.dispose);
  scene.resize();
  scene.paint(0, 0, true);
  scene.paint(7, 0, true);
  const backgrounds = vi
    .mocked(context.drawImage)
    .mock.calls.filter((args) => args[0] === (images[0] as unknown as HTMLImageElement));
  expect(backgrounds).toHaveLength(2);
  expect(backgrounds[0]).toEqual(backgrounds[1]);
  const draws = vi.mocked(gl.drawElements).mock.calls.length;
  scene.paint(7, 0, false);
  expect(gl.drawElements).toHaveBeenCalledTimes(draws);
  expect(context.drawImage).toHaveBeenLastCalledWith(images[1], 0, 0, canvas.width, canvas.height);
});

it('cancels sibling image loads when an eye state fails before touching the canvas', async () => {
  imageMode = 'fail-closed';
  await expect(createChristmasScene(document.createElement('canvas'), 't-claude')).rejects.toThrow(
    'image-unavailable',
  );
  expect(images.every((image) => image.src === '')).toBe(true);
  expect(HTMLCanvasElement.prototype.getContext).not.toHaveBeenCalled();
});

it('aborts all pending images before constructing a GL context', async () => {
  imageMode = 'pending';
  const controller = new AbortController();
  const result = createChristmasScene(document.createElement('canvas'), 't-deepseek', {
    signal: controller.signal,
  });
  controller.abort();
  await expect(result).rejects.toMatchObject({ name: 'AbortError' });
  expect(images.every((image) => image.src === '')).toBe(true);
  expect(HTMLCanvasElement.prototype.getContext).not.toHaveBeenCalled();
});

it('cleans partially compiled scenes on shader failure', async () => {
  vi.mocked(gl.getShaderParameter).mockReturnValueOnce(false);
  await expect(createChristmasScene(document.createElement('canvas'), 't-gemini')).rejects.toThrow(
    'shader-unavailable',
  );
  expect(gl.deleteShader).toHaveBeenCalledOnce();
  expect(gl.deleteProgram).toHaveBeenCalledOnce();
  expect(gl.deleteBuffer).not.toHaveBeenCalled();
  expect(lose).toHaveBeenCalledOnce();
  expect(images.every((image) => image.src === '')).toBe(true);
});

it('releases a vertex buffer when its paired index buffer cannot be allocated', async () => {
  vi.mocked(gl.createBuffer as () => WebGLBuffer | null)
    .mockReturnValueOnce({} as WebGLBuffer)
    .mockReturnValueOnce(null);
  await expect(createChristmasScene(document.createElement('canvas'), 't-claude')).rejects.toThrow(
    'renderer-unavailable',
  );
  expect(gl.deleteBuffer).toHaveBeenCalledOnce();
  expect(gl.deleteTexture).toHaveBeenCalledTimes(vi.mocked(gl.createTexture).mock.calls.length);
  expect(lose).toHaveBeenCalledOnce();
});

it('reveals the outer static art and stops its owner when the GL context is lost', async () => {
  const canvas = document.createElement('canvas');
  const failed = vi.fn();
  const scene = await createChristmasScene(canvas, 't-chatgpt', {}, failed);
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
  expect(gl.deleteTexture).toHaveBeenCalledTimes(vi.mocked(gl.createTexture).mock.calls.length);
});
