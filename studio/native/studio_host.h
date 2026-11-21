// SPDX-License-Identifier: AGPL-3.0-or-later
// studio_host.h — the native side of Sliqtly Game Studio.
//
// Everything the Ranger program cannot do itself: the SDL3 window and its
// input, the file system, the RangerGames engine (linked as librg_studio.a)
// and a screenshot. The Ranger operators in gfx_studio.rgr call these.
#pragma once
#include <cstdint>
#include <string>
#include <vector>
#include "studio_gl.h"

// --- window ------------------------------------------------------------------
int sh_open(const std::string& title, int w, int h, int hidden);
void sh_close();
void sh_poll();
int sh_should_close();
int sh_window_w();
int sh_window_h();
int sh_drawable_w();
int sh_drawable_h();
void sh_swap();
void sh_delay(int ms);
double sh_now_ms();
void sh_set_title(const std::string& title);
void sh_set_clipboard(const std::string& text);
std::string sh_get_clipboard();

// --- input -------------------------------------------------------------------
int sh_mouse_x();
int sh_mouse_y();
int sh_mouse_buttons();
int sh_wheel_y();
int sh_wheel_x();
int sh_shift();
int sh_ctrl();
// UIKey codes (1 backspace … 15 F5), 0 when none is left
int sh_pop_key();
// typed text since the last call; Ctrl/Cmd chords arrive as their letter
std::string sh_take_text();
// the frame's key presses and releases by KeyboardEvent.key name, for a game:
// "1 ArrowLeft\n0 ArrowLeft\n" (1 down, 0 up)
std::string sh_take_game_keys();

// --- files -------------------------------------------------------------------
// every file under root (dot-names skipped), one per line:
// "relative/path\tsize\tmtime_ms"
std::string sh_list_files(const std::string& root);
int sh_is_dir(const std::string& path);
int sh_exists(const std::string& path);
std::string sh_read_text(const std::string& path);
int sh_write_text(const std::string& path, const std::string& text);
std::vector<uint8_t> sh_read_bytes(const std::string& path);
std::string sh_real_path(const std::string& path);
std::string sh_env(const std::string& name);
double sh_file_mtime(const std::string& path);
// the folder the program is in, with a trailing slash
std::string sh_base_path();
// one line to stdout, flushed (for agents reading the log)
void sh_print(const std::string& text);

// --- the engine ----------------------------------------------------------------
int sh_rg_put_file(const std::string& vfsPath, const std::string& diskPath);
// only the first maxBytes of a file: a big picture's header is all the
// engine reads (its size); the Studio draws pictures from disk itself
int sh_rg_put_head(const std::string& vfsPath, const std::string& diskPath, int maxBytes);
void sh_rg_put_text(const std::string& vfsPath, const std::string& text);
// a fresh engine (empty file system), for a full reload
void sh_rg_reset();
// the running game's style.css read again (after a put), no restart; 1 done
int sh_rg_restyle();
int sh_rg_start(const std::string& id);
void sh_rg_resize(double w, double h);
void sh_rg_pointer(int kind, int id, double x, double y);
void sh_rg_key(int down, const std::string& name);
int sh_rg_frame(double dtMs);
int sh_rg_draw_len();
double sh_rg_draw_at(int i);
std::string sh_rg_string(int i);
std::string sh_rg_log();
std::string sh_rg_cmds();
double sh_rg_view(int i);
std::vector<uint8_t> sh_rg_read(const std::string& path);
std::string sh_rg_eval(const std::string& src);

// --- pictures ------------------------------------------------------------------
// decode PNG/JPEG/GIF/BMP and upload it as GPU texture `key`; 1 done, 0 not
// (sh_image_error says why). Its size is sh_image_w/h afterwards.
int sh_image_upload(int key, const std::vector<uint8_t>& bytes);
int sh_image_w();
int sh_image_h();
std::string sh_image_error();
// the window's pixels as a PNG file; 1 written, 0 not
int sh_screenshot(const std::string& path);
// part of the window (window coordinates) as a PNG file; 1 written, 0 not
int sh_screenshot_rect(const std::string& path, double x, double y, double w, double h);
// bytes, 0 when missing
double sh_file_size(const std::string& path);
int sh_mkdirs(const std::string& path);
int sh_append_text(const std::string& path, const std::string& text);
// "2026-11-21T14:05:09", local time
std::string sh_local_time();
// runs a shell command in the background (its own thread); 1 when started
int sh_spawn(const std::string& cmd);
// Nano Banana (Gemini image model): draws `out` (PNG) from prompt + ref PNG
// on a thread, transparent outside `mask` (u,v,... of the picture, "" none);
// writes out + ".err" on failure. 0 when no API key is set.
int sh_nano_banana(const std::string& job, const std::string& model, const std::string& prompt,
                   const std::string& ref, const std::string& out, const std::string& aspect,
                   const std::string& mask);
// where the Gemini API key comes from ("" when there is none)
std::string sh_image_key();

// --- MCP (stdio JSON-RPC, `sliqtly-studio --mcp <folder>`) ------------------------
// Takes stdout for the protocol (the Studio's own prints go to stderr from
// then on) and starts reading requests from stdin. initialize, tools/list and
// ping are answered here; a tools/call waits for the Studio's loop.
int sh_mcp_start();
// the next tool call's name, "" when none waits, "__quit" when stdin closed
std::string sh_mcp_next();
// an argument of the current call as text ("" when missing)
std::string sh_mcp_arg(const std::string& key);
void sh_mcp_reply(const std::string& text, int isError);
// a PNG file as an image, with a line of text
void sh_mcp_reply_image(const std::string& path, const std::string& text);
