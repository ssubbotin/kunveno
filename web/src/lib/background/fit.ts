// cover places an image so it fills the frame without distortion, cropping what does not fit: the
// image is sampled at 0.5 + (uv - 0.5) * (sx, sy) for frame coordinates uv in 0..1.
export function cover(srcW: number, srcH: number, dstW: number, dstH: number): { sx: number; sy: number } {
  const src = srcW / srcH;
  const dst = dstW / dstH;
  if (Math.abs(src - dst) < 1e-6) return { sx: 1, sy: 1 };
  return src > dst ? { sx: dst / src, sy: 1 } : { sx: 1, sy: src / dst };
}
