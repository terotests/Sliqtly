// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Node.js's file modules for a script run on a presentation's files
// (run_script, mcp-go/noderun.go), run in CErXes after the engine's own
// prelude and before the script.
//
//   fs, fs/promises   readFileSync, writeFileSync, appendFileSync, existsSync,
//                     statSync, readdirSync, mkdirSync, rmSync, unlinkSync,
//                     renameSync, copyFileSync, ... their callback forms and
//                     fs.promises
//   path              POSIX paths: join, resolve, dirname, basename, extname,
//                     relative, normalize, parse, format
//   process           argv, env, cwd(), chdir(), exit(), stdout.write()
//   buffer            Buffer.from / alloc / concat / byteLength, toString()
//
// with `require("fs")`, `require("node:fs")` and `import fs from "node:fs"`
// alike. The file system is the presentation's own and nothing else: its
// files are put in before the script runs (__nodeMount), at /data, /media,
// /apps, /code and /presentation.md; /tmp is scratch. What the script wrote
// or removed is read back after it (__nodeChanges) and the host decides what
// is kept. Nothing waits on the host while the script runs.
//
// A text file reads as a string with or without an encoding (Node gives a
// Buffer without one; .toString() and JSON.parse take either). A picture or
// a workbook is there by name and size, but its bytes are not handed in.
export const NODE_RUNTIME = String.raw`
(function () {
  var files = Object.create(null);
  var dirs = Object.create(null);
  var gone = Object.create(null);
  var cwd = "/";
  var argv = ["node", "/script.js"];
  var started = Date.now();

  // ---- UTF-8 and base64

  function utf8Bytes(s) {
    var out = [];
    for (var i = 0; i < s.length; i++) {
      var c = s.charCodeAt(i);
      if (c >= 0xd800 && c < 0xdc00 && i + 1 < s.length) {
        var d = s.charCodeAt(i + 1);
        if (d >= 0xdc00 && d < 0xe000) {
          c = 0x10000 + ((c - 0xd800) << 10) + (d - 0xdc00);
          i++;
        }
      }
      if (c < 0x80) out.push(c);
      else if (c < 0x800) out.push(0xc0 | (c >> 6), 0x80 | (c & 63));
      else if (c < 0x10000) out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
      else out.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 63), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
    }
    return new Uint8Array(out);
  }
  function utf8Text(b) {
    var s = "";
    var i = 0;
    while (i < b.length) {
      var c = b[i++];
      if (c >= 0xf0) {
        c = ((c & 7) << 18) | ((b[i++] & 63) << 12) | ((b[i++] & 63) << 6) | (b[i++] & 63);
      } else if (c >= 0xe0) {
        c = ((c & 15) << 12) | ((b[i++] & 63) << 6) | (b[i++] & 63);
      } else if (c >= 0xc0) {
        c = ((c & 31) << 6) | (b[i++] & 63);
      }
      if (c >= 0x10000) {
        c -= 0x10000;
        s += String.fromCharCode(0xd800 + (c >> 10), 0xdc00 + (c & 1023));
      } else {
        s += String.fromCharCode(c);
      }
    }
    return s;
  }
  var B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  function toBase64(b) {
    var s = "";
    for (var i = 0; i < b.length; i += 3) {
      var n = (b[i] << 16) | ((i + 1 < b.length ? b[i + 1] : 0) << 8) | (i + 2 < b.length ? b[i + 2] : 0);
      s += B64[(n >> 18) & 63] + B64[(n >> 12) & 63];
      s += i + 1 < b.length ? B64[(n >> 6) & 63] : "=";
      s += i + 2 < b.length ? B64[n & 63] : "=";
    }
    return s;
  }
  function fromBase64(s) {
    s = String(s).replace(/[^A-Za-z0-9+/_-]/g, "").replace(/-/g, "+").replace(/_/g, "/");
    var out = [];
    var bits = 0, acc = 0;
    for (var i = 0; i < s.length; i++) {
      acc = (acc << 6) | B64.indexOf(s[i]);
      bits += 6;
      if (bits >= 8) {
        bits -= 8;
        out.push((acc >> bits) & 255);
      }
    }
    return new Uint8Array(out);
  }

  // ---- Buffer: a Uint8Array that knows its text

  function dress(u) {
    Object.defineProperty(u, "toString", {
      value: function (enc, start, end) {
        var part = start === undefined && end === undefined ? this : this.subarray(start || 0, end === undefined ? this.length : end);
        return decode(part, enc);
      },
      enumerable: false
    });
    Object.defineProperty(u, "toJSON", { value: function () { return { type: "Buffer", data: Array.prototype.slice.call(this) }; }, enumerable: false });
    Object.defineProperty(u, "equals", { value: function (o) { if (!o || o.length !== this.length) return false; for (var i = 0; i < this.length; i++) if (this[i] !== o[i]) return false; return true; }, enumerable: false });
    Object.defineProperty(u, "write", { value: function (s, off) { var b = utf8Bytes(String(s)); this.set(b.subarray(0, Math.max(0, this.length - (off || 0))), off || 0); return Math.min(b.length, this.length - (off || 0)); }, enumerable: false });
    Object.defineProperty(u, "__isBuffer", { value: true, enumerable: false });
    return u;
  }
  function encoding(enc) {
    var e = String(enc || "utf8").toLowerCase().replace("-", "");
    if (e === "utf8" || e === "base64" || e === "base64url" || e === "hex" || e === "latin1" || e === "binary" || e === "ascii") return e;
    throw new TypeError("Unknown encoding: " + enc);
  }
  function decode(b, enc) {
    var e = encoding(enc);
    if (e === "utf8") return utf8Text(b);
    if (e === "base64") return toBase64(b);
    if (e === "base64url") return toBase64(b).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
    var s = "";
    for (var i = 0; i < b.length; i++) {
      if (e === "hex") s += (b[i] < 16 ? "0" : "") + b[i].toString(16);
      else s += String.fromCharCode(e === "ascii" ? b[i] & 127 : b[i]);
    }
    return s;
  }
  function encode(s, enc) {
    var e = encoding(enc);
    if (e === "utf8") return utf8Bytes(s);
    if (e === "base64" || e === "base64url") return fromBase64(s);
    var out = [];
    if (e === "hex") {
      for (var i = 0; i + 1 < s.length; i += 2) out.push(parseInt(s.substr(i, 2), 16));
    } else {
      for (var j = 0; j < s.length; j++) out.push(s.charCodeAt(j) & 255);
    }
    return new Uint8Array(out);
  }
  function bytesOf(data) {
    if (data instanceof Uint8Array) return data;
    if (data instanceof ArrayBuffer) return new Uint8Array(data);
    if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    return null;
  }
  var Buffer = {
    from: function (v, enc) {
      if (typeof v === "string") return dress(encode(v, enc));
      var b = bytesOf(v);
      if (b) return dress(new Uint8Array(b));
      if (v && v.type === "Buffer" && Array.isArray(v.data)) return dress(new Uint8Array(v.data));
      if (Array.isArray(v)) return dress(new Uint8Array(v));
      throw new TypeError("Buffer.from takes a string, an array or bytes");
    },
    alloc: function (n, fill) {
      var u = new Uint8Array(n);
      if (fill !== undefined) u.fill(typeof fill === "number" ? fill : utf8Bytes(String(fill))[0] || 0);
      return dress(u);
    },
    isBuffer: function (v) { return !!(v && v.__isBuffer); },
    byteLength: function (v, enc) { return typeof v === "string" ? encode(v, enc).length : (bytesOf(v) || []).length; },
    concat: function (list, total) {
      var n = 0;
      for (var i = 0; i < list.length; i++) n += list[i].length;
      var u = new Uint8Array(total === undefined ? n : total);
      var at = 0;
      for (var j = 0; j < list.length && at < u.length; j++) {
        u.set(list[j].subarray(0, u.length - at), at);
        at += list[j].length;
      }
      return dress(u);
    },
    isEncoding: function (e) { try { encoding(e); return true; } catch (x) { return false; } }
  };

  // ---- path (POSIX)

  function normalizeParts(p, absolute) {
    var out = [];
    var parts = p.split("/");
    for (var i = 0; i < parts.length; i++) {
      var s = parts[i];
      if (s === "" || s === ".") continue;
      if (s === "..") {
        if (out.length && out[out.length - 1] !== "..") out.pop();
        else if (!absolute) out.push("..");
        continue;
      }
      out.push(s);
    }
    return out;
  }
  function str(p, name) {
    if (typeof p !== "string") throw new TypeError("The \"" + (name || "path") + "\" argument must be of type string. Received " + (p === null ? "null" : typeof p));
    return p;
  }
  var path = {
    sep: "/",
    delimiter: ":",
    normalize: function (p) {
      str(p);
      if (p === "") return ".";
      var abs = p[0] === "/";
      var trail = p[p.length - 1] === "/";
      var s = normalizeParts(p, abs).join("/");
      if (s === "" && !abs) s = ".";
      if (s !== "" && trail) s += "/";
      return (abs ? "/" : "") + s;
    },
    join: function () {
      var parts = [];
      for (var i = 0; i < arguments.length; i++) {
        str(arguments[i]);
        if (arguments[i] !== "") parts.push(arguments[i]);
      }
      return parts.length ? path.normalize(parts.join("/")) : ".";
    },
    resolve: function () {
      var p = "";
      for (var i = arguments.length - 1; i >= 0 && p[0] !== "/"; i--) {
        str(arguments[i]);
        if (arguments[i] !== "") p = arguments[i] + (p ? "/" + p : "");
      }
      if (p[0] !== "/") p = cwd + (p ? "/" + p : "");
      return "/" + normalizeParts(p, true).join("/");
    },
    isAbsolute: function (p) { return str(p).length > 0 && p[0] === "/"; },
    dirname: function (p) {
      str(p);
      if (p === "") return ".";
      var s = p.replace(/\/+$/, "");
      if (s === "") return "/";
      var at = s.lastIndexOf("/");
      if (at < 0) return ".";
      if (at === 0) return "/";
      return s.slice(0, at).replace(/\/+$/, "") || "/";
    },
    basename: function (p, ext) {
      str(p);
      var s = p.replace(/\/+$/, "");
      var b = s.slice(s.lastIndexOf("/") + 1);
      if (ext !== undefined && b !== ext && b.slice(-ext.length) === ext) b = b.slice(0, b.length - ext.length);
      return b;
    },
    extname: function (p) {
      var b = path.basename(p);
      var at = b.lastIndexOf(".");
      return at <= 0 ? "" : b.slice(at);
    },
    relative: function (from, to) {
      var a = normalizeParts(path.resolve(from), true);
      var b = normalizeParts(path.resolve(to), true);
      var i = 0;
      while (i < a.length && i < b.length && a[i] === b[i]) i++;
      var up = [];
      for (var j = i; j < a.length; j++) up.push("..");
      return up.concat(b.slice(i)).join("/");
    },
    parse: function (p) {
      str(p);
      var root = p[0] === "/" ? "/" : "";
      var base = path.basename(p);
      var ext = path.extname(p);
      var dir = path.dirname(p);
      if (dir === "." && p.indexOf("/") < 0) dir = "";
      return { root: root, dir: dir, base: base, ext: ext, name: ext ? base.slice(0, base.length - ext.length) : base };
    },
    format: function (o) {
      var dir = o.dir || o.root || "";
      var base = o.base || ((o.name || "") + (o.ext || ""));
      if (!dir) return base;
      return dir === o.root ? dir + base : dir + "/" + base;
    },
    toNamespacedPath: function (p) { return p; }
  };
  path.posix = path;

  // ---- the file system

  var DESC = {
    ENOENT: "no such file or directory",
    EISDIR: "illegal operation on a directory",
    ENOTDIR: "not a directory",
    EEXIST: "file already exists",
    ENOTEMPTY: "directory not empty",
    EACCES: "permission denied",
    ENOTSUP: "operation not supported",
    EINVAL: "invalid argument"
  };
  var ERRNO = { ENOENT: -2, EISDIR: -21, ENOTDIR: -20, EEXIST: -17, ENOTEMPTY: -39, EACCES: -13, ENOTSUP: -95, EINVAL: -22 };
  function fail(code, syscall, p, why) {
    var e = new Error(code + ": " + DESC[code] + ", " + syscall + " '" + p + "'" + (why ? " (" + why + ")" : ""));
    e.code = code;
    e.errno = ERRNO[code];
    e.syscall = syscall;
    e.path = p;
    throw e;
  }
  function abs(p) {
    if (p && typeof p === "object" && typeof p.href === "string" && p.protocol === "file:") p = p.pathname;
    if (bytesOf(p)) p = utf8Text(bytesOf(p));
    return path.resolve(str(p));
  }
  function parentOf(a) { return path.dirname(a); }
  function addDirs(a) {
    var d = parentOf(a);
    while (!dirs[d]) {
      dirs[d] = true;
      d = parentOf(d);
    }
  }
  function opts(o, enc) {
    if (typeof o === "string") return { encoding: o };
    return o || (enc ? { encoding: enc } : {});
  }
  function isDir(a) { return !!dirs[a]; }
  function entry(a, syscall) {
    if (isDir(a)) fail("EISDIR", syscall, a);
    var f = files[a];
    if (!f) fail("ENOENT", syscall, a);
    return f;
  }
  function needParent(a, syscall) {
    var d = parentOf(a);
    if (files[d]) fail("ENOTDIR", syscall, a);
    if (!dirs[d]) fail("ENOENT", syscall, a);
  }
  function writable(a, syscall) {
    var f = files[a];
    if (f && f.readonly) fail("EACCES", syscall, a, f.why || "read only");
  }
  function contentOf(f, a, syscall, enc) {
    if (f.binary && !f.bytes) fail("ENOTSUP", syscall, a, "the bytes of a picture or a workbook are not handed to scripts; a workbook's sheets are there as CSV");
    if (f.bytes) {
      if (enc) return decode(f.bytes, enc);
      return dress(new Uint8Array(f.bytes));
    }
    if (enc && encoding(enc) !== "utf8") return decode(utf8Bytes(f.text), enc);
    return f.text;
  }
  function put(a, data, enc, syscall) {
    writable(a, syscall);
    needParent(a, syscall);
    var f = { changed: true, mtime: Date.now() };
    var b = bytesOf(data);
    if (b) {
      f.bytes = new Uint8Array(b);
      f.size = b.length;
    } else {
      var s = data === undefined || data === null ? String(data) : String(data);
      if (enc && encoding(enc) !== "utf8") {
        f.bytes = encode(s, enc);
        f.size = f.bytes.length;
      } else {
        f.text = s;
        f.size = utf8Bytes(s).length;
      }
    }
    var old = files[a];
    if (old && old.mounted) {
      f.mounted = true;
      f.orig = old.orig;
    }
    files[a] = f;
    delete gone[a];
  }
  function drop(a) {
    var f = files[a];
    delete files[a];
    if (f && f.mounted) gone[a] = true;
  }
  function Stats(a) {
    var dir = isDir(a);
    var f = files[a];
    this.size = dir ? 0 : f.size;
    this.mtimeMs = dir ? started : f.mtime || started;
    this.mtime = new Date(this.mtimeMs);
    this.atime = this.mtime;
    this.ctime = this.mtime;
    this.birthtime = new Date(started);
    this.mode = dir ? 16877 : (f.readonly ? 33060 : 33188);
    this.__dir = dir;
  }
  Stats.prototype.isFile = function () { return !this.__dir; };
  Stats.prototype.isDirectory = function () { return this.__dir; };
  Stats.prototype.isSymbolicLink = function () { return false; };
  function Dirent(name, parent, dir) {
    this.name = name;
    this.parentPath = parent;
    this.path = parent;
    this.__dir = dir;
  }
  Dirent.prototype.isFile = function () { return !this.__dir; };
  Dirent.prototype.isDirectory = function () { return this.__dir; };
  Dirent.prototype.isSymbolicLink = function () { return false; };
  function children(a) {
    var pre = a === "/" ? "/" : a + "/";
    var seen = Object.create(null);
    var out = [];
    function take(p, dir) {
      if (p.indexOf(pre) !== 0 || p === a) return;
      var rest = p.slice(pre.length);
      var name = rest.split("/")[0];
      if (seen[name]) return;
      seen[name] = true;
      out.push({ name: name, dir: dir || rest.indexOf("/") >= 0 });
    }
    for (var d in dirs) take(d, true);
    for (var f in files) take(f, false);
    out.sort(function (x, y) { return x.name < y.name ? -1 : x.name > y.name ? 1 : 0; });
    return out;
  }

  var fs = {
    constants: { F_OK: 0, R_OK: 4, W_OK: 2, X_OK: 1, COPYFILE_EXCL: 1 },
    existsSync: function (p) {
      try { var a = abs(p); return !!(files[a] || dirs[a]); } catch (e) { return false; }
    },
    accessSync: function (p, mode) {
      var a = abs(p);
      if (!files[a] && !dirs[a]) fail("ENOENT", "access", a);
      if ((mode & 2) && files[a] && files[a].readonly) fail("EACCES", "access", a);
    },
    readFileSync: function (p, o) {
      if (typeof p === "number") fail("ENOTSUP", "read", String(p), "file descriptors are not kept");
      var a = abs(p);
      return contentOf(entry(a, "open"), a, "open", opts(o).encoding);
    },
    writeFileSync: function (p, data, o) {
      var a = abs(p);
      var op = opts(o);
      if (isDir(a)) fail("EISDIR", "open", a);
      if (op.flag && String(op.flag).indexOf("x") >= 0 && files[a]) fail("EEXIST", "open", a);
      if (op.flag && String(op.flag)[0] === "a" && files[a]) return fs.appendFileSync(p, data, o);
      put(a, data, op.encoding, "open");
    },
    appendFileSync: function (p, data, o) {
      var a = abs(p);
      var op = opts(o);
      if (isDir(a)) fail("EISDIR", "open", a);
      var f = files[a];
      if (!f) return put(a, data, op.encoding, "open");
      if (f.binary && !f.bytes) fail("ENOTSUP", "open", a, "a picture or a workbook is not appended to");
      var add = bytesOf(data);
      if (f.bytes || add || (op.encoding && encoding(op.encoding) !== "utf8")) {
        var head = f.bytes || utf8Bytes(f.text);
        var tail = add || encode(String(data), op.encoding);
        var u = new Uint8Array(head.length + tail.length);
        u.set(head, 0);
        u.set(tail, head.length);
        put(a, u, null, "open");
      } else {
        put(a, f.text + String(data), null, "open");
      }
    },
    statSync: function (p, o) {
      var a = abs(p);
      if (!files[a] && !dirs[a]) {
        if (o && o.throwIfNoEntry === false) return undefined;
        fail("ENOENT", "stat", a);
      }
      return new Stats(a);
    },
    readdirSync: function (p, o) {
      var a = abs(p);
      if (files[a]) fail("ENOTDIR", "scandir", a);
      if (!dirs[a]) fail("ENOENT", "scandir", a);
      var op = opts(o);
      var out = [];
      function walk(dir, rel) {
        var list = children(dir);
        for (var i = 0; i < list.length; i++) {
          var c = list[i];
          var name = rel ? rel + "/" + c.name : c.name;
          out.push(op.withFileTypes ? new Dirent(c.name, dir, c.dir) : name);
          if (op.recursive && c.dir) walk(dir === "/" ? "/" + c.name : dir + "/" + c.name, name);
        }
      }
      walk(a, "");
      return out;
    },
    mkdirSync: function (p, o) {
      var a = abs(p);
      var op = typeof o === "object" && o ? o : {};
      if (files[a]) fail("EEXIST", "mkdir", a);
      if (dirs[a]) {
        if (op.recursive) return undefined;
        fail("EEXIST", "mkdir", a);
      }
      if (!op.recursive) {
        needParent(a, "mkdir");
        dirs[a] = true;
        return undefined;
      }
      var first = a;
      var d = a;
      while (!dirs[d]) {
        if (files[d]) fail("ENOTDIR", "mkdir", a);
        first = d;
        d = parentOf(d);
      }
      dirs[a] = true;
      addDirs(a);
      return first;
    },
    rmSync: function (p, o) {
      var a = abs(p);
      var op = o || {};
      if (files[a]) {
        writable(a, "rm");
        return drop(a);
      }
      if (!dirs[a]) {
        if (op.force) return;
        fail("ENOENT", "rm", a);
      }
      if (!op.recursive) fail("EISDIR", "rm", a, "use { recursive: true }");
      if (a === "/") fail("EACCES", "rm", a);
      var pre = a + "/";
      for (var f in files) if (f.indexOf(pre) === 0) writable(f, "rm");
      for (var g in files) if (g.indexOf(pre) === 0) drop(g);
      for (var d in dirs) if (d === a || d.indexOf(pre) === 0) delete dirs[d];
    },
    rmdirSync: function (p, o) {
      var a = abs(p);
      if (o && o.recursive) return fs.rmSync(p, { recursive: true });
      if (files[a]) fail("ENOTDIR", "rmdir", a);
      if (!dirs[a]) fail("ENOENT", "rmdir", a);
      if (children(a).length) fail("ENOTEMPTY", "rmdir", a);
      if (a !== "/") delete dirs[a];
    },
    unlinkSync: function (p) {
      var a = abs(p);
      if (dirs[a]) fail("EISDIR", "unlink", a);
      if (!files[a]) fail("ENOENT", "unlink", a);
      writable(a, "unlink");
      drop(a);
    },
    renameSync: function (from, to) {
      var a = abs(from);
      var b = abs(to);
      if (dirs[a]) fail("ENOTSUP", "rename", a, "folders are not renamed; copy their files");
      var f = entry(a, "rename");
      if (f.binary && !f.bytes) fail("ENOTSUP", "rename", a, "a picture or a workbook is not moved by scripts");
      writable(a, "rename");
      fs.copyFileSync(from, to);
      drop(a);
    },
    copyFileSync: function (from, to, mode) {
      var a = abs(from);
      var b = abs(to);
      var f = entry(a, "copyfile");
      if (f.binary && !f.bytes) fail("ENOTSUP", "copyfile", a, "a picture or a workbook is not copied by scripts");
      if ((mode & 1) && files[b]) fail("EEXIST", "copyfile", b);
      if (isDir(b)) fail("EISDIR", "copyfile", b);
      put(b, f.bytes ? f.bytes : f.text, null, "copyfile");
    },
    cpSync: function (from, to, o) {
      var a = abs(from);
      if (!dirs[a]) return fs.copyFileSync(from, to);
      if (!(o && o.recursive)) fail("EISDIR", "cp", a, "use { recursive: true }");
      var b = abs(to);
      fs.mkdirSync(b, { recursive: true });
      var list = fs.readdirSync(a, { recursive: true, withFileTypes: true });
      for (var i = 0; i < list.length; i++) {
        var src = list[i].parentPath + "/" + list[i].name;
        var dst = b + src.slice(a.length);
        if (list[i].isDirectory()) fs.mkdirSync(dst, { recursive: true });
        else fs.copyFileSync(src, dst);
      }
    },
    realpathSync: function (p) {
      var a = abs(p);
      if (!files[a] && !dirs[a]) fail("ENOENT", "realpath", a);
      return a;
    }
  };
  fs.lstatSync = fs.statSync;
  fs.realpathSync.native = fs.realpathSync;

  // the callback forms and fs.promises: the same calls, answered after the
  // script's current step, as Node answers them
  var asyncNames = ["access", "readFile", "writeFile", "appendFile", "stat", "lstat", "readdir", "mkdir", "rm", "rmdir", "unlink", "rename", "copyFile", "cp", "realpath"];
  var promises = {};
  asyncNames.forEach(function (name) {
    var sync = fs[name + "Sync"];
    promises[name] = function () {
      var args = arguments;
      return new Promise(function (ok, no) {
        try { ok(sync.apply(fs, args)); } catch (e) { no(e); }
      });
    };
    fs[name] = function () {
      var args = Array.prototype.slice.call(arguments);
      var cb = typeof args[args.length - 1] === "function" ? args.pop() : null;
      if (!cb) throw new TypeError("The \"cb\" argument must be of type function");
      var out, err = null;
      try { out = sync.apply(fs, args); } catch (e) { err = e; }
      queueMicrotask(function () { if (err) cb(err); else cb(null, out); });
    };
  });
  fs.exists = function (p, cb) { var ok = fs.existsSync(p); queueMicrotask(function () { cb(ok); }); };
  promises.constants = fs.constants;
  fs.promises = promises;

  // ---- process

  function ProcessExit(code) {
    this.name = "ProcessExit";
    this.code = code;
    this.message = "process.exit(" + code + ")";
  }
  ProcessExit.prototype = Object.create(Error.prototype);
  ProcessExit.prototype.constructor = ProcessExit;
  ProcessExit.prototype.toString = function () { return "ProcessExit: " + this.code; };
  var pending = "";
  function writeOut(s) {
    pending += String(s);
    var at = pending.lastIndexOf("\n");
    if (at >= 0) {
      var lines = pending.slice(0, at).split("\n");
      pending = pending.slice(at + 1);
      for (var i = 0; i < lines.length; i++) console.log(lines[i]);
    }
    return true;
  }
  var proc = {
    argv: argv,
    argv0: "node",
    execArgv: [],
    env: {},
    platform: "linux",
    version: "v22.0.0",
    versions: { node: "22.0.0" },
    pid: 1,
    exitCode: undefined,
    cwd: function () { return cwd; },
    chdir: function (d) {
      var a = abs(d);
      if (!dirs[a]) fail(files[a] ? "ENOTDIR" : "ENOENT", "chdir", a);
      cwd = a;
    },
    exit: function (code) {
      var c = code === undefined ? (proc.exitCode || 0) : Number(code) || 0;
      proc.exitCode = c;
      throw new ProcessExit(c);
    },
    hrtime: { bigint: function () { return BigInt(Math.round((Date.now() - started) * 1e6)); } },
    memoryUsage: function () { return { rss: 0, heapTotal: 0, heapUsed: 0, external: 0 }; },
    nextTick: function (fn) { var rest = Array.prototype.slice.call(arguments, 1); queueMicrotask(function () { fn.apply(null, rest); }); },
    on: function () { return proc; },
    stdout: { write: writeOut, isTTY: false },
    stderr: { write: writeOut, isTTY: false }
  };

  // ---- modules

  var util = {
    format: function () {
      var args = Array.prototype.slice.call(arguments);
      var f = String(args.shift());
      var s = f.replace(/%[sdifjoO%]/g, function (m) {
        if (m === "%%") return "%";
        if (!args.length) return m;
        var v = args.shift();
        if (m === "%d" || m === "%i") return String(m === "%i" ? parseInt(v, 10) : Number(v));
        if (m === "%f") return String(parseFloat(v));
        if (m === "%j" || m === "%o" || m === "%O") return JSON.stringify(v);
        return String(v);
      });
      return args.length ? s + " " + args.map(String).join(" ") : s;
    },
    inspect: function (v) { return typeof v === "string" ? "'" + v + "'" : JSON.stringify(v); },
    promisify: function (fn) {
      return function () {
        var args = Array.prototype.slice.call(arguments);
        return new Promise(function (ok, no) { args.push(function (e, v) { if (e) no(e); else ok(v); }); fn.apply(null, args); });
      };
    }
  };
  var os = { EOL: "\n", platform: function () { return "linux"; }, tmpdir: function () { return "/tmp"; }, homedir: function () { return "/"; }, cpus: function () { return []; } };
  var bufferModule = { Buffer: Buffer };
  var table = {
    fs: fs,
    "fs/promises": promises,
    path: path,
    "path/posix": path,
    process: proc,
    buffer: bufferModule,
    util: util,
    os: os
  };
  var names = [];
  for (var n in table) {
    var m = table[n];
    Object.defineProperty(m, "default", { value: m, enumerable: false, configurable: true });
    defineModule(n, m);
    defineModule("node:" + n, m);
    names.push(n);
  }
  function require(name) {
    var key = String(name).replace(/^node:/, "");
    if (table[key]) return table[key];
    if (key[0] === "." || key[0] === "/") throw new Error("Cannot find module '" + name + "': scripts are one file; Node's own modules are " + names.join(", "));
    throw new Error("Cannot find module '" + name + "': there are no npm packages here; Node's own modules are " + names.join(", "));
  }
  var exportsObj = {};
  globalThis.require = require;
  globalThis.process = proc;
  globalThis.Buffer = Buffer;
  globalThis.module = { exports: exportsObj };
  globalThis.exports = exportsObj;
  globalThis.__filename = "/script.js";
  globalThis.__dirname = "/";

  // ---- the host's side

  // { files: [{ path, text } | { path, size, binary: true } | { path, text,
  // readonly: true, why }], argv: [...], script: "code/x.js" }
  globalThis.__nodeMount = function (json) {
    var m = JSON.parse(json);
    dirs["/"] = true;
    var base = ["/data", "/media", "/apps", "/code", "/tmp"];
    for (var i = 0; i < base.length; i++) dirs[base[i]] = true;
    var list = m.files || [];
    for (var j = 0; j < list.length; j++) {
      var f = list[j];
      var a = path.resolve("/", f.path);
      files[a] = {
        text: f.binary ? undefined : String(f.text || ""),
        binary: !!f.binary,
        size: f.size !== undefined ? f.size : utf8Bytes(String(f.text || "")).length,
        readonly: !!f.readonly,
        why: f.why || "",
        mounted: true,
        orig: f.binary ? undefined : String(f.text || ""),
        mtime: started
      };
      addDirs(a);
    }
    if (m.script) {
      globalThis.__filename = path.resolve("/", m.script);
      globalThis.__dirname = path.dirname(globalThis.__filename);
      argv[1] = globalThis.__filename;
    }
    var more = m.argv || [];
    for (var k = 0; k < more.length; k++) argv.push(String(more[k]));
    return "ok";
  };

  // what the script left: { written: [{ path, text } | { path, base64 }],
  // removed: [paths], exitCode }
  globalThis.__nodeChanges = function () {
    if (pending) {
      console.log(pending);
      pending = "";
    }
    var written = [];
    for (var a in files) {
      var f = files[a];
      if (!f.changed) continue;
      if (!f.bytes && f.orig !== undefined && f.text === f.orig) continue;
      var rel = a.slice(1);
      if (f.bytes) written.push({ path: rel, base64: toBase64(f.bytes), size: f.bytes.length });
      else written.push({ path: rel, text: f.text, size: f.size });
    }
    written.sort(function (x, y) { return x.path < y.path ? -1 : 1; });
    var removed = [];
    for (var g in gone) removed.push(g.slice(1));
    removed.sort();
    return JSON.stringify({ written: written, removed: removed, exitCode: proc.exitCode === undefined ? 0 : proc.exitCode });
  };
})();
`;
