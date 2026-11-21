# SPDX-License-Identifier: AGPL-3.0-or-later
# Writes native/gfx_studio.rgr: one Ranger operator per native call.
# Each entry: (operator, return type, [(arg, type)], C name if different)
import os
os.chdir(os.path.join(os.path.dirname(os.path.abspath(__file__)), '..'))
ops = [
 ("sh_open","int",[("title","string"),("w","int"),("h","int"),("hidden","int")]),
 ("sh_close","void",[]),
 ("sh_poll","void",[]),
 ("sh_should_close","int",[]),
 ("sh_window_w","int",[]),("sh_window_h","int",[]),
 ("sh_drawable_w","int",[]),("sh_drawable_h","int",[]),
 ("sh_swap","void",[]),
 ("sh_delay","void",[("ms","int")]),
 ("sh_now_ms","double",[]),
 ("sh_set_title","void",[("t","string")]),
 ("sh_set_clipboard","void",[("t","string")]),
 ("sh_get_clipboard","string",[]),
 ("sh_mouse_x","int",[]),("sh_mouse_y","int",[]),("sh_mouse_buttons","int",[]),
 ("sh_wheel_y","int",[]),("sh_wheel_x","int",[]),
 ("sh_shift","int",[]),("sh_ctrl","int",[]),
 ("sh_pop_key","int",[]),
 ("sh_take_text","string",[]),
 ("sh_take_game_keys","string",[]),
 ("sh_list_files","string",[("root","string")]),
 ("sh_is_dir","int",[("p","string")]),
 ("sh_exists","int",[("p","string")]),
 ("sh_read_text","string",[("p","string")]),
 ("sh_write_text","int",[("p","string"),("t","string")]),
 ("sh_read_bytes","buffer",[("p","string")]),
 ("sh_real_path","string",[("p","string")]),
 ("sh_env","string",[("n","string")]),
 ("sh_base_path","string",[]),
 ("sh_file_mtime","double",[("p","string")]),
 ("sh_rg_put_file","int",[("vfs","string"),("disk","string")]),
 ("sh_rg_put_head","int",[("vfs","string"),("disk","string"),("max","int")]),
 ("sh_rg_put_text","void",[("vfs","string"),("t","string")]),
 ("sh_rg_start","int",[("id","string")]),
 ("sh_rg_resize","void",[("w","double"),("h","double")]),
 ("sh_rg_pointer","void",[("kind","int"),("id","int"),("x","double"),("y","double")]),
 ("sh_rg_key","void",[("down","int"),("name","string")]),
 ("sh_rg_frame","int",[("dt","double")]),
 ("sh_rg_draw_len","int",[]),
 ("sh_rg_draw_at","double",[("i","int")]),
 ("sh_rg_string","string",[("i","int")]),
 ("sh_rg_log","string",[]),
 ("sh_rg_cmds","string",[]),
 ("sh_rg_view","double",[("i","int")]),
 ("sh_rg_read","buffer",[("p","string")]),
 ("sh_rg_eval","string",[("src","string")]),
 ("sh_rg_reset","void",[]),
 ("sh_image_upload","int",[("key","int"),("bytes","buffer")]),
 ("sh_image_w","int",[]),("sh_image_h","int",[]),
 ("sh_image_error","string",[]),
 ("sh_rg_restyle","int",[]),
 ("sh_screenshot","int",[("p","string")]),
 ("sh_print","void",[("t","string")]),
 ("sh_screenshot_rect","int",[("p","string"),("x","double"),("y","double"),("w","double"),("h","double")]),
 ("sh_mkdirs","int",[("p","string")]),
 ("sh_append_text","int",[("p","string"),("t","string")]),
 ("sh_local_time","string",[]),
 ("sh_file_size","double",[("p","string")]),
 ("sh_spawn","int",[("cmd","string")]),
 ("sh_mcp_start","int",[]),
 ("sh_mcp_next","string",[]),
 ("sh_mcp_arg","string",[("k","string")]),
 ("sh_mcp_reply","void",[("t","string"),("err","int")]),
 ("sh_mcp_reply_image","void",[("p","string"),("t","string")]),
 # GL
 ("evg_begin","void",[("pw","int"),("ph","int"),("dw","int"),("dh","int")],"evggl_begin"),
 ("evg_set_atlas","void",[("px","buffer"),("w","int"),("h","int")],"evggl_set_atlas"),
 ("evg_rect","void",[(a,"double") for a in "x y w h r g b a radius".split()],"evggl_rect"),
 ("evg_border","void",[(a,"double") for a in "x y w h t r g b a radius".split()],"evggl_border"),
 ("evg_rect_rot","void",[(a,"double") for a in "x y w h r g b a radius rot".split()],"evggl_rect_rot"),
 ("evg_border_rot","void",[(a,"double") for a in "x y w h t r g b a radius rot".split()],"evggl_border_rot"),
 ("evg_text","void",[(a,"double") for a in "x y w h u0 v0 u1 v1 r g b a rot".split()],"evggl_text"),
 ("evg_clip","void",[(a,"double") for a in "x y w h".split()],"evggl_clip"),
 ("evg_flush","void",[],"evggl_flush"),
 ("evg_clip_off","void",[],"evggl_clip_off"),
 ("evg_tris","void",[("pts","[double]"),("r","double"),("g","double"),("b","double"),("a","double")],"evggl_tris_doubles"),
 ("evg_image_has","int",[("key","int")],"evggl_image_has"),
 ("evg_image_upload","void",[("key","int"),("px","buffer"),("w","int"),("h","int")],"evggl_image_upload"),
 ("evg_image_key","void",[("x","double"),("y","double"),("w","double"),("h","double"),("key","int"),("u0","double"),("v0","double"),("u1","double"),("v1","double"),("alpha","double"),("radius","double"),("rot","double")],"evggl_image_key"),
 ("evg_sphere","void",[("x","double"),("y","double"),("w","double"),("h","double"),("key","int"),("bx","double"),("by","double"),("bw","double"),("bh","double"),("p","[double]")],"evggl_sphere"),
 ("evg_image_forget_all","void",[],"evggl_image_forget_all"),
]
out = ["; SPDX-License-Identifier: AGPL-3.0-or-later",
"; gfx_studio.rgr — Ranger operators over the Studio's native side",
"; (native/studio_host.h: SDL3 window, files, the RangerGames engine, GL drawer).",
"; Written by native/gen_ops.py: change the list there and run it",
"; (python3 native/gen_ops.py from studio/), never this file by hand.",
"", "operators {"]
for op in ops:
    name, ret, args = op[0], op[1], op[2]
    cname = op[3] if len(op) > 3 else name
    sig = " ".join(f"{a}:{t}" for a,t in args)
    parts = []
    for i,(a,t) in enumerate(args):
        e = f"(e {i+1})"
        if t == "buffer" and cname.startswith("evggl"): parts.append(f'{e} ".data()"')
        elif t == "[double]": parts.append(f'{e} ".data(), (int)" {e} ".size()"')
        elif t == "double" and cname.startswith("evggl"): parts.append(f'"(float)(" {e} ")"')
        else: parts.append(e)
    body = ' ", " '.join(parts)
    if body: tpl = f'"{cname}(" {body} ")"'
    else: tpl = f'"{cname}()"'
    out.append(f"    {name} _:{ret} ({sig}) {{")
    out.append("        templates {")
    out.append(f'            cpp ( {tpl} (imp "\\"studio_host.h\\"") )')
    out.append("        }")
    out.append("    }")
out.append("}")
open("native/gfx_studio.rgr","w").write("\n".join(out)+"\n")
