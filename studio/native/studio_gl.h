// SPDX-License-Identifier: AGPL-3.0-or-later
// From EVGSheets datagrid/platform/sdl/evg_gl_native.h, with Studio additions.
#pragma once
#include <cstdint>

// Native EVG display-list → OpenGL drawer (same seam as lib/evg/gl/evg-webgl.js).
// Geometry is page pixels; the window drawable may be HiDPI — begin() takes both.

#ifdef __cplusplus
extern "C" {
#endif

int evggl_init(void);
void evggl_shutdown(void);

// Start a frame. pageW/H are display-list pixels; drawW/H are the GL drawable.
void evggl_begin(int pageW, int pageH, int drawW, int drawH);

// Optional white-on-transparent text atlas for this frame (RGBA).
void evggl_set_atlas(const uint8_t* rgba, int w, int h);

void evggl_rect(float x, float y, float w, float h,
                float r, float g, float b, float a, float radius);
void evggl_border(float x, float y, float w, float h, float thickness,
                  float r, float g, float b, float a, float radius);
// Studio: the same, turned rotDeg degrees about the box's centre.
void evggl_rect_rot(float x, float y, float w, float h,
                    float r, float g, float b, float a, float radius, float rotDeg);
void evggl_border_rot(float x, float y, float w, float h, float thickness,
                      float r, float g, float b, float a, float radius, float rotDeg);
void evggl_text(float x, float y, float w, float h,
                float u0, float v0, float u1, float v1,
                float r, float g, float b, float a, float rotDeg);
void evggl_image(float x, float y, float w, float h,
                 const uint8_t* rgba, int tw, int th, float radius);
// Clip everything drawn from here on to a page-coordinate rectangle; the
// caller intersects as clips nest. `evggl_clip_off` returns to unclipped.
void evggl_clip(float x, float y, float w, float h);
void evggl_clip_off(void);

void evggl_tris(const float* xy, int nFloats,
                float r, float g, float b, float a);
void evggl_tris_doubles(const double* xy, int nFloats,
                        float r, float g, float b, float a);

// Studio: a picture uploaded once under a key and drawn many times, with a
// crop (u0,v0)-(u1,v1), an opacity and a rotation in degrees.
int evggl_image_has(int key);
void evggl_image_upload(int key, const uint8_t* rgba, int tw, int th);
void evggl_image_key(float x, float y, float w, float h, int key,
                     float u0, float v0, float u1, float v1,
                     float alpha, float radius, float rotDeg);
void evggl_image_forget_all(void);
// Studio: a textured globe (RangerGames draw kind 8). (x,y,w,h) page units,
// (bx,by,bw,bh) the same box in game units, params the command's 20 numbers
// (24 with a detail window), texture `key` from evggl_image_upload.
void evggl_sphere(float x, float y, float w, float h, int key,
                  float bx, float by, float bw, float bh,
                  const double* params, int n);

// Flush batches, swap is caller's (SDL_GL_SwapWindow).
void evggl_flush(void);

#ifdef __cplusplus
}
#endif
