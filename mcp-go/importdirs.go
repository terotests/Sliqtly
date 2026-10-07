package main

// Import folders: on a server of one's own, create_presentation,
// update_presentation and vectorize_image may read pictures and data files
// from the server's own disk (a `path` instead of url or data_base64), but
// only from the folders SLIQTLY_IMPORT_DIRS (-import-dirs) names at start.
// Nothing is read anywhere else: sliqtly.com and the link-only server have
// no import folders.
//
// A path is opened through an os.Root of the folder it names, so ".." and
// symbolic links that lead out of the folder are refused by the operating
// system calls themselves, also when the folder changes between the check
// and the read. A link with an absolute target is followed only when it
// leads into an import folder, and is then read through that folder's Root.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// one allowed folder: the path as given (cleaned) and as the operating
// system resolves it, so a path through either spelling is found
type importDir struct {
	given    string
	resolved string
}

type importDirs []importDir

// the folders of SLIQTLY_IMPORT_DIRS: comma separated (a folder name may
// hold spaces), each absolute and an existing folder
func parseImportDirs(s string) (importDirs, error) {
	var out importDirs
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("SLIQTLY_IMPORT_DIRS: %q is not an absolute path", p)
		}
		given := filepath.Clean(p)
		resolved, err := filepath.EvalSymlinks(given)
		if err != nil {
			return nil, fmt.Errorf("SLIQTLY_IMPORT_DIRS: %q: %v", p, err)
		}
		st, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("SLIQTLY_IMPORT_DIRS: %q: %v", p, err)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("SLIQTLY_IMPORT_DIRS: %q is not a folder", p)
		}
		if resolved == string(filepath.Separator) {
			return nil, fmt.Errorf("SLIQTLY_IMPORT_DIRS: %q is the whole disk; name the folders the pictures are in", p)
		}
		out = append(out, importDir{given: given, resolved: resolved})
	}
	return out, nil
}

// the folders as the tools describe them
func (d importDirs) list() []string {
	out := make([]string, len(d))
	for i, x := range d {
		out[i] = x.given
	}
	return out
}

var errNotImported = errors.New("not inside the server's import folders")

// the bytes of the regular file at the absolute path p, which must lie in
// one of the folders; at most limit bytes (a bigger file is refused)
func (d importDirs) read(p string, limit int64) ([]byte, error) {
	if len(d) == 0 {
		return nil, errors.New("this server has no import folders (SLIQTLY_IMPORT_DIRS)")
	}
	if !filepath.IsAbs(p) {
		return nil, errors.New("not an absolute path")
	}
	clean := filepath.Clean(p)
	b, err := d.readFrom(clean, false, limit)
	if !errors.Is(err, errNotImported) {
		return b, err
	}
	// a link with an absolute target (os.Root follows only relative ones
	// that stay inside): where it leads, if that is in a folder too, is
	// opened through that folder's Root, so the read itself is confined
	real, rerr := filepath.EvalSymlinks(clean)
	if rerr != nil || real == clean {
		return nil, err
	}
	return d.readFrom(real, true, limit)
}

// the file at the clean absolute path p through the Root of the folder it
// lies in; resolved: p has no links, so only the folders' own spelling counts
func (d importDirs) readFrom(p string, resolved bool, limit int64) ([]byte, error) {
	for _, dir := range d {
		bases := []string{dir.given, dir.resolved}
		if resolved {
			bases = bases[1:]
		}
		for _, base := range bases {
			rel, err := filepath.Rel(base, p)
			if err != nil || !filepath.IsLocal(rel) {
				continue
			}
			return readInRoot(dir.resolved, rel, limit)
		}
	}
	return nil, errNotImported
}

func readInRoot(dir, rel string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("no such file")
		}
		var pe *os.PathError
		if errors.As(err, &pe) && strings.Contains(pe.Err.Error(), "escapes") {
			return nil, errNotImported
		}
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("not a file")
	}
	if st.Size() > limit {
		return nil, fmt.Errorf("larger than %d MB", limit>>20)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("larger than %d MB", limit>>20)
	}
	return b, nil
}
