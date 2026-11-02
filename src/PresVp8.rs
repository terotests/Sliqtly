// SPDX-License-Identifier: AGPL-3.0-or-later
//
// PresVp8.rs — Sliqtly's own video encoder: VP8 (RFC 6386) in WebM.
//
// Written in the Rust that Ranger reads: rustc builds it as it is, and rgrc
// writes the same program as JavaScript (scripts/build.mjs makes
// dist/vp8enc.js of it), Go, C++ and the rest. Both builds write the same
// bytes; scripts/check-vp8.mjs checks that, and that FFmpeg decodes every
// frame to exactly the picture the encoder kept.
//
// What it codes:
// - key frames of 16x16 intra macroblocks (DC, V, H or TM prediction, the
//   one nearest the source), with the second-order DC transform;
// - later frames from the last picture moved by a vector per macroblock
//   (whole pixels, found from the picture's overall motion and the
//   neighbours' vectors; chroma on half pixels through VP8's six-tap
//   filter), or intra, whichever costs less in error plus bits;
// - a key frame again when most of the picture is new (a cut, not a fade
//   or a move);
// - the coefficient probabilities fitted to each frame's tokens;
// - no loop filter, no B_PRED, no sub-pixel luma vectors (later steps).
// A frame equal to the one before is not coded at all: the container holds
// the earlier frame until the next one.
//
// Integers are i64 kept below 2^32, so that every target computes the same:
// JavaScript's bit operators are 32-bit, so the bool coder uses * and /.
// The probability and quantizer tables at the end are the format's own
// (RFC 6386; as in libvpx, BSD-3-Clause, the WebM Project authors).

pub struct BoolWriter {
    pub out: Vec<u8>,
    range: i64,
    bottom: i64,
    bit_count: i64,
}

impl BoolWriter {
    pub fn new() -> BoolWriter {
        BoolWriter { out: Vec::new(), range: 255, bottom: 0, bit_count: 24 }
    }

    fn add_one(&mut self) {
        let mut i = self.out.len() as i64 - 1;
        while i >= 0 && self.out[i as usize] == 255 {
            self.out[i as usize] = 0;
            i -= 1;
        }
        if i >= 0 {
            let v = self.out[i as usize] as i64;
            self.out[i as usize] = (v + 1) as u8;
        }
    }

    pub fn put(&mut self, bit: bool, prob: i64) {
        let split = 1 + (((self.range - 1) * prob) >> 8);
        if bit {
            self.bottom += split;
            self.range -= split;
        } else {
            self.range = split;
        }
        if self.bottom >= 4294967296 {
            self.add_one();
            self.bottom -= 4294967296;
        }
        while self.range < 128 {
            self.range = self.range * 2;
            if self.bottom >= 2147483648 {
                self.add_one();
                self.bottom -= 2147483648;
            }
            self.bottom = self.bottom * 2;
            self.bit_count -= 1;
            if self.bit_count == 0 {
                self.out.push((self.bottom / 16777216) as u8);
                self.bottom = self.bottom % 16777216;
                self.bit_count = 8;
            }
        }
    }

    pub fn literal(&mut self, value: i64, bits: i64) {
        let mut n = bits - 1;
        while n >= 0 {
            self.put(((value >> n) & 1) == 1, 128);
            n -= 1;
        }
    }

    pub fn flush(&mut self) {
        for _i in 0..32 {
            self.put(false, 128);
        }
    }
}

fn clamp255(v: i64) -> i64 {
    if v < 0 {
        0
    } else if v > 255 {
        255
    } else {
        v
    }
}

fn token_of(a: i64) -> i64 {
    if a <= 4 {
        a
    } else if a <= 6 {
        5
    } else if a <= 10 {
        6
    } else if a <= 18 {
        7
    } else if a <= 34 {
        8
    } else if a <= 66 {
        9
    } else {
        10
    }
}

// The forward transforms of libvpx (vp8/encoder/dct.c), which the format
// does not fix; the inverse ones below it does, bit for bit.
fn fdct4(inp: &Vec<i64>, out: &mut Vec<i64>) {
    let mut tmp: Vec<i64> = vec![0; 16];
    for i in 0..4 {
        let r = i * 4;
        let a1 = (inp[r] + inp[r + 3]) * 8;
        let b1 = (inp[r + 1] + inp[r + 2]) * 8;
        let c1 = (inp[r + 1] - inp[r + 2]) * 8;
        let d1 = (inp[r] - inp[r + 3]) * 8;
        tmp[r] = a1 + b1;
        tmp[r + 2] = a1 - b1;
        tmp[r + 1] = (c1 * 2217 + d1 * 5352 + 14500) >> 12;
        tmp[r + 3] = (d1 * 2217 - c1 * 5352 + 7500) >> 12;
    }
    for i in 0..4 {
        let a1 = tmp[i] + tmp[i + 12];
        let b1 = tmp[i + 4] + tmp[i + 8];
        let c1 = tmp[i + 4] - tmp[i + 8];
        let d1 = tmp[i] - tmp[i + 12];
        out[i] = (a1 + b1 + 7) >> 4;
        out[i + 8] = (a1 - b1 + 7) >> 4;
        let mut e = (c1 * 2217 + d1 * 5352 + 12000) >> 16;
        if d1 != 0 {
            e += 1;
        }
        out[i + 4] = e;
        out[i + 12] = (d1 * 2217 - c1 * 5352 + 51000) >> 16;
    }
}

fn fwalsh4(inp: &Vec<i64>, out: &mut Vec<i64>) {
    let mut tmp: Vec<i64> = vec![0; 16];
    for i in 0..4 {
        let r = i * 4;
        let a1 = (inp[r] + inp[r + 2]) * 4;
        let d1 = (inp[r + 1] + inp[r + 3]) * 4;
        let c1 = (inp[r + 1] - inp[r + 3]) * 4;
        let b1 = (inp[r] - inp[r + 2]) * 4;
        let mut z = a1 + d1;
        if a1 != 0 {
            z += 1;
        }
        tmp[r] = z;
        tmp[r + 1] = b1 + c1;
        tmp[r + 2] = b1 - c1;
        tmp[r + 3] = a1 - d1;
    }
    for i in 0..4 {
        let a1 = tmp[i] + tmp[i + 8];
        let d1 = tmp[i + 4] + tmp[i + 12];
        let c1 = tmp[i + 4] - tmp[i + 12];
        let b1 = tmp[i] - tmp[i + 8];
        let mut a2 = a1 + d1;
        let mut b2 = b1 + c1;
        let mut c2 = b1 - c1;
        let mut d2 = a1 - d1;
        if a2 < 0 {
            a2 += 1;
        }
        if b2 < 0 {
            b2 += 1;
        }
        if c2 < 0 {
            c2 += 1;
        }
        if d2 < 0 {
            d2 += 1;
        }
        out[i] = (a2 + 3) >> 3;
        out[i + 4] = (b2 + 3) >> 3;
        out[i + 8] = (c2 + 3) >> 3;
        out[i + 12] = (d2 + 3) >> 3;
    }
}

fn iwalsh4(inp: &Vec<i64>, out: &mut Vec<i64>) {
    let mut tmp: Vec<i64> = vec![0; 16];
    for i in 0..4 {
        let a1 = inp[i] + inp[i + 12];
        let b1 = inp[i + 4] + inp[i + 8];
        let c1 = inp[i + 4] - inp[i + 8];
        let d1 = inp[i] - inp[i + 12];
        tmp[i] = a1 + b1;
        tmp[i + 4] = c1 + d1;
        tmp[i + 8] = a1 - b1;
        tmp[i + 12] = d1 - c1;
    }
    for i in 0..4 {
        let r = i * 4;
        let a1 = tmp[r] + tmp[r + 3];
        let b1 = tmp[r + 1] + tmp[r + 2];
        let c1 = tmp[r + 1] - tmp[r + 2];
        let d1 = tmp[r] - tmp[r + 3];
        out[r] = (a1 + b1 + 3) >> 3;
        out[r + 1] = (c1 + d1 + 3) >> 3;
        out[r + 2] = (a1 - b1 + 3) >> 3;
        out[r + 3] = (d1 - c1 + 3) >> 3;
    }
}

// The decoder's inverse DCT (RFC 6386 14.3): the residual of one 4x4 block.
fn idct4(inp: &Vec<i64>, out: &mut Vec<i64>) {
    let mut tmp: Vec<i64> = vec![0; 16];
    for i in 0..4 {
        let a1 = inp[i] + inp[i + 8];
        let b1 = inp[i] - inp[i + 8];
        let mut t1 = (inp[i + 4] * 35468) >> 16;
        let mut t2 = inp[i + 12] + ((inp[i + 12] * 20091) >> 16);
        let c1 = t1 - t2;
        t1 = inp[i + 4] + ((inp[i + 4] * 20091) >> 16);
        t2 = (inp[i + 12] * 35468) >> 16;
        let d1 = t1 + t2;
        tmp[i] = a1 + d1;
        tmp[i + 12] = a1 - d1;
        tmp[i + 4] = b1 + c1;
        tmp[i + 8] = b1 - c1;
    }
    for i in 0..4 {
        let r = i * 4;
        let a1 = tmp[r] + tmp[r + 2];
        let b1 = tmp[r] - tmp[r + 2];
        let mut t1 = (tmp[r + 1] * 35468) >> 16;
        let mut t2 = tmp[r + 3] + ((tmp[r + 3] * 20091) >> 16);
        let c1 = t1 - t2;
        t1 = tmp[r + 1] + ((tmp[r + 1] * 20091) >> 16);
        t2 = (tmp[r + 3] * 35468) >> 16;
        let d1 = t1 + t2;
        out[r] = (a1 + d1 + 4) >> 3;
        out[r + 3] = (a1 - d1 + 4) >> 3;
        out[r + 1] = (b1 + c1 + 4) >> 3;
        out[r + 2] = (b1 - c1 + 4) >> 3;
    }
}

pub struct Vp8Encoder {
    pub width: i64,
    pub height: i64,
    /// quantizer index 0..127: lower is sharper and larger
    pub qindex: i64,
    /// at most this many frames between key frames; 0 = only when the picture changes
    pub key_interval: i64,
    /// the last frame was a key frame
    pub last_key: bool,
    mbw: i64,
    mbh: i64,
    pw: i64,
    cw: i64,
    frames: i64,
    since_key: i64,
    // the source and the decoder's picture, padded to whole macroblocks
    sy: Vec<u8>,
    su: Vec<u8>,
    sv: Vec<u8>,
    ry: Vec<u8>,
    ru: Vec<u8>,
    rv: Vec<u8>,
    // the last frame's picture, which inter macroblocks predict from
    ly: Vec<u8>,
    lu: Vec<u8>,
    lv: Vec<u8>,
    // tables
    coef_probs: Vec<i64>,
    default_probs: Vec<i64>,
    update_probs: Vec<i64>,
    cost: Vec<i64>,
    zigzag: Vec<i64>,
    bands: Vec<i64>,
    path_off: Vec<i64>,
    path_len: Vec<i64>,
    path: Vec<i64>,
    cat_base: Vec<i64>,
    cat_len: Vec<i64>,
    cat_off: Vec<i64>,
    cat_probs: Vec<i64>,
    mode_ctx: Vec<i64>,
    mv_update: Vec<i64>,
    mv_probs: Vec<i64>,
    // quantizer steps: y1 dc/ac, y2 dc/ac, uv dc/ac
    steps: Vec<i64>,
    // per frame
    above_ctx: Vec<i64>,
    left_ctx: Vec<i64>,
    tokens: Vec<i64>,
    mb_skip: Vec<i64>,
    mb_inter: Vec<i64>,
    mb_ymode: Vec<i64>,
    mb_uvmode: Vec<i64>,
    // inter macroblocks: motion vector (1/8 pel, as the decoder keeps it),
    // mode (0 ZEROMV, 1 NEARESTMV, 2 NEARMV, 3 NEWMV), the three counts its
    // mode is coded with, and NEWMV's difference from the best vector
    mb_mvr: Vec<i64>,
    mb_mvc: Vec<i64>,
    mb_mode: Vec<i64>,
    mb_cnt: Vec<i64>,
    mb_dr: Vec<i64>,
    mb_dc: Vec<i64>,
    // the near vectors of the macroblock being coded: nearest, near, best
    // (row, column each, 1/8 pel), counts 0..2, then the vector chosen
    // (row, column, whole pixels)
    near: Vec<i64>,
    // the whole picture's motion this frame (pixels), a search start
    gmv_r: i64,
    gmv_c: i64,
}

impl Vp8Encoder {
    pub fn new(width: i64, height: i64, qindex: i64) -> Vp8Encoder {
        let mbw = (width + 15) / 16;
        let mbh = (height + 15) / 16;
        let pw = mbw * 16;
        let ph = mbh * 16;
        let cw = mbw * 8;
        let ch = mbh * 8;
        let defaults = default_coef_probs();
        let mut e = Vp8Encoder {
            width: width,
            height: height,
            qindex: 0,
            key_interval: 0,
            last_key: false,
            mbw: mbw,
            mbh: mbh,
            pw: pw,
            cw: cw,
            frames: 0,
            since_key: 0,
            sy: vec![0; (pw * ph) as usize],
            su: vec![0; (cw * ch) as usize],
            sv: vec![0; (cw * ch) as usize],
            ry: vec![0; (pw * ph) as usize],
            ru: vec![0; (cw * ch) as usize],
            rv: vec![0; (cw * ch) as usize],
            ly: Vec::new(),
            lu: Vec::new(),
            lv: Vec::new(),
            coef_probs: defaults.clone(),
            default_probs: defaults,
            update_probs: coef_update_probs(),
            cost: prob_cost(),
            zigzag: vec![0, 1, 4, 8, 5, 2, 3, 6, 9, 12, 13, 10, 7, 11, 14, 15],
            bands: vec![0, 1, 2, 3, 6, 4, 5, 6, 6, 6, 6, 6, 6, 6, 6, 7, 0],
            path_off: Vec::new(),
            path_len: Vec::new(),
            path: Vec::new(),
            cat_base: vec![5, 7, 11, 19, 35, 67],
            cat_len: vec![1, 2, 3, 4, 5, 11],
            cat_off: vec![0, 1, 3, 6, 10, 15],
            cat_probs: vec![
                159, 165, 145, 173, 148, 140, 176, 155, 140, 135, 180, 157, 141, 134, 130, 254, 254, 243, 230, 196,
                177, 153, 140, 133, 130, 129,
            ],
            mode_ctx: mode_contexts(),
            mv_update: mv_update_probs(),
            mv_probs: vec![
                162, 128, 225, 146, 172, 147, 214, 39, 156, 128, 129, 132, 75, 145, 178, 206, 239, 254, 254, //
                164, 128, 204, 170, 119, 235, 140, 230, 228, 128, 130, 130, 74, 148, 180, 203, 236, 254, 254,
            ],
            steps: vec![0; 6],
            above_ctx: vec![0; (mbw * 9) as usize],
            left_ctx: vec![0; 9],
            tokens: Vec::new(),
            mb_skip: vec![0; (mbw * mbh) as usize],
            mb_inter: vec![0; (mbw * mbh) as usize],
            mb_ymode: vec![0; (mbw * mbh) as usize],
            mb_uvmode: vec![0; (mbw * mbh) as usize],
            mb_mvr: vec![0; (mbw * mbh) as usize],
            mb_mvc: vec![0; (mbw * mbh) as usize],
            mb_mode: vec![0; (mbw * mbh) as usize],
            mb_cnt: vec![0; (mbw * mbh * 3) as usize],
            mb_dr: vec![0; (mbw * mbh) as usize],
            mb_dc: vec![0; (mbw * mbh) as usize],
            near: vec![0; 11],
            gmv_r: 0,
            gmv_c: 0,
        };
        e.fix_paths();
        e.set_quality(qindex);
        e
    }

    // The token tree (RFC 6386 13.2) as paths of (node * 2 + bit) from the
    // root, a token's probability being the node's. Tokens: 0 ZERO, 1 ONE,
    // 2..4, 5..10 CAT1..CAT6, 11 EOB.
    fn fix_paths(&mut self) {
        // after the root's 1: THREE FOUR CAT1 CAT2 CAT3 CAT4 CAT5 CAT6
        let tails: Vec<i64> = vec![
            3, 5, 6, 9, 10, 3, 5, 6, 9, 11, 3, 5, 7, 12, 14, 3, 5, 7, 12, 15, //
            3, 5, 7, 13, 16, 18, 3, 5, 7, 13, 16, 19, 3, 5, 7, 13, 17, 20, 3, 5, 7, 13, 17, 21,
        ];
        let lens: Vec<i64> = vec![6, 6, 6, 6, 7, 7, 7, 7];
        // token order: EOB | ZERO | ONE | TWO, then THREE..CAT6 from tails
        let mut path: Vec<i64> = vec![0, 1, 2, 1, 3, 4, 1, 3, 5, 6, 8];
        let mut off: Vec<i64> = vec![1, 3, 6, 0, 0, 0, 0, 0, 0, 0, 0, 0];
        let mut len: Vec<i64> = vec![2, 3, 5, 0, 0, 0, 0, 0, 0, 0, 0, 1];
        let mut t = 0;
        let mut k: usize = 0;
        while t < 8 {
            off[(t + 3) as usize] = path.len() as i64;
            len[(t + 3) as usize] = lens[t as usize];
            path.push(1);
            for _j in 0..lens[t as usize] - 1 {
                path.push(tails[k]);
                k += 1;
            }
            t += 1;
        }
        self.path = path;
        self.path_off = off;
        self.path_len = len;
    }

    pub fn set_quality(&mut self, qindex: i64) {
        let q = if qindex < 0 {
            0
        } else if qindex > 127 {
            127
        } else {
            qindex
        };
        self.qindex = q;
        let dc = dc_qlookup();
        let ac = ac_qlookup();
        let i = q as usize;
        let mut y2ac = ac[i] * 155 / 100;
        if y2ac < 8 {
            y2ac = 8;
        }
        let mut uvdc = dc[i];
        if uvdc > 132 {
            uvdc = 132;
        }
        self.steps = vec![dc[i], ac[i], dc[i] * 2, y2ac, uvdc, ac[i]];
    }

    /// RGBA rows top down (4 bytes a pixel) into the padded Y, U and V planes:
    /// BT.601, limited range, as FFmpeg's yuv420p.
    pub fn load_rgba(&mut self, rgba: &Vec<u8>) {
        let w = self.width;
        let h = self.height;
        let pw = self.pw;
        let ph = self.mbh * 16;
        for y in 0..ph {
            let sy = if y < h { y } else { h - 1 };
            for x in 0..pw {
                let sx = if x < w { x } else { w - 1 };
                let p = ((sy * w + sx) * 4) as usize;
                let r = rgba[p] as i64;
                let g = rgba[p + 1] as i64;
                let b = rgba[p + 2] as i64;
                self.sy[(y * pw + x) as usize] = (((66 * r + 129 * g + 25 * b + 128) >> 8) + 16) as u8;
            }
        }
        let cw = self.cw;
        let ch = self.mbh * 8;
        for y in 0..ch {
            for x in 0..cw {
                let mut r = 0;
                let mut g = 0;
                let mut b = 0;
                for d in 0..4 {
                    let mut px = x * 2 + (d & 1);
                    let mut py = y * 2 + (d >> 1);
                    if px >= w {
                        px = w - 1;
                    }
                    if py >= h {
                        py = h - 1;
                    }
                    let p = ((py * w + px) * 4) as usize;
                    r += rgba[p] as i64;
                    g += rgba[p + 1] as i64;
                    b += rgba[p + 2] as i64;
                }
                r = (r + 2) >> 2;
                g = (g + 2) >> 2;
                b = (b + 2) >> 2;
                let i = (y * cw + x) as usize;
                self.su[i] = (((-38 * r - 74 * g + 112 * b + 128) >> 8) + 128) as u8;
                self.sv[i] = (((112 * r - 94 * g - 18 * b + 128) >> 8) + 128) as u8;
            }
        }
    }

    // Intra prediction of a size x size block at (x0, y0) of a plane, from
    // the picture decoded so far; outside it, 127 above and 129 to the left.
    fn predict(plane: &Vec<u8>, stride: i64, x0: i64, y0: i64, size: i64, mode: i64, out: &mut Vec<i64>) {
        let mut above: Vec<i64> = vec![127; size as usize];
        let mut left: Vec<i64> = vec![129; size as usize];
        if y0 > 0 {
            for i in 0..size {
                above[i as usize] = plane[((y0 - 1) * stride + x0 + i) as usize] as i64;
            }
        }
        if x0 > 0 {
            for j in 0..size {
                left[j as usize] = plane[((y0 + j) * stride + x0 - 1) as usize] as i64;
            }
        }
        let corner = if y0 == 0 {
            127
        } else if x0 == 0 {
            129
        } else {
            plane[((y0 - 1) * stride + x0 - 1) as usize] as i64
        };
        if mode == 0 {
            let shift = if size == 16 { 3 } else { 2 };
            let mut sum = 0;
            let mut count = 0;
            if y0 > 0 {
                for i in 0..size {
                    sum += above[i as usize];
                }
                count += 1;
            }
            if x0 > 0 {
                for j in 0..size {
                    sum += left[j as usize];
                }
                count += 1;
            }
            let v = if count == 0 {
                128
            } else {
                let s = shift + count;
                (sum + (1 << (s - 1))) >> s
            };
            for k in 0..size * size {
                out[k as usize] = v;
            }
        } else {
            for j in 0..size {
                for i in 0..size {
                    let v = if mode == 1 {
                        above[i as usize]
                    } else if mode == 2 {
                        left[j as usize]
                    } else {
                        clamp255(left[j as usize] + above[i as usize] - corner)
                    };
                    out[(j * size + i) as usize] = v;
                }
            }
        }
    }

    fn sad(plane: &Vec<u8>, stride: i64, x0: i64, y0: i64, size: i64, pred: &Vec<i64>) -> i64 {
        let mut s = 0;
        for j in 0..size {
            let row = (y0 + j) * stride + x0;
            for i in 0..size {
                let d = plane[(row + i) as usize] as i64 - pred[(j * size + i) as usize];
                s += if d < 0 { -d } else { d };
            }
        }
        s
    }

    fn copy_block(plane: &Vec<u8>, stride: i64, x0: i64, y0: i64, size: i64, out: &mut Vec<i64>) {
        for j in 0..size {
            let row = (y0 + j) * stride + x0;
            for i in 0..size {
                out[(j * size + i) as usize] = plane[(row + i) as usize] as i64;
            }
        }
    }

    fn quantize(c: i64, step: i64, dc: bool) -> i64 {
        let a = if c < 0 { -c } else { c };
        let round = if dc { step / 2 } else { (step * 3) / 8 };
        let mut q = (a + round) / step;
        if q > 2048 {
            q = 2048;
        }
        if c < 0 {
            -q
        } else {
            q
        }
    }

    // One 4x4 block: residual of source minus prediction, transformed and
    // quantized into q[base..base+16] (raster order); the DC is left in
    // dcs[k] unquantized when the block's DC goes to Y2.
    fn transform_block(
        src: &Vec<u8>,
        stride: i64,
        x0: i64,
        y0: i64,
        pred: &Vec<i64>,
        psize: i64,
        px: i64,
        py: i64,
        q: &mut Vec<i64>,
        base: i64,
        dc_step: i64,
        ac_step: i64,
        y2: bool,
    ) -> i64 {
        let mut res: Vec<i64> = vec![0; 16];
        for j in 0..4 {
            for i in 0..4 {
                let s = src[((y0 + j) * stride + x0 + i) as usize] as i64;
                res[(j * 4 + i) as usize] = s - pred[((py + j) * psize + px + i) as usize];
            }
        }
        let mut coef: Vec<i64> = vec![0; 16];
        fdct4(&res, &mut coef);
        for k in 1..16 {
            q[(base + k) as usize] = Vp8Encoder::quantize(coef[k as usize], ac_step, false);
        }
        if y2 {
            q[base as usize] = 0;
            coef[0]
        } else {
            q[base as usize] = Vp8Encoder::quantize(coef[0], dc_step, true);
            0
        }
    }

    // The decoder's picture of one 4x4 block: prediction plus the inverse
    // transform of the dequantized coefficients, into out[base..] (rows of
    // `stride`) at (x0, y0).
    fn reconstruct_block(
        out: &mut Vec<i64>,
        stride: i64,
        base: i64,
        x0: i64,
        y0: i64,
        pred: &Vec<i64>,
        psize: i64,
        px: i64,
        py: i64,
        dq: &Vec<i64>,
    ) {
        let mut res: Vec<i64> = vec![0; 16];
        idct4(dq, &mut res);
        for j in 0..4 {
            for i in 0..4 {
                let v = pred[((py + j) * psize + px + i) as usize] + res[(j * 4 + i) as usize];
                out[(base + (y0 + j) * stride + x0 + i) as usize] = clamp255(v);
            }
        }
    }

    fn put_pred(plane: &mut Vec<u8>, stride: i64, x0: i64, y0: i64, size: i64, pred: &Vec<i64>) {
        for j in 0..size {
            for i in 0..size {
                plane[((y0 + j) * stride + x0 + i) as usize] = pred[(j * size + i) as usize] as u8;
            }
        }
    }

    // Tokens of one block into the frame's list: (context index, token and
    // whether the EOB branch is skipped, magnitude, sign). Answers whether the
    // block has coefficients, the context of the blocks right and below.
    fn tokenize(&mut self, q: &Vec<i64>, base: i64, typ: i64, ctx: i64) -> i64 {
        let first = if typ == 0 { 1 } else { 0 };
        let mut eob = 0;
        let mut c = 15;
        while c >= first {
            if q[(base + self.zigzag[c as usize]) as usize] != 0 {
                eob = c + 1;
                break;
            }
            c -= 1;
        }
        let mut c = first;
        let mut pt = ctx;
        let mut skip = 0;
        while c < eob {
            let v = q[(base + self.zigzag[c as usize]) as usize];
            let a = if v < 0 { -v } else { v };
            let tok = token_of(a);
            self.tokens.push(((typ * 8 + self.bands[c as usize]) * 3 + pt) * 11);
            self.tokens.push(tok + skip * 16);
            self.tokens.push(a);
            self.tokens.push(if v < 0 { 1 } else { 0 });
            pt = if tok == 0 {
                0
            } else if tok == 1 {
                1
            } else {
                2
            };
            skip = if tok == 0 { 1 } else { 0 };
            c += 1;
        }
        if c < 16 {
            self.tokens.push(((typ * 8 + self.bands[c as usize]) * 3 + pt) * 11);
            self.tokens.push(11);
            self.tokens.push(0);
            self.tokens.push(0);
        }
        if eob > first {
            1
        } else {
            0
        }
    }

    fn encode_mb(&mut self, mx: i64, my: i64, key: bool, copy_only: bool) {
        let pw = self.pw;
        let cw = self.cw;
        let x0 = mx * 16;
        let y0 = my * 16;
        let cx = mx * 8;
        let cy = my * 8;
        let idx = (my * self.mbw + mx) as usize;
        // the inter candidate: the last picture where the vector points
        let mut pred: Vec<i64> = vec![0; 256];
        let mut pu: Vec<i64> = vec![0; 64];
        let mut pv: Vec<i64> = vec![0; 64];
        let mut dr = 0;
        let mut dc = 0;
        let mut inter_sad = 0;
        if !key {
            self.find_near(mx, my);
            if !copy_only {
                self.motion_search(mx, my);
                dr = self.near[9];
                dc = self.near[10];
            }
            Vp8Encoder::copy_block(&self.ly, pw, x0 + dc, y0 + dr, 16, &mut pred);
            Vp8Encoder::predict_chroma(&self.lu, cw, cx, cy, dr, dc, &mut pu);
            Vp8Encoder::predict_chroma(&self.lv, cw, cx, cy, dr, dc, &mut pv);
            inter_sad = Vp8Encoder::sad(&self.sy, pw, x0, y0, 16, &pred);
        }
        let mut q: Vec<i64> = vec![0; 400];
        let mut rec: Vec<i64> = vec![0; 384];
        if copy_only || (!key && inter_sad == 0 && self.chroma_same(cx, cy, &pu, &pv)) {
            // nothing to code: the vector's picture is the source
            self.finish_mb(mx, my, idx, true, dr, dc, 0, 0, &q, &pred, &pu, &pv, &rec, false);
            return;
        }
        // the intra candidate: the 16x16 and chroma modes nearest the source
        let mut ipred: Vec<i64> = vec![0; 256];
        let mut ipu: Vec<i64> = vec![0; 64];
        let mut ipv: Vec<i64> = vec![0; 64];
        let mut cand: Vec<i64> = vec![0; 256];
        let mut cu: Vec<i64> = vec![0; 64];
        let mut ymode = -1;
        let mut best_sad = 0;
        for m in 0..4 {
            Vp8Encoder::predict(&self.ry, pw, x0, y0, 16, m, &mut cand);
            let s = Vp8Encoder::sad(&self.sy, pw, x0, y0, 16, &cand);
            if ymode < 0 || s < best_sad {
                ymode = m;
                best_sad = s;
            }
        }
        let mut uvmode = -1;
        let mut best_uv = 0;
        for m in 0..4 {
            Vp8Encoder::predict(&self.ru, cw, cx, cy, 8, m, &mut cu);
            Vp8Encoder::predict(&self.rv, cw, cx, cy, 8, m, &mut cand);
            let s = Vp8Encoder::sad(&self.su, cw, cx, cy, 8, &cu) + Vp8Encoder::sad(&self.sv, cw, cx, cy, 8, &cand);
            if uvmode < 0 || s < best_uv {
                uvmode = m;
                best_uv = s;
            }
        }
        Vp8Encoder::predict(&self.ry, pw, x0, y0, 16, ymode, &mut ipred);
        Vp8Encoder::predict(&self.ru, cw, cx, cy, 8, uvmode, &mut ipu);
        Vp8Encoder::predict(&self.rv, cw, cx, cy, 8, uvmode, &mut ipv);
        if key {
            self.code_mb(x0, y0, cx, cy, &ipred, &ipu, &ipv, &mut q, &mut rec);
            self.finish_mb(mx, my, idx, false, 0, 0, ymode, uvmode, &q, &ipred, &ipu, &ipv, &rec, true);
            return;
        }
        // both coded; the one of least distortion plus rate wins. An inter
        // block whose difference quantizes to nothing would keep the old
        // picture's leftovers for good; the distortion sees them.
        let step = self.steps[1];
        let lambda = step * step / 8 + 1;
        let mut iq: Vec<i64> = vec![0; 400];
        let mut irec: Vec<i64> = vec![0; 384];
        let d_inter = self.code_mb(x0, y0, cx, cy, &pred, &pu, &pv, &mut q, &mut rec);
        let d_intra = self.code_mb(x0, y0, cx, cy, &ipred, &ipu, &ipv, &mut iq, &mut irec);
        let j_inter = d_inter * 256 + lambda * (Vp8Encoder::coef_bits(&q) + self.mode_cost(dr * 8, dc * 8));
        let j_intra = d_intra * 256 + lambda * (Vp8Encoder::coef_bits(&iq) + 8 * 256);
        if j_intra < j_inter {
            self.finish_mb(mx, my, idx, false, 0, 0, ymode, uvmode, &iq, &ipred, &ipu, &ipv, &irec, true);
        } else {
            self.finish_mb(mx, my, idx, true, dr, dc, 0, 0, &q, &pred, &pu, &pv, &rec, true);
        }
    }

    fn chroma_same(&self, cx: i64, cy: i64, pu: &Vec<i64>, pv: &Vec<i64>) -> bool {
        Vp8Encoder::sad(&self.su, self.cw, cx, cy, 8, pu) == 0 && Vp8Encoder::sad(&self.sv, self.cw, cx, cy, 8, pv) == 0
    }

    // About the bits (1/256) the coefficients take: each nonzero one by
    // its size, each block that has any its end.
    fn coef_bits(q: &Vec<i64>) -> i64 {
        let mut bits = 0;
        for b in 0..25 {
            let mut any = false;
            for k in 0..16 {
                let v = q[(b * 16 + k) as usize];
                if v != 0 {
                    any = true;
                    let mut a = if v < 0 { -v } else { v };
                    let mut len = 0;
                    while a > 0 {
                        len += 1;
                        a = a / 2;
                    }
                    bits += 3 + 2 * len;
                }
            }
            if any {
                bits += 2;
            }
        }
        bits * 256
    }

    // The macroblock coded with one prediction: its coefficients into q
    // (16 Y blocks, 4 U, 4 V, then Y2, 16 each), the decoder's picture of it
    // into rec (Y 16x16, U 8x8, V 8x8). Answers the squared error.
    fn code_mb(
        &self,
        x0: i64,
        y0: i64,
        cx: i64,
        cy: i64,
        pred: &Vec<i64>,
        pu: &Vec<i64>,
        pv: &Vec<i64>,
        q: &mut Vec<i64>,
        rec: &mut Vec<i64>,
    ) -> i64 {
        let pw = self.pw;
        let cw = self.cw;
        let mut dcs: Vec<i64> = vec![0; 16];
        for b in 0..16 {
            let bx = (b % 4) * 4;
            let by = (b / 4) * 4;
            dcs[b as usize] =
                Vp8Encoder::transform_block(&self.sy, pw, x0 + bx, y0 + by, pred, 16, bx, by, q, b * 16, 0, self.steps[1], true);
        }
        let mut wh: Vec<i64> = vec![0; 16];
        fwalsh4(&dcs, &mut wh);
        q[384] = Vp8Encoder::quantize(wh[0], self.steps[2], true);
        for k in 1..16 {
            q[(384 + k) as usize] = Vp8Encoder::quantize(wh[k as usize], self.steps[3], false);
        }
        for b in 0..4 {
            let bx = (b % 2) * 4;
            let by = (b / 2) * 4;
            Vp8Encoder::transform_block(&self.su, cw, cx + bx, cy + by, pu, 8, bx, by, q, 256 + b * 16, self.steps[4], self.steps[5], false);
            Vp8Encoder::transform_block(&self.sv, cw, cx + bx, cy + by, pv, 8, bx, by, q, 320 + b * 16, self.steps[4], self.steps[5], false);
        }
        // the decoder's picture
        let mut y2: Vec<i64> = vec![0; 16];
        y2[0] = q[384] * self.steps[2];
        for k in 1..16 {
            y2[k as usize] = q[(384 + k) as usize] * self.steps[3];
        }
        iwalsh4(&y2, &mut dcs);
        let mut dq: Vec<i64> = vec![0; 16];
        for b in 0..16 {
            let bx = (b % 4) * 4;
            let by = (b / 4) * 4;
            dq[0] = dcs[b as usize];
            for k in 1..16 {
                dq[k as usize] = q[(b * 16 + k) as usize] * self.steps[1];
            }
            Vp8Encoder::reconstruct_block(rec, 16, 0, bx, by, pred, 16, bx, by, &dq);
        }
        for b in 0..4 {
            let bx = (b % 2) * 4;
            let by = (b / 2) * 4;
            dq[0] = q[(256 + b * 16) as usize] * self.steps[4];
            for k in 1..16 {
                dq[k as usize] = q[(256 + b * 16 + k) as usize] * self.steps[5];
            }
            Vp8Encoder::reconstruct_block(rec, 8, 256, bx, by, pu, 8, bx, by, &dq);
            dq[0] = q[(320 + b * 16) as usize] * self.steps[4];
            for k in 1..16 {
                dq[k as usize] = q[(320 + b * 16 + k) as usize] * self.steps[5];
            }
            Vp8Encoder::reconstruct_block(rec, 8, 320, bx, by, pv, 8, bx, by, &dq);
        }
        let mut sse = 0;
        for j in 0..16 {
            for i in 0..16 {
                let d = self.sy[((y0 + j) * pw + x0 + i) as usize] as i64 - rec[(j * 16 + i) as usize];
                sse += d * d;
            }
        }
        for j in 0..8 {
            for i in 0..8 {
                let at = ((cy + j) * cw + cx + i) as usize;
                let du = self.su[at] as i64 - rec[(256 + j * 8 + i) as usize];
                let dv = self.sv[at] as i64 - rec[(320 + j * 8 + i) as usize];
                sse += du * du + dv * dv;
            }
        }
        sse
    }

    // The macroblock decided: its modes kept for the header, its tokens
    // (or a skip) with the neighbours' contexts, its picture into the frame.
    fn finish_mb(
        &mut self,
        mx: i64,
        my: i64,
        idx: usize,
        inter: bool,
        dr: i64,
        dc: i64,
        ymode: i64,
        uvmode: i64,
        q: &Vec<i64>,
        pred: &Vec<i64>,
        pu: &Vec<i64>,
        pv: &Vec<i64>,
        rec: &Vec<i64>,
        coded: bool,
    ) {
        self.mb_inter[idx] = if inter { 1 } else { 0 };
        self.mb_ymode[idx] = ymode;
        self.mb_uvmode[idx] = uvmode;
        self.set_inter_mode(idx, inter, dr, dc);
        let pw = self.pw;
        let cw = self.cw;
        let x0 = mx * 16;
        let y0 = my * 16;
        let cx = mx * 8;
        let cy = my * 8;
        let mut nonzero = false;
        if coded {
            for k in 0..400 {
                if q[k] != 0 {
                    nonzero = true;
                    break;
                }
            }
        }
        self.mb_skip[idx] = if nonzero { 0 } else { 1 };
        let a = (mx * 9) as usize;
        if !nonzero {
            for k in 0..9 {
                self.above_ctx[a + k] = 0;
                self.left_ctx[k] = 0;
            }
            Vp8Encoder::put_pred(&mut self.ry, pw, x0, y0, 16, pred);
            Vp8Encoder::put_pred(&mut self.ru, cw, cx, cy, 8, pu);
            Vp8Encoder::put_pred(&mut self.rv, cw, cx, cy, 8, pv);
            return;
        }
        // tokens: Y2, Y, U, V with their neighbours' contexts
        let c8 = self.above_ctx[a + 8] + self.left_ctx[8];
        let f = self.tokenize(q, 384, 1, c8);
        self.above_ctx[a + 8] = f;
        self.left_ctx[8] = f;
        for b in 0..16 {
            let col = (b % 4) as usize;
            let row = (b / 4) as usize;
            let ctx = self.above_ctx[a + col] + self.left_ctx[row];
            let f = self.tokenize(q, b * 16, 0, ctx);
            self.above_ctx[a + col] = f;
            self.left_ctx[row] = f;
        }
        for p in 0..2 {
            for b in 0..4 {
                let col = (4 + p * 2 + b % 2) as usize;
                let row = (4 + p * 2 + b / 2) as usize;
                let ctx = self.above_ctx[a + col] + self.left_ctx[row];
                let f = self.tokenize(q, 256 + p * 64 + b * 16, 2, ctx);
                self.above_ctx[a + col] = f;
                self.left_ctx[row] = f;
            }
        }
        for j in 0..16 {
            for i in 0..16 {
                self.ry[((y0 + j) * pw + x0 + i) as usize] = rec[(j * 16 + i) as usize] as u8;
            }
        }
        for j in 0..8 {
            for i in 0..8 {
                let at = ((cy + j) * cw + cx + i) as usize;
                self.ru[at] = rec[(256 + j * 8 + i) as usize] as u8;
                self.rv[at] = rec[(320 + j * 8 + i) as usize] as u8;
            }
        }
    }

    // The decoder's near vectors of a macroblock (vp8_find_near_mvs, as
    // decodemv.c reads them): its above, left and above-left inter
    // neighbours' vectors, ranked by weight, clamped to the picture.
    fn find_near(&mut self, mx: i64, my: i64) {
        let w = self.mbw;
        let idx = my * w + mx;
        let mut nm: Vec<i64> = vec![0; 8];
        let mut cnt: Vec<i64> = vec![0; 4];
        let mut ci = 0;
        let mut ni = 0;
        for k in 0..3 {
            let (ok, n, weight) = if k == 0 {
                (my > 0, idx - w, 2)
            } else if k == 1 {
                (mx > 0, idx - 1, 2)
            } else {
                (mx > 0 && my > 0, idx - w - 1, 1)
            };
            if !ok || self.mb_inter[n as usize] == 0 {
                continue;
            }
            let r = self.mb_mvr[n as usize];
            let c = self.mb_mvc[n as usize];
            if r != 0 || c != 0 {
                if k == 0 || r != nm[(ni * 2) as usize] || c != nm[(ni * 2 + 1) as usize] {
                    ni += 1;
                    nm[(ni * 2) as usize] = r;
                    nm[(ni * 2 + 1) as usize] = c;
                    ci += 1;
                }
                cnt[ci as usize] += weight;
            } else {
                cnt[0] += weight;
            }
        }
        if cnt[3] > 0 && nm[(ni * 2) as usize] == nm[2] && nm[(ni * 2 + 1) as usize] == nm[3] {
            cnt[1] += 1;
        }
        if cnt[2] > cnt[1] {
            let t = cnt[1];
            cnt[1] = cnt[2];
            cnt[2] = t;
            for j in 0..2 {
                let t2 = nm[2 + j];
                nm[2 + j] = nm[4 + j];
                nm[4 + j] = t2;
            }
        }
        let bi = if cnt[1] >= cnt[0] { 1 } else { 0 };
        let lo_c = -(mx * 128) - 128;
        let hi_c = (w - 1 - mx) * 128 + 128;
        let lo_r = -(my * 128) - 128;
        let hi_r = (self.mbh - 1 - my) * 128 + 128;
        let picks: Vec<i64> = vec![1, 2, bi];
        for j in 0..3 {
            let m = picks[j] as usize;
            let mut r = nm[m * 2];
            let mut c = nm[m * 2 + 1];
            if c < lo_c {
                c = lo_c;
            } else if c > hi_c {
                c = hi_c;
            }
            if r < lo_r {
                r = lo_r;
            } else if r > hi_r {
                r = hi_r;
            }
            self.near[j * 2] = r;
            self.near[j * 2 + 1] = c;
        }
        self.near[6] = cnt[0];
        self.near[7] = cnt[1];
        self.near[8] = cnt[2];
        self.near[9] = 0;
        self.near[10] = 0;
    }

    // Whether a whole-pixel vector (dr, dc) predicts the macroblock from
    // inside the coded picture: the luma block, and the chroma block with
    // its filter's reach when the chroma vector falls on a half pixel.
    fn mv_ok(&self, mx: i64, my: i64, dr: i64, dc: i64) -> bool {
        let x = mx * 16 + dc;
        let y = my * 16 + dr;
        if x < 0 || y < 0 || x + 16 > self.pw || y + 16 > self.mbh * 16 {
            return false;
        }
        // NEWMV codes its difference from the best vector: at most 1023 quarter pixels
        let br = dr * 8 - self.near[4];
        let bc = dc * 8 - self.near[5];
        if br > 2040 || br < -2040 || bc > 2040 || bc < -2040 {
            return false;
        }
        let cx = mx * 8 + ((dc * 4) >> 3);
        let cy = my * 8 + ((dr * 4) >> 3);
        let half = (dc % 2 != 0) || (dr % 2 != 0);
        let reach = if half { 2 } else { 0 };
        let reach2 = if half { 3 } else { 0 };
        cx - reach >= 0 && cy - reach >= 0 && cx + 8 + reach2 <= self.cw && cy + 8 + reach2 <= self.mbh * 8
    }

    fn mv_component_cost(&self, v: i64, base: usize) -> i64 {
        let p = &self.mv_probs;
        let x = if v < 0 { -v } else { v };
        let mut c = 0;
        if x < 8 {
            c += self.cost[p[base] as usize];
            let b2 = (x >> 2) & 1;
            let b1 = (x >> 1) & 1;
            let b0 = x & 1;
            c += self.bit_cost(b2, p[base + 2]);
            let n1 = if b2 == 0 { base + 3 } else { base + 6 };
            c += self.bit_cost(b1, p[n1]);
            let n2 = if b2 == 0 { base + 4 + b1 as usize } else { base + 7 + b1 as usize };
            c += self.bit_cost(b0, p[n2]);
            if x == 0 {
                return c;
            }
        } else {
            c += self.cost[(256 - p[base]) as usize];
            for i in 0..10 {
                if i != 3 || (x & 0xFFF0) != 0 {
                    c += self.bit_cost((x >> i) & 1, p[base + 9 + i as usize]);
                }
            }
        }
        c + 256
    }

    fn bit_cost(&self, bit: i64, prob: i64) -> i64 {
        if bit == 0 {
            self.cost[prob as usize]
        } else {
            self.cost[(256 - prob) as usize]
        }
    }

    // The bits (1/256) of coding vector (r, c) (1/8 pel) with the near vectors.
    fn mode_cost(&self, r: i64, c: i64) -> i64 {
        let p0 = self.mode_ctx[(self.near[6] * 4) as usize];
        let p1 = self.mode_ctx[(self.near[7] * 4 + 1) as usize];
        let p2 = self.mode_ctx[(self.near[8] * 4 + 2) as usize];
        let p3 = self.mode_ctx[3];
        if r == 0 && c == 0 {
            return self.bit_cost(0, p0);
        }
        let mut cost = self.bit_cost(1, p0);
        if r == self.near[0] && c == self.near[1] {
            return cost + self.bit_cost(0, p1);
        }
        cost += self.bit_cost(1, p1);
        if r == self.near[2] && c == self.near[3] {
            return cost + self.bit_cost(0, p2);
        }
        cost + self.bit_cost(1, p2)
            + self.bit_cost(0, p3)
            + self.mv_component_cost((r - self.near[4]) / 2, 0)
            + self.mv_component_cost((c - self.near[5]) / 2, 19)
    }

    fn sad_at(&self, mx: i64, my: i64, dr: i64, dc: i64) -> i64 {
        let pw = self.pw;
        let mut s = 0;
        for j in 0..16 {
            let a = (my * 16 + j) * pw + mx * 16;
            let b = (my * 16 + j + dr) * pw + mx * 16 + dc;
            for i in 0..16 {
                let d = self.sy[(a + i) as usize] as i64 - self.ly[(b + i) as usize] as i64;
                s += if d < 0 { -d } else { d };
            }
        }
        s
    }

    // The whole-pixel vector of least SAD plus rate: the zero, near and
    // neighbours' vectors and the picture's motion tried, the best refined
    // by steps of 8, 4, 2 and 1 pixels.
    fn motion_search(&mut self, mx: i64, my: i64) {
        let lambda = self.steps[1] / 2 + 1;
        let idx = my * self.mbw + mx;
        let mut cands: Vec<i64> = vec![
            0, 0, //
            self.near[0] / 8, self.near[1] / 8, //
            self.near[2] / 8, self.near[3] / 8, //
            self.gmv_r, self.gmv_c,
        ];
        if mx > 0 {
            cands.push(self.mb_mvr[(idx - 1) as usize] / 8);
            cands.push(self.mb_mvc[(idx - 1) as usize] / 8);
        }
        if my > 0 {
            cands.push(self.mb_mvr[(idx - self.mbw) as usize] / 8);
            cands.push(self.mb_mvc[(idx - self.mbw) as usize] / 8);
        }
        let mut br = 0;
        let mut bc = 0;
        let mut best = -1;
        let mut k = 0;
        while k < cands.len() {
            let r = cands[k];
            let c = cands[k + 1];
            k += 2;
            if !self.mv_ok(mx, my, r, c) {
                continue;
            }
            let j = self.sad_at(mx, my, r, c) * 256 + lambda * self.mode_cost(r * 8, c * 8);
            if best < 0 || j < best {
                best = j;
                br = r;
                bc = c;
            }
        }
        if best < 0 {
            return;
        }
        if best > 0 {
            let mut step = 8;
            while step > 0 {
                let mut moved = true;
                while moved {
                    moved = false;
                    for d in 0..4 {
                        let r = br + if d == 0 { step } else if d == 1 { -step } else { 0 };
                        let c = bc + if d == 2 { step } else if d == 3 { -step } else { 0 };
                        if !self.mv_ok(mx, my, r, c) {
                            continue;
                        }
                        let j = self.sad_at(mx, my, r, c) * 256 + lambda * self.mode_cost(r * 8, c * 8);
                        if j < best {
                            best = j;
                            br = r;
                            bc = c;
                            moved = true;
                        }
                    }
                }
                step = step / 2;
            }
        }
        self.near[9] = br;
        self.near[10] = bc;
    }

    // An inter macroblock's mode from its vector and the near vectors.
    fn set_inter_mode(&mut self, idx: usize, inter: bool, dr: i64, dc: i64) {
        let r = if inter { dr * 8 } else { 0 };
        let c = if inter { dc * 8 } else { 0 };
        self.mb_mvr[idx] = r;
        self.mb_mvc[idx] = c;
        if !inter {
            return;
        }
        self.mb_cnt[idx * 3] = self.near[6];
        self.mb_cnt[idx * 3 + 1] = self.near[7];
        self.mb_cnt[idx * 3 + 2] = self.near[8];
        self.mb_mode[idx] = if r == 0 && c == 0 {
            0
        } else if r == self.near[0] && c == self.near[1] {
            1
        } else if r == self.near[2] && c == self.near[3] {
            2
        } else {
            3
        };
        self.mb_dr[idx] = r - self.near[4];
        self.mb_dc[idx] = c - self.near[5];
    }

    // The 8x8 chroma prediction for a whole-pixel luma vector (dr, dc): the
    // chroma vector is half of it, so it may fall on a half pixel, which
    // VP8's six-tap filter (3, -16, 77, 77, -16, 3) gives, rows then columns.
    fn predict_chroma(plane: &Vec<u8>, stride: i64, cx: i64, cy: i64, dr: i64, dc: i64, out: &mut Vec<i64>) {
        let x = cx + ((dc * 4) >> 3);
        let y = cy + ((dr * 4) >> 3);
        let fx = dc % 2 != 0;
        let fy = dr % 2 != 0;
        if !fx && !fy {
            Vp8Encoder::copy_block(plane, stride, x, y, 8, out);
            return;
        }
        let taps: Vec<i64> = vec![3, -16, 77, 77, -16, 3];
        let mut tmp: Vec<i64> = vec![0; 13 * 8];
        for r in 0..13 {
            let row = (y - 2 + r) * stride;
            for c in 0..8 {
                let v = if fx {
                    let mut sum = 0;
                    for t in 0..6 {
                        sum += taps[t as usize] * plane[(row + x + c - 2 + t) as usize] as i64;
                    }
                    clamp255((sum + 64) >> 7)
                } else {
                    plane[(row + x + c) as usize] as i64
                };
                tmp[(r * 8 + c) as usize] = v;
            }
        }
        for r in 0..8 {
            for c in 0..8 {
                let v = if fy {
                    let mut sum = 0;
                    for t in 0..6 {
                        sum += taps[t as usize] * tmp[((r + t) * 8 + c) as usize];
                    }
                    clamp255((sum + 64) >> 7)
                } else {
                    tmp[((r + 2) * 8 + c) as usize]
                };
                out[(r * 8 + c) as usize] = v;
            }
        }
    }

    // The picture's motion against the last one: the shift of its column
    // and row profiles that matches best.
    fn global_motion(&mut self) {
        let pw = self.pw;
        let ph = self.mbh * 16;
        let mut cs: Vec<i64> = vec![0; pw as usize];
        let mut cl: Vec<i64> = vec![0; pw as usize];
        let mut rs: Vec<i64> = vec![0; ph as usize];
        let mut rl: Vec<i64> = vec![0; ph as usize];
        let mut y = 0;
        while y < ph {
            let row = y * pw;
            for x in 0..pw {
                let a = self.sy[(row + x) as usize] as i64;
                let b = self.ly[(row + x) as usize] as i64;
                cs[x as usize] += a;
                cl[x as usize] += b;
                rs[y as usize] += a;
                rl[y as usize] += b;
            }
            y += 1;
        }
        self.gmv_c = Vp8Encoder::profile_shift(&cs, &cl, pw / 4);
        self.gmv_r = Vp8Encoder::profile_shift(&rs, &rl, ph / 4);
    }

    // The d in -range..range for which cur[i] best matches last[i + d].
    fn profile_shift(cur: &Vec<i64>, last: &Vec<i64>, range: i64) -> i64 {
        let n = cur.len() as i64;
        let mut best = 0;
        let mut best_err = -1;
        let mut d = -range;
        while d <= range {
            let lo = if d < 0 { -d } else { 0 };
            let hi = if d > 0 { n - d } else { n };
            if hi - lo >= n / 2 {
                let mut e = 0;
                for i in lo..hi {
                    let v = cur[i as usize] - last[(i + d) as usize];
                    e += if v < 0 { -v } else { v };
                }
                // per overlapping sample, scaled so that whole numbers compare
                let err = e * 1024 / (hi - lo);
                if best_err < 0 || err < best_err || (err == best_err && (if d < 0 { -d } else { d }) < (if best < 0 { -best } else { best })) {
                    best_err = err;
                    best = d;
                }
            }
            d += 1;
        }
        best
    }

    fn put_mv_component(h: &mut BoolWriter, v: i64, p: &Vec<i64>, base: usize) {
        let x = if v < 0 { -v } else { v };
        if x < 8 {
            h.put(false, p[base]);
            let b2 = (x >> 2) & 1;
            let b1 = (x >> 1) & 1;
            h.put(b2 == 1, p[base + 2]);
            h.put(b1 == 1, if b2 == 0 { p[base + 3] } else { p[base + 6] });
            let n2 = if b2 == 0 { base + 4 + b1 as usize } else { base + 7 + b1 as usize };
            h.put((x & 1) == 1, p[n2]);
            if x == 0 {
                return;
            }
        } else {
            h.put(true, p[base]);
            for i in 0..3 {
                h.put(((x >> i) & 1) == 1, p[base + 9 + i as usize]);
            }
            let mut i = 9;
            while i > 3 {
                h.put(((x >> i) & 1) == 1, p[base + 9 + i as usize]);
                i -= 1;
            }
            if (x & 0xFFF0) != 0 {
                h.put(((x >> 3) & 1) == 1, p[base + 9 + 3]);
            }
        }
        h.put(v < 0, p[base + 1]);
    }

    // How much of the picture is new: the share of macroblocks (in 1/1000)
    // that the last picture, in place or moved with the whole picture,
    // predicts worse than their own mean does. A change of brightness (a
    // fade) costs a DC coefficient, so the difference is measured from its
    // own mean: a cut counts, a fade or a move does not.
    fn changed_share(&self) -> i64 {
        let mut changed = 0;
        let gr = self.gmv_r;
        let gc = self.gmv_c;
        let ph = self.mbh * 16;
        for my in 0..self.mbh {
            for mx in 0..self.mbw {
                let mut s = self.change_at(mx, my, 0, 0);
                let x = mx * 16 + gc;
                let y = my * 16 + gr;
                if (gr != 0 || gc != 0) && x >= 0 && y >= 0 && x + 16 <= self.pw && y + 16 <= ph {
                    let m = self.change_at(mx, my, gr, gc);
                    if m < s {
                        s = m;
                    }
                }
                if s > 6 * 256 && s > self.flat_sad(mx, my) {
                    changed += 1;
                }
            }
        }
        changed * 1000 / (self.mbw * self.mbh)
    }

    // The SAD of a macroblock's difference from the last picture at
    // (dr, dc), less the difference's mean.
    fn change_at(&self, mx: i64, my: i64, dr: i64, dc: i64) -> i64 {
        let pw = self.pw;
        let mut d: Vec<i64> = vec![0; 256];
        let mut sum = 0;
        for j in 0..16 {
            let a = (my * 16 + j) * pw + mx * 16;
            let b = (my * 16 + j + dr) * pw + mx * 16 + dc;
            for i in 0..16 {
                let v = self.sy[(a + i) as usize] as i64 - self.ly[(b + i) as usize] as i64;
                d[(j * 16 + i) as usize] = v;
                sum += v;
            }
        }
        let mean = if sum < 0 { -((-sum + 128) / 256) } else { (sum + 128) / 256 };
        let mut s = 0;
        for k in 0..256 {
            let v = d[k] - mean;
            s += if v < 0 { -v } else { v };
        }
        s
    }

    // A macroblock's SAD against its own mean: about what intra prediction leaves.
    fn flat_sad(&self, mx: i64, my: i64) -> i64 {
        let pw = self.pw;
        let mut sum = 0;
        for j in 0..16 {
            let row = (my * 16 + j) * pw + mx * 16;
            for i in 0..16 {
                sum += self.sy[(row + i) as usize] as i64;
            }
        }
        let mean = (sum + 128) / 256;
        let mut s = 0;
        for j in 0..16 {
            let row = (my * 16 + j) * pw + mx * 16;
            for i in 0..16 {
                let d = self.sy[(row + i) as usize] as i64 - mean;
                s += if d < 0 { -d } else { d };
            }
        }
        s
    }

    fn clamp_prob(p: i64) -> i64 {
        if p < 1 {
            1
        } else if p > 255 {
            255
        } else {
            p
        }
    }

    /// The frame loaded with load_rgba, coded: the VP8 frame's bytes.
    pub fn encode(&mut self) -> Vec<u8> {
        self.encode_frame(false)
    }

    /// The last frame again, as a frame that changes nothing.
    pub fn encode_repeat(&mut self) -> Vec<u8> {
        self.encode_frame(true)
    }

    fn encode_frame(&mut self, repeat: bool) -> Vec<u8> {
        let mut key = self.frames == 0;
        self.ly = self.ry.clone();
        self.lu = self.ru.clone();
        self.lv = self.rv.clone();
        self.gmv_r = 0;
        self.gmv_c = 0;
        if !key && !repeat {
            self.global_motion();
            if self.key_interval > 0 && self.since_key >= self.key_interval {
                key = true;
            } else if self.changed_share() > 600 {
                key = true;
            }
        }
        if key {
            self.coef_probs = self.default_probs.clone();
            self.since_key = 0;
        }
        self.tokens = Vec::new();
        for k in 0..self.above_ctx.len() {
            self.above_ctx[k] = 0;
        }
        for my in 0..self.mbh {
            for k in 0..9 {
                self.left_ctx[k] = 0;
            }
            for mx in 0..self.mbw {
                self.encode_mb(mx, my, key, repeat);
            }
        }

        // the coefficient probabilities this frame's tokens want
        let n = self.coef_probs.len();
        let mut counts: Vec<i64> = vec![0; n * 2];
        let mut t = 0;
        while t < self.tokens.len() {
            let base = self.tokens[t];
            let tok = self.tokens[t + 1] % 16;
            let skip = self.tokens[t + 1] / 16;
            let off = self.path_off[tok as usize];
            let len = self.path_len[tok as usize];
            let mut i = skip;
            while i < len {
                let nb = self.path[(off + i) as usize];
                counts[((base + nb / 2) * 2 + nb % 2) as usize] += 1;
                i += 1;
            }
            t += 4;
        }
        let mut updates: Vec<i64> = vec![0; n];
        for i in 0..n {
            let n0 = counts[i * 2];
            let n1 = counts[i * 2 + 1];
            if n0 + n1 == 0 {
                continue;
            }
            let old = self.coef_probs[i];
            let new = Vp8Encoder::clamp_prob((n0 * 256 + (n0 + n1) / 2) / (n0 + n1));
            let u = self.update_probs[i];
            let old_cost = n0 * self.cost[old as usize] + n1 * self.cost[(256 - old) as usize] + self.cost[u as usize];
            let new_cost =
                n0 * self.cost[new as usize] + n1 * self.cost[(256 - new) as usize] + self.cost[(256 - u) as usize] + 8 * 256;
            if new != old && new_cost < old_cost {
                updates[i] = new;
                self.coef_probs[i] = new;
            }
        }

        // partition 1: the frame header and the macroblocks' modes
        let mbs = self.mbw * self.mbh;
        let mut skips = 0;
        let mut intras = 0;
        for i in 0..mbs as usize {
            skips += self.mb_skip[i];
            if self.mb_inter[i] == 0 {
                intras += 1;
            }
        }
        let prob_skip = Vp8Encoder::clamp_prob(((mbs - skips) * 256 + mbs / 2) / mbs);
        let prob_intra = Vp8Encoder::clamp_prob((intras * 256 + mbs / 2) / mbs);
        let mut h = BoolWriter::new();
        if key {
            h.literal(0, 1); // colour space
            h.literal(0, 1); // clamping required
        }
        h.literal(0, 1); // no segmentation
        h.literal(0, 1); // normal loop filter
        h.literal(0, 6); // filter level 0: none
        h.literal(0, 3); // sharpness
        h.literal(0, 1); // no loop filter deltas
        h.literal(0, 2); // one token partition
        h.literal(self.qindex, 7);
        for _i in 0..5 {
            h.literal(0, 1); // no quantizer deltas
        }
        if !key {
            h.literal(0, 1); // refresh golden
            h.literal(0, 1); // refresh alt
            h.literal(0, 2); // copy to golden: none
            h.literal(0, 2); // copy to alt: none
            h.literal(0, 1); // sign bias golden
            h.literal(0, 1); // sign bias alt
        }
        h.literal(1, 1); // keep the probabilities
        if !key {
            h.literal(1, 1); // refresh last
        }
        for i in 0..n {
            let up = updates[i] > 0;
            h.put(up, self.update_probs[i]);
            if up {
                h.literal(updates[i], 8);
            }
        }
        h.literal(1, 1); // macroblocks may skip
        h.literal(prob_skip, 8);
        if !key {
            h.literal(prob_intra, 8);
            h.literal(255, 8); // last frame
            h.literal(128, 8); // golden (unused)
            h.literal(0, 1); // y mode probabilities kept
            h.literal(0, 1); // uv mode probabilities kept
            for i in 0..38 {
                h.put(false, self.mv_update[i]);
            }
        }
        for my in 0..self.mbh {
            for mx in 0..self.mbw {
                let i = (my * self.mbw + mx) as usize;
                h.put(self.mb_skip[i] == 1, prob_skip);
                let ym = self.mb_ymode[i];
                let uv = self.mb_uvmode[i];
                if key {
                    // kf_ymode_tree, probabilities 145 156 163 128
                    h.put(true, 145);
                    if ym == 0 || ym == 1 {
                        h.put(false, 156);
                        h.put(ym == 1, 163);
                    } else {
                        h.put(true, 156);
                        h.put(ym == 3, 128);
                    }
                    Vp8Encoder::put_uv_mode(&mut h, uv, 142, 114, 183);
                } else if self.mb_inter[i] == 1 {
                    h.put(true, prob_intra);
                    h.put(false, 255); // the last frame
                    // the mv_ref tree, its probabilities by the near vectors' counts
                    let mode = self.mb_mode[i];
                    h.put(mode > 0, self.mode_ctx[(self.mb_cnt[i * 3] * 4) as usize]);
                    if mode > 0 {
                        h.put(mode > 1, self.mode_ctx[(self.mb_cnt[i * 3 + 1] * 4 + 1) as usize]);
                        if mode > 1 {
                            h.put(mode > 2, self.mode_ctx[(self.mb_cnt[i * 3 + 2] * 4 + 2) as usize]);
                            if mode > 2 {
                                h.put(false, self.mode_ctx[3]); // not SPLITMV
                                Vp8Encoder::put_mv_component(&mut h, self.mb_dr[i] / 2, &self.mv_probs, 0);
                                Vp8Encoder::put_mv_component(&mut h, self.mb_dc[i] / 2, &self.mv_probs, 19);
                            }
                        }
                    }
                } else {
                    h.put(false, prob_intra);
                    // ymode_tree, probabilities 112 86 140 37
                    if ym == 0 {
                        h.put(false, 112);
                    } else {
                        h.put(true, 112);
                        if ym == 1 || ym == 2 {
                            h.put(false, 86);
                            h.put(ym == 2, 140);
                        } else {
                            h.put(true, 86);
                            h.put(false, 37);
                        }
                    }
                    Vp8Encoder::put_uv_mode(&mut h, uv, 162, 101, 204);
                }
            }
        }
        h.flush();

        // partition 2: the tokens
        let mut w = BoolWriter::new();
        let mut t = 0;
        while t < self.tokens.len() {
            let base = self.tokens[t];
            let tok = self.tokens[t + 1] % 16;
            let skip = self.tokens[t + 1] / 16;
            let a = self.tokens[t + 2];
            let off = self.path_off[tok as usize];
            let len = self.path_len[tok as usize];
            let mut i = skip;
            while i < len {
                let nb = self.path[(off + i) as usize];
                w.put(nb % 2 == 1, self.coef_probs[(base + nb / 2) as usize]);
                i += 1;
            }
            if tok >= 5 && tok <= 10 {
                let c = (tok - 5) as usize;
                let extra = a - self.cat_base[c];
                let bits = self.cat_len[c];
                for k in 0..bits {
                    let bit = ((extra >> (bits - 1 - k)) & 1) == 1;
                    w.put(bit, self.cat_probs[(self.cat_off[c] + k) as usize]);
                }
            }
            if tok != 0 && tok != 11 {
                w.put(self.tokens[t + 3] == 1, 128);
            }
            t += 4;
        }
        w.flush();

        let mut out: Vec<u8> = Vec::new();
        let size = h.out.len() as i64;
        let tag = (if key { 0 } else { 1 }) + 16 + size * 32;
        out.push((tag % 256) as u8);
        out.push(((tag / 256) % 256) as u8);
        out.push(((tag / 65536) % 256) as u8);
        if key {
            out.push(157);
            out.push(1);
            out.push(42);
            out.push((self.width % 256) as u8);
            out.push((self.width / 256) as u8);
            out.push((self.height % 256) as u8);
            out.push((self.height / 256) as u8);
        }
        for b in h.out.iter() {
            out.push(*b);
        }
        for b in w.out.iter() {
            out.push(*b);
        }
        self.frames += 1;
        self.since_key += 1;
        self.last_key = key;
        self.tokens = Vec::new();
        out
    }

    fn put_uv_mode(h: &mut BoolWriter, uv: i64, p0: i64, p1: i64, p2: i64) {
        if uv == 0 {
            h.put(false, p0);
        } else {
            h.put(true, p0);
            if uv == 1 {
                h.put(false, p1);
            } else {
                h.put(true, p1);
                h.put(uv == 3, p2);
            }
        }
    }

    /// The decoded picture's planes, padded to whole macroblocks (for tests):
    /// Y, then U, then V.
    pub fn recon(&self) -> Vec<u8> {
        let mut out = self.ry.clone();
        for b in self.ru.iter() {
            out.push(*b);
        }
        for b in self.rv.iter() {
            out.push(*b);
        }
        out
    }
}

// The coded frames of one video, kept until the file is written: frame n
// shows from n / fps until the next coded frame (a frame equal to the one
// before is not coded).
pub struct Vp8Video {
    pub encoder: Vp8Encoder,
    pub fps: i64,
    /// the quantizer index of a still picture
    pub quality: i64,
    /// added to it while the picture moves: a moving picture hides detail,
    /// and the picture it stops at is coded again at `quality`
    pub motion_extra: i64,
    settle: bool,
    data: Vec<u8>,
    starts: Vec<i64>,
    numbers: Vec<i64>,
    keys: Vec<i64>,
    total: i64,
}

impl Vp8Video {
    pub fn new(width: i64, height: i64, fps: i64, qindex: i64) -> Vp8Video {
        Vp8Video {
            encoder: Vp8Encoder::new(width, height, qindex),
            fps: if fps > 0 { fps } else { 30 },
            quality: qindex,
            motion_extra: 24,
            settle: false,
            data: Vec::new(),
            starts: Vec::new(),
            numbers: Vec::new(),
            keys: Vec::new(),
            total: 0,
        }
    }

    fn keep(&mut self, frame: &Vec<u8>, n: i64) {
        self.starts.push(self.data.len() as i64);
        self.numbers.push(n);
        self.keys.push(if self.encoder.last_key { 1 } else { 0 });
        for b in frame.iter() {
            self.data.push(*b);
        }
    }

    /// Frame n (0, 1, 2, ...) as RGBA; `same` when it equals frame n - 1,
    /// which then is not coded.
    pub fn add(&mut self, rgba: &Vec<u8>, n: i64, same: bool) {
        if n + 1 > self.total {
            self.total = n + 1;
        }
        if same && self.numbers.len() > 0 {
            if self.settle {
                self.settle_at(n);
            }
            return;
        }
        // the first picture, and a new slide, start still; later ones move
        // until a frame repeats
        let first = self.numbers.len() == 0;
        self.encoder.set_quality(if first { self.quality } else { self.quality + self.motion_extra });
        self.encoder.load_rgba(rgba);
        let f = self.encoder.encode();
        self.settle = !first && self.motion_extra > 0;
        self.keep(&f, n);
    }

    // The picture stopped: the same picture again at the still quality,
    // coded as what it adds to the coarser one.
    fn settle_at(&mut self, n: i64) {
        self.encoder.set_quality(self.quality);
        let f = self.encoder.encode();
        self.settle = false;
        self.keep(&f, n);
    }

    /// The video lasts `frames` frames (the last picture held until then).
    pub fn set_length(&mut self, frames: i64) {
        self.total = frames;
    }

    // A file ends at its last frame's time; a held last picture is coded
    // once more one frame before the end: settled, or as a frame that
    // changes nothing.
    fn close_hold(&mut self) {
        let k = self.numbers.len();
        if self.settle && k > 0 && self.numbers[k - 1] < self.total - 1 {
            let n = self.total - 1;
            self.settle_at(n);
            return;
        }
        if k > 0 && self.numbers[k - 1] < self.total - 1 {
            let f = self.encoder.encode_repeat();
            let n = self.total - 1;
            self.keep(&f, n);
        }
    }

    pub fn frame_count(&self) -> i64 {
        self.numbers.len() as i64
    }

    pub fn key_count(&self) -> i64 {
        let mut k = 0;
        for v in self.keys.iter() {
            k += *v;
        }
        k
    }

    fn frame_end(&self, i: usize) -> i64 {
        if i + 1 < self.starts.len() {
            self.starts[i + 1]
        } else {
            self.data.len() as i64
        }
    }

    /// The video as IVF: a 32-byte header, then each frame with its size and number.
    pub fn ivf(&mut self) -> Vec<u8> {
        self.close_hold();
        let mut out: Vec<u8> = vec![68, 75, 73, 70, 0, 0, 32, 0, 86, 80, 56, 48];
        put_le(&mut out, self.encoder.width, 2);
        put_le(&mut out, self.encoder.height, 2);
        put_le(&mut out, self.fps, 4);
        put_le(&mut out, 1, 4);
        put_le(&mut out, self.numbers.len() as i64, 4);
        put_le(&mut out, 0, 4);
        for i in 0..self.numbers.len() {
            let a = self.starts[i];
            let b = self.frame_end(i);
            put_le(&mut out, b - a, 4);
            put_le(&mut out, self.numbers[i], 4);
            put_le(&mut out, 0, 4);
            for k in a..b {
                out.push(self.data[k as usize]);
            }
        }
        out
    }

    fn ms(&self, n: i64) -> i64 {
        (n * 1000 + self.fps / 2) / self.fps
    }

    /// The video as WebM (Matroska with VP8): a cluster from each key frame,
    /// and cues to them for seeking.
    pub fn webm(&mut self) -> Vec<u8> {
        self.close_hold();
        let mut out: Vec<u8> = Vec::new();
        // EBML header: version 1, ids up to 4 bytes, sizes up to 8, "webm" 2
        let mut head: Vec<u8> = Vec::new();
        el_uint(&mut head, 0x4286, 1);
        el_uint(&mut head, 0x42F7, 1);
        el_uint(&mut head, 0x42F2, 4);
        el_uint(&mut head, 0x42F3, 8);
        el_bytes(&mut head, 0x4282, &vec![119, 101, 98, 109]);
        el_uint(&mut head, 0x4287, 2);
        el_uint(&mut head, 0x4285, 2);
        el_bytes(&mut out, 0x1A45DFA3, &head);

        let mut info: Vec<u8> = Vec::new();
        el_uint(&mut info, 0x2AD7B1, 1000000);
        let name: Vec<u8> = vec![83, 108, 105, 113, 116, 108, 121];
        el_bytes(&mut info, 0x4D80, &name);
        el_bytes(&mut info, 0x5741, &name);
        el_bytes(&mut info, 0x4489, &double_bytes(self.ms(self.total)));
        let mut info_el: Vec<u8> = Vec::new();
        el_bytes(&mut info_el, 0x1549A966, &info);

        let mut video: Vec<u8> = Vec::new();
        el_uint(&mut video, 0xB0, self.encoder.width);
        el_uint(&mut video, 0xBA, self.encoder.height);
        let mut track: Vec<u8> = Vec::new();
        el_uint(&mut track, 0xD7, 1);
        el_uint(&mut track, 0x73C5, 1);
        el_uint(&mut track, 0x83, 1);
        el_bytes(&mut track, 0x86, &vec![86, 95, 86, 80, 56]);
        el_uint(&mut track, 0x9C, 0);
        el_bytes(&mut track, 0xE0, &video);
        let mut tracks: Vec<u8> = Vec::new();
        el_bytes(&mut tracks, 0xAE, &track);
        let mut tracks_el: Vec<u8> = Vec::new();
        el_bytes(&mut tracks_el, 0x1654AE6B, &tracks);

        // clusters, at positions counted from the first of them
        let mut clusters: Vec<u8> = Vec::new();
        let mut cue_times: Vec<i64> = Vec::new();
        let mut cue_pos: Vec<i64> = Vec::new();
        let mut i = 0;
        let count = self.numbers.len();
        while i < count {
            let t0 = self.ms(self.numbers[i]);
            let mut body: Vec<u8> = Vec::new();
            el_uint(&mut body, 0xE7, t0);
            let mut j = i;
            while j < count && (j == i || (self.keys[j] == 0 && self.ms(self.numbers[j]) - t0 < 30000)) {
                let a = self.starts[j];
                let b = self.frame_end(j);
                let rel = self.ms(self.numbers[j]) - t0;
                let mut block: Vec<u8> = vec![129, ((rel / 256) % 256) as u8, (rel % 256) as u8];
                block.push(if self.keys[j] == 1 { 128 } else { 0 });
                for k in a..b {
                    block.push(self.data[k as usize]);
                }
                el_bytes(&mut body, 0xA3, &block);
                j += 1;
            }
            if self.keys[i] == 1 {
                cue_times.push(t0);
                cue_pos.push(clusters.len() as i64);
            }
            el_bytes(&mut clusters, 0x1F43B675, &body);
            i = j;
        }

        // the seek head names where Info, Tracks and Cues start; its own
        // size is fixed (8-byte positions), so the positions can be known
        let seek_len = 3 * (2 + 1 + 2 + 1 + 4 + 2 + 1 + 8) + 4 + 1;
        let info_at = seek_len;
        let tracks_at = info_at + info_el.len() as i64;
        let clusters_at = tracks_at + tracks_el.len() as i64;
        let cues_at = clusters_at + clusters.len() as i64;
        let mut cues: Vec<u8> = Vec::new();
        for c in 0..cue_times.len() {
            let mut tp: Vec<u8> = Vec::new();
            el_uint(&mut tp, 0xF7, 1);
            el_uint(&mut tp, 0xF1, clusters_at + cue_pos[c]);
            let mut point: Vec<u8> = Vec::new();
            el_uint(&mut point, 0xB3, cue_times[c]);
            el_bytes(&mut point, 0xB7, &tp);
            el_bytes(&mut cues, 0xBB, &point);
        }
        let mut seek: Vec<u8> = Vec::new();
        seek_entry(&mut seek, 0x1549A966, info_at);
        seek_entry(&mut seek, 0x1654AE6B, tracks_at);
        seek_entry(&mut seek, 0x1C53BB6B, cues_at);
        let mut segment: Vec<u8> = Vec::new();
        el_bytes(&mut segment, 0x114D9B74, &seek);
        append(&mut segment, &info_el);
        append(&mut segment, &tracks_el);
        append(&mut segment, &clusters);
        el_bytes(&mut segment, 0x1C53BB6B, &cues);
        el_bytes(&mut out, 0x18538067, &segment);
        out
    }
}

fn append(out: &mut Vec<u8>, data: &Vec<u8>) {
    for b in data.iter() {
        out.push(*b);
    }
}

fn put_le(out: &mut Vec<u8>, v: i64, bytes: i64) {
    let mut x = v;
    for _i in 0..bytes {
        out.push((x % 256) as u8);
        x = x / 256;
    }
}

fn put_be(out: &mut Vec<u8>, v: i64, bytes: i64) {
    let mut d: i64 = 1;
    for _i in 1..bytes {
        d = d * 256;
    }
    for _i in 0..bytes {
        out.push(((v / d) % 256) as u8);
        d = d / 256;
    }
}

fn el_id(out: &mut Vec<u8>, id: i64) {
    let n = if id >= 16777216 {
        4
    } else if id >= 65536 {
        3
    } else if id >= 256 {
        2
    } else {
        1
    };
    put_be(out, id, n);
}

// An element size as an EBML variable-length integer.
fn el_size(out: &mut Vec<u8>, size: i64) {
    if size < 127 {
        out.push((128 + size) as u8);
    } else if size < 16383 {
        put_be(out, 16384 + size, 2);
    } else if size < 2097151 {
        put_be(out, 2097152 + size, 3);
    } else if size < 268435455 {
        put_be(out, 268435456 + size, 4);
    } else {
        out.push(1);
        put_be(out, size, 7);
    }
}

fn el_bytes(out: &mut Vec<u8>, id: i64, data: &Vec<u8>) {
    el_id(out, id);
    el_size(out, data.len() as i64);
    append(out, data);
}

fn el_uint(out: &mut Vec<u8>, id: i64, v: i64) {
    let mut n = 1;
    let mut x = v / 256;
    while x > 0 {
        n += 1;
        x = x / 256;
    }
    el_id(out, id);
    el_size(out, n);
    put_be(out, v, n);
}

fn seek_entry(out: &mut Vec<u8>, id: i64, pos: i64) {
    let mut e: Vec<u8> = Vec::new();
    let mut idb: Vec<u8> = Vec::new();
    el_id(&mut idb, id);
    el_bytes(&mut e, 0x53AB, &idb);
    el_id(&mut e, 0x53AC);
    el_size(&mut e, 8);
    put_be(&mut e, pos, 8);
    el_bytes(out, 0x4DBB, &e);
}

// A whole number as the 8 bytes of an IEEE 754 double, big-endian.
fn double_bytes(v: i64) -> Vec<u8> {
    let mut out: Vec<u8> = Vec::new();
    if v <= 0 {
        for _i in 0..8 {
            out.push(0);
        }
        return out;
    }
    let mut e = 0;
    let mut p: i64 = 1;
    while p * 2 <= v {
        p = p * 2;
        e += 1;
    }
    // mantissa = (v - 2^e) * 2^(52 - e), split at bit 32
    let mut m = v - p;
    for _i in 0..(52 - e) {
        m = m * 2;
    }
    let hi = (1023 + e) * 1048576 + m / 4294967296;
    let lo = m % 4294967296;
    put_be(&mut out, hi, 4);
    put_be(&mut out, lo, 4);
    out
}

// ---------------------------------------------------------------------------
// The format's tables (RFC 6386): default and update probabilities of the
// coefficient tokens [4 block types][8 bands][3 contexts][11 nodes], the
// quantizer steps by index, the ZEROMV..SPLITMV mode contexts [6][4], the
// motion vector update probabilities [2][19], and the cost of a bit of
// probability p/256 in 1/256 bits.
// ---------------------------------------------------------------------------

fn default_coef_probs() -> Vec<i64> {
    vec![
        128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128,
        128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 253, 136, 254, 255, 228, 219, 128, 128, 128, 128, 128,
        189, 129, 242, 255, 227, 213, 255, 219, 128, 128, 128, 106, 126, 227, 252, 214, 209, 255, 255, 128, 128, 128,
        1, 98, 248, 255, 236, 226, 255, 255, 128, 128, 128, 181, 133, 238, 254, 221, 234, 255, 154, 128, 128, 128,
        78, 134, 202, 247, 198, 180, 255, 219, 128, 128, 128, 1, 185, 249, 255, 243, 255, 128, 128, 128, 128, 128,
        184, 150, 247, 255, 236, 224, 128, 128, 128, 128, 128, 77, 110, 216, 255, 236, 230, 128, 128, 128, 128, 128,
        1, 101, 251, 255, 241, 255, 128, 128, 128, 128, 128, 170, 139, 241, 252, 236, 209, 255, 255, 128, 128, 128,
        37, 116, 196, 243, 228, 255, 255, 255, 128, 128, 128, 1, 204, 254, 255, 245, 255, 128, 128, 128, 128, 128,
        207, 160, 250, 255, 238, 128, 128, 128, 128, 128, 128, 102, 103, 231, 255, 211, 171, 128, 128, 128, 128, 128,
        1, 152, 252, 255, 240, 255, 128, 128, 128, 128, 128, 177, 135, 243, 255, 234, 225, 128, 128, 128, 128, 128,
        80, 129, 211, 255, 194, 224, 128, 128, 128, 128, 128, 1, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128,
        246, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128, 255, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128,
        198, 35, 237, 223, 193, 187, 162, 160, 145, 155, 62, 131, 45, 198, 221, 172, 176, 220, 157, 252, 221, 1,
        68, 47, 146, 208, 149, 167, 221, 162, 255, 223, 128, 1, 149, 241, 255, 221, 224, 255, 255, 128, 128, 128,
        184, 141, 234, 253, 222, 220, 255, 199, 128, 128, 128, 81, 99, 181, 242, 176, 190, 249, 202, 255, 255, 128,
        1, 129, 232, 253, 214, 197, 242, 196, 255, 255, 128, 99, 121, 210, 250, 201, 198, 255, 202, 128, 128, 128,
        23, 91, 163, 242, 170, 187, 247, 210, 255, 255, 128, 1, 200, 246, 255, 234, 255, 128, 128, 128, 128, 128,
        109, 178, 241, 255, 231, 245, 255, 255, 128, 128, 128, 44, 130, 201, 253, 205, 192, 255, 255, 128, 128, 128,
        1, 132, 239, 251, 219, 209, 255, 165, 128, 128, 128, 94, 136, 225, 251, 218, 190, 255, 255, 128, 128, 128,
        22, 100, 174, 245, 186, 161, 255, 199, 128, 128, 128, 1, 182, 249, 255, 232, 235, 128, 128, 128, 128, 128,
        124, 143, 241, 255, 227, 234, 128, 128, 128, 128, 128, 35, 77, 181, 251, 193, 211, 255, 205, 128, 128, 128,
        1, 157, 247, 255, 236, 231, 255, 255, 128, 128, 128, 121, 141, 235, 255, 225, 227, 255, 255, 128, 128, 128,
        45, 99, 188, 251, 195, 217, 255, 224, 128, 128, 128, 1, 1, 251, 255, 213, 255, 128, 128, 128, 128, 128,
        203, 1, 248, 255, 255, 128, 128, 128, 128, 128, 128, 137, 1, 177, 255, 224, 255, 128, 128, 128, 128, 128,
        253, 9, 248, 251, 207, 208, 255, 192, 128, 128, 128, 175, 13, 224, 243, 193, 185, 249, 198, 255, 255, 128,
        73, 17, 171, 221, 161, 179, 236, 167, 255, 234, 128, 1, 95, 247, 253, 212, 183, 255, 255, 128, 128, 128,
        239, 90, 244, 250, 211, 209, 255, 255, 128, 128, 128, 155, 77, 195, 248, 188, 195, 255, 255, 128, 128, 128,
        1, 24, 239, 251, 218, 219, 255, 205, 128, 128, 128, 201, 51, 219, 255, 196, 186, 128, 128, 128, 128, 128,
        69, 46, 190, 239, 201, 218, 255, 228, 128, 128, 128, 1, 191, 251, 255, 255, 128, 128, 128, 128, 128, 128,
        223, 165, 249, 255, 213, 255, 128, 128, 128, 128, 128, 141, 124, 248, 255, 255, 128, 128, 128, 128, 128, 128,
        1, 16, 248, 255, 255, 128, 128, 128, 128, 128, 128, 190, 36, 230, 255, 236, 255, 128, 128, 128, 128, 128,
        149, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128, 1, 226, 255, 128, 128, 128, 128, 128, 128, 128, 128,
        247, 192, 255, 128, 128, 128, 128, 128, 128, 128, 128, 240, 128, 255, 128, 128, 128, 128, 128, 128, 128, 128,
        1, 134, 252, 255, 255, 128, 128, 128, 128, 128, 128, 213, 62, 250, 255, 255, 128, 128, 128, 128, 128, 128,
        55, 93, 255, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128,
        128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128,
        202, 24, 213, 235, 186, 191, 220, 160, 240, 175, 255, 126, 38, 182, 232, 169, 184, 228, 174, 255, 187, 128,
        61, 46, 138, 219, 151, 178, 240, 170, 255, 216, 128, 1, 112, 230, 250, 199, 191, 247, 159, 255, 255, 128,
        166, 109, 228, 252, 211, 215, 255, 174, 128, 128, 128, 39, 77, 162, 232, 172, 180, 245, 178, 255, 255, 128,
        1, 52, 220, 246, 198, 199, 249, 220, 255, 255, 128, 124, 74, 191, 243, 183, 193, 250, 221, 255, 255, 128,
        24, 71, 130, 219, 154, 170, 243, 182, 255, 255, 128, 1, 182, 225, 249, 219, 240, 255, 224, 128, 128, 128,
        149, 150, 226, 252, 216, 205, 255, 171, 128, 128, 128, 28, 108, 170, 242, 183, 194, 254, 223, 255, 255, 128,
        1, 81, 230, 252, 204, 203, 255, 192, 128, 128, 128, 123, 102, 209, 247, 188, 196, 255, 233, 128, 128, 128,
        20, 95, 153, 243, 164, 173, 255, 203, 128, 128, 128, 1, 222, 248, 255, 216, 213, 128, 128, 128, 128, 128,
        168, 175, 246, 252, 235, 205, 255, 255, 128, 128, 128, 47, 116, 215, 255, 211, 212, 255, 255, 128, 128, 128,
        1, 121, 236, 253, 212, 214, 255, 255, 128, 128, 128, 141, 84, 213, 252, 201, 202, 255, 219, 128, 128, 128,
        42, 80, 160, 240, 162, 185, 255, 205, 128, 128, 128, 1, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128,
        244, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128, 238, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128,
    ]
}

fn coef_update_probs() -> Vec<i64> {
    vec![
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 176, 246, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        223, 241, 252, 255, 255, 255, 255, 255, 255, 255, 255, 249, 253, 253, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 244, 252, 255, 255, 255, 255, 255, 255, 255, 255, 234, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        253, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 246, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        239, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255, 254, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 248, 254, 255, 255, 255, 255, 255, 255, 255, 255, 251, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        251, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255, 254, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 254, 253, 255, 254, 255, 255, 255, 255, 255, 255, 250, 255, 254, 255, 254, 255, 255, 255, 255, 255, 255,
        254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        217, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 225, 252, 241, 253, 255, 255, 254, 255, 255, 255, 255,
        234, 250, 241, 250, 253, 255, 253, 254, 255, 255, 255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        223, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255, 238, 253, 254, 254, 255, 255, 255, 255, 255, 255, 255,
        255, 248, 254, 255, 255, 255, 255, 255, 255, 255, 255, 249, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 253, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        247, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255, 252, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        253, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 254, 253, 255, 255, 255, 255, 255, 255, 255, 255, 250, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        186, 251, 250, 255, 255, 255, 255, 255, 255, 255, 255, 234, 251, 244, 254, 255, 255, 255, 255, 255, 255, 255,
        251, 251, 243, 253, 254, 255, 254, 255, 255, 255, 255, 255, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        236, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255, 251, 253, 253, 254, 254, 255, 255, 255, 255, 255, 255,
        255, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255, 254, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        254, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        248, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 250, 254, 252, 254, 255, 255, 255, 255, 255, 255, 255,
        248, 254, 249, 253, 255, 255, 255, 255, 255, 255, 255, 255, 253, 253, 255, 255, 255, 255, 255, 255, 255, 255,
        246, 253, 253, 255, 255, 255, 255, 255, 255, 255, 255, 252, 254, 251, 254, 254, 255, 255, 255, 255, 255, 255,
        255, 254, 252, 255, 255, 255, 255, 255, 255, 255, 255, 248, 254, 253, 255, 255, 255, 255, 255, 255, 255, 255,
        253, 255, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255, 251, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        245, 251, 254, 255, 255, 255, 255, 255, 255, 255, 255, 253, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 251, 253, 255, 255, 255, 255, 255, 255, 255, 255, 252, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 252, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        249, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 253, 255, 255, 255, 255, 255, 255, 255, 255, 250, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
        254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    ]
}

fn dc_qlookup() -> Vec<i64> {
    vec![
        4, 5, 6, 7, 8, 9, 10, 10, 11, 12, 13, 14, 15, 16, 17, 17,
        18, 19, 20, 20, 21, 21, 22, 22, 23, 23, 24, 25, 25, 26, 27, 28,
        29, 30, 31, 32, 33, 34, 35, 36, 37, 37, 38, 39, 40, 41, 42, 43,
        44, 45, 46, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58,
        59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74,
        75, 76, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89,
        91, 93, 95, 96, 98, 100, 101, 102, 104, 106, 108, 110, 112, 114, 116, 118,
        122, 124, 126, 128, 130, 132, 134, 136, 138, 140, 143, 145, 148, 151, 154, 157,
    ]
}

fn ac_qlookup() -> Vec<i64> {
    vec![
        4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
        20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35,
        36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51,
        52, 53, 54, 55, 56, 57, 58, 60, 62, 64, 66, 68, 70, 72, 74, 76,
        78, 80, 82, 84, 86, 88, 90, 92, 94, 96, 98, 100, 102, 104, 106, 108,
        110, 112, 114, 116, 119, 122, 125, 128, 131, 134, 137, 140, 143, 146, 149, 152,
        155, 158, 161, 164, 167, 170, 173, 177, 181, 185, 189, 193, 197, 201, 205, 209,
        213, 217, 221, 225, 229, 234, 239, 245, 249, 254, 259, 264, 269, 274, 279, 284,
    ]
}

fn mode_contexts() -> Vec<i64> {
    vec![
        7, 1, 1, 143,
        14, 18, 14, 107,
        135, 64, 57, 68,
        60, 56, 128, 65,
        159, 134, 128, 34,
        234, 188, 128, 28,
    ]
}

fn mv_update_probs() -> Vec<i64> {
    vec![
        237, 246, 253, 253, 254, 254, 254, 254, 254, 254, 254, 254, 254, 254, 250, 250, 252, 254, 254,
        231, 243, 245, 253, 254, 254, 254, 254, 254, 254, 254, 254, 254, 254, 251, 251, 254, 254, 254,
    ]
}

fn prob_cost() -> Vec<i64> {
    vec![
        2048, 2048, 1792, 1642, 1536, 1454, 1386, 1329, 1280, 1236, 1198, 1162, 1130, 1101, 1073, 1048,
        1024, 1002, 980, 961, 942, 924, 906, 890, 874, 859, 845, 831, 817, 804, 792, 780,
        768, 757, 746, 735, 724, 714, 705, 695, 686, 676, 668, 659, 650, 642, 634, 626,
        618, 611, 603, 596, 589, 582, 575, 568, 561, 555, 548, 542, 536, 530, 524, 518,
        512, 506, 501, 495, 490, 484, 479, 474, 468, 463, 458, 453, 449, 444, 439, 434,
        430, 425, 420, 416, 412, 407, 403, 399, 394, 390, 386, 382, 378, 374, 370, 366,
        362, 358, 355, 351, 347, 343, 340, 336, 333, 329, 326, 322, 319, 315, 312, 309,
        305, 302, 299, 296, 292, 289, 286, 283, 280, 277, 274, 271, 268, 265, 262, 259,
        256, 253, 250, 247, 245, 242, 239, 236, 234, 231, 228, 226, 223, 220, 218, 215,
        212, 210, 207, 205, 202, 200, 197, 195, 193, 190, 188, 185, 183, 181, 178, 176,
        174, 171, 169, 167, 164, 162, 160, 158, 156, 153, 151, 149, 147, 145, 143, 140,
        138, 136, 134, 132, 130, 128, 126, 124, 122, 120, 118, 116, 114, 112, 110, 108,
        106, 104, 102, 101, 99, 97, 95, 93, 91, 89, 87, 86, 84, 82, 80, 78,
        77, 75, 73, 71, 70, 68, 66, 64, 63, 61, 59, 58, 56, 54, 53, 51,
        49, 48, 46, 44, 43, 41, 40, 38, 36, 35, 33, 32, 30, 28, 27, 25,
        24, 22, 21, 19, 18, 16, 15, 13, 12, 10, 9, 7, 6, 4, 3, 1,
    ]
}
