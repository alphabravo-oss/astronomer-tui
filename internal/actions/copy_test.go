package actions

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyFromContainerExtractsBoundedArchive(t *testing.T) {
	destination := t.TempDir()
	var got ExecTarget
	err := CopyFromContainer(context.Background(), func(_ context.Context, target ExecTarget, _ io.Reader, stdout, _ io.Writer, tty bool) error {
		got = target
		if tty {
			t.Fatal("copy stream must not allocate a TTY")
		}
		tw := tar.NewWriter(stdout)
		if err := tw.WriteHeader(&tar.Header{Name: "app/config.yaml", Mode: 0o644, Size: 5, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write([]byte("safe\n")); err != nil {
			return err
		}
		return tw.Close()
	}, CopySpec{
		Direction: CopyFromPod, Namespace: "default", Pod: "api", Container: "app",
		RemotePath: "/etc/app", LocalPath: destination, MaxBytes: 1024, MaxFiles: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "app", "config.yaml")) // #nosec G304 -- test path is rooted in t.TempDir.
	if err != nil || string(data) != "safe\n" {
		t.Fatalf("unexpected extracted file %q err=%v", data, err)
	}
	if strings.Join(got.Command, " ") != "tar -C /etc -cf - -- app" {
		t.Fatalf("unexpected remote command %#v", got.Command)
	}
}

func TestCopyFromContainerRejectsTraversalLinksAndLimits(t *testing.T) {
	for name, tc := range map[string]struct {
		header   tar.Header
		maxBytes int64
	}{
		"traversal": {header: tar.Header{Name: "../escape", Typeflag: tar.TypeReg}},
		"symlink":   {header: tar.Header{Name: "app/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
		"limit":     {header: tar.Header{Name: "app/large", Typeflag: tar.TypeReg, Size: 8}, maxBytes: 4},
	} {
		t.Run(name, func(t *testing.T) {
			destination := t.TempDir()
			err := CopyFromContainer(context.Background(), func(_ context.Context, _ ExecTarget, _ io.Reader, stdout, _ io.Writer, _ bool) error {
				tw := tar.NewWriter(stdout)
				if err := tw.WriteHeader(&tc.header); err != nil {
					return err
				}
				if tc.header.Size > 0 {
					_, _ = tw.Write(bytes.Repeat([]byte{'x'}, int(tc.header.Size)))
				}
				return tw.Close()
			}, CopySpec{Direction: CopyFromPod, Namespace: "default", Pod: "api", Container: "app", RemotePath: "/etc/app", LocalPath: destination, MaxBytes: tc.maxBytes, MaxFiles: 10})
			if err == nil {
				t.Fatal("expected unsafe archive rejection")
			}
			if _, statErr := os.Stat(filepath.Join(destination, "escape")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("escape path was created: %v", statErr)
			}
		})
	}
}

func TestCopyToContainerStreamsSafeArchive(t *testing.T) {
	source := filepath.Join(t.TempDir(), "payload")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	var names []string
	err := CopyToContainer(context.Background(), func(_ context.Context, target ExecTarget, stdin io.Reader, _ io.Writer, _ io.Writer, tty bool) error {
		if tty || strings.Join(target.Command, " ") != "tar -C /tmp -xf -" {
			t.Fatalf("unexpected copy target %#v tty=%v", target, tty)
		}
		tr := tar.NewReader(stdin)
		for {
			header, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			names = append(names, header.Name)
			if header.Name == "payload/hello.txt" {
				body, _ := io.ReadAll(tr)
				if string(body) != "hello" {
					t.Fatalf("unexpected body %q", body)
				}
			}
		}
		return nil
	}, CopySpec{Direction: CopyToPod, Namespace: "default", Pod: "api", Container: "app", LocalPath: source, RemotePath: "/tmp", MaxBytes: 1024, MaxFiles: 10})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "payload,payload/hello.txt" {
		t.Fatalf("unexpected archive names %#v", names)
	}
}

func TestCopyToContainerRejectsSymlinkAndInvalidRemote(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	executor := func(context.Context, ExecTarget, io.Reader, io.Writer, io.Writer, bool) error { return nil }
	err := CopyToContainer(context.Background(), executor, CopySpec{Direction: CopyToPod, Namespace: "default", Pod: "api", Container: "app", LocalPath: link, RemotePath: "/tmp"})
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
	err = CopyToContainer(context.Background(), executor, CopySpec{Direction: CopyToPod, Namespace: "default", Pod: "api", Container: "app", LocalPath: dir, RemotePath: "relative"})
	if err == nil {
		t.Fatal("expected relative remote path rejection")
	}
	err = CopyToContainer(context.Background(), executor, CopySpec{Direction: CopyToPod, Namespace: "default", Pod: "api", Container: "app", LocalPath: dir, RemotePath: "/tmp", MaxBytes: DefaultCopyMaxBytes + 1})
	if err == nil {
		t.Fatal("expected excessive copy limit rejection")
	}
}

func TestInstallNoReplaceRejectsExistingDestination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "destination")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installNoReplace(source, destination); err == nil {
		t.Fatal("expected no-overwrite install failure")
	}
	data, err := os.ReadFile(destination) // #nosec G304 -- test path is rooted in t.TempDir.
	if err != nil || string(data) != "existing" {
		t.Fatalf("existing destination changed to %q err=%v", data, err)
	}
}
