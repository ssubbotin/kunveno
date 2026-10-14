// WebGL2 compositing for the background effect, inside the worker. Textures follow image order
// (row 0 at the top); passes into framebuffers keep that order, only the last pass to the canvas
// flips, since the canvas origin is at the bottom.
import { cover } from './fit';
import { gaussian } from './kernel';

const VERT = `#version 300 es
in vec2 a;
uniform bool uFlip;
out vec2 v;
void main() {
  v = vec2(a.x * 0.5 + 0.5, uFlip ? 0.5 - a.y * 0.5 : a.y * 0.5 + 0.5);
  gl_Position = vec4(a, 0.0, 1.0);
}`;

// Temporal smoothing of the mask: the new mask is blended with the previous result, so edges do
// not flicker between frames.
const MASK = `#version 300 es
precision mediump float;
in vec2 v;
uniform sampler2D uNew;
uniform sampler2D uPrev;
uniform float uKeep;
out vec4 o;
void main() {
  float m = mix(texture(uNew, v).r, texture(uPrev, v).r, uKeep);
  o = vec4(m, m, m, 1.0);
}`;

// One direction of a separable Gaussian blur; weights from kernel.ts, radius 8.
const BLUR = `#version 300 es
precision mediump float;
in vec2 v;
uniform sampler2D uSrc;
uniform vec2 uStep;
uniform float uW[9];
out vec4 o;
void main() {
  vec4 c = texture(uSrc, v) * uW[0];
  for (int i = 1; i < 9; i++) {
    c += texture(uSrc, v + uStep * float(i)) * uW[i];
    c += texture(uSrc, v - uStep * float(i)) * uW[i];
  }
  o = c;
}`;

// The final picture: the person from the frame over the blurred frame or the image, by the soft mask.
const MIX = `#version 300 es
precision mediump float;
in vec2 v;
uniform sampler2D uFrame;
uniform sampler2D uBack;
uniform sampler2D uMask;
uniform vec2 uCover;
uniform bool uImage;
out vec4 o;
void main() {
  float m = smoothstep(0.35, 0.75, texture(uMask, v).r);
  vec2 bv = uImage ? 0.5 + (v - 0.5) * uCover : v;
  o = vec4(mix(texture(uBack, bv).rgb, texture(uFrame, v).rgb, m), 1.0);
}`;

type Target = { tex: WebGLTexture; fb: WebGLFramebuffer; w: number; h: number };

export class Compositor {
  private gl: WebGL2RenderingContext;
  private progs: Record<'mask' | 'blur' | 'mix', WebGLProgram>;
  private frameTex: WebGLTexture;
  private maskTex: WebGLTexture;
  private imageTex: WebGLTexture;
  private imageSize: [number, number] | null = null;
  private masks: [Target, Target];
  private small: [Target, Target];
  private hasMask = false;
  private readonly weights = gaussian(8, 4);

  constructor(private canvas: OffscreenCanvas, private w: number, private h: number) {
    const gl = canvas.getContext('webgl2', { alpha: false, antialias: false, premultipliedAlpha: false });
    if (!gl) throw new Error('webgl2');
    this.gl = gl;
    canvas.width = w;
    canvas.height = h;
    this.progs = { mask: this.program(MASK), blur: this.program(BLUR), mix: this.program(MIX) };
    const buf = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, buf);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW); // one triangle covers the screen
    gl.enableVertexAttribArray(0);
    gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);
    this.frameTex = this.texture();
    this.maskTex = this.texture();
    this.imageTex = this.texture();
    this.masks = [this.target(256, 144), this.target(256, 144)];
    this.small = [this.target(w >> 2, h >> 2), this.target(w >> 2, h >> 2)];
  }

  private program(frag: string): WebGLProgram {
    const gl = this.gl;
    const p = gl.createProgram()!;
    for (const [type, src] of [[gl.VERTEX_SHADER, VERT], [gl.FRAGMENT_SHADER, frag]] as const) {
      const s = gl.createShader(type)!;
      gl.shaderSource(s, src);
      gl.compileShader(s);
      if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s) ?? 'shader');
      gl.attachShader(p, s);
    }
    gl.bindAttribLocation(p, 0, 'a');
    gl.linkProgram(p);
    if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(p) ?? 'program');
    return p;
  }

  private texture(): WebGLTexture {
    const gl = this.gl;
    const t = gl.createTexture()!;
    gl.bindTexture(gl.TEXTURE_2D, t);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    return t;
  }

  private target(w: number, h: number): Target {
    const gl = this.gl;
    const tex = this.texture();
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, null);
    const fb = gl.createFramebuffer()!;
    gl.bindFramebuffer(gl.FRAMEBUFFER, fb);
    gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, tex, 0);
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    return { tex, fb, w, h };
  }

  private run(prog: WebGLProgram, to: Target | null, flip: boolean, setup: (u: (n: string) => WebGLUniformLocation | null) => void): void {
    const gl = this.gl;
    gl.useProgram(prog);
    gl.bindFramebuffer(gl.FRAMEBUFFER, to ? to.fb : null);
    gl.viewport(0, 0, to ? to.w : this.w, to ? to.h : this.h);
    gl.uniform1i(gl.getUniformLocation(prog, 'uFlip'), flip ? 1 : 0);
    setup((n) => gl.getUniformLocation(prog, n));
    gl.drawArrays(gl.TRIANGLES, 0, 3);
  }

  private bind(unit: number, tex: WebGLTexture): number {
    this.gl.activeTexture(this.gl.TEXTURE0 + unit);
    this.gl.bindTexture(this.gl.TEXTURE_2D, tex);
    return unit;
  }

  setMask(mask: Uint8Array, mw: number, mh: number): void {
    const gl = this.gl;
    gl.bindTexture(gl.TEXTURE_2D, this.maskTex);
    gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.R8, mw, mh, 0, gl.RED, gl.UNSIGNED_BYTE, mask);
    const [prev, next] = this.masks;
    this.run(this.progs.mask, next, false, (u) => {
      gl.uniform1i(u('uNew'), this.bind(0, this.maskTex));
      gl.uniform1i(u('uPrev'), this.bind(1, prev.tex));
      gl.uniform1f(u('uKeep'), this.hasMask ? 0.4 : 0.0);
    });
    this.masks = [next, prev];
    this.hasMask = true;
  }

  setImage(img: ImageBitmap | null): void {
    if (!img) {
      this.imageSize = null;
      return;
    }
    const gl = this.gl;
    gl.bindTexture(gl.TEXTURE_2D, this.imageTex);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, img);
    this.imageSize = [img.width, img.height];
  }

  draw(frame: TexImageSource, mode: 'blur' | 'image'): void {
    const gl = this.gl;
    gl.bindTexture(gl.TEXTURE_2D, this.frameTex);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, frame);
    const mask = this.masks[0].tex;
    let back = this.imageTex;
    const image = mode === 'image' && this.imageSize !== null;
    if (!image) {
      // Blur: the frame reduced 4x, blurred across, then down, in the small targets.
      const [a, b] = this.small;
      this.run(this.progs.blur, a, false, (u) => {
        gl.uniform1i(u('uSrc'), this.bind(0, this.frameTex));
        gl.uniform2f(u('uStep'), 1 / a.w, 0);
        gl.uniform1fv(u('uW'), this.weights);
      });
      this.run(this.progs.blur, b, false, (u) => {
        gl.uniform1i(u('uSrc'), this.bind(0, a.tex));
        gl.uniform2f(u('uStep'), 0, 1 / b.h);
        gl.uniform1fv(u('uW'), this.weights);
      });
      back = b.tex;
    }
    const c = image && this.imageSize ? cover(this.imageSize[0], this.imageSize[1], this.w, this.h) : { sx: 1, sy: 1 };
    this.run(this.progs.mix, null, true, (u) => {
      gl.uniform1i(u('uFrame'), this.bind(0, this.frameTex));
      gl.uniform1i(u('uBack'), this.bind(1, back));
      gl.uniform1i(u('uMask'), this.bind(2, mask));
      gl.uniform2f(u('uCover'), c.sx, c.sy);
      gl.uniform1i(u('uImage'), image ? 1 : 0);
    });
  }

  dispose(): void {
    this.gl.getExtension('WEBGL_lose_context')?.loseContext();
  }
}
