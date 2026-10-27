package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/windlass-dev/windlass/internal/agent"
)

type fsLocal struct{ l *Local }

func (f fsLocal) DiscoverProjects(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(f.l.cfg.ProjectsDir)
	if err != nil {
		return nil, err
	}
	projects := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !agent.ValidProjectName(entry.Name()) {
			continue
		}
		dir := filepath.Join(f.l.cfg.ProjectsDir, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err == nil {
			projects = append(projects, entry.Name())
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "compose.yml")); err == nil {
			projects = append(projects, entry.Name())
		}
	}
	sort.Strings(projects)
	return projects, nil
}

// hasDriveLetter reports whether p begins with a Windows drive letter such as
// "C:". filepath.VolumeName and filepath.IsAbs only detect these on Windows,
// so on other hosts a value like `C:\config` cleans to `C:/config` and would
// otherwise be joined into the project directory instead of rejected.
func hasDriveLetter(p string) bool {
	return len(p) >= 2 && p[1] == ':' &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z'))
}

func (f fsLocal) projectDir(project string) (string, error) {
	if !agent.ValidProjectName(project) {
		return "", fmt.Errorf("invalid project name %q", project)
	}
	dir := filepath.Join(f.l.cfg.ProjectsDir, project)
	if err := rejectSymlinkComponents(f.l.cfg.ProjectsDir, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// rejectSymlinkComponents prevents a lexically safe project-relative path
// from escaping through a symlink already present on disk. The projects root
// itself is trusted configuration; every existing component below it is not.
func rejectSymlinkComponents(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path contains symlink %q", current)
		}
	}
	return nil
}

// resolve validates rel and returns the absolute path, guaranteed to stay
// inside the project directory.
func (f fsLocal) resolve(project, rel string) (string, error) {
	dir, err := f.projectDir(project)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(rel, "\\", "/")))
	// Reject absolute paths, Windows rooted/drive paths, and any traversal.
	// filepath.IsAbs/VolumeName do not recognise a Windows drive letter on
	// non-Windows hosts, so `C:\...` would otherwise be treated as a relative
	// path; hasDriveLetter catches it explicitly.
	if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" || hasDriveLetter(clean) ||
		strings.HasPrefix(clean, string(filepath.Separator)) ||
		clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path %q", rel)
	}
	abs := filepath.Join(dir, clean)
	if abs != dir && !strings.HasPrefix(abs, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path %q", rel)
	}
	if err := rejectSymlinkComponents(f.l.cfg.ProjectsDir, abs); err != nil {
		return "", fmt.Errorf("invalid path %q: %w", rel, err)
	}
	return abs, nil
}

func (f fsLocal) ReadFile(ctx context.Context, project, rel string) ([]byte, error) {
	path, err := f.resolve(project, rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (f fsLocal) WriteFile(ctx context.Context, project, rel string, data []byte, mode fs.FileMode) error {
	path, err := f.resolve(project, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	// Atomic: temp file in the same directory, then rename.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".windlass-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if mode != 0 {
		if err := os.Chmod(tmpName, mode); err != nil && runtime.GOOS != "windows" {
			return err
		}
	}
	return os.Rename(tmpName, path)
}

func (f fsLocal) List(ctx context.Context, project, rel string) ([]agent.FileInfo, error) {
	path, err := f.resolve(project, rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]agent.FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, agent.FileInfo{
			Name:    e.Name(),
			Size:    info.Size(),
			IsDir:   e.IsDir(),
			ModTime: info.ModTime(),
		})
	}
	return out, nil
}

func (f fsLocal) EnsureProject(ctx context.Context, project string) (string, error) {
	dir, err := f.projectDir(project)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

func (f fsLocal) RemoveProject(ctx context.Context, project string) error {
	dir, err := f.projectDir(project)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func (f fsLocal) CopyProject(ctx context.Context, from, to string, skip []string) ([]string, error) {
	src, err := f.projectDir(from)
	if err != nil {
		return nil, err
	}
	dst, err := f.projectDir(to)
	if err != nil {
		return nil, err
	}
	// Every read and write goes through a root, so a path a running
	// container swaps for a symlink after the walk saw it still cannot
	// reach outside either project.
	in, err := os.OpenRoot(src)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	out, err := os.OpenRoot(dst)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	var skipped, links []string
	err = fs.WalkDir(in.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rel == "." {
			return walkErr
		}
		if slices.Contains(skip, strings.Split(rel, "/")[0]) {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if walkErr != nil {
			skipped = append(skipped, rel)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			skipped = append(skipped, rel)
			return nil
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			return out.MkdirAll(rel, mode.Perm()|0o700)
		case mode.IsRegular():
			if err := copyFile(in, out, rel, mode.Perm()); err != nil {
				if os.IsPermission(err) || errors.Is(err, errNotRegular) {
					skipped = append(skipped, rel)
					return nil
				}
				return err
			}
			return nil
		case mode&os.ModeSymlink != 0:
			link, err := in.Readlink(rel)
			if err != nil || !linkStaysInside(dst, filepath.Join(dst, filepath.FromSlash(rel)), link) {
				skipped = append(skipped, rel)
				return nil
			}
			links = append(links, rel)
			return out.Symlink(link, rel)
		default:
			skipped = append(skipped, rel)
			return nil
		}
	})
	if err != nil {
		return skipped, err
	}
	// A link can read as inside and still leave: with here -> ".", the
	// target here/../x is the sibling x. The root resolves each copied link
	// the way the kernel will and refuses one that escapes; a link left
	// dangling by a skipped one is harmless.
	for _, rel := range links {
		if _, err := out.Stat(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if err := out.Remove(rel); err != nil {
				return skipped, err
			}
			skipped = append(skipped, rel)
		}
	}
	return skipped, nil
}

func linkStaysInside(root, path, link string) bool {
	if filepath.IsAbs(link) {
		return false
	}
	resolved := filepath.Join(filepath.Dir(path), link)
	return resolved == root || strings.HasPrefix(resolved, root+string(filepath.Separator))
}

// errNotRegular is a file that stopped being a regular file between the
// walk and the open.
var errNotRegular = errors.New("not a regular file")

func copyFile(in, out *os.Root, rel string, perm fs.FileMode) error {
	r, err := in.Open(rel)
	if err != nil {
		return err
	}
	defer r.Close()
	if info, err := r.Stat(); err != nil {
		return err
	} else if !info.Mode().IsRegular() {
		return errNotRegular
	}
	w, err := out.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func (f fsLocal) Usage(ctx context.Context, project string, skip []string) (int64, error) {
	dir, err := f.projectDir(project)
	if err != nil {
		return 0, err
	}
	var total int64
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel != "." && slices.Contains(skip, strings.Split(filepath.ToSlash(rel), "/")[0]) {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if walkErr != nil {
			if d != nil && d.IsDir() && rel != "." {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

func (f fsLocal) FreeSpace(ctx context.Context) (uint64, error) {
	return freeBytes(f.l.cfg.ProjectsDir)
}
