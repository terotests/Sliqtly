// SPDX-License-Identifier: AGPL-3.0-or-later

// The programs on slides (```app) tried here: create_presentation and
// update_presentation run each program's first frames in CErXes, the engine
// the page runs them in (cerxes.wasm from the built page, run with wazero),
// and the report says a syntax error, what the program threw, or a view()
// that gave no element tree, before anyone opens the deck.
//
// The engine comes with the built page (webdist, the Docker image's web
// stage; SLIQTLY_CERXES_DIR for a folder of one's own). Without it nothing
// is tried and nothing is said.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// how long a program's loading and first frames may take: the page stops
// one after 3 s (web/apps.js LIMIT_MS)
const cerxesCheckTime = 3 * time.Second

// frames run: the first view(), and two ticks after it
const cerxesCheckFrames = 3

type cerxesEngine struct {
	once     sync.Once
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	runtime  string
	err      error
}

var cerxes cerxesEngine

// the folder the engine is read from: SLIQTLY_CERXES_DIR, else the built page
func cerxesFiles() fs.FS {
	if dir := os.Getenv("SLIQTLY_CERXES_DIR"); dir != "" {
		return os.DirFS(dir)
	}
	sub, err := fs.Sub(webdistFiles, "webdist")
	if err != nil {
		return nil
	}
	return sub
}

// rawText is the text of `export const NAME = String.raw`…`;`, the form
// web/apps-runtime.js and componentengine's runtime.js keep theirs in.
func rawText(module string) (string, error) {
	at := strings.Index(module, "String.raw`")
	end := strings.LastIndex(module, "`")
	if at < 0 || end <= at+len("String.raw`") {
		return "", errors.New("no String.raw` text")
	}
	return module[at+len("String.raw`") : end], nil
}

func (e *cerxesEngine) start() error {
	e.once.Do(func() {
		files := cerxesFiles()
		if files == nil {
			e.err = errors.New("no built page")
			return
		}
		wasm, err := fs.ReadFile(files, "cerxes.wasm")
		if err != nil {
			e.err = err
			return
		}
		var parts []string
		for _, f := range []string{"cerxes-runtime.js", "apps-runtime.js", "script-sel.js", "script-runtime.js", "sprite-runtime.js", "presenter-runtime.js"} {
			b, err := fs.ReadFile(files, f)
			if err != nil {
				e.err = err
				return
			}
			text, err := rawText(string(b))
			if err != nil {
				e.err = errors.New(f + ": " + err.Error())
				return
			}
			parts = append(parts, text)
		}
		e.runtime = strings.Join(parts, "\n")
		ctx := context.Background()
		e.rt = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
		wasi_snapshot_preview1.MustInstantiate(ctx, e.rt)
		e.compiled, e.err = e.rt.CompileModule(ctx, wasm)
	})
	return e.err
}

// one engine instance's calls
type cerxesRun struct {
	ctx context.Context
	mod api.Module
}

func (r *cerxesRun) call(fn string, args ...uint64) (uint64, error) {
	f := r.mod.ExportedFunction(fn)
	if f == nil {
		return 0, errors.New("cerxes.wasm has no " + fn)
	}
	res, err := f.Call(r.ctx, args...)
	if err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}

func (r *cerxesRun) put(s string) (uint64, uint64, error) {
	p, err := r.call("cx_alloc", uint64(len(s)))
	if err != nil {
		return 0, 0, err
	}
	if !r.mod.Memory().Write(uint32(p), []byte(s)) {
		return 0, 0, errors.New("out of memory")
	}
	return p, uint64(len(s)), nil
}

// the engine's last result (a value, or the error's text)
func (r *cerxesRun) result() string {
	p, _ := r.call("cx_result_ptr")
	n, _ := r.call("cx_result_len")
	b, ok := r.mod.Memory().Read(uint32(p), uint32(n))
	if !ok {
		return ""
	}
	return string(b)
}

// eval runs src in engine `en`: "" when it ran, else the error
func (r *cerxesRun) eval(en uint64, src string) (string, error) {
	p, n, err := r.put(src)
	if err != nil {
		return "", err
	}
	failed, err := r.call("cx_eval", en, p, n)
	if err != nil {
		return "", err
	}
	r.call("cx_free", p, n)
	if failed != 0 {
		return r.result(), nil
	}
	return "", nil
}

// AppCheck is host_app_check: why the program `src` does not run (its
// syntax error, what it threw in its first frames, a view() that is no
// element tree), "" when it runs, "-" when there is no engine to try it in.
func (h *McpHost) AppCheck(src string, w, hgt float64) string {
	return h.appRun(src, w, hgt).why
}

// AppRun is host_app_run: AppCheck's answer, the element tree of the
// program's last frame tried (PresPlayView.setTree), how long it asked its
// slide to stay (presentation.video, 0 none) and whether it runs on its own
// clock (it defines tick), as {"why", "tree", "video", "tick"}; the tree is
// "" when it did not run.
func (h *McpHost) AppRun(src string, w, hgt float64) string {
	a := h.appRun(src, w, hgt)
	b, _ := json.Marshal(map[string]any{"why": a.why, "tree": a.tree, "video": a.video, "tick": a.tick})
	return string(b)
}

type appRan struct {
	why, tree string
	video     float64
	tick      bool
}

// videoAsked: the seconds a frame's asks give with presentation.video, -1 for none
func videoAsked(asks string) float64 {
	var list []struct {
		K       string  `json:"k"`
		Seconds float64 `json:"seconds"`
	}
	if json.Unmarshal([]byte(asks), &list) != nil {
		return -1
	}
	got := -1.0
	for _, a := range list {
		if a.K == "video" {
			got = a.Seconds
		}
	}
	return got
}

func (h *McpHost) appRun(src string, w, hgt float64) appRan {
	if err := cerxes.start(); err != nil {
		return appRan{why: "-"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), cerxesCheckTime)
	defer cancel()
	mod, err := cerxes.rt.InstantiateModule(ctx, cerxes.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize"))
	if err != nil {
		return appRan{why: "-"}
	}
	defer mod.Close(context.Background())
	r := &cerxesRun{ctx: ctx, mod: mod}
	last := ""
	video := 0.0
	tick := false
	why, err := func() (string, error) {
		en, err := r.call("cx_new")
		if err != nil {
			return "", err
		}
		if why, err := r.eval(en, cerxes.runtime); err != nil || why != "" {
			if why != "" {
				err = errors.New("runtime: " + why)
			}
			return "", err
		}
		if why, err := r.eval(en, src); err != nil || why != "" {
			return why, err
		}
		if why, err := r.eval(en, "typeof tick"); err == nil && why == "" {
			tick = r.result() == "function"
		}
		name, nn, err := r.put("__deckFrame")
		if err != nil {
			return "", err
		}
		for i := 0; i < cerxesCheckFrames; i++ {
			arg, _ := json.Marshal(map[string]any{
				"w": w, "h": hgt, "dt": 0.016, "time": float64(i) * 0.016,
				"keys": map[string]any{}, "pointer": map[string]any{"x": 0, "y": 0, "down": false, "inside": false},
				"events": []any{}, "deck": map[string]any{"slide": 1, "slides": 1, "home": 1, "mode": "present", "data": map[string]any{}},
			})
			ap, an, err := r.put(string(arg))
			if err != nil {
				return "", err
			}
			failed, err := r.call("cx_call", en, name, nn, ap, an)
			if err != nil {
				return "", err
			}
			r.call("cx_free", ap, an)
			out := r.result()
			if failed != 0 {
				return out, nil
			}
			// the tree, a line break, the asks (web/apps-runtime.js __deckFrame)
			tree := out
			if at := strings.LastIndex(out, "\n"); at >= 0 {
				tree = out[:at]
				if v := videoAsked(out[at+1:]); v >= 0 {
					video = v
				}
			}
			if !strings.HasPrefix(strings.TrimSpace(tree), "{") {
				return "view() did not give an element tree", nil
			}
			last = tree
		}
		return "", nil
	}()
	if err != nil {
		if ctx.Err() != nil {
			return appRan{why: "its first frames took longer than 3 s (an endless loop?)"}
		}
		// the engine itself failed, not the program: nothing to say of it
		return appRan{why: "-"}
	}
	if why != "" {
		return appRan{why: why}
	}
	return appRan{tree: last, video: video, tick: tick}
}

// ScriptRun is host_script_run: a slide's script (`{script=…}`) loaded and
// its runtime function `fn` called with `arg` (web/script-runtime.js:
// __scriptFinal for where it ends, __scriptAt for a moment of it). The frame
// as JSON; "!" and why when the script does not run; "-" when there is no
// engine to run it in.
func (h *McpHost) ScriptRun(src, fn, arg string) string {
	if err := cerxes.start(); err != nil {
		return "-"
	}
	ctx, cancel := context.WithTimeout(context.Background(), cerxesCheckTime)
	defer cancel()
	mod, err := cerxes.rt.InstantiateModule(ctx, cerxes.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize"))
	if err != nil {
		return "-"
	}
	defer mod.Close(context.Background())
	r := &cerxesRun{ctx: ctx, mod: mod}
	out, err := func() (string, error) {
		en, err := r.call("cx_new")
		if err != nil {
			return "", err
		}
		if why, err := r.eval(en, cerxes.runtime); err != nil || why != "" {
			if why != "" {
				err = errors.New("runtime: " + why)
			}
			return "", err
		}
		// the slide's entities before the script, so a find() at its top
		// level finds them (web/apps.js treeFirst)
		if why, err := r.eval(en, "(function (a) { if (a && a.tree) __setTree(a.tree); })("+arg+");"); err != nil || why != "" {
			if why != "" {
				err = errors.New("tree: " + why)
			}
			return "", err
		}
		if why, err := r.eval(en, src); err != nil || why != "" {
			return "!" + why, err
		}
		name, nn, err := r.put(fn)
		if err != nil {
			return "", err
		}
		ap, an, err := r.put(arg)
		if err != nil {
			return "", err
		}
		failed, err := r.call("cx_call", en, name, nn, ap, an)
		if err != nil {
			return "", err
		}
		res := r.result()
		if failed != 0 {
			return "!" + res, nil
		}
		return res, nil
	}()
	if err != nil {
		if ctx.Err() != nil {
			return "!it took longer than 3 s (an endless loop?)"
		}
		return "-"
	}
	return out
}
