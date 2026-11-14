export function createMythicMoment() {
  const layer = document.createElement('canvas'),
    mask = document.createElement('canvas');
  const context = layer.getContext('2d'),
    matte = mask.getContext('2d');
  const dispose = () => {
    layer.width = layer.height = mask.width = mask.height = 0;
  };
  if (!context || !matte) {
    dispose();
    throw new Error('renderer-unavailable');
  }
  return {
    paint(
      ctx: CanvasRenderingContext2D,
      expanded: CanvasImageSource,
      character: CanvasImageSource,
      x: number,
      y: number,
      width: number,
      height: number,
      frameWidth: number,
      frameHeight: number,
    ) {
      const w = Math.max(1, Math.round(width)),
        h = Math.max(1, Math.round(height));
      if (layer.width !== w || layer.height !== h) {
        layer.width = mask.width = w;
        layer.height = mask.height = h;
        const feather = 32 / 1024;
        const gradient = matte.createLinearGradient(0, 0, w, 0);
        gradient.addColorStop(0, 'transparent');
        gradient.addColorStop(feather, '#fff');
        gradient.addColorStop(1 - feather, '#fff');
        gradient.addColorStop(1, 'transparent');
        matte.fillStyle = gradient;
        matte.fillRect(0, 0, w, h);
      }
      context.globalCompositeOperation = 'source-over';
      context.clearRect(0, 0, w, h);
      context.drawImage(character, 0, 0, w, h);
      context.globalCompositeOperation = 'destination-in';
      context.drawImage(mask, 0, 0);
      ctx.save();
      ctx.drawImage(expanded, 0, 0, frameWidth, frameHeight);
      ctx.globalCompositeOperation = 'destination-out';
      ctx.drawImage(mask, x, y, width, height);
      // Add complementary premultiplied coverage; source-over would dim the 32px seam.
      ctx.globalCompositeOperation = 'lighter';
      ctx.drawImage(layer, x, y, width, height);
      ctx.restore();
    },
    dispose,
  };
}
