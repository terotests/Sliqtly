// SPDX-License-Identifier: AGPL-3.0-or-later
//
// PresVp8Check.rs — frames for scripts/check-vp8.mjs: a small picture that
// moves (whole and half chroma pixels), holds still, cuts to another
// picture and fades. Prints the encoder's own picture after each coded
// frame ("R <hex>": Y, U, V padded to whole macroblocks), then the IVF and
// the WebM. The check runs it built by rustc and by Ranger (JavaScript),
// requires the same lines from both, and has FFmpeg decode the IVF to the
// same pictures.

#[allow(non_snake_case)]
mod PresVp8;
use PresVp8::Vp8Video;

fn hex(data: &Vec<u8>) -> String {
    let digits: Vec<char> = "0123456789abcdef".chars().collect();
    let mut s = String::new();
    for b in data.iter() {
        let v = *b as usize;
        s.push(digits[v / 16]);
        s.push(digits[v % 16]);
    }
    s
}

// Frame n of the test video, RGBA.
fn frame(w: i64, h: i64, n: i64) -> Vec<u8> {
    let mut px: Vec<u8> = vec![0; (w * h * 4) as usize];
    // 0..5 move, 6..9 hold, 10.. another picture fading in
    let t = if n < 6 { n } else { 5 };
    let cut = n >= 10;
    let fade = if cut { (n - 10) * 40 } else { 0 };
    for y in 0..h {
        for x in 0..w {
            let p = ((y * w + x) * 4) as usize;
            let xs = x + t * 5;
            let ys = y + t * 3;
            let mut r = (xs * 4) % 256;
            let mut g = (ys * 5 + xs * xs / 7) % 256;
            let mut b = 128;
            if x >= 10 + t * 3 && x < 26 + t * 3 && y >= 12 && y < 30 {
                r = 250;
                g = 240;
                b = 20;
            }
            if (xs / 3 + ys / 5) % 7 == 0 && ys > 30 {
                r = 0;
                g = 0;
                b = 0;
            }
            if cut {
                r = (x * 2 + fade) % 256;
                g = 200 - (y % 50);
                b = if (x / 8 + y / 8) % 2 == 0 { 30 } else { 220 };
            }
            px[p] = r as u8;
            px[p + 1] = g as u8;
            px[p + 2] = b as u8;
            px[p + 3] = 255;
        }
    }
    px
}

fn main() {
    let w = 150;
    let h = 100;
    let mut v = Vp8Video::new(w, h, 10, 24);
    let mut coded = 0;
    for n in 0..14 {
        let same = n >= 7 && n < 10;
        v.add(&frame(w, h, n), n, same);
        if v.frame_count() > coded {
            coded = v.frame_count();
            println!("R {}", hex(&v.encoder.recon()));
        }
    }
    v.set_length(20);
    let ivf = v.ivf();
    if v.frame_count() > coded {
        println!("R {}", hex(&v.encoder.recon()));
    }
    println!("IVF {}", hex(&ivf));
    println!("WEBM {}", hex(&v.webm()));
    println!("KEYS {}", v.key_count());
}
