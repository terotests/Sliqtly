// SPDX-License-Identifier: AGPL-3.0-or-later

// Text that would show a literal "\n" in a game instead of a line break: JSX
// text with \n in it (JSX text has no escapes) or a doubled escape ("a\\nb")
// in a string or JSON. write_files answers with a warning for each, so the
// assistant that wrote it fixes it. The same check runs on RangerGames' own
// games (tests/visible-newlines.mjs there).

package main

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	gamesJSXNewline     = regexp.MustCompile(">[^<>{}\"'`\\n]*\\\\n[^<>{}\"'`\\n]*[<{]")
	gamesDoubledNewline = regexp.MustCompile(`(^|[^\\])(\\\\)+n`)
)

const gamesNewlineAdvice = "shows a literal \\n on screen. Text has no line breaks (white space collapses as in HTML): write each line as its own <div>."

// warnings for one written file, at most a few
func gamesTextWarnings(path string, data []byte) []string {
	jsx := strings.HasSuffix(path, ".tsx") || strings.HasSuffix(path, ".jsx")
	if !jsx && !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".js") && !strings.HasSuffix(path, ".json") {
		return nil
	}
	out := []string{}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "rg-allow-backslash-n") {
			continue
		}
		what := ""
		if jsx && gamesJSXNewline.MatchString(line) {
			what = "JSX text with \\n"
		} else if gamesDoubledNewline.MatchString(line) {
			what = "a doubled \\\\n in a string"
		}
		if what == "" {
			continue
		}
		s := strings.TrimSpace(line)
		if r := []rune(s); len(r) > 100 {
			s = string(r[:100]) + "…"
		}
		out = append(out, fmt.Sprintf("%s:%d: %s %s (%s)", path, i+1, what, gamesNewlineAdvice, s))
		if len(out) == 5 {
			break
		}
	}
	return out
}
