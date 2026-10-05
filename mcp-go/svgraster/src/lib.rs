// SPDX-License-Identifier: AGPL-3.0-or-later
//
// resvg behind a handful of exports with no imports, for svgraster.go.
// Memory crosses as (pointer, length) into this module's own memory:
// the host asks for room with `alloc`, writes, calls, and reads the result
// where `out_ptr` / `out_len` say.

use resvg::{tiny_skia, usvg};
use std::sync::Arc;

static mut FONTS: Option<Arc<usvg::fontdb::Database>> = None;
static mut OUT: Vec<u8> = Vec::new();

fn fonts() -> &'static mut Arc<usvg::fontdb::Database> {
    #[allow(static_mut_refs)]
    unsafe {
        FONTS.get_or_insert_with(|| Arc::new(usvg::fontdb::Database::new()))
    }
}

#[no_mangle]
pub extern "C" fn alloc(len: usize) -> *mut u8 {
    let mut v = Vec::<u8>::with_capacity(len.max(1));
    let p = v.as_mut_ptr();
    std::mem::forget(v);
    p
}

#[no_mangle]
pub unsafe extern "C" fn dealloc(p: *mut u8, len: usize) {
    drop(Vec::from_raw_parts(p, 0, len.max(1)));
}

/// A font face the SVG's text may use; `generic` 1: it also stands for
/// sans-serif and for serif, which is where resvg goes for a family it does
/// not have (there is no serif face here), 0: by its own name only.
#[no_mangle]
pub unsafe extern "C" fn add_font(p: *const u8, len: usize, generic: u32) {
    let data = std::slice::from_raw_parts(p, len).to_vec();
    let db = Arc::make_mut(fonts());
    let ids = db.load_font_source(usvg::fontdb::Source::Binary(Arc::new(data)));
    if let Some(id) = ids.first() {
        if let Some(face) = db.face(*id) {
            if let Some((name, _)) = face.families.first() {
                let name = name.clone();
                if generic == 1 {
                    db.set_sans_serif_family(name.clone());
                    db.set_serif_family(name);
                }
            }
        }
    }
}

#[no_mangle]
pub extern "C" fn out_ptr() -> *const u8 {
    #[allow(static_mut_refs)]
    unsafe {
        OUT.as_ptr()
    }
}

#[no_mangle]
pub extern "C" fn out_len() -> usize {
    #[allow(static_mut_refs)]
    unsafe {
        OUT.len()
    }
}

/// Draws the SVG in (p, len) to `w` × `h` pixels, its picture stretched to
/// that box (the host gives the box the SVG's own shape). The pixels, RGBA
/// premultiplied, row after row, are then at out_ptr. 0 when drawn; 1 when
/// it does not parse (out holds the reason as UTF-8); 2 for a bad size.
#[no_mangle]
pub unsafe extern "C" fn render(p: *const u8, len: usize, w: u32, h: u32) -> u32 {
    #[allow(static_mut_refs)]
    let out = &mut OUT;
    out.clear();
    out.shrink_to_fit();
    let data = std::slice::from_raw_parts(p, len);
    let mut opt = usvg::Options::default();
    opt.fontdb = fonts().clone();
    opt.font_family = "sans-serif".into();
    // a picture inside the SVG: data: URLs only, as a browser draws an SVG
    // shown as a picture (no files, no network)
    opt.image_href_resolver = usvg::ImageHrefResolver {
        resolve_data: usvg::ImageHrefResolver::default_data_resolver(),
        resolve_string: Box::new(|_, _| None),
    };
    let tree = match usvg::Tree::from_data(data, &opt) {
        Ok(t) => t,
        Err(e) => {
            out.extend_from_slice(e.to_string().as_bytes());
            return 1;
        }
    };
    let Some(mut pix) = tiny_skia::Pixmap::new(w, h) else { return 2 };
    let size = tree.size();
    let t = tiny_skia::Transform::from_scale(w as f32 / size.width(), h as f32 / size.height());
    resvg::render(&tree, t, &mut pix.as_mut());
    *out = pix.take();
    0
}
