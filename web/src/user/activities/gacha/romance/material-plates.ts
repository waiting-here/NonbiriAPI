/** Native-coordinate material plates shared by the five dedicated SP renderers.
 * Face/limb articulation stays in each scene. These meshes only carry independently
 * extracted free hair/cloth ends; a cut seam is never allowed to slide open.
 */
type Point = readonly [number, number];
type Plate = {
  name: string;
  file: string;
  crop: readonly [number, number, number, number];
  polygon: readonly Point[];
  pins?: readonly (readonly Point[])[];
  vector: Point;
  root: number;
  end: number;
  delay: number;
  seam: number;
  material: string;
  rootOnly?: boolean;
  rigid?: boolean;
  pinX?: Point;
  clip?: boolean;
  axis?: 'x' | 'y';
  pivot?: Point;
  rotation?: number;
  sewnGroup?: string;
  cycle?: number;
  pinMargin?: number;
};
type Manifest = { canvas: Point; cycle: number; source?: string; repair?: string; layers: Plate[] };
export type MaterialSceneId = 'n-gemini' | 'o-claude' | 'p-claude' | 'q-glm' | 'r-chatgpt';
export type MaterialDirectory = 'material-layers' | 'environment-light' | 'environment-dark';
export type MaterialPlateSource = {
  name: string;
  path: string;
  rect: readonly [number, number, number, number];
};

/** Resource-viewer descriptors; rect is in the adopted native source canvas. */
export async function materialPlateSources(
  id: MaterialSceneId,
  directory: MaterialDirectory = 'material-layers',
  signal?: AbortSignal,
): Promise<MaterialPlateSource[]> {
  const base = `/assets/gacha/motion/${id}/${directory}/`;
  const response = await fetch(base + 'manifest.json', { signal });
  if (!response.ok) throw new Error('material-manifest-unavailable');
  const manifest: Manifest = await response.json();
  const rect = [0, 0, ...manifest.canvas] as const;
  return [
    ...(manifest.source
      ? [{ name: 'adopted-source', path: `/assets/gacha/motion/${id}/${manifest.source}`, rect }]
      : []),
    { name: 'material-cut-mask', path: base + 'cut-mask.webp', rect },
    ...(manifest.repair ? [{ name: 'occlusion-backing', path: base + manifest.repair, rect }] : []),
    ...manifest.layers.map((plate) => ({
      name: plate.name,
      path: base + plate.file,
      rect: plate.crop,
    })),
  ];
}
type Source = HTMLImageElement | HTMLCanvasElement;
const uploadedSizes = new WeakMap<WebGLTexture, Point>();

/** Resize a caller-owned texture only when its projected material buffer changes. */
export function uploadMaterialPlate(
  gl: WebGLRenderingContext,
  texture: WebGLTexture,
  surface: HTMLCanvasElement,
) {
  gl.bindTexture(gl.TEXTURE_2D, texture);
  const previous = uploadedSizes.get(texture);
  if (!previous || previous[0] !== surface.width || previous[1] !== surface.height) {
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, surface);
    uploadedSizes.set(texture, [surface.width, surface.height]);
  } else {
    gl.texSubImage2D(gl.TEXTURE_2D, 0, 0, 0, gl.RGBA, gl.UNSIGNED_BYTE, surface);
  }
}
const smooth = (v: number) => {
  const t = Math.max(0, Math.min(1, v));
  return t * t * (3 - 2 * t);
};

// A breeze arrives, rests, then settles. Hair responds before heavier cloth;
// there is no continuous universal oscillation or whole-character deformation.
export function materialImpulse(seconds: number, cycle: number, delay = 0, heavy = false) {
  const t = (((seconds - delay) % cycle) + cycle) % cycle;
  const attack = heavy ? 4.4 : 2.8;
  const crest = smooth((t - 1.5) / attack) * (1 - smooth((t - 8.5) / 6));
  const release = smooth((t - 15.5) / 2.5) * (1 - smooth((t - 20) / 4));
  return crest - release * (heavy ? 0.18 : 0.3);
}

function seamDistance(x: number, y: number, polygon: readonly Point[]) {
  let distance = Infinity;
  for (let i = 0; i < polygon.length; i++) {
    const a = polygon[i],
      b = polygon[(i + 1) % polygon.length];
    const dx = b[0] - a[0],
      dy = b[1] - a[1];
    const t = Math.max(
      0,
      Math.min(1, ((x - a[0]) * dx + (y - a[1]) * dy) / Math.max(1, dx * dx + dy * dy)),
    );
    distance = Math.min(distance, Math.hypot(x - a[0] - dx * t, y - a[1] - dy * t));
  }
  return distance;
}

function mesh(plate: Plate | undefined, canvas: Point) {
  const [x, y, width, height] = plate?.crop ?? [0, 0, ...canvas];
  const columns = plate ? Math.ceil(width / 12) : 1;
  const rows = plate ? Math.ceil(height / 12) : 1;
  const vertices: number[] = [];
  function vertex(col: number, row: number) {
    const u = col / columns,
      v = row / rows;
    const px = x + u * width,
      py = y + v * height;
    const coordinate = plate?.axis === 'x' ? px : py;
    const progress = plate ? smooth((coordinate - plate.root) / (plate.end - plate.root || 1)) : 0;
    // A 24px undeformed collar protects all internal cuts and hand contacts.
    const distance = plate
      ? Math.min(
          seamDistance(px, py, plate.polygon),
          ...(plate.pins ?? []).map((pin) => seamDistance(px, py, pin)),
        )
      : 0;
    const edgePin = plate?.rootOnly
      ? 1
      : plate
        ? smooth((distance - (plate.pinMargin ?? 10)) / plate.seam)
        : 0;
    const contactPin = plate?.pinX
      ? smooth((px - plate.pinX[0]) / (plate.pinX[1] - plate.pinX[0]))
      : 1;
    vertices.push(px, py, u, v, plate?.rigid ? 1 : progress * edgePin * contactPin);
  }
  for (let row = 0; row < rows; row++)
    for (let col = 0; col < columns; col++) {
      vertex(col, row);
      vertex(col + 1, row);
      vertex(col, row + 1);
      vertex(col, row + 1);
      vertex(col + 1, row);
      vertex(col + 1, row + 1);
    }
  return new Float32Array(vertices);
}

function loadImage(url: string, signal: AbortSignal, owned: Set<HTMLImageElement>) {
  return new Promise<HTMLImageElement>((resolve, reject) => {
    const image = new Image();
    owned.add(image);
    const finish = () => {
      signal.removeEventListener('abort', abort);
      image.onload = image.onerror = null;
    };
    const abort = () => {
      finish();
      image.src = '';
      reject(new DOMException('Aborted', 'AbortError'));
    };
    image.onload = () => {
      finish();
      resolve(image);
    };
    image.onerror = () => {
      finish();
      reject(new Error('material-plate-unavailable'));
    };
    if (signal.aborted) {
      abort();
      return;
    }
    signal.addEventListener('abort', abort, { once: true });
    image.src = url;
  });
}

export async function createMaterialPlates(
  id: string,
  source: Source,
  signal?: AbortSignal,
  directory = 'material-layers',
) {
  const base = `/assets/gacha/motion/${id}/${directory}/`;
  const controller = new AbortController();
  const ownedImages = new Set<HTMLImageElement>();
  const ownedCanvases: HTMLCanvasElement[] = [];
  const surface = document.createElement('canvas');
  const textures: WebGLTexture[] = [],
    buffers: WebGLBuffer[] = [],
    shaders: WebGLShader[] = [];
  let graphics: WebGLRenderingContext | null = null;
  let ownedProgram: WebGLProgram | null = null;
  let disposed = false;
  const releaseImages = () => {
    for (const image of ownedImages) {
      image.onload = image.onerror = null;
      image.src = '';
    }
    ownedImages.clear();
  };
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    controller.abort();
    signal?.removeEventListener('abort', dispose);
    releaseImages();
    ownedCanvases.forEach((canvas) => {
      canvas.width = canvas.height = 0;
    });
    textures.forEach((texture) => graphics?.deleteTexture(texture));
    buffers.forEach((buffer) => graphics?.deleteBuffer(buffer));
    shaders.forEach((shader) => graphics?.deleteShader(shader));
    graphics?.deleteProgram(ownedProgram);
    graphics?.getExtension('WEBGL_lose_context')?.loseContext();
    surface.width = surface.height = 0;
  };
  signal?.addEventListener('abort', dispose, { once: true });
  try {
    if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
    const response = await fetch(base + 'manifest.json', { signal: controller.signal });
    if (!response.ok) throw new Error('material-manifest-unavailable');
    const manifest: Manifest = await response.json();
    const sourceWidth = source instanceof HTMLImageElement ? source.naturalWidth : source.width;
    const sourceHeight = source instanceof HTMLImageElement ? source.naturalHeight : source.height;
    const resolutionLimit = Math.min(
      4096 / manifest.canvas[0],
      4096 / manifest.canvas[1],
      Math.max(1, Math.min(sourceWidth / manifest.canvas[0], sourceHeight / manifest.canvas[1])),
    );
    const load = (file: string) => loadImage(base + file, controller.signal, ownedImages);
    const [mask, repair, ...plates] = await Promise.all([
      load('cut-mask.webp'),
      manifest.repair ? load(manifest.repair) : null,
      ...manifest.layers.map((plate) => load(plate.file)),
    ]);
    if (controller.signal.aborted) throw new DOMException('Aborted', 'AbortError');
    [surface.width, surface.height] = manifest.canvas;
    const gl = surface.getContext('webgl', {
      alpha: true,
      premultipliedAlpha: true,
      antialias: false,
    });
    graphics = gl;
    if (!gl) throw new Error('material-renderer-unavailable');
    const program = gl.createProgram();
    ownedProgram = program;
    if (!program) throw new Error('material-renderer-unavailable');
    for (const [type, code] of [
      [
        gl.VERTEX_SHADER,
        `attribute vec2 position,texcoord;attribute float weight;
        uniform vec2 size,offset,pivot;uniform float angle;varying vec2 uv,original,projected;
        void main(){uv=texcoord;original=position/size;
        vec2 local=position-pivot;float c=cos(angle),s=sin(angle);
        vec2 p=(pivot+vec2(c*local.x-s*local.y,s*local.x+c*local.y)+offset*weight)/size;
        projected=p;gl_Position=vec4(p.x*2.-1.,1.-p.y*2.,0.,1.);}`,
      ],
      [
        gl.FRAGMENT_SHADER,
        `precision highp float;varying vec2 uv,original,projected;
        uniform sampler2D art,cut;uniform float basePass,clipLayer,opacity;
        void main(){vec4 c=texture2D(art,uv);
        float coverage=(1.-basePass*texture2D(cut,original).r)
          *mix(1.,texture2D(cut,projected).r,clipLayer)*opacity;
        gl_FragColor=c*coverage;}`,
      ],
    ] as const) {
      const shader = gl.createShader(type);
      if (!shader) throw new Error('material-shader-unavailable');
      shaders.push(shader);
      gl.shaderSource(shader, code);
      gl.compileShader(shader);
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS))
        throw new Error(gl.getShaderInfoLog(shader) ?? 'material-shader-unavailable');
      gl.attachShader(program, shader);
    }
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS))
      throw new Error('material-shader-unavailable');
    gl.useProgram(program);
    // Filter premultiplied pixels, including repaired holes and silk edges.
    // Multiplying after interpolation would pull hidden RGB into the silhouette.
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    const texture = (image: Source, unit = 0) => {
      const result = gl.createTexture();
      if (!result) throw new Error('material-texture-unavailable');
      textures.push(result);
      gl.activeTexture(gl.TEXTURE0 + unit);
      gl.bindTexture(gl.TEXTURE_2D, result);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
      return result;
    };
    const sourceTexture = texture(source);
    texture(mask!, 1);
    const repairTexture = repair ? texture(repair) : undefined;
    // Different materials sewn to one panel keep a common mesh. Reassemble their
    // complementary coverage once, with additive premultiplied alpha, so the
    // lining/outer-fabric seam cannot acquire a filtering crack or slide apart.
    const renderLayers: Plate[] = [];
    const plateTextures: WebGLTexture[] = [];
    const groups = new Set<string>();
    manifest.layers.forEach((plate, index) => {
      if (plate.sewnGroup && groups.has(plate.sewnGroup)) return;
      renderLayers.push(plate);
      if (!plate.sewnGroup) {
        plateTextures.push(texture(plates[index]!));
        return;
      }
      groups.add(plate.sewnGroup);
      const joined = document.createElement('canvas');
      ownedCanvases.push(joined);
      [joined.width, joined.height] = plate.crop.slice(2) as [number, number];
      const context = joined.getContext('2d');
      if (!context) throw new Error('material-composite-unavailable');
      context.globalCompositeOperation = 'lighter';
      manifest.layers.forEach((member, memberIndex) => {
        if (member.sewnGroup !== plate.sewnGroup) return;
        if (member.crop.some((value, coordinate) => value !== plate.crop[coordinate]))
          throw new Error('material-sewn-registration-mismatch');
        context.drawImage(plates[memberIndex]!, 0, 0);
      });
      plateTextures.push(texture(joined));
      joined.width = joined.height = 0;
    });
    // Only this loader's decoded images are released. The adopted source belongs
    // to the calling scene and is never cleared by the material renderer.
    releaseImages();
    const meshes = [undefined, ...renderLayers].map((plate) => {
      const buffer = gl.createBuffer();
      if (!buffer) throw new Error('material-buffer-unavailable');
      buffers.push(buffer);
      gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
      const vertices = mesh(plate, manifest.canvas);
      gl.bufferData(gl.ARRAY_BUFFER, vertices, gl.STATIC_DRAW);
      return { buffer, count: vertices.length / 5 };
    });
    gl.uniform1i(gl.getUniformLocation(program, 'art'), 0);
    gl.uniform1i(gl.getUniformLocation(program, 'cut'), 1);
    gl.uniform2f(gl.getUniformLocation(program, 'size'), ...manifest.canvas);
    const offset = gl.getUniformLocation(program, 'offset'),
      basePass = gl.getUniformLocation(program, 'basePass');
    const clipLayer = gl.getUniformLocation(program, 'clipLayer'),
      opacity = gl.getUniformLocation(program, 'opacity');
    const pivot = gl.getUniformLocation(program, 'pivot'),
      angle = gl.getUniformLocation(program, 'angle');
    const attributes = ['position', 'texcoord', 'weight'].map((name) =>
      gl.getAttribLocation(program, name),
    );
    function drawPart(
      tex: WebGLTexture,
      index: number,
      dx = 0,
      dy = 0,
      cut = false,
      clip = false,
      alpha = 1,
      rotation = 0,
      anchor: Point = [0, 0],
    ) {
      gl!.bindBuffer(gl!.ARRAY_BUFFER, meshes[index].buffer);
      attributes.forEach((attribute, i) => {
        gl!.enableVertexAttribArray(attribute);
        gl!.vertexAttribPointer(attribute, i === 2 ? 1 : 2, gl!.FLOAT, false, 20, [0, 8, 16][i]);
      });
      gl!.activeTexture(gl!.TEXTURE0);
      gl!.bindTexture(gl!.TEXTURE_2D, tex);
      gl!.uniform2f(offset, dx, dy);
      gl!.uniform1f(basePass, cut ? 1 : 0);
      gl!.uniform1f(clipLayer, clip ? 1 : 0);
      gl!.uniform1f(opacity, alpha);
      gl!.uniform2f(pivot, ...anchor);
      gl!.uniform1f(angle, rotation);
      gl!.drawArrays(gl!.TRIANGLES, 0, meshes[index].count);
    }
    return {
      surface,
      dispose,
      setResolution(projectedWidth: number, projectedHeight: number) {
        if (disposed) return;
        const scale = Math.min(
          resolutionLimit,
          Math.max(
            projectedWidth / manifest.canvas[0],
            projectedHeight / manifest.canvas[1],
            1 / 16,
          ),
        );
        const width = Math.max(1, Math.ceil(manifest.canvas[0] * scale));
        const height = Math.max(1, Math.ceil(manifest.canvas[1] * scale));
        if (surface.width !== width || surface.height !== height) {
          surface.width = width;
          surface.height = height;
        }
      },
      draw(seconds: number, articulatedSource?: HTMLCanvasElement) {
        if (disposed) return surface;
        gl.useProgram(program);
        gl.viewport(0, 0, surface.width, surface.height);
        gl.clearColor(0, 0, 0, 0);
        gl.clear(gl.COLOR_BUFFER_BIT);
        gl.enable(gl.BLEND);
        // Cut source and backing have complementary coverage. Adding these two
        // premultiplied passes prevents a 75%-alpha seam at a half-covered edge.
        gl.blendFunc(gl.ONE, gl.ONE);
        if (articulatedSource) {
          gl.activeTexture(gl.TEXTURE0);
          gl.bindTexture(gl.TEXTURE_2D, sourceTexture);
          gl.texSubImage2D(gl.TEXTURE_2D, 0, 0, 0, gl.RGBA, gl.UNSIGNED_BYTE, articulatedSource);
        }
        if (repairTexture) drawPart(repairTexture, 0);
        drawPart(sourceTexture, 0, 0, 0, true);
        gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
        for (const [index, plate] of renderLayers.entries()) {
          if (plate.material === 'cloud') {
            // One-way advection. A quiet dissolve closes the long drift without
            // reversing the cloud direction or moving the foreground wires.
            const phase =
              (((seconds % manifest.cycle) + manifest.cycle) % manifest.cycle) / manifest.cycle;
            const reset = smooth((phase - 0.88) / 0.12);
            drawPart(
              plateTextures[index],
              index + 1,
              plate.vector[0] * phase,
              0,
              false,
              true,
              1 - reset,
            );
            if (reset)
              drawPart(
                plateTextures[index],
                index + 1,
                plate.vector[0] * (phase - 1),
                0,
                false,
                true,
                reset,
              );
          } else {
            const impulse =
              plate.material === 'flame'
                ? 0.65 * Math.sin(seconds * 7.1 + plate.delay) +
                  0.35 * Math.sin(seconds * 11.3 - plate.delay)
                : plate.material === 'water'
                  ? 0.62 * Math.sin(seconds * 1.23 + plate.delay) +
                    0.38 * Math.sin(seconds * 2.17 - plate.delay)
                  : materialImpulse(
                      seconds,
                      plate.cycle ?? manifest.cycle,
                      plate.delay,
                      plate.material.includes('cloth'),
                    );
            drawPart(
              plateTextures[index],
              index + 1,
              plate.vector[0] * impulse,
              plate.vector[1] * impulse,
              false,
              !!plate.clip,
              1,
              (plate.rotation ?? 0) * impulse,
              plate.pivot,
            );
          }
        }
        return surface;
      },
    };
  } catch (error) {
    dispose();
    throw error;
  }
}
