// SPDX-License-Identifier: AGPL-3.0-or-later
// studio_gl.cpp — EVG display list → OpenGL for Sliqtly Game Studio.
//
// Taken from EVGSheets datagrid/platform/sdl/evg_gl_native.cpp (the code
// editor's native painter, same author, AGPL). Mirrors lib/evg/gl/evg-webgl.js:
// SDF rounded rects, text atlas, path tris. Studio additions: pictures keep
// their texture between frames (a game's textures are drawn every frame and
// can be large), they take a crop (UVs), an opacity and a rotation.

#include "studio_gl.h"
#include <unordered_map>

#include <cmath>
#include <cstdio>
#include <cstring>
#include <string>
#include <vector>

#if defined(__APPLE__)
#include <OpenGL/gl3.h>
#elif defined(__arm__) || defined(__aarch64__)
#include <GLES2/gl2.h>
#define EVGGL_GLES 1
#else
#define GL_GLEXT_PROTOTYPES 1
#include <GL/gl.h>
#include <GL/glext.h>
#endif

#ifndef EVGGL_GLES
// Desktop core / compatibility entry points used below.
#endif

namespace {

enum Mode { MODE_SHAPE = 0, MODE_TEXT = 1, MODE_IMAGE = 2 };

GLuint g_prog = 0;
GLuint g_pathProg = 0;
GLuint g_vao = 0;
GLuint g_cornerVbo = 0;
GLuint g_rectVbo = 0;
GLuint g_colorVbo = 0;
GLuint g_shapeVbo = 0;
GLuint g_uvVbo = 0;
GLuint g_rotVbo = 0;
GLuint g_pathVao = 0;
GLuint g_pathVbo = 0;
GLuint g_atlasTex = 0;
GLint g_uPage = -1;
GLint g_uAtlas = -1;
GLint g_uImage = -1;
GLint g_pathPage = -1;
GLint g_pathColor = -1;

int g_pageW = 1;
int g_pageH = 1;
int g_drawW = 1;
int g_drawH = 1;

std::vector<float> g_rects;
std::vector<float> g_colors;
std::vector<float> g_shapes;
std::vector<float> g_uvs;
std::vector<float> g_rots;

struct ImageRun {
  int start = 0;
  int count = 0;
  GLuint tex = 0;
};
std::vector<ImageRun> g_imageRuns;
std::vector<GLuint> g_tempImageTex;

struct TriRun {
  std::vector<float> verts;
  float r = 0, g = 0, b = 0, a = 1;
};
std::vector<TriRun> g_triRuns;

struct QuadRun {
  int start = 0;
  int count = 0;
  GLuint tex = 0; // 0 = atlas / shape
  int isImage = 0;
  // The clip in force when this run was gathered, in PAGE coordinates.
  // `clipOn == 0` means none.
  int clipOn = 0;
  float clipX = 0.f, clipY = 0.f, clipW = 0.f, clipH = 0.f;
};
std::vector<QuadRun> g_quadRuns;
int g_runStart = 0;

// A clip is a scissor, and a scissor is pipeline state rather than
// per-instance data — so a change of clip has to END the run being gathered,
// because everything in a run is drawn by one call and one call has one
// scissor. The caller intersects as clips nest; this only records what is in
// force.
//
// Without it, a label wider than its cell was drawn in full — across whatever
// its neighbour had in it and over the row numbers — because the clip that
// said where to stop never reached the screen.
int g_clipOn = 0;
float g_clipX = 0.f, g_clipY = 0.f, g_clipW = 0.f, g_clipH = 0.f;

GLuint compile(GLenum type, const char* src) {
  GLuint s = glCreateShader(type);
  glShaderSource(s, 1, &src, nullptr);
  glCompileShader(s);
  GLint ok = GL_FALSE;
  glGetShaderiv(s, GL_COMPILE_STATUS, &ok);
  if (ok != GL_TRUE) {
    char log[1024];
    GLsizei len = 0;
    glGetShaderInfoLog(s, 1024, &len, log);
    std::fprintf(stderr, "[evggl] shader compile failed: %.*s\n", (int)len, log);
    glDeleteShader(s);
    return 0;
  }
  return s;
}

GLuint link(GLuint vs, GLuint fs) {
  GLuint p = glCreateProgram();
  glAttachShader(p, vs);
  glAttachShader(p, fs);
  glBindAttribLocation(p, 0, "aCorner");
  glBindAttribLocation(p, 1, "aRect");
  glBindAttribLocation(p, 2, "aColor");
  glBindAttribLocation(p, 3, "aShape");
  glBindAttribLocation(p, 4, "aUV");
  glBindAttribLocation(p, 5, "aRot");
  glLinkProgram(p);
  glDeleteShader(vs);
  glDeleteShader(fs);
  GLint ok = GL_FALSE;
  glGetProgramiv(p, GL_LINK_STATUS, &ok);
  if (ok != GL_TRUE) {
    char log[1024];
    GLsizei len = 0;
    glGetProgramInfoLog(p, 1024, &len, log);
    std::fprintf(stderr, "[evggl] link failed: %.*s\n", (int)len, log);
    glDeleteProgram(p);
    return 0;
  }
  return p;
}

const char* VERT = R"GLSL(
#version 150
in vec2 aCorner;
in vec4 aRect;
in vec4 aColor;
in vec3 aShape;
in vec4 aUV;
in float aRot;
uniform vec2 uPage;
out vec4 vColor;
out vec2 vLocal;
out vec2 vHalf;
out float vRadius;
out float vThickness;
out float vMode;
out vec2 vUV;
void main() {
  vec2 p = aRect.xy + aCorner * aRect.zw;
  if (aRot != 0.0) {
    vec2 c = aRect.xy + aRect.zw * 0.5;
    float s = sin(aRot), co = cos(aRot);
    vec2 d = p - c;
    p = c + vec2(d.x * co - d.y * s, d.x * s + d.y * co);
  }
  vec2 ndc = vec2((p.x / uPage.x) * 2.0 - 1.0, 1.0 - (p.y / uPage.y) * 2.0);
  gl_Position = vec4(ndc, 0.0, 1.0);
  vColor = aColor;
  vHalf = aRect.zw * 0.5;
  vLocal = (aCorner - 0.5) * aRect.zw;
  vRadius = aShape.x;
  vThickness = aShape.y;
  vMode = aShape.z;
  vUV = mix(aUV.xy, aUV.zw, aCorner);
}
)GLSL";

const char* FRAG = R"GLSL(
#version 150
in vec4 vColor;
in vec2 vLocal;
in vec2 vHalf;
in float vRadius;
in float vThickness;
in float vMode;
in vec2 vUV;
uniform sampler2D uAtlas;
uniform sampler2D uImage;
out vec4 outColor;

float sdRoundedBox(vec2 p, vec2 b, float r) {
  vec2 q = abs(p) - b + r;
  return length(max(q, 0.0)) + min(max(q.x, q.y), 0.0) - r;
}

float boxCoverage(out float d) {
  float r = min(vRadius, min(vHalf.x, vHalf.y));
  d = sdRoundedBox(vLocal, vHalf, r);
  float aa = clamp(fwidth(d), 0.35, 1.5);
  return smoothstep(aa, -aa, d);
}

void main() {
  if (vMode > 1.5) {
    vec4 tex = texture(uImage, vUV);
    float d;
    float cov = boxCoverage(d);
    if (cov <= 0.001) discard;
    outColor = vec4(tex.rgb, tex.a * cov * vColor.a);
    return;
  }
  if (vMode > 0.5) {
    // Coverage comes from a COLOUR channel, not from alpha.
    //
    // The browser builds this atlas with Canvas2D, where clearRect leaves the
    // surface transparent and fillText writes the glyph's coverage into alpha.
    // The native host builds it with SoftCanvas, which has no transparent
    // surface: `clear` writes opaque black and the blitter keeps the
    // destination opaque, so every texel of the atlas has alpha 255 and
    // sampling alpha here made every run a solid rectangle in its own colour.
    //
    // What SoftCanvas does give is exact coverage in the colour channels:
    // white glyphs composited over black leave r = g = b = coverage. So read
    // one of those. EvgGlPainter.buildAtlas paints white-on-black precisely so
    // this holds — the two have to be changed together.
    float cov = texture(uAtlas, vUV).r;
    if (cov <= 0.001) discard;
    outColor = vec4(vColor.rgb, vColor.a * cov);
    return;
  }
  float d;
  float alpha = boxCoverage(d);
  if (vThickness > 0.0) {
    float inner = -vThickness;
    float aa = clamp(fwidth(d), 0.35, 1.5);
    alpha = alpha * smoothstep(inner - aa, inner + aa, d);
  }
  if (alpha <= 0.001) discard;
  outColor = vec4(vColor.rgb, vColor.a * alpha);
}
)GLSL";

const char* PATH_VERT = R"GLSL(
#version 150
in vec2 aPos;
uniform vec2 uPage;
void main() {
  vec2 ndc = vec2((aPos.x / uPage.x) * 2.0 - 1.0, 1.0 - (aPos.y / uPage.y) * 2.0);
  gl_Position = vec4(ndc, 0.0, 1.0);
}
)GLSL";

const char* PATH_FRAG = R"GLSL(
#version 150
uniform vec4 uColor;
out vec4 outColor;
void main() { outColor = uColor; }
)GLSL";

void flushQuadRun() {
  int n = (int)(g_rects.size() / 4);
  int count = n - g_runStart;
  if (count > 0) {
    QuadRun r;
    r.start = g_runStart;
    r.count = count;
    r.tex = 0;
    r.isImage = 0;
    r.clipOn = g_clipOn;
    r.clipX = g_clipX;
    r.clipY = g_clipY;
    r.clipW = g_clipW;
    r.clipH = g_clipH;
    g_quadRuns.push_back(r);
  }
  g_runStart = n;
}

GLuint makeTex(const uint8_t* rgba, int w, int h) {
  GLuint t = 0;
  glGenTextures(1, &t);
  glBindTexture(GL_TEXTURE_2D, t);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_LINEAR);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MAG_FILTER, GL_LINEAR);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_S, GL_CLAMP_TO_EDGE);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_T, GL_CLAMP_TO_EDGE);
  glPixelStorei(GL_UNPACK_ALIGNMENT, 1);
  glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, w, h, 0, GL_RGBA, GL_UNSIGNED_BYTE, rgba);
  return t;
}

void pushInstance(float x, float y, float w, float h,
                  float r, float g, float b, float a,
                  float radius, float thick, float mode,
                  float u0, float v0, float u1, float v1, float rotDeg) {
  g_rects.push_back(x); g_rects.push_back(y); g_rects.push_back(w); g_rects.push_back(h);
  g_colors.push_back(r); g_colors.push_back(g); g_colors.push_back(b); g_colors.push_back(a);
  g_shapes.push_back(radius); g_shapes.push_back(thick); g_shapes.push_back(mode);
  g_uvs.push_back(u0); g_uvs.push_back(v0); g_uvs.push_back(u1); g_uvs.push_back(v1);
  g_rots.push_back(rotDeg * 3.14159265f / 180.f);
}

} // namespace

extern "C" int evggl_init(void) {
  if (g_prog != 0) return 1;
  GLuint vs = compile(GL_VERTEX_SHADER, VERT);
  GLuint fs = compile(GL_FRAGMENT_SHADER, FRAG);
  if (!vs || !fs) return 0;
  g_prog = link(vs, fs);
  if (!g_prog) return 0;

  GLuint pvs = compile(GL_VERTEX_SHADER, PATH_VERT);
  GLuint pfs = compile(GL_FRAGMENT_SHADER, PATH_FRAG);
  if (!pvs || !pfs) return 0;
  g_pathProg = glCreateProgram();
  glAttachShader(g_pathProg, pvs);
  glAttachShader(g_pathProg, pfs);
  glBindAttribLocation(g_pathProg, 0, "aPos");
  glLinkProgram(g_pathProg);
  glDeleteShader(pvs);
  glDeleteShader(pfs);
  GLint ok = GL_FALSE;
  glGetProgramiv(g_pathProg, GL_LINK_STATUS, &ok);
  if (ok != GL_TRUE) {
    std::fprintf(stderr, "[evggl] path program link failed\n");
    return 0;
  }

  g_uPage = glGetUniformLocation(g_prog, "uPage");
  g_uAtlas = glGetUniformLocation(g_prog, "uAtlas");
  g_uImage = glGetUniformLocation(g_prog, "uImage");
  g_pathPage = glGetUniformLocation(g_pathProg, "uPage");
  g_pathColor = glGetUniformLocation(g_pathProg, "uColor");

  glGenVertexArrays(1, &g_vao);
  glBindVertexArray(g_vao);

  const float quad[8] = {0, 0, 1, 0, 0, 1, 1, 1};
  glGenBuffers(1, &g_cornerVbo);
  glBindBuffer(GL_ARRAY_BUFFER, g_cornerVbo);
  glBufferData(GL_ARRAY_BUFFER, sizeof(quad), quad, GL_STATIC_DRAW);
  glEnableVertexAttribArray(0);
  glVertexAttribPointer(0, 2, GL_FLOAT, GL_FALSE, 0, (void*)0);
  glVertexAttribDivisor(0, 0);

  auto makeInst = [](GLuint* buf, GLuint loc, int size) {
    glGenBuffers(1, buf);
    glBindBuffer(GL_ARRAY_BUFFER, *buf);
    glBufferData(GL_ARRAY_BUFFER, 4, nullptr, GL_STREAM_DRAW);
    glEnableVertexAttribArray(loc);
    glVertexAttribPointer(loc, size, GL_FLOAT, GL_FALSE, 0, (void*)0);
    glVertexAttribDivisor(loc, 1);
  };
  makeInst(&g_rectVbo, 1, 4);
  makeInst(&g_colorVbo, 2, 4);
  makeInst(&g_shapeVbo, 3, 3);
  makeInst(&g_uvVbo, 4, 4);
  makeInst(&g_rotVbo, 5, 1);

  glGenVertexArrays(1, &g_pathVao);
  glBindVertexArray(g_pathVao);
  glGenBuffers(1, &g_pathVbo);
  glBindBuffer(GL_ARRAY_BUFFER, g_pathVbo);
  glEnableVertexAttribArray(0);
  glVertexAttribPointer(0, 2, GL_FLOAT, GL_FALSE, 0, (void*)0);

  glGenTextures(1, &g_atlasTex);
  glBindTexture(GL_TEXTURE_2D, g_atlasTex);
  unsigned char px[4] = {0, 0, 0, 0};
  glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, 1, 1, 0, GL_RGBA, GL_UNSIGNED_BYTE, px);

  glBindVertexArray(0);
  return 1;
}

extern "C" void evggl_shutdown(void) {
  // Soft teardown; process exit also releases GL.
  g_prog = 0;
}

static void studio_draw_sphere(int idx);
static void studio_spheres_clear();

extern "C" void evggl_begin(int pageW, int pageH, int drawW, int drawH) {
  studio_spheres_clear();
  g_pageW = pageW > 0 ? pageW : 1;
  g_pageH = pageH > 0 ? pageH : 1;
  g_drawW = drawW > 0 ? drawW : g_pageW;
  g_drawH = drawH > 0 ? drawH : g_pageH;
  g_rects.clear();
  g_colors.clear();
  g_shapes.clear();
  g_uvs.clear();
  g_rots.clear();
  g_quadRuns.clear();
  g_triRuns.clear();
  g_runStart = 0;
  g_clipOn = 0;
  for (GLuint t : g_tempImageTex) glDeleteTextures(1, &t);
  g_tempImageTex.clear();
  g_imageRuns.clear();
}

extern "C" void evggl_set_atlas(const uint8_t* rgba, int w, int h) {
  if (!rgba || w <= 0 || h <= 0) return;
  glBindTexture(GL_TEXTURE_2D, g_atlasTex);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_LINEAR);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MAG_FILTER, GL_LINEAR);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_S, GL_CLAMP_TO_EDGE);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_T, GL_CLAMP_TO_EDGE);
  glPixelStorei(GL_UNPACK_ALIGNMENT, 1);
  glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, w, h, 0, GL_RGBA, GL_UNSIGNED_BYTE, rgba);
}

extern "C" void evggl_rect(float x, float y, float w, float h,
                           float r, float g, float b, float a, float radius) {
  pushInstance(x, y, w, h, r, g, b, a, radius, 0.f, (float)MODE_SHAPE, 0, 0, 0, 0, 0);
}

extern "C" void evggl_border(float x, float y, float w, float h, float thickness,
                             float r, float g, float b, float a, float radius) {
  pushInstance(x, y, w, h, r, g, b, a, radius, thickness, (float)MODE_SHAPE, 0, 0, 0, 0, 0);
}

extern "C" void evggl_rect_rot(float x, float y, float w, float h,
                               float r, float g, float b, float a, float radius, float rotDeg) {
  pushInstance(x, y, w, h, r, g, b, a, radius, 0.f, (float)MODE_SHAPE, 0, 0, 0, 0, rotDeg);
}

extern "C" void evggl_border_rot(float x, float y, float w, float h, float thickness,
                                 float r, float g, float b, float a, float radius, float rotDeg) {
  pushInstance(x, y, w, h, r, g, b, a, radius, thickness, (float)MODE_SHAPE, 0, 0, 0, 0, rotDeg);
}

extern "C" void evggl_text(float x, float y, float w, float h,
                           float u0, float v0, float u1, float v1,
                           float r, float g, float b, float a, float rotDeg) {
  pushInstance(x, y, w, h, r, g, b, a, 0, 0, (float)MODE_TEXT, u0, v0, u1, v1, rotDeg);
}

extern "C" void evggl_image(float x, float y, float w, float h,
                            const uint8_t* rgba, int tw, int th, float radius) {
  if (!rgba || tw <= 0 || th <= 0) return;
  flushQuadRun();
  GLuint tex = makeTex(rgba, tw, th);
  g_tempImageTex.push_back(tex);
  pushInstance(x, y, w, h, 1, 1, 1, 1, radius, 0, (float)MODE_IMAGE, 0, 0, 1, 1, 0);
  QuadRun run;
  run.start = (int)(g_rects.size() / 4) - 1;
  run.count = 1;
  run.tex = tex;
  run.isImage = 1;
  run.clipOn = g_clipOn;
  run.clipX = g_clipX; run.clipY = g_clipY;
  run.clipW = g_clipW; run.clipH = g_clipH;
  g_quadRuns.push_back(run);
  g_runStart = (int)(g_rects.size() / 4);
}

// The clip in force from here on, in page coordinates. The caller has already
// intersected it with any enclosing clip; passing w or h of zero means an
// empty region and nothing after it draws until the clip is cleared.
extern "C" void evggl_clip(float x, float y, float w, float h) {
  flushQuadRun();
  g_clipOn = 1;
  g_clipX = x; g_clipY = y; g_clipW = w; g_clipH = h;
}

extern "C" void evggl_clip_off(void) {
  flushQuadRun();
  g_clipOn = 0;
}

extern "C" void evggl_tris(const float* xy, int nFloats,
                           float r, float g, float b, float a) {
  if (!xy || nFloats < 6) return;
  flushQuadRun();
  TriRun tr;
  tr.verts.assign(xy, xy + nFloats);
  tr.r = r; tr.g = g; tr.b = b; tr.a = a;
  g_triRuns.push_back(std::move(tr));
  int triIndex = (int)g_triRuns.size() - 1;
  QuadRun marker;
  marker.start = -1 - triIndex;
  marker.count = 0;
  marker.tex = 0;
  marker.isImage = 2;
  marker.clipOn = g_clipOn;
  marker.clipX = g_clipX; marker.clipY = g_clipY;
  marker.clipW = g_clipW; marker.clipH = g_clipH;
  g_quadRuns.push_back(marker);
}

extern "C" void evggl_tris_doubles(const double* xy, int nFloats,
                                   float r, float g, float b, float a) {
  if (!xy || nFloats < 6) return;
  std::vector<float> f;
  f.reserve((size_t)nFloats);
  for (int i = 0; i < nFloats; ++i) f.push_back((float)xy[i]);
  evggl_tris(f.data(), nFloats, r, g, b, a);
}

extern "C" void evggl_flush(void) {
  if (g_prog == 0) {
    if (!evggl_init()) return;
  }
  flushQuadRun();

  glViewport(0, 0, g_drawW, g_drawH);
  glDisable(GL_DEPTH_TEST);
  glEnable(GL_BLEND);
  glBlendFunc(GL_SRC_ALPHA, GL_ONE_MINUS_SRC_ALPHA);
  glClearColor(0.92f, 0.93f, 0.95f, 1.f);
  glClear(GL_COLOR_BUFFER_BIT);

  auto upload = [](GLuint buf, const std::vector<float>& data) {
    glBindBuffer(GL_ARRAY_BUFFER, buf);
    glBufferData(GL_ARRAY_BUFFER, (GLsizeiptr)(data.size() * sizeof(float)),
                 data.empty() ? nullptr : data.data(), GL_STREAM_DRAW);
  };
  upload(g_rectVbo, g_rects);
  upload(g_colorVbo, g_colors);
  upload(g_shapeVbo, g_shapes);
  upload(g_uvVbo, g_uvs);
  upload(g_rotVbo, g_rots);

  auto pointAt = [](int first) {
    auto bind = [&](GLuint buf, GLuint loc, int size) {
      glBindBuffer(GL_ARRAY_BUFFER, buf);
      glVertexAttribPointer(loc, size, GL_FLOAT, GL_FALSE, 0,
                            (void*)(intptr_t)(first * size * (int)sizeof(float)));
    };
    bind(g_rectVbo, 1, 4);
    bind(g_colorVbo, 2, 4);
    bind(g_shapeVbo, 3, 3);
    bind(g_uvVbo, 4, 4);
    bind(g_rotVbo, 5, 1);
  };

  // A page-coordinate clip becomes a scissor box in DRAWABLE pixels, measured
  // from the BOTTOM — which is where GL puts the origin and the page does not.
  const float sxScale = (float)g_drawW / (float)g_pageW;
  const float syScale = (float)g_drawH / (float)g_pageH;
  bool scissorOn = false;
  auto applyClip = [&](const QuadRun& run) {
    if (!run.clipOn) {
      if (scissorOn) { glDisable(GL_SCISSOR_TEST); scissorOn = false; }
      return;
    }
    if (!scissorOn) { glEnable(GL_SCISSOR_TEST); scissorOn = true; }
    int sx = (int)lroundf(run.clipX * sxScale);
    int sy = (int)lroundf(((float)g_pageH - (run.clipY + run.clipH)) * syScale);
    int sw = (int)lroundf(run.clipW * sxScale);
    int sh = (int)lroundf(run.clipH * syScale);
    if (sw < 0) sw = 0;
    if (sh < 0) sh = 0;
    glScissor(sx, sy, sw, sh);
  };

  int triCursor = 0;
  for (const QuadRun& run : g_quadRuns) {
    applyClip(run);
    if (run.isImage == 2) {
      // Tri marker: start = -1 - index
      int idx = -1 - run.start;
      if (idx >= 0 && idx < (int)g_triRuns.size()) {
        const TriRun& tr = g_triRuns[(size_t)idx];
        glUseProgram(g_pathProg);
        glBindVertexArray(g_pathVao);
        glUniform2f(g_pathPage, (float)g_pageW, (float)g_pageH);
        glUniform4f(g_pathColor, tr.r, tr.g, tr.b, tr.a);
        glBindBuffer(GL_ARRAY_BUFFER, g_pathVbo);
        glBufferData(GL_ARRAY_BUFFER, (GLsizeiptr)(tr.verts.size() * sizeof(float)),
                     tr.verts.data(), GL_STREAM_DRAW);
        glDrawArrays(GL_TRIANGLES, 0, (GLsizei)(tr.verts.size() / 2));
      }
      (void)triCursor;
      continue;
    }
    if (run.isImage == 3) {
      studio_draw_sphere(-1 - run.start);
      continue;
    }
    if (run.count <= 0) continue;
    glUseProgram(g_prog);
    glBindVertexArray(g_vao);
    glUniform2f(g_uPage, (float)g_pageW, (float)g_pageH);
    glUniform1i(g_uAtlas, 0);
    glUniform1i(g_uImage, 1);
    glActiveTexture(GL_TEXTURE0);
    glBindTexture(GL_TEXTURE_2D, g_atlasTex);
    if (run.isImage && run.tex) {
      glActiveTexture(GL_TEXTURE1);
      glBindTexture(GL_TEXTURE_2D, run.tex);
    }
    pointAt(run.start);
    glDrawArraysInstanced(GL_TRIANGLE_STRIP, 0, 4, run.count);
  }

  if (scissorOn) glDisable(GL_SCISSOR_TEST);
  // Any trailing tris that were not interleaved (none expected).
  glBindVertexArray(0);
}


// --- pictures that stay on the GPU ---------------------------------------------
// Keyed by the caller's number for the picture (Studio's image cache index).
// The first draw uploads; later draws only draw.
static std::unordered_map<int, GLuint> g_cachedTex;

extern "C" int evggl_image_has(int key) {
  return g_cachedTex.count(key) ? 1 : 0;
}

extern "C" void evggl_image_upload(int key, const uint8_t* rgba, int tw, int th) {
  if (!rgba || tw <= 0 || th <= 0) return;
  auto it = g_cachedTex.find(key);
  if (it != g_cachedTex.end()) glDeleteTextures(1, &it->second);
  GLuint t = makeTex(rgba, tw, th);
  // big textures drawn small (a planet far away) need mipmaps or they shimmer
  glBindTexture(GL_TEXTURE_2D, t);
  glGenerateMipmap(GL_TEXTURE_2D);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_LINEAR_MIPMAP_LINEAR);
  g_cachedTex[key] = t;
}

extern "C" void evggl_image_key(float x, float y, float w, float h, int key,
                                float u0, float v0, float u1, float v1,
                                float alpha, float radius, float rotDeg) {
  auto it = g_cachedTex.find(key);
  if (it == g_cachedTex.end()) return;
  flushQuadRun();
  pushInstance(x, y, w, h, 1, 1, 1, alpha, radius, 0, (float)MODE_IMAGE, u0, v0, u1, v1, rotDeg);
  QuadRun run;
  run.start = (int)(g_rects.size() / 4) - 1;
  run.count = 1;
  run.tex = it->second;
  run.isImage = 1;
  run.clipOn = g_clipOn;
  run.clipX = g_clipX; run.clipY = g_clipY;
  run.clipW = g_clipW; run.clipH = g_clipH;
  g_quadRuns.push_back(run);
  g_runStart = (int)(g_rects.size() / 4);
}

extern "C" void evggl_image_forget_all(void) {
  for (auto& kv : g_cachedTex) glDeleteTextures(1, &kv.second);
  g_cachedTex.clear();
}


// --- textured globes (RangerGames draw kind 8) --------------------------------
// The same ray-cast as RangerGames web/js/painter.js (SPHERE_FS): every pixel
// of the box looks along fwd*focal + rgt*(px - w/2) - upv*(py - h/2) and reads
// the map at the hit point's longitude and latitude. `box` is the command's
// rectangle in GAME units (the camera numbers are in game units too); the quad
// itself is placed in page units.
namespace {
struct Sphere {
  float x, y, w, h;      // page units
  float box[4];          // game units
  float p[40];           // the command's numbers (20, 24, 32 or 40 of them)
  int n;
  GLuint tex, ltex, htex, gtex;  // map, city lights, shading heights, shape heights (0: none)
};
std::vector<Sphere> g_spheres;
GLuint g_sphereProg = 0, g_sphereVao = 0, g_sphereVbo = 0;
const char* SU_NAMES[] = {"tex", "ltex", "htex", "gtex", "eye", "rgt", "upv", "fwd", "sun", "airc", "box", "win", "view",
                          "focal", "night", "lit", "useWin", "air", "lgain", "useL", "relief",
                          "useH", "hlo", "hhi", "hamp", "nearM", "hrel", "hfreq", "crat", "mapAz", "mapK", "uRect", "uPage"};
enum { SU_TEX, SU_LTEX, SU_HTEX, SU_GTEX, SU_EYE, SU_RGT, SU_UPV, SU_FWD, SU_SUN, SU_AIRC, SU_BOX, SU_WIN, SU_VIEW,
       SU_FOCAL, SU_NIGHT, SU_LIT, SU_USEWIN, SU_AIR, SU_LGAIN, SU_USEL, SU_RELIEF,
       SU_USEH, SU_HLO, SU_HHI, SU_HAMP, SU_NEARM, SU_HREL, SU_HFREQ, SU_CRAT, SU_MAPAZ, SU_MAPK, SU_RECT, SU_PAGE, SU_COUNT };
GLint g_su[SU_COUNT];

const char* SPHERE_VERT = R"GLSL(
#version 150
in vec2 aCorner;
uniform vec4 uRect;
uniform vec2 uPage;
out vec2 uv;
void main() {
  uv = aCorner;
  vec2 p = uRect.xy + aCorner * uRect.zw;
  gl_Position = vec4((p.x / uPage.x) * 2.0 - 1.0, 1.0 - (p.y / uPage.y) * 2.0, 0.0, 1.0);
}
)GLSL";

// web/js/painter.js SPHERE_FS (RangerGames), as GLSL 1.50: keep the two the same
const char* SPHERE_FRAG = R"GLSL(
#version 150
in vec2 uv;
out vec4 o;
uniform sampler2D tex, ltex, htex, gtex; uniform vec3 eye, rgt, upv, fwd, sun, airc; uniform vec4 box, win; uniform vec2 view;
uniform float focal, night, lit, useWin, air, lgain, useL, relief;
uniform float useH, hlo, hhi, hamp, nearM, hrel, hfreq, crat, mapAz, mapK;
vec4 airGlow(vec3 d, vec3 lo, float alt, float len, bool hit) {  float up = dot(normalize(lo), sun);  float day = smoothstep(-0.28, 0.12, up);  float fw = pow(max(dot(d, sun), 0.0), 10.0) * smoothstep(-0.45, 0.02, up);  float dusk = smoothstep(-0.3, -0.02, up) * (1.0 - smoothstep(0.0, 0.25, up));  float dens = exp(-alt * 4.5) * (1.0 - alt);  float depth = dens * len / (air * 2.0);  float a = clamp(depth * (0.55 * day + 2.6 * fw + 0.25 * dusk), 0.0, 1.0);  vec3 col = airc * (0.7 + 0.6 * day) + vec3(1.0, 0.96, 0.9) * fw * 3.0 + vec3(1.0, 0.55, 0.3) * dusk * 0.8;  return vec4(col, a);}
float lum(vec2 t) { vec3 c = texture(tex, t).rgb; return dot(c, vec3(0.3, 0.5, 0.2)); }
float lumAt(vec3 q, float eps, float more) { vec2 t = vec2(atan(q.y, q.x) / 6.2831853 + 0.5, 0.5 - asin(clamp(q.z, -1.0, 1.0)) / 3.1415927);  float span = 1.0;  if (useWin > 0.5) { if (t.x < win.x) t.x += 1.0; else if (t.x > win.z) t.x -= 1.0; span = win.z - win.x; t = (t - win.xy) / (win.zw - win.xy); }  float ta = 6.2831853 * span / float(textureSize(tex, 0).x);  return dot(textureLod(tex, t, max(0.0, log2(eps / ta)) + more).rgb, vec3(0.3, 0.5, 0.2)); }
vec2 lonlat(vec3 p) { return vec2(atan(p.y, p.x) / 6.2831853 + 0.5, 0.5 - asin(clamp(p.z, -1.0, 1.0)) / 3.1415927); }
// GLSL 1.50 leaves uint(negative int) undefined (Mesa gives 0): keep the bits by hand
uint u32(int x) { return uint(x + 1073741824) - 1073741824u; }
uint hsh(ivec3 c) { uint h = (u32(c.x) * 0x8da6b343u) ^ (u32(c.y) * 0xd8163841u) ^ (u32(c.z) * 0xcb1ab31fu);  h ^= h >> 16u; h *= 0x7feb352du; h ^= h >> 15u; h *= 0x846ca68bu; h ^= h >> 16u; return h; }
float hv(ivec3 c) { return float(hsh(c) >> 8u) / 16777215.0; }
float vnoise(vec3 x) { vec3 f = floor(x); ivec3 c = ivec3(f); vec3 t = x - f; t = t * t * (3.0 - 2.0 * t);  float a = mix(hv(c), hv(c + ivec3(1, 0, 0)), t.x), b = mix(hv(c + ivec3(0, 1, 0)), hv(c + ivec3(1, 1, 0)), t.x);  float e = mix(hv(c + ivec3(0, 0, 1)), hv(c + ivec3(1, 0, 1)), t.x), g = mix(hv(c + ivec3(0, 1, 1)), hv(c + ivec3(1, 1, 1)), t.x);  return mix(mix(a, b, t.y), mix(e, g, t.y), t.z); }
float crater(vec3 p, float f, int o) { vec3 x = p * f + vec3(0.37, 0.61, 0.19) * float(o & 1); vec3 fc = floor(x); ivec3 c = ivec3(fc);  uint h = hsh(ivec3(c.x + 1000 * (o + 1), c.y, c.z)); if ((h & 255u) >= 170u) return 0.0;  uint g = hsh(ivec3(c.x, c.y + 7919, c.z + 31 * o));  vec3 j = vec3(float((h >> 8u) & 255u), float((h >> 16u) & 255u), float((g >> 8u) & 255u)) / 255.0 * 0.5 + 0.25;  float rr = 0.08 + 0.1 * float(g & 255u) / 255.0; float dd = length(x - fc - j) / rr;  if (dd >= 1.4) return 0.0;  float k = rr / f; float b = dd < 1.0 ? 0.4 * (dd * dd - 1.0) + 0.08 * smoothstep(0.6, 1.0, dd) : 0.08 * (1.0 - smoothstep(1.0, 1.4, dd));  return b * k; }
float bumps(vec3 p, int n) { if (hamp <= 0.0) return 0.0; float s = 0.0, a = 1.0, f = hfreq;  for (int k = 0; k < 9; k++) { if (k >= n) break; s += a * (2.0 * vnoise(p * f) - 1.0); a *= 0.48; f *= 2.03; }  float cr = 0.0; if (crat > 0.0) { float cf = hfreq * 0.9; int nc = n >= 5 ? n - 2 : max(n - 2, 1);    for (int k = 0; k < 7; k++) { if (k >= nc) break; cr += crater(p, cf, 2 * k) + crater(p, cf, 2 * k + 1); cf *= 2.7; } }  return s * hamp + cr * crat; }
float dec(vec4 c) { return (c.r * 65280.0 + c.g * 255.0) / 65535.0; }
float shape(vec2 t) { ivec2 sz = textureSize(gtex, 0); vec2 x = t * vec2(sz) - 0.5; vec2 f = floor(x); vec2 a = x - f;  int i0 = int(f.x) % sz.x; if (i0 < 0) i0 += sz.x; int i1 = (i0 + 1) % sz.x;  int j0 = clamp(int(f.y), 0, sz.y - 1), j1 = clamp(int(f.y) + 1, 0, sz.y - 1);  float top = mix(dec(texelFetch(gtex, ivec2(i0, j0), 0)), dec(texelFetch(gtex, ivec2(i1, j0), 0)), a.x);  float bot = mix(dec(texelFetch(gtex, ivec2(i0, j1), 0)), dec(texelFetch(gtex, ivec2(i1, j1), 0)), a.x);  return mix(top, bot, a.y); }
float ground(vec3 p, int n) { return 1.0 + hlo + (hhi - hlo) * shape(lonlat(p)) + bumps(p, n); }
float shadeH(vec3 p, float lod) { return (hhi - hlo) * dec(textureLod(htex, lonlat(p), lod)); }
bool march(vec3 d, float pix, out float t) {  float Ro = 1.0 + hhi + 2.0 * hamp + 0.05 * crat / hfreq, Ri = 1.0 + hlo - 2.0 * hamp - 0.25 * crat / hfreq;  float bq = dot(eye, d), ee = dot(eye, eye); float ds = bq * bq - ee + Ro * Ro;  t = 0.0; if (ds < 0.0) return false;  float t0 = max(0.0, -bq - sqrt(ds)), t1 = -bq + sqrt(ds);  float di = bq * bq - ee + Ri * Ri; if (di > 0.0 && -bq - sqrt(di) > 0.0) t1 = min(t1, -bq - sqrt(di));  if (t1 <= t0) return false;  float tp = t0; t = t0; bool hit = false;  for (int i = 0; i < 160; i++) {    vec3 x = eye + d * t; float r = length(x); float f = r - ground(x / r, 3);    if (f < 0.0) { hit = true; break; }    if (f < t * pix * 0.3) { hit = true; tp = t; break; }    tp = t; t += max(f * 0.55, t * pix * 0.4);    if (t > t1) return false;  }  if (!hit) return false;  float a = tp, b = t;  for (int i = 0; i < 7; i++) { float m = (a + b) * 0.5; vec3 x = eye + d * m; float r = length(x);    if (r - ground(x / r, 5) < 0.0) b = m; else a = m; }  t = b; return true;}
float sunRay(vec3 x, float sz) {  float k = 1.0, t = max(sz * 4.0, 2e-6);  for (int i = 0; i < 24; i++) { vec3 y = x + sun * t; float r = length(y); if (r > 1.0 + hhi + 2.0 * hamp + 0.05 * crat / hfreq) break;    float h = r - ground(y / r, 3); k = min(k, 10.0 * h / t); if (k < 0.0) return 0.0; t *= 1.35; }  return clamp(k, 0.0, 1.0);}
void main() {  vec2 px = box.xy + uv * box.zw;  vec3 d = normalize(fwd * focal + rgt * (px.x - view.x * 0.5) - upv * (px.y - view.y * 0.5));  float b = dot(eye, d); vec3 q = eye - b * d; float q2 = dot(q, q); float disc = 1.0 - q2;  bool terr = useH > 0.5 && nearM > 0.5; float pix = 1.0 / focal; float th = 0.0;  if (terr && useWin > 0.5) { vec3 pw = normalize(disc >= 0.0 ? eye + d * (-b - sqrt(disc)) : q); vec2 w0 = lonlat(pw);    float mg = 0.004 + 0.5 * max(hhi - hlo, 0.0); float uw = w0.x; if (uw < win.x - mg) uw += 1.0; else if (uw > win.z + mg) uw -= 1.0;    if (uw < win.x - mg || uw > win.z + mg || w0.y < win.y - mg || w0.y > win.w + mg) discard; }  bool hit = terr ? march(d, pix, th) : (disc >= 0.0 && -b >= sqrt(max(disc, 0.0)));  if (!hit) {    float R = 1.0 + air;    if (air <= 0.0 || useWin > 0.5 || q2 >= R * R || -b < 0.0) discard;    float hq = sqrt(q2);    vec4 g = airGlow(d, q, max(hq - 1.0, 0.0) / air, 2.0 * sqrt(R * R - q2), false);    if (g.a < 0.003) discard;    o = vec4(min(g.rgb, vec3(1.0)) * g.a, g.a); return;  }  if (!terr) th = -b - sqrt(max(disc, 0.0));  vec3 p = normalize(eye + d * th);  vec2 uvw = lonlat(p); float u = uvw.x, v = uvw.y;  float u2 = fract(u + 0.5) - 0.5; vec2 du = vec2(dFdx(u), dFdy(u)); vec2 du2 = vec2(dFdx(u2), dFdy(u2));  if (dot(du2, du2) < dot(du, du)) du = du2;  vec2 dv = vec2(dFdx(v), dFdy(v));  float fp = max(th * pix, 1e-7);  if (terr) { float k = fp / 6.2831853 / max(0.05, sqrt(1.0 - p.z * p.z)); du = vec2(k, 0.0); dv = vec2(0.0, fp / 3.1415927); }  if (useWin > 0.5) {    float uw = u; if (uw < win.x) uw += 1.0; else if (uw > win.z) uw -= 1.0;    if (uw < win.x || uw > win.z || v < win.y || v > win.w) discard;    vec2 k = 1.0 / (win.zw - win.xy); u = (uw - win.x) * k.x; v = (v - win.y) * k.y; du *= k.x; dv *= k.y;  }  vec3 c = textureGrad(tex, vec2(u, v), vec2(du.x, dv.x), vec2(du.y, dv.y)).rgb;  vec3 n = p;  vec3 east = normalize(vec3(-p.y, p.x, 0.0) + vec3(1e-6, 0.0, 0.0)); vec3 north = cross(p, east);  if (useH > 0.5) {    float ta = 6.2831853 / float(textureSize(htex, 0).x); float eps = max(fp, ta * 0.5);    float lod = max(0.0, log2(eps / ta)); int nb = 0;    if (terr) { nb = 5; float wl = 1.0 / (hfreq * pow(2.03, 5.0)); for (int k = 0; k < 3; k++) { if (wl < fp * 3.0) break; nb++; wl /= 2.03; } }    float gx = (shadeH(normalize(p + east * eps), lod) - shadeH(normalize(p - east * eps), lod)) / (2.0 * eps);    float gy = (shadeH(normalize(p + north * eps), lod) - shadeH(normalize(p - north * eps), lod)) / (2.0 * eps);    vec3 tl = hrel * (gx * east + gy * north);    if (nb > 0) { float eb = max(fp, 1e-7); float b0 = bumps(p, nb);      tl += (bumps(normalize(p + east * eb), nb) - b0) / eb * east + (bumps(normalize(p + north * eb), nb) - b0) / eb * north; }    if (terr && mapK > 0.0) { float l0 = lumAt(p, fp, 0.0), lb = lumAt(p, fp, 3.0); float sl = clamp((l0 - lb) / mapK, -1.5, 1.5);      tl += sl * (cos(mapAz) * east + sin(mapAz) * north); c *= clamp(lb / max(l0, 0.02), 0.5, 2.0); }    n = normalize(p - tl);  } else if (relief != 0.0) {    vec2 ts = 1.0 / vec2(textureSize(tex, 0)); float clat = max(0.05, sqrt(1.0 - p.z * p.z));    float hx = (lum(vec2(u + ts.x, v)) - lum(vec2(u - ts.x, v))) / (2.0 * ts.x * 6.2831853 * clat);    float hy = (lum(vec2(u, v - ts.y)) - lum(vec2(u, v + ts.y))) / (2.0 * ts.y * 3.1415927);    n = normalize(p - relief * (hx * east + hy * north));  }  float s = dot(p, sun);  if (lit > 0.5 && useH > 0.5) {    float sn = dot(n, sun); float k = smoothstep(-0.04, terr ? 0.22 : 0.3, s) * clamp(sn / max(s, 0.06), 0.0, 1.6);    if (terr && k > 0.0 && sn > 0.0) k *= sunRay(eye + d * th, fp);    c *= mix(1.0 - night, 1.0, min(k, 1.25));  } else if (lit > 0.5) { float sn = dot(n, sun); float k = smoothstep(-0.06, 0.3, s); if (relief != 0.0) k *= smoothstep(-0.25, 0.2, sn) * (0.55 + 0.45 * smoothstep(0.0, 0.6, sn / max(s, 0.05)));    c *= mix(1.0 - night, 1.0, k) * (0.8 + 0.2 * max(sn, 0.0)); }  if (useL > 0.5 && lit > 0.5) {    vec3 L = texture(ltex, lonlat(p)).rgb; c += L * vec3(1.0, 0.88, 0.65) * lgain * (1.0 - smoothstep(-0.12, 0.06, s));  }  if (air > 0.0 && useWin < 0.5) {    float R = 1.0 + air; float t0 = -b - sqrt(max(R * R - q2, 0.0));    vec4 g = airGlow(d, p, 0.0, max(th - max(t0, 0.0), 0.0), true);    c = mix(c, g.rgb, g.a * 0.85);  }  o = vec4(min(c, vec3(1.0)), 1.0);}
)GLSL";

bool sphereInit() {
  if (g_sphereProg) return true;
  GLuint vs = compile(GL_VERTEX_SHADER, SPHERE_VERT);
  GLuint fs = compile(GL_FRAGMENT_SHADER, SPHERE_FRAG);
  if (!vs || !fs) return false;
  GLuint p = glCreateProgram();
  glAttachShader(p, vs);
  glAttachShader(p, fs);
  glBindAttribLocation(p, 0, "aCorner");
  glLinkProgram(p);
  glDeleteShader(vs);
  glDeleteShader(fs);
  GLint ok = GL_FALSE;
  glGetProgramiv(p, GL_LINK_STATUS, &ok);
  if (ok != GL_TRUE) { std::fprintf(stderr, "[evggl] sphere link failed\n"); return false; }
  g_sphereProg = p;
  for (int i = 0; i < SU_COUNT; i++) g_su[i] = glGetUniformLocation(p, SU_NAMES[i]);
  const float corners[] = {0, 0, 1, 0, 0, 1, 1, 1};
  glGenVertexArrays(1, &g_sphereVao);
  glBindVertexArray(g_sphereVao);
  glGenBuffers(1, &g_sphereVbo);
  glBindBuffer(GL_ARRAY_BUFFER, g_sphereVbo);
  glBufferData(GL_ARRAY_BUFFER, sizeof(corners), corners, GL_STATIC_DRAW);
  glEnableVertexAttribArray(0);
  glVertexAttribPointer(0, 2, GL_FLOAT, GL_FALSE, 0, nullptr);
  glBindVertexArray(0);
  return true;
}
} // namespace

static void studio_spheres_clear() { g_spheres.clear(); }

static void bindTex(int unit, GLuint t) {
  glActiveTexture(GL_TEXTURE0 + unit);
  glBindTexture(GL_TEXTURE_2D, t);
}

static void studio_draw_sphere(int idx) {
  if (idx < 0 || idx >= (int)g_spheres.size() || !sphereInit()) return;
  const Sphere& s = g_spheres[(size_t)idx];
  const float* p = s.p;
  bool w32 = s.n >= 32, w40 = s.n >= 40 && s.htex && s.gtex;
  bool useWin = w32 ? p[24] > 0.5f : s.n >= 24;
  glUseProgram(g_sphereProg);
  glBindVertexArray(g_sphereVao);
  bindTex(0, s.tex);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_S, useWin ? GL_CLAMP_TO_EDGE : GL_REPEAT);
  bindTex(1, s.ltex);
  bindTex(2, w40 ? s.htex : 0);
  bindTex(3, w40 ? s.gtex : 0);
  // the shape is read bilinearly at its own size (as the game reads it)
  if (w40) glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_LINEAR);
  glActiveTexture(GL_TEXTURE0);
  for (int i = 0; i < 4; i++) glUniform1i(g_su[SU_TEX + i], i);
  glUniform3f(g_su[SU_EYE], p[0], p[1], p[2]);
  glUniform3f(g_su[SU_RGT], p[3], p[4], p[5]);
  glUniform3f(g_su[SU_UPV], p[6], p[7], p[8]);
  glUniform3f(g_su[SU_FWD], p[9], p[10], p[11]);
  glUniform3f(g_su[SU_SUN], p[12], p[13], p[14]);
  glUniform1f(g_su[SU_FOCAL], p[15]);
  glUniform2f(g_su[SU_VIEW], p[16], p[17]);
  glUniform1f(g_su[SU_NIGHT], p[18]);
  glUniform1f(g_su[SU_LIT], p[19]);
  glUniform1f(g_su[SU_USEWIN], useWin ? 1.f : 0.f);
  glUniform4f(g_su[SU_WIN], useWin ? p[20] : 0.f, useWin ? p[21] : 0.f, useWin ? p[22] : 1.f, useWin ? p[23] : 1.f);
  glUniform1f(g_su[SU_AIR], w32 ? p[25] : 0.f);
  glUniform3f(g_su[SU_AIRC], w32 ? p[26] : 0.f, w32 ? p[27] : 0.f, w32 ? p[28] : 0.f);
  glUniform1f(g_su[SU_LGAIN], w32 ? p[29] : 0.f);
  glUniform1f(g_su[SU_USEL], s.ltex ? 1.f : 0.f);
  glUniform1f(g_su[SU_RELIEF], w32 ? p[30] : 0.f);
  glUniform1f(g_su[SU_USEH], w40 ? 1.f : 0.f);
  glUniform1f(g_su[SU_HLO], w40 ? p[32] : 0.f);
  glUniform1f(g_su[SU_HHI], w40 ? p[33] : 0.f);
  glUniform1f(g_su[SU_HAMP], w40 ? p[34] : 0.f);
  glUniform1f(g_su[SU_NEARM], w40 ? p[35] : 0.f);
  glUniform1f(g_su[SU_HREL], w40 ? p[36] : 0.f);
  glUniform1f(g_su[SU_HFREQ], w40 ? p[37] : 1.f);
  glUniform1f(g_su[SU_CRAT], w40 ? p[38] : 0.f);
  glUniform1f(g_su[SU_MAPAZ], w40 && useWin ? p[31] : 0.f);
  glUniform1f(g_su[SU_MAPK], w40 && useWin ? p[39] : 0.f);
  glUniform4f(g_su[SU_BOX], s.box[0], s.box[1], s.box[2], s.box[3]);
  glUniform4f(g_su[SU_RECT], s.x, s.y, s.w, s.h);
  glUniform2f(g_su[SU_PAGE], (float)g_pageW, (float)g_pageH);
  // the glowing air comes out premultiplied
  glBlendFunc(GL_ONE, GL_ONE_MINUS_SRC_ALPHA);
  glDrawArrays(GL_TRIANGLE_STRIP, 0, 4);
  glBlendFunc(GL_SRC_ALPHA, GL_ONE_MINUS_SRC_ALPHA);
  glBindVertexArray(0);
}

static GLuint cachedTex(double key) {
  if (key <= 0) return 0;
  auto it = g_cachedTex.find((int)key);
  return it == g_cachedTex.end() ? 0 : it->second;
}

// params: the command's numbers, then three picture keys (city lights,
// shading heights, shape heights; 0: none), so n is the count + 3
extern "C" void evggl_sphere(float x, float y, float w, float h, int key,
                             float bx, float by, float bw, float bh,
                             const double* params, int n) {
  auto it = g_cachedTex.find(key);
  int np = n - 3;
  if (it == g_cachedTex.end() || np < 20) return;
  flushQuadRun();
  Sphere s{};
  s.x = x; s.y = y; s.w = w; s.h = h;
  s.box[0] = bx; s.box[1] = by; s.box[2] = bw; s.box[3] = bh;
  s.n = np > 40 ? 40 : np;
  for (int i = 0; i < s.n; i++) s.p[i] = (float)params[i];
  s.tex = it->second;
  s.ltex = cachedTex(params[np]);
  s.htex = cachedTex(params[np + 1]);
  s.gtex = cachedTex(params[np + 2]);
  g_spheres.push_back(s);
  QuadRun run;
  run.start = -1 - (int)(g_spheres.size() - 1);
  run.count = 0;
  run.isImage = 3;
  run.clipOn = g_clipOn;
  run.clipX = g_clipX; run.clipY = g_clipY;
  run.clipW = g_clipW; run.clipH = g_clipH;
  g_quadRuns.push_back(run);
  g_runStart = (int)(g_rects.size() / 4);
}
