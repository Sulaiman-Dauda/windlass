package local

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
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

// inBackupsDir returns the cleaned path if it names a file inside the
// backups dir.
func (f fsLocal) inBackupsDir(path string) (string, error) {
	clean := filepath.Clean(path)
	if !strings.HasPrefix(clean, f.l.backupsDir()+string(filepath.Separator)) {
		return "", fmt.Errorf("archive outside backups dir: %s", path)
	}
	return clean, nil
}

func (f fsLocal) RemoveArchive(ctx context.Context, archivePath string) error {
	clean, err := f.inBackupsDir(archivePath)
	if err != nil {
		return err
	}
	err = os.Remove(clean)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// spoolPrefix starts every SpoolFile name.
const spoolPrefix = ".windlass-spool-"

// leftoverAge is how old a .windlass- temp file in the backups dir must be
// before it counts as a crash's orphan. A spool is in use from the start of
// its dump to the end of its archive; six hours is far beyond both.
const leftoverAge = 6 * time.Hour

// removeLeftovers deletes spool and archive temp files a crash left in the
// backups dir; a spooled dump can run to gigabytes. It runs at startup and
// before each spool, and spares recent files in case another process is
// mid-backup on the same data dir.
func (l *Local) removeLeftovers() {
	dir := l.backupsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), ".windlass-") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > leftoverAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// SpoolFile writes r to a private temp file in the backups dir. The name
// starts with .windlass-spool- so a file left by a crash is plainly ours.
func (f fsLocal) SpoolFile(ctx context.Context, r io.Reader) (string, int64, error) {
	backups := f.l.backupsDir()
	if err := os.MkdirAll(backups, 0o700); err != nil {
		return "", 0, err
	}
	f.l.removeLeftovers()
	out, err := os.CreateTemp(backups, spoolPrefix+"*")
	if err != nil {
		return "", 0, err
	}
	n, err := io.Copy(out, ctxReader{ctx, r})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out.Name())
		return "", 0, err
	}
	return out.Name(), n, nil
}

// ctxReader stops a copy once its context ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func (f fsLocal) ArchiveProject(ctx context.Context, project string, extraFiles map[string]string) (agent.ArchiveInfo, error) {
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
	nonce := make([]byte, 6)
	if _, err := rand.Read(nonce); err != nil {
		return agent.ArchiveInfo{}, fmt.Errorf("backup filename: %w", err)
	}
	name := fmt.Sprintf("%s-%s-%s.tar.gz", project, time.Now().UTC().Format("20060102-150405"), hex.EncodeToString(nonce))
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
		if _, replaced := extraFiles[filepath.ToSlash(rel)]; replaced {
			return nil
		}
		// .windlass/backup holds the dump a restore extracted. It is never
		// archived again: a new backup's dump comes only from extraFiles,
		// so an archive cannot carry an old dump its record says it lacks.
		if info.IsDir() && filepath.ToSlash(rel) == agent.BackupStagingDir {
			return filepath.SkipDir
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
	if walkErr == nil {
		for name, src := range extraFiles {
			if walkErr = f.addExtra(tw, name, src); walkErr != nil {
				break
			}
		}
	}

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

// addExtra writes the spooled file src into the archive as name. src must be
// a regular file inside the backups dir, so a caller cannot pull another
// file on the node into a backup.
func (f fsLocal) addExtra(tw *tar.Writer, name, src string) error {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid extra archive path %q", name)
	}
	path, err := f.inBackupsDir(src)
	if err != nil {
		return err
	}
	if filepath.Dir(path) != f.l.backupsDir() || !strings.HasPrefix(filepath.Base(path), spoolPrefix) {
		return fmt.Errorf("extra archive file %q is not a spooled file", name)
	}
	// Lstat first, so a link planted in the backups dir is refused rather
	// than followed.
	if st, err := os.Lstat(path); err != nil {
		return err
	} else if !st.Mode().IsRegular() {
		return fmt.Errorf("extra archive file %q is not a regular file", name)
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name: filepath.ToSlash(clean), Mode: 0o600, Size: st.Size(),
		ModTime: time.Now().UTC(), Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, io.LimitReader(in, st.Size()))
	return err
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
			if err := w.Close(); err != nil {
				return err
			}
		default:
			// symlinks etc. are not produced by ArchiveProject
		}
	}

	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.Rename(staging, dir)
}
