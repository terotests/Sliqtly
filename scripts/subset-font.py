#!/usr/bin/env python3
"""Cut a TrueType face down to the characters a slide uses.

    python3 scripts/subset-font.py IN.ttf OUT.ttf

Lato from Google Fonts is 650-720 KB a face: outlines for some 3 000 glyphs
(Cyrillic, Greek, Vietnamese, ...) and 210 KB of kerning pairs between all of
them. A deck in Lato fetched four faces, 2.7 MB, before its first slide.

What stays: the glyphs of Latin (with every extension), the combining marks,
punctuation, currency, letterlike symbols, number forms, arrows, mathematical
operators and geometric shapes, and the components of composite glyphs. Glyph
ids do not change; a glyph that goes keeps its id with no outline. `cmap` is
written again (format 4) with only the kept characters, so a browser draws
the rest from another face instead of a blank. GPOS and GSUB go: the slide
layout measures with hmtx alone and draws no ligatures, so the screen and the
layout keep agreeing.

No dependencies beyond the standard library (fontTools cannot be installed
everywhere this runs).
"""
import struct
import sys

KEEP_RANGES = [
    (0x0020, 0x024F),  # Basic Latin, Latin-1, Latin Extended-A/B
    (0x0250, 0x02FF),  # IPA, spacing modifiers
    (0x0300, 0x036F),  # combining marks
    (0x1E00, 0x1EFF),  # Latin Extended Additional
    (0x2000, 0x206F),  # general punctuation
    (0x2070, 0x209F),  # super- and subscripts
    (0x20A0, 0x20CF),  # currency
    (0x2100, 0x218F),  # letterlike, number forms
    (0x2190, 0x21FF),  # arrows
    (0x2200, 0x22FF),  # mathematical operators
    (0x25A0, 0x25FF),  # geometric shapes
    (0xFB00, 0xFB06),  # Latin ligatures
]
DROP_TABLES = {b"GPOS", b"GSUB", b"DSIG", b"hdmx", b"LTSH", b"VDMX"}


def kept(cp):
    return any(a <= cp <= b for a, b in KEEP_RANGES)


def read_tables(data):
    n = struct.unpack(">H", data[4:6])[0]
    tables = {}
    for i in range(n):
        tag, _cs, off, ln = struct.unpack(">4sIII", data[12 + 16 * i:28 + 16 * i])
        tables[tag] = data[off:off + ln]
    return tables


def cmap_map(cmap):
    """Unicode → glyph id from the best subtable (format 12 or 4)."""
    n = struct.unpack(">H", cmap[2:4])[0]
    best = None
    for i in range(n):
        pid, eid, off = struct.unpack(">HHI", cmap[4 + 8 * i:12 + 8 * i])
        fmt = struct.unpack(">H", cmap[off:off + 2])[0]
        if pid in (0, 3) and fmt in (4, 12):
            if best is None or fmt == 12:
                best = (fmt, off)
    if best is None:
        raise SystemExit("no Unicode cmap (format 4 or 12)")
    fmt, off = best
    out = {}
    if fmt == 12:
        ngroups = struct.unpack(">I", cmap[off + 12:off + 16])[0]
        for g in range(ngroups):
            s, e, gid = struct.unpack(">III", cmap[off + 16 + 12 * g:off + 28 + 12 * g])
            for cp in range(s, e + 1):
                out[cp] = gid + (cp - s)
        return out
    segx2 = struct.unpack(">H", cmap[off + 6:off + 8])[0]
    seg = segx2 // 2
    ends = struct.unpack(">%dH" % seg, cmap[off + 14:off + 14 + segx2])
    base = off + 16 + segx2
    starts = struct.unpack(">%dH" % seg, cmap[base:base + segx2])
    deltas = struct.unpack(">%dh" % seg, cmap[base + segx2:base + 2 * segx2])
    ro_at = base + 2 * segx2
    ros = struct.unpack(">%dH" % seg, cmap[ro_at:ro_at + segx2])
    for i in range(seg):
        for cp in range(starts[i], ends[i] + 1):
            if cp == 0xFFFF:
                continue
            if ros[i] == 0:
                gid = (cp + deltas[i]) & 0xFFFF
            else:
                at = ro_at + 2 * i + ros[i] + 2 * (cp - starts[i])
                gid = struct.unpack(">H", cmap[at:at + 2])[0]
                if gid:
                    gid = (gid + deltas[i]) & 0xFFFF
            if gid:
                out[cp] = gid
    return out


def cmap_format4(mapping):
    """A cmap table with one format 4 subtable (3,1) for `mapping` (BMP)."""
    cps = sorted(cp for cp in mapping if cp <= 0xFFFF)
    segs = []
    for cp in cps:
        gid = mapping[cp]
        if segs and cp == segs[-1][1] + 1 and gid == segs[-1][2] + (cp - segs[-1][0]):
            segs[-1][1] = cp
        else:
            segs.append([cp, cp, gid])
    segs.append([0xFFFF, 0xFFFF, 0])
    n = len(segs)
    ends = [s[1] for s in segs]
    starts = [s[0] for s in segs]
    deltas = [((s[2] - s[0]) & 0xFFFF) if s[0] != 0xFFFF else 1 for s in segs]
    ros = [0] * n
    p = 1
    while p * 2 <= n:
        p *= 2
    search = 2 * p
    sub = struct.pack(">HHHHHHH", 4, 0, 0, 2 * n, search, p.bit_length() - 1, 2 * n - search)
    sub += struct.pack(">%dH" % n, *ends) + b"\0\0" + struct.pack(">%dH" % n, *starts)
    sub += struct.pack(">%dH" % n, *deltas) + struct.pack(">%dH" % n, *ros)
    sub = sub[:2] + struct.pack(">H", len(sub)) + sub[4:]
    return struct.pack(">HHHHI", 0, 1, 3, 1, 12) + sub


def components(glyph):
    """The glyph ids a composite glyph is built from."""
    if len(glyph) < 10 or struct.unpack(">h", glyph[0:2])[0] >= 0:
        return []
    out, at = [], 10
    while True:
        flags, gid = struct.unpack(">HH", glyph[at:at + 4])
        out.append(gid)
        at += 4 + (4 if flags & 0x0001 else 2)
        if flags & 0x0008:
            at += 2
        elif flags & 0x0040:
            at += 4
        elif flags & 0x0080:
            at += 8
        if not flags & 0x0020:
            return out


def checksum(b):
    b = b + b"\0" * ((4 - len(b) % 4) % 4)
    return sum(struct.unpack(">%dI" % (len(b) // 4), b)) & 0xFFFFFFFF


def subset(data):
    t = read_tables(data)
    head = bytearray(t[b"head"])
    long_loca = struct.unpack(">h", head[50:52])[0] == 1
    n_glyphs = struct.unpack(">H", t[b"maxp"][4:6])[0]
    loca = t[b"loca"]
    if long_loca:
        offs = struct.unpack(">%dI" % (n_glyphs + 1), loca[:4 * (n_glyphs + 1)])
    else:
        offs = [o * 2 for o in struct.unpack(">%dH" % (n_glyphs + 1), loca[:2 * (n_glyphs + 1)])]
    glyf = t[b"glyf"]
    mapping = {cp: g for cp, g in cmap_map(t[b"cmap"]).items() if kept(cp)}
    keep = {0} | set(mapping.values())
    todo = list(keep)
    while todo:
        g = todo.pop()
        for c in components(glyf[offs[g]:offs[g + 1]]):
            if c not in keep:
                keep.add(c)
                todo.append(c)
    new_glyf, new_offs = bytearray(), []
    for g in range(n_glyphs):
        new_offs.append(len(new_glyf))
        if g in keep:
            new_glyf += glyf[offs[g]:offs[g + 1]]
            new_glyf += b"\0" * ((4 - len(new_glyf) % 4) % 4)
    new_offs.append(len(new_glyf))
    head[50:52] = struct.pack(">h", 1)
    head[8:12] = b"\0\0\0\0"
    t[b"head"] = bytes(head)
    t[b"loca"] = struct.pack(">%dI" % len(new_offs), *new_offs)
    t[b"glyf"] = bytes(new_glyf)
    t[b"cmap"] = cmap_format4(mapping)
    for tag in DROP_TABLES:
        t.pop(tag, None)
    tags = sorted(t)
    n = len(tags)
    p = 1
    while p * 2 <= n:
        p *= 2
    out = bytearray(struct.pack(">IHHHH", 0x00010000, n, 16 * p, p.bit_length() - 1, 16 * n - 16 * p))
    at = 12 + 16 * n
    body = bytearray()
    head_at = 0
    for tag in tags:
        b = t[tag]
        if tag == b"head":
            head_at = at + len(body)
        out += struct.pack(">4sIII", tag, checksum(b), at + len(body), len(b))
        body += b + b"\0" * ((4 - len(b) % 4) % 4)
    out += body
    adj = (0xB1B0AFBA - checksum(bytes(out))) & 0xFFFFFFFF
    out[head_at + 8:head_at + 12] = struct.pack(">I", adj)
    return bytes(out), len(keep), n_glyphs


if __name__ == "__main__":
    src, dst = sys.argv[1], sys.argv[2]
    data = open(src, "rb").read()
    out, k, n = subset(data)
    open(dst, "wb").write(out)
    print(f"{dst}: {len(data)} → {len(out)} bytes, {k} of {n} glyphs drawn")
