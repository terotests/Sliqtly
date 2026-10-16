// SPDX-License-Identifier: AGPL-3.0-or-later

// run_script: a JavaScript or TypeScript program run on a presentation's
// files in CErXes, the engine programs on slides run in, with Node.js's fs,
// path, process and Buffer emulated over the deck's own files
// (web/node-runtime.js). The files go in before the program runs and what it
// wrote comes back after it; the program sees nothing else.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
)

// how long a script may run
const nodeRunTime = 10 * time.Second

// at most this much of what the script printed is kept
const nodeRunOutput = 64 * 1024

type nodeRan struct {
	// false: no engine on this server
	Ran     bool            `json:"ran"`
	Error   string          `json:"error"`
	Output  string          `json:"output"`
	Changes json.RawMessage `json:"changes"`
}

// NodeRun is host_node_run: the program `code` run after the deck's files
// `mount` ({files, argv, script}, web/node-runtime.js __nodeMount) are put
// in; {"ran", "error", "output", "changes"} with changes as __nodeChanges
// gives them ({written, removed, exitCode}).
func (h *McpHost) NodeRun(mount, code string) string {
	b, _ := json.Marshal(nodeRun(mount, code))
	return string(b)
}

func nodeRuntime() (string, error) {
	files := cerxesFiles()
	if files == nil {
		return "", errors.New("no built page")
	}
	b, err := fs.ReadFile(files, "node-runtime.js")
	if err != nil {
		return "", err
	}
	return rawText(string(b))
}

func nodeRun(mount, code string) nodeRan {
	if err := cerxes.start(); err != nil {
		return nodeRan{}
	}
	runtime, err := nodeRuntime()
	if err != nil {
		return nodeRan{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), nodeRunTime)
	defer cancel()
	mod, err := cerxes.rt.InstantiateModule(ctx, cerxes.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize"))
	if err != nil {
		return nodeRan{}
	}
	defer mod.Close(context.Background())
	r := &cerxesRun{ctx: ctx, mod: mod}
	out := nodeRan{Ran: true, Changes: json.RawMessage("null")}
	var printed strings.Builder
	keep := func() {
		p, _ := r.call("cx_output_ptr")
		n, _ := r.call("cx_output_len")
		if b, ok := r.mod.Memory().Read(uint32(p), uint32(n)); ok && len(b) > 0 {
			if printed.Len() > 0 && !strings.HasSuffix(printed.String(), "\n") {
				printed.WriteString("\n")
			}
			printed.Write(b)
		}
	}
	call := func(en uint64, fn, arg string) (string, bool, error) {
		name, nn, err := r.put(fn)
		if err != nil {
			return "", false, err
		}
		ap, an, err := r.put(arg)
		if err != nil {
			return "", false, err
		}
		failed, err := r.call("cx_call", en, name, nn, ap, an)
		if err != nil {
			return "", false, err
		}
		r.call("cx_free", ap, an)
		r.call("cx_free", name, nn)
		keep()
		return r.result(), failed != 0, nil
	}
	err = func() error {
		en, err := r.call("cx_new")
		if err != nil {
			return err
		}
		if why, err := r.eval(en, runtime); err != nil || why != "" {
			if why != "" {
				err = errors.New("runtime: " + why)
			}
			return err
		}
		if res, failed, err := call(en, "__nodeMount", mount); err != nil || failed {
			if failed {
				err = errors.New("mount: " + res)
			}
			return err
		}
		why, err := r.eval(en, code)
		if err != nil {
			return err
		}
		keep()
		out.Error = why
		res, failed, err := call(en, "__nodeChanges", "")
		if err != nil {
			return err
		}
		if failed {
			return errors.New("changes: " + res)
		}
		out.Changes = json.RawMessage(res)
		return nil
	}()
	text := printed.String()
	if len(text) > nodeRunOutput {
		text = text[:nodeRunOutput] + "\n… (cut: the script printed more than 64 KB)"
	}
	out.Output = strings.TrimRight(text, "\n")
	if err != nil {
		if ctx.Err() != nil {
			out.Error = "it ran longer than 10 s (an endless loop?); nothing it wrote is kept"
		} else {
			out.Error = "the engine stopped: " + err.Error() + "; nothing the script wrote is kept"
		}
		out.Changes = json.RawMessage("null")
	}
	return out
}
