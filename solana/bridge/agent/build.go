package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime/debug"
)

// BuildHash is the running binary's sha256 and what the Go toolchain recorded of its source: the reproducible
// build hash of §5.8, which the launch notice pins for hlwatch and hlrecover alike (K36: the members who
// execute by hand use hlrecover) — each holder compiles their own and compares. tool names the binary in the line.
func BuildHash(tool string) string {
	out := tool + " build: "
	if exe, err := os.Executable(); err == nil {
		if f, err := os.Open(exe); err == nil {
			h := sha256.New()
			if _, err := io.Copy(h, f); err == nil {
				out += "sha256 " + hex.EncodeToString(h.Sum(nil))
			}
			f.Close()
		}
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" || s.Key == "vcs.modified" {
				out += " " + s.Key + "=" + s.Value
			}
		}
		out += " go=" + bi.GoVersion
	}
	return out
}
