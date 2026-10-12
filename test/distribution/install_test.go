package distribution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallerVerifiesBeforeInstalling(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX installer")
	}
	for _, scenario := range []string{"valid", "tamper", "signature"} {
		tamper := scenario == "tamper"
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			archive := fmt.Sprintf("astrotui_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
			var data bytes.Buffer
			gz := gzip.NewWriter(&data)
			tw := tar.NewWriter(gz)
			binary := []byte("verified fixture")
			if err := tw.WriteHeader(&tar.Header{Name: "astrotui", Mode: 0755, Size: int64(len(binary))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(binary); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(data.Bytes()), archive)
			if tamper {
				data.WriteString("tampered")
			}
			for name, body := range map[string][]byte{archive: data.Bytes(), "checksums.txt": []byte(checksum), "checksums.txt.sig": []byte("signature"), "checksums.txt.pem": []byte("certificate")} {
				if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			mock := filepath.Join(dir, "mock")
			dest := filepath.Join(dir, "dest")
			if err := os.Mkdir(mock, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dest, 0700); err != nil {
				t.Fatal(err)
			}
			// curl -fsSL URL -o PATH: fixture URL's basename determines the download.
			script := "#!/bin/sh\nname=${2##*/}\ncp \"$INSTALL_FIXTURES/$name\" \"$4\"\n"
			if err := os.WriteFile(filepath.Join(mock, "curl"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(mock, "cosign"), []byte("#!/bin/sh\n[ \"$1\" = verify-blob ] || exit 2\ncase \"$*\" in *--certificate-oidc-issuer*https://token.actions.githubusercontent.com*) ;; *) exit 3;; esac\n[ \"$INSTALL_SCENARIO\" != signature ] || { echo signature-rejected >&2; exit 1; }\n"), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "../../install.sh")
			cmd.Env = append(os.Environ(), "PATH="+mock+string(os.PathListSeparator)+os.Getenv("PATH"), "PREFIX="+dest, "INSTALL_FIXTURES="+dir, "INSTALL_SCENARIO="+scenario)
			out, err := cmd.CombinedOutput()
			installed, readErr := os.ReadFile(filepath.Join(dest, "astrotui"))
			if scenario == "signature" {
				if err == nil || !strings.Contains(string(out), "signature-rejected") || !os.IsNotExist(readErr) {
					t.Fatalf("untrusted release installed: %v %s", err, out)
				}
			} else if tamper {
				if err == nil || !strings.Contains(string(out), "checksum verification failed") || !os.IsNotExist(readErr) {
					t.Fatalf("tampered artifact installed: err=%v output=%s", err, out)
				}
			} else if err != nil || readErr != nil || !bytes.Equal(installed, binary) {
				t.Fatalf("verified install failed: err=%v output=%s", err, out)
			}
		})
	}
}
