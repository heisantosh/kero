//go:build illumos || solaris

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const forkRepo = "heisantosh/kero"

func init() {
	Platforms = append(Platforms, "solaris/amd64")
	installPlatform = installIllumos
}

func installIllumos(ctx context.Context, version, dir string) error {
	name := Asset(version, "illumos", runtime.GOARCH)
	// 1. Try official signed release from upstream repo.
	if archive, err := get(ctx, URL(version, name), maxArchive); err == nil {
		if sig, err := get(ctx, URL(version, name+".sig"), 4096); err == nil {
			if err := Verify(archive, version, "illumos", runtime.GOARCH, string(sig)); err == nil {
				return Replace(archive, dir)
			}
		}
	}
	// 2. Try prebuilt release from fork repo with SHA-256 verification.
	forkBase := "https://github.com/" + forkRepo + "/releases/download/v" + version + "/"
	if archive, err := get(ctx, forkBase+name, maxArchive); err == nil {
		if sumBytes, err := get(ctx, forkBase+name+".sha256", 4096); err == nil {
			fields := strings.Fields(string(sumBytes))
			sum := sha256.Sum256(archive)
			if len(fields) > 0 && strings.EqualFold(fields[0], hex.EncodeToString(sum[:])) {
				return Replace(archive, dir)
			}
		}
	}
	// 3. Fallback: build kero-server for this version from source on-host.
	return buildAndInstallIllumos(ctx, version, dir, name)
}

func buildAndInstallIllumos(ctx context.Context, version, dir, assetName string) error {
	tmp, err := os.MkdirTemp("", "kero-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	env := os.Environ()
	pathEnv := os.Getenv("PATH")
	for _, extra := range []string{"/opt/ooce/bin", "/usr/gnu/bin", "/usr/bin"} {
		if !strings.Contains(pathEnv, extra) {
			pathEnv = extra + ":" + pathEnv
		}
	}
	env = append(env, "PATH="+pathEnv, "KERO_VERSION="+version)

	repoDir := filepath.Join(tmp, "kero")
	run := func(workDir string, prog string, args ...string) error {
		cmd := exec.CommandContext(ctx, prog, args...)
		cmd.Dir = workDir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %w (%s)", prog, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := run(tmp, "git", "clone", "--depth=50", "https://github.com/"+forkRepo+".git", repoDir); err != nil {
		return fmt.Errorf("cloning %s for Kero %s: %w", forkRepo, version, err)
	}
	// Fetch the requested upstream tag and merge it if it has newer commits.
	if err := run(repoDir, "git", "fetch", "--depth=50", "https://github.com/"+Repo+".git", "refs/tags/v"+version); err == nil {
		_ = run(repoDir, "git", "-c", "user.name=kero", "-c", "user.email=kero@localhost", "merge", "--no-edit", "-X", "ours", "FETCH_HEAD")
	}
	if err := run(repoDir, "sh", "scripts/build-server.sh", "illumos/"+runtime.GOARCH); err != nil {
		return fmt.Errorf("building Kero %s for illumos: %w", version, err)
	}
	archive, err := os.ReadFile(filepath.Join(repoDir, "build", "server", assetName))
	if err != nil {
		return err
	}
	return Replace(archive, dir)
}
