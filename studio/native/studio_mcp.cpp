// SPDX-License-Identifier: AGPL-3.0-or-later
// studio_mcp.cpp — the Studio as an MCP server over stdio (JSON-RPC 2.0, one
// message per line). `sliqtly-studio --mcp <folder>` keeps its window, so a
// person can watch while an agent drives the game.
//
// A reader thread parses requests. initialize, tools/list and ping are
// answered at once; a tools/call is queued for the Studio's frame loop
// (sh_mcp_next / sh_mcp_arg / sh_mcp_reply), because only that loop may touch
// the engine and the GL context.

#include "studio_host.h"

#include <cctype>
#include <cstdio>
#include <cstdlib>
#include <deque>
#include <iterator>
#include <fstream>
#include <map>
#include <mutex>
#include <sstream>
#include <thread>
#include <unistd.h>

namespace {

// --- a small JSON reader ---------------------------------------------------------
struct JVal {
    enum Kind { NUL, BOOL, NUM, STR, ARR, OBJ } kind = NUL;
    bool b = false;
    double n = 0;
    std::string s;    // STR; for NUM the number as written
    std::vector<JVal> arr;
    std::map<std::string, JVal> obj;
    const JVal* get(const std::string& k) const {
        auto it = obj.find(k);
        return it == obj.end() ? nullptr : &it->second;
    }
};

struct JParser {
    const std::string& t;
    size_t i = 0;
    bool bad = false;
    explicit JParser(const std::string& text) : t(text) {}
    void ws() { while (i < t.size() && (t[i] == ' ' || t[i] == '\t' || t[i] == '\n' || t[i] == '\r')) i++; }
    static void utf8(std::string& o, unsigned cp) {
        if (cp < 0x80) o += (char)cp;
        else if (cp < 0x800) { o += (char)(0xC0 | (cp >> 6)); o += (char)(0x80 | (cp & 63)); }
        else if (cp < 0x10000) { o += (char)(0xE0 | (cp >> 12)); o += (char)(0x80 | ((cp >> 6) & 63)); o += (char)(0x80 | (cp & 63)); }
        else { o += (char)(0xF0 | (cp >> 18)); o += (char)(0x80 | ((cp >> 12) & 63)); o += (char)(0x80 | ((cp >> 6) & 63)); o += (char)(0x80 | (cp & 63)); }
    }
    unsigned hex4() {
        unsigned v = 0;
        for (int k = 0; k < 4 && i < t.size(); k++, i++) {
            char c = t[i];
            v <<= 4;
            if (c >= '0' && c <= '9') v |= c - '0';
            else if (c >= 'a' && c <= 'f') v |= c - 'a' + 10;
            else if (c >= 'A' && c <= 'F') v |= c - 'A' + 10;
            else bad = true;
        }
        return v;
    }
    std::string str() {
        std::string o;
        i++;  // "
        while (i < t.size() && t[i] != '"') {
            char c = t[i++];
            if (c != '\\') { o += c; continue; }
            if (i >= t.size()) break;
            char e = t[i++];
            switch (e) {
                case 'n': o += '\n'; break;
                case 't': o += '\t'; break;
                case 'r': o += '\r'; break;
                case 'b': o += '\b'; break;
                case 'f': o += '\f'; break;
                case 'u': {
                    unsigned cp = hex4();
                    if (cp >= 0xD800 && cp < 0xDC00 && i + 1 < t.size() && t[i] == '\\' && t[i + 1] == 'u') {
                        i += 2;
                        unsigned lo = hex4();
                        cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00);
                    }
                    utf8(o, cp);
                    break;
                }
                default: o += e;
            }
        }
        i++;  // "
        return o;
    }
    JVal val() {
        JVal v;
        ws();
        if (i >= t.size()) { bad = true; return v; }
        char c = t[i];
        if (c == '{') {
            v.kind = JVal::OBJ;
            i++;
            ws();
            if (i < t.size() && t[i] == '}') { i++; return v; }
            while (i < t.size() && !bad) {
                ws();
                if (t[i] != '"') { bad = true; break; }
                std::string k = str();
                ws();
                if (i >= t.size() || t[i] != ':') { bad = true; break; }
                i++;
                v.obj[k] = val();
                ws();
                if (i < t.size() && t[i] == ',') { i++; continue; }
                if (i < t.size() && t[i] == '}') { i++; break; }
                bad = true;
            }
        } else if (c == '[') {
            v.kind = JVal::ARR;
            i++;
            ws();
            if (i < t.size() && t[i] == ']') { i++; return v; }
            while (i < t.size() && !bad) {
                v.arr.push_back(val());
                ws();
                if (i < t.size() && t[i] == ',') { i++; continue; }
                if (i < t.size() && t[i] == ']') { i++; break; }
                bad = true;
            }
        } else if (c == '"') {
            v.kind = JVal::STR;
            v.s = str();
        } else if (t.compare(i, 4, "true") == 0) { v.kind = JVal::BOOL; v.b = true; i += 4; }
        else if (t.compare(i, 5, "false") == 0) { v.kind = JVal::BOOL; i += 5; }
        else if (t.compare(i, 4, "null") == 0) { i += 4; }
        else {
            size_t st = i;
            while (i < t.size() && (std::isdigit((unsigned char)t[i]) || t[i] == '-' || t[i] == '+' || t[i] == '.' || t[i] == 'e' || t[i] == 'E')) i++;
            if (i == st) { bad = true; return v; }
            v.kind = JVal::NUM;
            v.s = t.substr(st, i - st);
            v.n = std::atof(v.s.c_str());
        }
        return v;
    }
};

std::string jstr(const std::string& s) {
    std::string o = "\"";
    for (unsigned char c : s) {
        switch (c) {
            case '"': o += "\\\""; break;
            case '\\': o += "\\\\"; break;
            case '\n': o += "\\n"; break;
            case '\r': o += "\\r"; break;
            case '\t': o += "\\t"; break;
            default:
                if (c < 0x20) { char b[8]; std::snprintf(b, sizeof b, "\\u%04x", c); o += b; }
                else o += (char)c;
        }
    }
    return o + "\"";
}

// the id exactly as the client sent it
std::string idText(const JVal* id) {
    if (!id) return "null";
    if (id->kind == JVal::STR) return jstr(id->s);
    if (id->kind == JVal::NUM) return id->s;
    return "null";
}

std::string argText(const JVal& v) {
    switch (v.kind) {
        case JVal::STR: return v.s;
        case JVal::NUM: return v.s;
        case JVal::BOOL: return v.b ? "true" : "false";
        default: return "";
    }
}

const char* TOOLS = R"JSON([
{"name":"status","description":"What the Studio shows now: project, game, running or stopped, current scene, the game's scenes, speed, paused, frames run, the last log lines and the open comments.","inputSchema":{"type":"object","properties":{}}},
{"name":"jump_scene","description":"Restart the game and jump to one of its scenes (the game's SCENES object). Answers the scenes when the name is unknown.","inputSchema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}},
{"name":"restart","description":"Restart the game from the beginning (files are synced from disk first).","inputSchema":{"type":"object","properties":{}}},
{"name":"run_frames","description":"Run the game N frames at 60 fps as fast as possible (also when paused), then answer with the log they wrote. Use it to reach a later moment before a screenshot.","inputSchema":{"type":"object","properties":{"frames":{"type":"integer","minimum":1,"maximum":36000}},"required":["frames"]}},
{"name":"screenshot","description":"A PNG of the game area (area 'game', default), the whole Studio window (area 'window') or the cartoon page open in the comic editor (area 'cartoon'), as it is drawn now.","inputSchema":{"type":"object","properties":{"area":{"type":"string","enum":["game","window","cartoon"]}}}},
{"name":"eval","description":"Evaluate JavaScript in the running game's global scope and answer the result as text (JSON.stringify it yourself for objects). Game variables and functions are reachable by name.","inputSchema":{"type":"object","properties":{"code":{"type":"string"}},"required":["code"]}},
{"name":"log","description":"The last lines of the game's log (console output, errors, Studio notes).","inputSchema":{"type":"object","properties":{"lines":{"type":"integer","minimum":1,"maximum":500}}}},
{"name":"set_speed","description":"Game frames per window frame (1, 4, 16, 64 or any 1-256) and/or pause the game.","inputSchema":{"type":"object","properties":{"speed":{"type":"integer","minimum":1,"maximum":256},"paused":{"type":"boolean"}}}},
{"name":"press_key","description":"Press and release a key in the game, by KeyboardEvent.key name (ArrowLeft, ' ', Enter, a ...). down/up only presses or releases it. frames: how many frames to hold it (default 1).","inputSchema":{"type":"object","properties":{"key":{"type":"string"},"mode":{"type":"string","enum":["tap","down","up"]},"frames":{"type":"integer","minimum":1,"maximum":6000}},"required":["key"]}},
{"name":"tap","description":"Tap the game at x,y in game coordinates (the game's own width and height, see status).","inputSchema":{"type":"object","properties":{"x":{"type":"number"},"y":{"type":"number"}},"required":["x","y"]}},
{"name":"comments_list","description":"The comments a person (or an agent) left on the game: id, time, game, scene, frame, text, screenshot path, done. Open ones by default.","inputSchema":{"type":"object","properties":{"all":{"type":"boolean"}}}},
{"name":"comment_add","description":"Leave a comment on the game as it is now (with a screenshot of the game area), for the person to read.","inputSchema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}},
{"name":"comment_done","description":"Mark a comment handled (after the change it asked for is made).","inputSchema":{"type":"object","properties":{"id":{"type":"string"},"note":{"type":"string"}},"required":["id"]}},
{"name":"cartoon_open","description":"Open the comic editor (Sarjakuva) on a cartoon of the game: file is its path relative to the game folder (cartoons/<name>.cartoon.json), scene its index; new=true makes a new empty cartoon. Answers like cartoon_get.","inputSchema":{"type":"object","properties":{"file":{"type":"string"},"scene":{"type":"integer","minimum":0},"new":{"type":"boolean"}}}},
{"name":"cartoon_get","description":"The cartoon open in the comic editor: its file on disk, the scene's panels in page units, the style in words, how to add bubbles, pen lines, colour fills and pictures, and the whole JSON. Change a cartoon by writing that file; the editor reads it again at once. Requests a person made with 'Pyydä AI:ta' are comments whose text starts with [AI] (comments_list); each has a brief in .studio/cartoon-requests/<id>.md.","inputSchema":{"type":"object","properties":{}}},
{"name":"list_assets","description":"Pictures, sounds and other files of the game and of its studio.json resource folders, with size and, for pictures already drawn, their pixel size.","inputSchema":{"type":"object","properties":{"filter":{"type":"string"}}}}
])JSON";

std::mutex g_mu;
std::deque<std::pair<std::string, JVal>> g_calls;  // id text, request
bool g_quit = false;
FILE* g_out = nullptr;
std::string g_cur_id;
JVal g_cur_args;
bool g_has_cur = false;

void send(const std::string& msg) {
    std::lock_guard<std::mutex> lk(g_mu);
    if (!g_out) return;
    std::fwrite(msg.data(), 1, msg.size(), g_out);
    std::fputc('\n', g_out);
    std::fflush(g_out);
}

void result(const std::string& id, const std::string& body) {
    send("{\"jsonrpc\":\"2.0\",\"id\":" + id + ",\"result\":" + body + "}");
}

void error(const std::string& id, int code, const std::string& msg) {
    send("{\"jsonrpc\":\"2.0\",\"id\":" + id + ",\"error\":{\"code\":" + std::to_string(code) + ",\"message\":" + jstr(msg) + "}}");
}

void handle(const std::string& line) {
    JParser p(line);
    JVal req = p.val();
    if (p.bad || req.kind != JVal::OBJ) {
        error("null", -32700, "parse error");
        return;
    }
    const JVal* m = req.get("method");
    const JVal* idv = req.get("id");
    std::string id = idText(idv);
    std::string method = m && m->kind == JVal::STR ? m->s : "";
    if (!idv) return;  // a notification (initialized, cancelled): nothing to answer
    if (method == "initialize") {
        std::string ver = "2025-06-18";
        const JVal* params = req.get("params");
        if (params) {
            const JVal* pv = params->get("protocolVersion");
            if (pv && pv->kind == JVal::STR) ver = pv->s;
        }
        result(id, "{\"protocolVersion\":" + jstr(ver) +
                   ",\"capabilities\":{\"tools\":{}},\"serverInfo\":{\"name\":\"sliqtly-studio\",\"version\":\"0.3\"},"
                   "\"instructions\":" + jstr("Sliqtly Game Studio: the game folder is on disk; edit its files directly and the Studio reloads them (hot reload keeps state when the game has hotSave/hotLoad). Use jump_scene to reach a scene, run_frames to move time, screenshot to look, eval to inspect variables, comments_list to read what the person asked for.") + "}");
        return;
    }
    if (method == "ping") { result(id, "{}"); return; }
    if (method == "tools/list") {
        // one message per line: the list is written over several lines above
        std::string tools;
        for (const char* c = TOOLS; *c; c++) if (*c != '\n') tools += *c;
        result(id, "{\"tools\":" + tools + "}");
        return;
    }
    if (method == "tools/call") {
        std::lock_guard<std::mutex> lk(g_mu);
        g_calls.emplace_back(id, req);
        return;
    }
    error(id, -32601, "no method " + method);
}

void reader() {
    std::string line;
    int c;
    while ((c = std::fgetc(stdin)) != EOF) {
        if (c == '\n') {
            if (!line.empty()) handle(line);
            line.clear();
        } else {
            line += (char)c;
        }
    }
    if (!line.empty()) handle(line);
    std::lock_guard<std::mutex> lk(g_mu);
    g_quit = true;
}

std::string base64(const std::vector<uint8_t>& d) {
    static const char* A = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    std::string o;
    o.reserve((d.size() + 2) / 3 * 4);
    size_t i = 0;
    for (; i + 2 < d.size(); i += 3) {
        uint32_t v = (d[i] << 16) | (d[i + 1] << 8) | d[i + 2];
        o += A[v >> 18]; o += A[(v >> 12) & 63]; o += A[(v >> 6) & 63]; o += A[v & 63];
    }
    if (i + 1 == d.size()) {
        uint32_t v = d[i] << 16;
        o += A[v >> 18]; o += A[(v >> 12) & 63]; o += "==";
    } else if (i + 2 == d.size()) {
        uint32_t v = (d[i] << 16) | (d[i + 1] << 8);
        o += A[v >> 18]; o += A[(v >> 12) & 63]; o += A[(v >> 6) & 63]; o += '=';
    }
    return o;
}

}  // namespace

int sh_mcp_start() {
    int fd = dup(1);
    if (fd < 0) return 0;
    g_out = fdopen(fd, "w");
    if (!g_out) return 0;
    // everything the Studio prints (fonts, [studio] lines) goes to stderr now
    std::fflush(stdout);
    dup2(2, 1);
    std::thread(reader).detach();
    return 1;
}

std::string sh_mcp_next() {
    std::lock_guard<std::mutex> lk(g_mu);
    if (g_has_cur) return "";  // the previous call has no reply yet
    if (g_calls.empty()) return g_quit ? "__quit" : "";
    auto call = std::move(g_calls.front());
    g_calls.pop_front();
    g_cur_id = call.first;
    g_cur_args = JVal();
    std::string name;
    const JVal* params = call.second.get("params");
    if (params) {
        const JVal* n = params->get("name");
        if (n && n->kind == JVal::STR) name = n->s;
        const JVal* a = params->get("arguments");
        if (a) g_cur_args = *a;
    }
    g_has_cur = true;
    return name.empty() ? "__unnamed" : name;
}

std::string sh_mcp_arg(const std::string& key) {
    std::lock_guard<std::mutex> lk(g_mu);
    const JVal* v = g_cur_args.get(key);
    return v ? argText(*v) : "";
}

void sh_mcp_reply(const std::string& text, int isError) {
    std::string id;
    {
        std::lock_guard<std::mutex> lk(g_mu);
        if (!g_has_cur) return;
        id = g_cur_id;
        g_has_cur = false;
    }
    result(id, "{\"content\":[{\"type\":\"text\",\"text\":" + jstr(text) + "}],\"isError\":" + (isError ? "true" : "false") + "}");
}

void sh_mcp_reply_image(const std::string& path, const std::string& text) {
    std::ifstream f(path, std::ios::binary);
    std::vector<uint8_t> bytes((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());
    if (bytes.empty()) {
        sh_mcp_reply("screenshot failed: " + path, 1);
        return;
    }
    std::string id;
    {
        std::lock_guard<std::mutex> lk(g_mu);
        if (!g_has_cur) return;
        id = g_cur_id;
        g_has_cur = false;
    }
    result(id, "{\"content\":[{\"type\":\"image\",\"data\":\"" + base64(bytes) + "\",\"mimeType\":\"image/png\"},{\"type\":\"text\",\"text\":" + jstr(text) + "}]}");
}
