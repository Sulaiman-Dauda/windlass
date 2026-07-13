package local

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/windlass-dev/windlass/internal/agent"
)

type fsLocal struct{ l *Local }

// projectNameRe constrains names to what is safe as a directory name AND a
// compose project name.
var projectNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func (f fsLocal) projectDir(project string) (string, error) {
	if !projectNameRe.MatchString(project) {
		return "", fmt.Errorf("invalid project name %q", project)
	}
	return filepath.Join(f.l.cfg.ProjectsDir, project), nil
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
	if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" ||
		strings.HasPrefix(clean, string(filepath.Separator)) ||
		clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path %q", rel)
	}
	abs := filepath.Join(dir, clean)
	if abs != dir && !strings.HasPrefix(abs, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path %q", rel)
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
