// SPDX-License-Identifier: AGPL-3.0-or-later
// studio_host.cpp — SDL3 window, input, files, the engine and screenshots for
// Sliqtly Game Studio. See studio_host.h for what each call is for.

#include "studio_host.h"
#include "studio_gl.h"

#include <SDL3/SDL.h>

#include <algorithm>
#include <chrono>
#include <ctime>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <sstream>
#include <thread>

#if defined(__APPLE__)
#include <OpenGL/gl3.h>
#elif defined(__arm__) || defined(__aarch64__)
#include <GLES2/gl2.h>
#else
#define GL_GLEXT_PROTOTYPES 1
#include <GL/gl.h>
#endif

// stb_image v2.34 (public domain / MIT, Sean Barrett): PNG (8 and 16 bit,
// palette), JPEG (baseline and progressive), GIF, BMP, TGA. Fast enough for
// big textures; WebP goes through Ranger's own decoder.
#define STB_IMAGE_IMPLEMENTATION
#define STBI_NO_STDIO
#define STBI_NO_HDR
#define STBI_NO_LINEAR
#include "third_party/stb_image.h"

namespace fs = std::filesystem;

// The engine's C ABI (RangerGames crates/rg_web/src/abi.rs, in librg_studio.a)
extern "C" {
uint8_t* rg_alloc(size_t n);
void rg_free(uint8_t* p, size_t n);
void rg_put(const uint8_t* pp, size_t pn, const uint8_t* dp, size_t dn);
int32_t rg_start(const uint8_t* p, size_t n);
void rg_resize(double w, double h);
void rg_pointer(int32_t kind, int32_t id, double x, double y);
void rg_key(int32_t down, const uint8_t* p, size_t n);
int32_t rg_frame(double dt);
const float* rg_draw_ptr();
size_t rg_draw_len();
const uint8_t* rg_strings_ptr();
size_t rg_strings_len();
const uint8_t* rg_cmds_ptr();
size_t rg_cmds_len();
const uint8_t* rg_log_ptr();
size_t rg_log_len();
double rg_view(int32_t i);
int32_t rg_read(const uint8_t* p, size_t n);
const uint8_t* rg_result_ptr();
size_t rg_result_len();
int32_t rg_eval(const uint8_t* p, size_t n);
void rg_studio_reset();
void rg_textures(int32_t on);
void rg_detail(int32_t on);
int32_t rg_restyle();
}

// ---------------------------------------------------------------------------
// window
// ---------------------------------------------------------------------------
static SDL_Window* g_win = nullptr;
static SDL_GLContext g_gl = nullptr;
static int g_close = 0;
static int g_w = 0, g_h = 0;

static int g_mx = 0, g_my = 0, g_btn = 0;
// a press seen since the last read: a quick tap (down and up within one
// frame) still reaches the app as one frame of "down"
static int g_btn_tap = 0;
static float g_wheel_y = 0.f, g_wheel_x = 0.f;
static std::vector<int> g_keys;
static std::string g_text;
static std::string g_game_keys;

enum {
    K_BACKSPACE = 1, K_ENTER = 2, K_TAB = 3, K_ESCAPE = 4, K_LEFT = 5, K_RIGHT = 6,
    K_UP = 7, K_DOWN = 8, K_DEL = 9, K_HOME = 10, K_END = 11, K_PAGEUP = 12,
    K_PAGEDOWN = 13, K_F2 = 14, K_F5 = 15
};

static int map_key(SDL_Keycode k) {
    switch (k) {
        case SDLK_BACKSPACE: return K_BACKSPACE;
        case SDLK_RETURN: case SDLK_KP_ENTER: return K_ENTER;
        case SDLK_TAB: return K_TAB;
        case SDLK_ESCAPE: return K_ESCAPE;
        case SDLK_LEFT: return K_LEFT;
        case SDLK_RIGHT: return K_RIGHT;
        case SDLK_UP: return K_UP;
        case SDLK_DOWN: return K_DOWN;
        case SDLK_DELETE: return K_DEL;
        case SDLK_HOME: return K_HOME;
        case SDLK_END: return K_END;
        case SDLK_PAGEUP: return K_PAGEUP;
        case SDLK_PAGEDOWN: return K_PAGEDOWN;
        case SDLK_F2: return K_F2;
        case SDLK_F5: return K_F5;
        default: return 0;
    }
}

// KeyboardEvent.key, which is what a game's rg.onKey reads
static std::string key_name(SDL_Keycode k) {
    switch (k) {
        case SDLK_LEFT: return "ArrowLeft";
        case SDLK_RIGHT: return "ArrowRight";
        case SDLK_UP: return "ArrowUp";
        case SDLK_DOWN: return "ArrowDown";
        case SDLK_SPACE: return " ";
        case SDLK_RETURN: case SDLK_KP_ENTER: return "Enter";
        case SDLK_ESCAPE: return "Escape";
        case SDLK_BACKSPACE: return "Backspace";
        case SDLK_TAB: return "Tab";
        case SDLK_LSHIFT: case SDLK_RSHIFT: return "Shift";
        case SDLK_LCTRL: case SDLK_RCTRL: return "Control";
        default: break;
    }
    if (k >= SDLK_A && k <= SDLK_Z) return std::string(1, (char)('a' + (k - SDLK_A)));
    if (k >= SDLK_0 && k <= SDLK_9) return std::string(1, (char)('0' + (k - SDLK_0)));
    return "";
}

int sh_open(const std::string& title, int w, int h, int hidden) {
    if (!SDL_Init(SDL_INIT_VIDEO)) {
        std::fprintf(stderr, "[studio] SDL_Init failed: %s\n", SDL_GetError());
        return 0;
    }
    SDL_GL_SetAttribute(SDL_GL_CONTEXT_PROFILE_MASK, SDL_GL_CONTEXT_PROFILE_CORE);
    SDL_GL_SetAttribute(SDL_GL_CONTEXT_MAJOR_VERSION, 3);
    SDL_GL_SetAttribute(SDL_GL_CONTEXT_MINOR_VERSION, 2);
    SDL_GL_SetAttribute(SDL_GL_DOUBLEBUFFER, 1);
    SDL_GL_SetAttribute(SDL_GL_STENCIL_SIZE, 8);
    SDL_WindowFlags flags = SDL_WINDOW_OPENGL | SDL_WINDOW_RESIZABLE | SDL_WINDOW_HIGH_PIXEL_DENSITY;
    if (hidden) flags |= SDL_WINDOW_HIDDEN;
    g_win = SDL_CreateWindow(title.c_str(), w, h, flags);
    if (!g_win) {
        std::fprintf(stderr, "[studio] SDL_CreateWindow failed: %s\n", SDL_GetError());
        return 0;
    }
    g_gl = SDL_GL_CreateContext(g_win);
    if (!g_gl) {
        std::fprintf(stderr, "[studio] OpenGL 3.2 core context failed: %s\n", SDL_GetError());
        SDL_DestroyWindow(g_win);
        g_win = nullptr;
        return 0;
    }
    SDL_GL_MakeCurrent(g_win, g_gl);
    SDL_GL_SetSwapInterval(hidden ? 0 : 1);
    SDL_StartTextInput(g_win);
    g_w = w;
    g_h = h;
    if (!evggl_init()) {
        std::fprintf(stderr, "[studio] evggl_init failed\n");
        return 0;
    }
    return 1;
}

void sh_close() {
    evggl_image_forget_all();
    if (g_gl) SDL_GL_DestroyContext(g_gl);
    if (g_win) SDL_DestroyWindow(g_win);
    g_gl = nullptr;
    g_win = nullptr;
    SDL_Quit();
}

void sh_poll() {
    SDL_Event ev;
    while (SDL_PollEvent(&ev)) {
        switch (ev.type) {
            case SDL_EVENT_QUIT:
            case SDL_EVENT_WINDOW_CLOSE_REQUESTED:
                g_close = 1;
                break;
            case SDL_EVENT_MOUSE_MOTION:
                g_mx = (int)ev.motion.x;
                g_my = (int)ev.motion.y;
                break;
            case SDL_EVENT_MOUSE_BUTTON_DOWN:
            case SDL_EVENT_MOUSE_BUTTON_UP: {
                g_mx = (int)ev.button.x;
                g_my = (int)ev.button.y;
                int bit = ev.button.button == SDL_BUTTON_LEFT ? 1 : ev.button.button == SDL_BUTTON_RIGHT ? 2 : 4;
                if (ev.type == SDL_EVENT_MOUSE_BUTTON_DOWN) { g_btn |= bit; g_btn_tap |= bit; } else g_btn &= ~bit;
                break;
            }
            case SDL_EVENT_MOUSE_WHEEL: {
                float y = ev.wheel.y, x = ev.wheel.x;
                if (ev.wheel.direction == SDL_MOUSEWHEEL_FLIPPED) { y = -y; x = -x; }
                g_wheel_y += y;
                g_wheel_x += x;
                break;
            }
            case SDL_EVENT_KEY_DOWN:
            case SDL_EVENT_KEY_UP: {
                bool down = ev.type == SDL_EVENT_KEY_DOWN;
                std::string name = key_name(ev.key.key);
                if (!name.empty() && !(down && ev.key.repeat)) {
                    g_game_keys += (down ? "1 " : "0 ") + name + "\n";
                }
                if (!down) break;
                int mapped = map_key(ev.key.key);
                if (mapped) g_keys.push_back(mapped);
                if (ev.key.mod & (SDL_KMOD_CTRL | SDL_KMOD_GUI)) {
                    SDL_Keycode k = ev.key.key;
                    if (k >= SDLK_A && k <= SDLK_Z) g_text.push_back((char)('a' + (k - SDLK_A)));
                    if (k == SDLK_SPACE) g_text.push_back(' ');
                }
                break;
            }
            case SDL_EVENT_TEXT_INPUT: {
                // With Ctrl/Cmd held the chord already went in as its letter
                SDL_Keymod m = SDL_GetModState();
                if (!(m & (SDL_KMOD_CTRL | SDL_KMOD_GUI))) g_text += ev.text.text;
                break;
            }
            default:
                break;
        }
    }
}

int sh_should_close() { return g_close; }

int sh_window_w() {
    if (!g_win) return g_w;
    int w = 0, h = 0;
    SDL_GetWindowSize(g_win, &w, &h);
    return w;
}
int sh_window_h() {
    if (!g_win) return g_h;
    int w = 0, h = 0;
    SDL_GetWindowSize(g_win, &w, &h);
    return h;
}
int sh_drawable_w() {
    if (!g_win) return g_w;
    int w = 0, h = 0;
    SDL_GetWindowSizeInPixels(g_win, &w, &h);
    return w;
}
int sh_drawable_h() {
    if (!g_win) return g_h;
    int w = 0, h = 0;
    SDL_GetWindowSizeInPixels(g_win, &w, &h);
    return h;
}

void sh_swap() {
    evggl_flush();
    if (g_win) SDL_GL_SwapWindow(g_win);
}
void sh_delay(int ms) { SDL_Delay((Uint32)ms); }
double sh_now_ms() {
    using namespace std::chrono;
    return (double)duration_cast<microseconds>(steady_clock::now().time_since_epoch()).count() / 1000.0;
}
void sh_set_title(const std::string& title) {
    if (g_win) SDL_SetWindowTitle(g_win, title.c_str());
}
void sh_set_clipboard(const std::string& text) { SDL_SetClipboardText(text.c_str()); }
std::string sh_get_clipboard() {
    char* t = SDL_GetClipboardText();
    std::string s = t ? t : "";
    SDL_free(t);
    return s;
}

int sh_mouse_x() { return g_mx; }
int sh_mouse_y() { return g_my; }
int sh_mouse_buttons() { int b = g_btn | g_btn_tap; g_btn_tap = 0; return b; }
int sh_wheel_y() {
    int v = (int)g_wheel_y;
    g_wheel_y -= (float)v;
    return v;
}
int sh_wheel_x() {
    int v = (int)g_wheel_x;
    g_wheel_x -= (float)v;
    return v;
}
int sh_shift() { return (SDL_GetModState() & SDL_KMOD_SHIFT) ? 1 : 0; }
int sh_ctrl() { return (SDL_GetModState() & (SDL_KMOD_CTRL | SDL_KMOD_GUI)) ? 1 : 0; }
int sh_pop_key() {
    if (g_keys.empty()) return 0;
    int k = g_keys.front();
    g_keys.erase(g_keys.begin());
    return k;
}
std::string sh_take_text() {
    std::string t;
    t.swap(g_text);
    return t;
}
std::string sh_take_game_keys() {
    std::string t;
    t.swap(g_game_keys);
    return t;
}

// ---------------------------------------------------------------------------
// files
// ---------------------------------------------------------------------------
static long long mtime_ms(const fs::path& p) {
    std::error_code ec;
    auto t = fs::last_write_time(p, ec);
    if (ec) return 0;
    return (long long)std::chrono::duration_cast<std::chrono::milliseconds>(t.time_since_epoch()).count();
}

std::string sh_list_files(const std::string& root) {
    std::vector<std::string> lines;
    std::error_code ec;
    fs::path base(root);
    if (!fs::is_directory(base, ec)) return "";
    auto it = fs::recursive_directory_iterator(base, fs::directory_options::skip_permission_denied, ec);
    for (; !ec && it != fs::recursive_directory_iterator(); it.increment(ec)) {
        const fs::path& p = it->path();
        std::string name = p.filename().string();
        if (!name.empty() && name[0] == '.') {
            if (it->is_directory(ec)) it.disable_recursion_pending();
            continue;
        }
        if (name == "node_modules" || name == "target") {
            if (it->is_directory(ec)) it.disable_recursion_pending();
            continue;
        }
        if (!it->is_regular_file(ec)) continue;
        std::string rel = fs::relative(p, base, ec).generic_string();
        std::ostringstream line;
        line << rel << "\t" << (long long)it->file_size(ec) << "\t" << mtime_ms(p);
        lines.push_back(line.str());
    }
    std::sort(lines.begin(), lines.end());
    std::string out;
    for (auto& l : lines) {
        out += l;
        out += "\n";
    }
    return out;
}

int sh_is_dir(const std::string& path) {
    std::error_code ec;
    return fs::is_directory(path, ec) ? 1 : 0;
}
int sh_exists(const std::string& path) {
    std::error_code ec;
    return fs::exists(path, ec) ? 1 : 0;
}
std::string sh_read_text(const std::string& path) {
    std::ifstream f(path, std::ios::binary);
    if (!f) return "";
    std::ostringstream ss;
    ss << f.rdbuf();
    return ss.str();
}
int sh_write_text(const std::string& path, const std::string& text) {
    std::error_code ec;
    fs::path p(path);
    if (p.has_parent_path()) fs::create_directories(p.parent_path(), ec);
    // write beside, then rename: an agent reading the file never sees half of it
    std::string tmp = path + ".studio-tmp";
    {
        std::ofstream f(tmp, std::ios::binary | std::ios::trunc);
        if (!f) return 0;
        f.write(text.data(), (std::streamsize)text.size());
        if (!f) return 0;
    }
    fs::rename(tmp, p, ec);
    return ec ? 0 : 1;
}
std::vector<uint8_t> sh_read_bytes(const std::string& path) {
    std::ifstream f(path, std::ios::binary);
    if (!f) return {};
    return std::vector<uint8_t>((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());
}
std::string sh_real_path(const std::string& path) {
    std::error_code ec;
    auto p = fs::weakly_canonical(fs::absolute(path, ec), ec);
    return ec ? path : p.generic_string();
}
std::string sh_base_path() {
    const char* b = SDL_GetBasePath();
    return b ? b : "";
}
double sh_file_mtime(const std::string& path) { return (double)mtime_ms(fs::path(path)); }
void sh_print(const std::string& text) {
    std::fwrite(text.data(), 1, text.size(), stdout);
    std::fputc('\n', stdout);
    std::fflush(stdout);
}
std::string sh_env(const std::string& name) {
    const char* v = std::getenv(name.c_str());
    return v ? v : "";
}

// ---------------------------------------------------------------------------
// the engine
// ---------------------------------------------------------------------------
static std::vector<std::string> g_strings;

static void put_bytes(const std::string& vfsPath, const uint8_t* data, size_t n) {
    rg_put((const uint8_t*)vfsPath.data(), vfsPath.size(), data, n);
}

int sh_rg_put_file(const std::string& vfsPath, const std::string& diskPath) {
    std::vector<uint8_t> b = sh_read_bytes(diskPath);
    if (b.empty() && !sh_exists(diskPath)) return 0;
    put_bytes(vfsPath, b.data(), b.size());
    return 1;
}
int sh_rg_put_head(const std::string& vfsPath, const std::string& diskPath, int maxBytes) {
    std::ifstream f(diskPath, std::ios::binary);
    if (!f) return 0;
    std::vector<uint8_t> b((size_t)std::max(0, maxBytes));
    f.read((char*)b.data(), (std::streamsize)b.size());
    b.resize((size_t)f.gcount());
    put_bytes(vfsPath, b.data(), b.size());
    return 1;
}
void sh_rg_reset() { rg_studio_reset(); }
int sh_rg_restyle() { return rg_restyle(); }
void sh_rg_put_text(const std::string& vfsPath, const std::string& text) {
    put_bytes(vfsPath, (const uint8_t*)text.data(), text.size());
}
int sh_rg_start(const std::string& id) {
    // the Studio draws textured globes and their detail pictures (kind 8)
    rg_textures(1);
    rg_detail(1);
    return rg_start((const uint8_t*)id.data(), id.size());
}
void sh_rg_resize(double w, double h) { rg_resize(w, h); }
void sh_rg_pointer(int kind, int id, double x, double y) { rg_pointer(kind, id, x, y); }
void sh_rg_key(int down, const std::string& name) { rg_key(down, (const uint8_t*)name.data(), name.size()); }

int sh_rg_frame(double dtMs) {
    int r = rg_frame(dtMs);
    g_strings.clear();
    const char* p = (const char*)rg_strings_ptr();
    size_t n = rg_strings_len();
    size_t start = 0;
    for (size_t i = 0; i <= n; i++) {
        if (i == n || p[i] == 0) {
            g_strings.emplace_back(p + start, i - start);
            start = i + 1;
        }
    }
    return r;
}
int sh_rg_draw_len() { return (int)rg_draw_len(); }
double sh_rg_draw_at(int i) { return (double)rg_draw_ptr()[i]; }
std::string sh_rg_string(int i) {
    if (i < 0 || i >= (int)g_strings.size()) return "";
    return g_strings[(size_t)i];
}
std::string sh_rg_log() { return std::string((const char*)rg_log_ptr(), rg_log_len()); }
std::string sh_rg_cmds() { return std::string((const char*)rg_cmds_ptr(), rg_cmds_len()); }
double sh_rg_view(int i) { return rg_view(i); }
std::vector<uint8_t> sh_rg_read(const std::string& path) {
    int n = rg_read((const uint8_t*)path.data(), path.size());
    if (n < 0) return {};
    const uint8_t* p = rg_result_ptr();
    return std::vector<uint8_t>(p, p + rg_result_len());
}
std::string sh_rg_eval(const std::string& src) {
    rg_eval((const uint8_t*)src.data(), src.size());
    return std::string((const char*)rg_result_ptr(), rg_result_len());
}

// ---------------------------------------------------------------------------
// pictures
// ---------------------------------------------------------------------------
static int g_img_w = 0, g_img_h = 0;
static std::string g_img_error;

int sh_image_upload(int key, const std::vector<uint8_t>& bytes) {
    g_img_w = g_img_h = 0;
    g_img_error.clear();
    if (bytes.empty()) { g_img_error = "empty"; return 0; }
    int w = 0, h = 0, n = 0;
    stbi_uc* px = stbi_load_from_memory(bytes.data(), (int)bytes.size(), &w, &h, &n, 4);
    if (!px) {
        const char* why = stbi_failure_reason();
        g_img_error = why ? why : "unreadable";
        return 0;
    }
    evggl_image_upload(key, px, w, h);
    stbi_image_free(px);
    g_img_w = w;
    g_img_h = h;
    return 1;
}
int sh_image_w() { return g_img_w; }
int sh_image_h() { return g_img_h; }
std::string sh_image_error() { return g_img_error; }

// ---------------------------------------------------------------------------
// screenshot: the back buffer as an uncompressed (stored) PNG
// ---------------------------------------------------------------------------
static uint32_t crc_table[256];
static void crc_init() {
    for (uint32_t n = 0; n < 256; n++) {
        uint32_t c = n;
        for (int k = 0; k < 8; k++) c = (c & 1) ? 0xEDB88320u ^ (c >> 1) : c >> 1;
        crc_table[n] = c;
    }
}
static uint32_t crc(const uint8_t* b, size_t n, uint32_t c = 0xFFFFFFFFu) {
    for (size_t i = 0; i < n; i++) c = crc_table[(c ^ b[i]) & 0xFF] ^ (c >> 8);
    return c;
}
static void be32(std::vector<uint8_t>& o, uint32_t v) {
    o.push_back((uint8_t)(v >> 24)); o.push_back((uint8_t)(v >> 16));
    o.push_back((uint8_t)(v >> 8)); o.push_back((uint8_t)v);
}
static void chunk(std::vector<uint8_t>& o, const char* type, const std::vector<uint8_t>& data) {
    be32(o, (uint32_t)data.size());
    size_t at = o.size();
    o.insert(o.end(), type, type + 4);
    o.insert(o.end(), data.begin(), data.end());
    be32(o, crc(&o[at], data.size() + 4) ^ 0xFFFFFFFFu);
}

// Part of the back buffer, in window coordinates, as a PNG file.
int sh_screenshot_rect(const std::string& path, double lx, double ly, double lw, double lh) {
    int dw = sh_drawable_w(), dh = sh_drawable_h();
    int ww = sh_window_w(), wh = sh_window_h();
    if (dw <= 0 || dh <= 0) return 0;
    double sx = ww > 0 ? (double)dw / ww : 1.0, sy = wh > 0 ? (double)dh / wh : 1.0;
    int x0 = std::max(0, (int)(lx * sx)), y0 = std::max(0, (int)(ly * sy));
    int w = std::min(dw - x0, (int)(lw * sx)), h = std::min(dh - y0, (int)(lh * sy));
    if (w <= 0 || h <= 0) return 0;
    std::vector<uint8_t> px((size_t)w * h * 4);
    glPixelStorei(GL_PACK_ALIGNMENT, 1);
    // GL rows count from the bottom
    glReadPixels(x0, dh - (y0 + h), w, h, GL_RGBA, GL_UNSIGNED_BYTE, px.data());
    crc_init();
    std::vector<uint8_t> raw;
    raw.reserve((size_t)(w * 3 + 1) * h);
    for (int y = h - 1; y >= 0; y--) {
        raw.push_back(0);
        const uint8_t* row = &px[(size_t)y * w * 4];
        for (int x = 0; x < w; x++) {
            raw.push_back(row[x * 4]);
            raw.push_back(row[x * 4 + 1]);
            raw.push_back(row[x * 4 + 2]);
        }
    }
    std::vector<uint8_t> z = {0x78, 0x01};
    uint32_t a = 1, b = 0;
    for (uint8_t v : raw) { a = (a + v) % 65521; b = (b + a) % 65521; }
    size_t pos = 0;
    while (pos < raw.size() || raw.empty()) {
        size_t len = std::min<size_t>(65535, raw.size() - pos);
        bool last = pos + len >= raw.size();
        z.push_back(last ? 1 : 0);
        z.push_back((uint8_t)len); z.push_back((uint8_t)(len >> 8));
        z.push_back((uint8_t)~len); z.push_back((uint8_t)((~len) >> 8));
        z.insert(z.end(), raw.begin() + pos, raw.begin() + pos + len);
        pos += len;
        if (last) break;
    }
    be32(z, (b << 16) | a);
    std::vector<uint8_t> out = {0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A};
    std::vector<uint8_t> ihdr;
    be32(ihdr, (uint32_t)w); be32(ihdr, (uint32_t)h);
    ihdr.push_back(8); ihdr.push_back(2); ihdr.push_back(0); ihdr.push_back(0); ihdr.push_back(0);
    chunk(out, "IHDR", ihdr);
    chunk(out, "IDAT", z);
    chunk(out, "IEND", {});
    std::ofstream f(path, std::ios::binary | std::ios::trunc);
    if (!f) return 0;
    f.write((const char*)out.data(), (std::streamsize)out.size());
    return f ? 1 : 0;
}

int sh_screenshot(const std::string& path) {
    return sh_screenshot_rect(path, 0, 0, sh_window_w(), sh_window_h());
}

double sh_file_size(const std::string& path) {
    std::error_code ec;
    auto n = fs::file_size(path, ec);
    return ec ? 0.0 : (double)n;
}

int sh_mkdirs(const std::string& path) {
    std::error_code ec;
    fs::create_directories(path, ec);
    return fs::is_directory(path, ec) ? 1 : 0;
}

int sh_append_text(const std::string& path, const std::string& text) {
    std::ofstream f(path, std::ios::binary | std::ios::app);
    if (!f) return 0;
    f.write(text.data(), (std::streamsize)text.size());
    return f ? 1 : 0;
}

std::string sh_local_time() {
    std::time_t t = std::time(nullptr);
    std::tm tmv{};
    localtime_r(&t, &tmv);
    char buf[32];
    std::strftime(buf, sizeof buf, "%Y-%m-%dT%H:%M:%S", &tmv);
    return buf;
}

// An AI command for the cartoon editor ("Pyydä AI:ta"): run in the background
// so the window keeps drawing. The command line is the person's own
// (SLIQTLY_STUDIO_AI), with its input and output files already in it.
int sh_spawn(const std::string& cmd) {
    if (cmd.empty()) return 0;
    std::thread([cmd]() { int rc = std::system(cmd.c_str()); (void)rc; }).detach();
    return 1;
}
