package local

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

// backupsDir defaults to a sibling of the projects dir.
func (l *Local) backupsDir() string {
	return filepath.Join(filepath.Dir(l.cfg.ProjectsDir), "backups")
}

func (f fsLocal) BackupsDir(ctx context.Context) (string, error) {
	dir := f.l.backupsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func (f fsLocal) RemoveArchive(ctx context.Context, archivePath string) error {
	backups := f.l.backupsDir()
	clean := filepath.Clean(archivePath)
	if !strings.HasPrefix(clean, backups+string(filepath.Separator)) {
		return fmt.Errorf("archive outside backups dir: %s", archivePath)
	}
	err := os.Remove(clean)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (f fsLocal) ArchiveProject(ctx context.Context, project string) (agent.ArchiveInfo, error) {
	dir, err := f.projectDir(project)
	if err != nil {
		return agent.ArchiveInfo{}, err
	}
	if _, err := os.Stat(dir); err != nil {
		return agent.ArchiveInfo{}, fmt.Errorf("project dir: %w", err)
	}

	backups := f.l.backupsDir()
	if err := os.MkdirAll(backups, 0o700); err != nil {
		return agent.ArchiveInfo{}, err
	}
	name := fmt.Sprintf("%s-%s.tar.gz", project, time.Now().UTC().Format("20060102-150405"))
	dest := filepath.Join(backups, name)

	out, err := os.CreateTemp(backups, ".windlass-*")
	if err != nil {
		return agent.ArchiveInfo{}, err
	}
	tmpName := out.Name()
	defer os.Remove(tmpName)

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Skip git internals: backups capture the deployable state, and a
		// git project re-syncs its history from the remote.
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			src, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, src)
			src.Close()
			return err
		}
		return nil
	})

	if err := tw.Close(); err != nil && walkErr == nil {
		walkErr = err
	}
	if err := gz.Close(); err != nil && walkErr == nil {
		walkErr = err
	}
	if err := out.Close(); err != nil && walkErr == nil {
		walkErr = err
	}
	if walkErr != nil {
		return agent.ArchiveInfo{}, walkErr
	}

	if err := os.Rename(tmpName, dest); err != nil {
		return agent.ArchiveInfo{}, err
	}
	st, err := os.Stat(dest)
	if err != nil {
		return agent.ArchiveInfo{}, err
	}
	return agent.ArchiveInfo{Path: dest, Size: st.Size()}, nil
}

func (f fsLocal) RestoreProject(ctx context.Context, project, archivePath string) error {
	dir, err := f.projectDir(project)
	if err != nil {
		return err
	}
	// Only archives from our backups dir are accepted.
	backups := f.l.backupsDir()
	clean := filepath.Clean(archivePath)
	if !strings.HasPrefix(clean, backups+string(filepath.Separator)) {
		return fmt.Errorf("archive outside backups dir: %s", archivePath)
	}

	src, err := os.Open(clean)
	if err != nil {
		return err
	}
	defer src.Close()
	gz, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	// Extract to a staging dir first so a corrupt archive can't leave a
	// half-restored project.
	staging, err := os.MkdirTemp(filepath.Dir(dir), ".windlass-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		rel := filepath.Clean(filepath.FromSlash(hdr.Name))
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive contains invalid path %q", hdr.Name)
		}
		target := filepath.Join(staging, rel)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, io.LimitReader(tr, 1<<31)); err != nil {
				w.Close()
				return err
			}
			w.Close()
		default:
			// symlinks etc. are not produced by ArchiveProject
		}
	}

	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.Rename(staging, dir)
}
