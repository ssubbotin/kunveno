// gaussian returns the weights of a separable Gaussian blur: the centre and one side (the other
// side mirrors it), normalised so the whole kernel sums to one.
export function gaussian(radius: number, sigma: number): Float32Array {
  const w = new Float32Array(radius + 1);
  for (let i = 0; i <= radius; i++) w[i] = Math.exp(-(i * i) / (2 * sigma * sigma));
  const sum = w[0] + 2 * w.slice(1).reduce((a, b) => a + b, 0);
  return w.map((v) => v / sum);
}
