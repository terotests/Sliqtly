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
  float p[20];
  float win[4];
  int useWin;
  GLuint tex;
};
std::vector<Sphere> g_spheres;
GLuint g_sphereProg = 0, g_sphereVao = 0, g_sphereVbo = 0;
GLint g_su[16];
enum { SU_TEX, SU_EYE, SU_RGT, SU_UPV, SU_FWD, SU_SUN, SU_BOX, SU_VIEW, SU_FOCAL, SU_NIGHT, SU_LIT, SU_WIN, SU_USEWIN, SU_RECT, SU_PAGE };

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

const char* SPHERE_FRAG = R"GLSL(
#version 150
in vec2 uv;
out vec4 o;
uniform sampler2D tex;
uniform vec3 eye, rgt, upv, fwd, sun;
uniform vec4 box, win;
uniform vec2 view;
uniform float focal, night, lit, useWin;
void main() {
  vec2 px = box.xy + uv * box.zw;
  vec3 d = normalize(fwd * focal + rgt * (px.x - view.x * 0.5) - upv * (px.y - view.y * 0.5));
  float b = dot(eye, d); vec3 q = eye - b * d; float disc = 1.0 - dot(q, q);
  if (disc < 0.0 || -b < sqrt(disc)) discard;
  vec3 p = normalize(q - sqrt(disc) * d);
  float u = atan(p.y, p.x) / 6.2831853 + 0.5; float v = 0.5 - asin(clamp(p.z, -1.0, 1.0)) / 3.1415927;
  float u2 = fract(u + 0.5) - 0.5; vec2 du = vec2(dFdx(u), dFdy(u)); vec2 du2 = vec2(dFdx(u2), dFdy(u2));
  if (dot(du2, du2) < dot(du, du)) du = du2;
  vec2 dv = vec2(dFdx(v), dFdy(v));
  if (useWin > 0.5) {
    float uw = u; if (uw < win.x) uw += 1.0; else if (uw > win.z) uw -= 1.0;
    if (uw < win.x || uw > win.z || v < win.y || v > win.w) discard;
    vec2 k = 1.0 / (win.zw - win.xy); u = (uw - win.x) * k.x; v = (v - win.y) * k.y; du *= k.x; dv *= k.y;
  }
  vec3 c = textureGrad(tex, vec2(u, v), vec2(du.x, dv.x), vec2(du.y, dv.y)).rgb;
  if (lit > 0.5) { float s = dot(p, sun); c *= mix(1.0 - night, 1.0, smoothstep(-0.06, 0.3, s)) * (0.8 + 0.2 * max(s, 0.0)); }
  o = vec4(c, 1.0);
}
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
  const char* names[] = {"tex", "eye", "rgt", "upv", "fwd", "sun", "box", "view", "focal", "night", "lit", "win", "useWin", "uRect", "uPage"};
  for (int i = 0; i <= SU_PAGE; i++) g_su[i] = glGetUniformLocation(p, names[i]);
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

static void studio_draw_sphere(int idx) {
  if (idx < 0 || idx >= (int)g_spheres.size() || !sphereInit()) return;
  const Sphere& s = g_spheres[(size_t)idx];
  glUseProgram(g_sphereProg);
  glBindVertexArray(g_sphereVao);
  glActiveTexture(GL_TEXTURE0);
  glBindTexture(GL_TEXTURE_2D, s.tex);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_S, s.useWin ? GL_CLAMP_TO_EDGE : GL_REPEAT);
  glUniform1i(g_su[SU_TEX], 0);
  glUniform3f(g_su[SU_EYE], s.p[0], s.p[1], s.p[2]);
  glUniform3f(g_su[SU_RGT], s.p[3], s.p[4], s.p[5]);
  glUniform3f(g_su[SU_UPV], s.p[6], s.p[7], s.p[8]);
  glUniform3f(g_su[SU_FWD], s.p[9], s.p[10], s.p[11]);
  glUniform3f(g_su[SU_SUN], s.p[12], s.p[13], s.p[14]);
  glUniform1f(g_su[SU_FOCAL], s.p[15]);
  glUniform2f(g_su[SU_VIEW], s.p[16], s.p[17]);
  glUniform1f(g_su[SU_NIGHT], s.p[18]);
  glUniform1f(g_su[SU_LIT], s.p[19]);
  glUniform4f(g_su[SU_BOX], s.box[0], s.box[1], s.box[2], s.box[3]);
  glUniform4f(g_su[SU_WIN], s.win[0], s.win[1], s.win[2], s.win[3]);
  glUniform1f(g_su[SU_USEWIN], s.useWin ? 1.f : 0.f);
  glUniform4f(g_su[SU_RECT], s.x, s.y, s.w, s.h);
  glUniform2f(g_su[SU_PAGE], (float)g_pageW, (float)g_pageH);
  glDrawArrays(GL_TRIANGLE_STRIP, 0, 4);
  glBindVertexArray(0);
}

extern "C" void evggl_sphere(float x, float y, float w, float h, int key,
                             float bx, float by, float bw, float bh,
                             const double* params, int n) {
  auto it = g_cachedTex.find(key);
  if (it == g_cachedTex.end() || n < 20) return;
  flushQuadRun();
  Sphere s{};
  s.x = x; s.y = y; s.w = w; s.h = h;
  s.box[0] = bx; s.box[1] = by; s.box[2] = bw; s.box[3] = bh;
  for (int i = 0; i < 20; i++) s.p[i] = (float)params[i];
  s.useWin = n >= 24 ? 1 : 0;
  for (int i = 0; i < 4; i++) s.win[i] = s.useWin ? (float)params[20 + i] : 0.f;
  s.tex = it->second;
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
