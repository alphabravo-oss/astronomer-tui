//revive:disable:exported package-comments
package actions

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	DefaultCopyMaxBytes = int64(512 << 20)
	DefaultCopyMaxFiles = 10_000
)

type CopyDirection string

const (
	CopyFromPod CopyDirection = "from-pod"
	CopyToPod   CopyDirection = "to-pod"
)

type CopySpec struct {
	Direction  CopyDirection
	Namespace  string
	Pod        string
	Container  string
	LocalPath  string
	RemotePath string
	MaxBytes   int64
	MaxFiles   int
}

type StreamExecutor func(context.Context, ExecTarget, io.Reader, io.Writer, io.Writer, bool) error

func (s CopySpec) withDefaults() CopySpec {
	if s.MaxBytes == 0 {
		s.MaxBytes = DefaultCopyMaxBytes
	}
	if s.MaxFiles == 0 {
		s.MaxFiles = DefaultCopyMaxFiles
	}
	return s
}

func ValidateCopySpec(spec CopySpec) error {
	if spec.Direction != CopyFromPod && spec.Direction != CopyToPod {
		return fmt.Errorf("copy direction must be %q or %q", CopyFromPod, CopyToPod)
	}
	if strings.TrimSpace(spec.Namespace) == "" || strings.TrimSpace(spec.Pod) == "" || strings.TrimSpace(spec.Container) == "" {
		return errors.New("copy requires namespace, pod, and container")
	}
	if strings.TrimSpace(spec.LocalPath) == "" {
		return errors.New("local path is required")
	}
	remote := path.Clean(strings.TrimSpace(spec.RemotePath))
	if !path.IsAbs(remote) || remote == "/" || remote == "." {
		return errors.New("remote path must be an absolute path below the container root")
	}
	if spec.MaxBytes < 0 || spec.MaxFiles < 0 {
		return errors.New("copy limits cannot be negative")
	}
	if spec.MaxBytes > DefaultCopyMaxBytes || spec.MaxFiles > DefaultCopyMaxFiles {
		return fmt.Errorf("copy limits cannot exceed %d bytes or %d entries", DefaultCopyMaxBytes, DefaultCopyMaxFiles)
	}
	return nil
}

// CopyFromContainer streams a tar archive from the container and extracts it
// into an existing local directory. Link and special-file entries are rejected,
// extraction is bounded, and the final top-level path is installed atomically.
func CopyFromContainer(ctx context.Context, exec StreamExecutor, spec CopySpec) error {
	spec = spec.withDefaults()
	if err := ValidateCopySpec(spec); err != nil {
		return err
	}
	if exec == nil {
		return errors.New("copy executor is required")
	}
	destination, err := filepath.Abs(spec.LocalPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(destination)
	if err != nil {
		return fmt.Errorf("local destination: %w", err)
	}
	if !info.IsDir() {
		return errors.New("local destination must be an existing directory")
	}
	remote := path.Clean(spec.RemotePath)
	base := path.Base(remote)
	if _, err := os.Lstat(filepath.Join(destination, filepath.FromSlash(base))); err == nil {
		return fmt.Errorf("local destination %q already exists", filepath.Join(destination, filepath.FromSlash(base)))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	archiveReader, archiveWriter := io.Pipe()
	execResult := make(chan error, 1)
	go func() {
		target := ExecTarget{Namespace: spec.Namespace, Pod: spec.Pod, Container: spec.Container, Command: []string{"tar", "-C", path.Dir(remote), "-cf", "-", "--", base}}
		err := exec(ctx, target, nil, archiveWriter, io.Discard, false)
		_ = archiveWriter.CloseWithError(err)
		execResult <- err
	}()

	extractErr := extractTarAtomic(ctx, archiveReader, destination, base, spec.MaxBytes, spec.MaxFiles)
	if extractErr != nil {
		_ = archiveReader.CloseWithError(extractErr)
	} else {
		_ = archiveReader.Close()
	}
	execErr := <-execResult
	if extractErr != nil {
		return extractErr
	}
	if execErr != nil {
		return fmt.Errorf("container tar failed: %w", execErr)
	}
	return nil
}

// CopyToContainer creates a bounded archive containing one local file or
// directory and streams it to an absolute directory in the container.
func CopyToContainer(ctx context.Context, exec StreamExecutor, spec CopySpec) error {
	spec = spec.withDefaults()
	if err := ValidateCopySpec(spec); err != nil {
		return err
	}
	if exec == nil {
		return errors.New("copy executor is required")
	}
	source, err := filepath.Abs(spec.LocalPath)
	if err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("local source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return errors.New("local source must be a regular file or directory, not a link or special file")
	}

	archiveReader, archiveWriter := io.Pipe()
	archiveResult := make(chan error, 1)
	go func() {
		err := writeLocalTar(ctx, archiveWriter, source, spec.MaxBytes, spec.MaxFiles)
		_ = archiveWriter.CloseWithError(err)
		archiveResult <- err
	}()
	target := ExecTarget{Namespace: spec.Namespace, Pod: spec.Pod, Container: spec.Container, Command: []string{"tar", "-C", path.Clean(spec.RemotePath), "-xf", "-"}}
	execErr := exec(ctx, target, archiveReader, io.Discard, io.Discard, false)
	if execErr != nil {
		_ = archiveReader.CloseWithError(execErr)
	} else {
		_ = archiveReader.Close()
	}
	archiveErr := <-archiveResult
	if archiveErr != nil {
		return archiveErr
	}
	if execErr != nil {
		return fmt.Errorf("container tar failed: %w", execErr)
	}
	return nil
}

func extractTarAtomic(ctx context.Context, reader io.Reader, destination, expectedRoot string, maxBytes int64, maxFiles int) error {
	temp, err := os.MkdirTemp(destination, ".astrotui-copy-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	if err := os.Chmod(temp, 0o700); err != nil { // #nosec G302 -- private extraction directories require owner traversal.
		return err
	}

	// Bound archive metadata as well as declared file sizes. Four KiB per entry
	// covers tar headers, padding, and ordinary PAX metadata without allowing a
	// malicious container to stream unbounded extended headers.
	streamLimit := maxBytes + int64(maxFiles)*4096
	tr := tar.NewReader(io.LimitReader(reader, streamLimit+1))
	var total int64
	files := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid tar stream: %w", err)
		}
		files++
		if files > maxFiles {
			return fmt.Errorf("copy exceeds %d file entries", maxFiles)
		}
		name := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if name == "." || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || (name != expectedRoot && !strings.HasPrefix(name, expectedRoot+"/")) {
			return fmt.Errorf("unsafe tar path %q", header.Name)
		}
		if header.Size < 0 || header.Size > maxBytes-total {
			return fmt.Errorf("copy exceeds %d-byte uncompressed limit", maxBytes)
		}
		total += header.Size
		local := filepath.Join(temp, filepath.FromSlash(name))
		if !strings.HasPrefix(local, temp+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe tar path %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(local, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, 0:
			if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if header.Mode&0o100 != 0 {
				mode = 0o700
			}
			file, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) // #nosec G304 -- local is traversal-checked beneath the private extraction root.
			if err != nil {
				return err
			}
			written, copyErr := io.CopyN(file, tr, header.Size)
			closeErr := file.Close()
			if copyErr != nil || written != header.Size {
				return fmt.Errorf("truncated tar entry %q", header.Name)
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("tar entry %q is a link or special file", header.Name)
		}
	}
	source := filepath.Join(temp, filepath.FromSlash(expectedRoot))
	if _, err := os.Lstat(source); err != nil {
		return fmt.Errorf("archive did not contain expected root %q", expectedRoot)
	}
	return installNoReplace(source, filepath.Join(destination, filepath.FromSlash(expectedRoot)))
}

func installNoReplace(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		// Hard-linking within the destination filesystem is atomic and fails if
		// another process creates the destination after our initial check.
		if err := os.Link(source, destination); err != nil {
			return fmt.Errorf("install copied file without overwrite: %w", err)
		}
		return os.Remove(source)
	}
	if !info.IsDir() {
		return errors.New("copied root is not a regular file or directory")
	}
	// Directories cannot be installed with portable rename-no-replace. Reserve
	// the final name exclusively, then move already-validated children from the
	// private staging directory.
	if err := os.Mkdir(destination, 0o700); err != nil {
		return fmt.Errorf("reserve copied directory without overwrite: %w", err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	if err := os.Remove(source); err != nil {
		return err
	}
	return nil
}

func writeLocalTar(ctx context.Context, writer io.Writer, source string, maxBytes int64, maxFiles int) error {
	tw := tar.NewWriter(writer)
	base := filepath.Base(source)
	parent := filepath.Dir(source)
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	var total int64
	files := 0
	err = filepath.WalkDir(source, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("local source contains link or special file %q", current)
		}
		files++
		if files > maxFiles {
			return fmt.Errorf("copy exceeds %d file entries", maxFiles)
		}
		if info.Mode().IsRegular() {
			total += info.Size()
			if total > maxBytes {
				return fmt.Errorf("copy exceeds %d-byte uncompressed limit", maxBytes)
			}
		}
		rel, err := filepath.Rel(parent, current)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		header.Mode &= 0o700
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := root.Open(rel)
		if err != nil {
			return err
		}
		written, copyErr := io.CopyN(tw, file, info.Size())
		closeErr := file.Close()
		if copyErr != nil || written != info.Size() {
			return fmt.Errorf("local source changed while copying %q", current)
		}
		return closeErr
	})
	if err != nil {
		_ = tw.Close()
		return err
	}
	if files == 0 || base == "." {
		_ = tw.Close()
		return errors.New("local source is empty or invalid")
	}
	return tw.Close()
}
