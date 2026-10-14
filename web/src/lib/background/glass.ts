// The glass-block background: every block shows its own piece of the scene, shifted a little in a
// direction that belongs to the block, so the picture breaks at the block borders as behind a
// glass-block wall. The shift comes from a hash of the block's position, the same formula as in the
// compositor's shader, so it is stable from frame to frame.
const fract = (x: number) => x - Math.floor(x);

// blockShift returns the shift of block (bx, by) in block widths, each coordinate in -0.3..0.3.
export function blockShift(bx: number, by: number): [number, number] {
  const hx = fract(Math.sin(bx * 127.1 + by * 311.7) * 43758.5453);
  const hy = fract(Math.sin(bx * 269.5 + by * 183.3) * 43758.5453);
  return [(hx - 0.5) * 0.6, (hy - 0.5) * 0.6];
}
