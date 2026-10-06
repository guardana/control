//go:build unix

package gatewayconfig

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/files"
)

// placed writes the document at conf/gateway.yaml in a fresh directory and
// gives the file and conf the modes named, set after the write so the umask
// cannot narrow them.
func placed(t *testing.T, dirMode, fileMode fs.FileMode) string {
	t.Helper()
	written := writeDocument(t, document)
	dir := filepath.Join(t.TempDir(), "conf")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "gateway.yaml")
	if err := os.Rename(written, path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, dirMode); err != nil {
		t.Fatal(err)
	}
	return path
}

// refusedFor checks that err refuses the configuration at path as want, names
// path, and quotes nothing the file holds.
func refusedFor(t *testing.T, err error, want error, path string) {
	t.Helper()
	switch {
	case err == nil:
		t.Errorf("Load(%s) was accepted, want %v", path, want)
	case !errors.Is(err, want):
		t.Errorf("Load(%s) = %v, want %v", path, err, want)
	case !strings.Contains(err.Error(), path):
		t.Errorf("the refusal %q does not name %s", err, path)
	case strings.Contains(err.Error(), "orders-assistant"):
		t.Errorf("the refusal %q quotes the file", err)
	}
}

// TestAConfigurationOthersMayWriteIsRefused: whoever may write the file sets
// the mode, the policy keys and the pause file. Others may only read it; the
// group may write it only where the group is the owner's own, as a umask of
// 002 under a user private group leaves an extracted or checked-out file.
func TestAConfigurationOthersMayWriteIsRefused(t *testing.T) {
	for _, mode := range []fs.FileMode{0o666, 0o646, 0o602} {
		path := placed(t, 0o700, mode)
		_, err := Load(path, nil)
		refusedFor(t, err, files.ErrMode, path)
	}
	for _, mode := range []fs.FileMode{0o600, 0o640, 0o644, 0o444, 0o400} {
		path := placed(t, 0o700, mode)
		if _, err := Load(path, nil); err != nil {
			t.Errorf("a file of mode %04o: %v", mode, err)
		}
	}
	for _, mode := range []fs.FileMode{0o664, 0o620} {
		path := placed(t, 0o700, mode)
		sharedGroupOf(t, path)
		_, err := Load(path, nil)
		refusedFor(t, err, files.ErrMode, path)
		privateGroupOf(t, path)
		if _, err := Load(path, nil); err != nil {
			t.Errorf("a file of mode %04o, its group its owner's own: %v", mode, err)
		}
	}
}

// TestAConfigurationInADirectoryOthersMayWriteIsRefused: whoever may write the
// directory may put another file at the name, unless the directory is sticky,
// where only the file's owner may. The group may write it only where the
// group is the owner's own.
func TestAConfigurationInADirectoryOthersMayWriteIsRefused(t *testing.T) {
	for _, mode := range []fs.FileMode{0o777, 0o707, 0o703} {
		path := placed(t, mode, 0o600)
		_, err := Load(path, nil)
		refusedFor(t, err, files.ErrMode, path)
	}
	for _, mode := range []fs.FileMode{0o700, 0o750, 0o755, 0o711, fs.ModeSticky | 0o777, fs.ModeSticky | 0o707} {
		path := placed(t, mode, 0o600)
		if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode()&(fs.ModeSticky|fs.ModePerm) != mode {
			t.Fatalf("the directory is %v, %v; want %v", info.Mode(), err, mode)
		}
		if _, err := Load(path, nil); err != nil {
			t.Errorf("a directory of mode %v: %v", mode, err)
		}
	}
	for _, mode := range []fs.FileMode{0o775, 0o770} {
		path := placed(t, mode, 0o600)
		sharedGroupOf(t, filepath.Dir(path))
		_, err := Load(path, nil)
		refusedFor(t, err, files.ErrMode, path)
		privateGroupOf(t, filepath.Dir(path))
		if _, err := Load(path, nil); err != nil {
			t.Errorf("a directory of mode %v, its group its owner's own: %v", mode, err)
		}
	}
}

// TestALinkIsJudgedByTheFileItReaches: a ConfigMap is a chain of links, so a
// link is followed, and the file it reaches and that file's directory are
// what is judged.
func TestALinkIsJudgedByTheFileItReaches(t *testing.T) {
	good := placed(t, 0o755, 0o644)
	writable := placed(t, 0o700, 0o666)
	inOpenDir := placed(t, 0o777, 0o600)
	for _, c := range []struct {
		name, target string
		want         error
	}{
		{"a good file", good, nil},
		{"a file others may write", writable, files.ErrMode},
		{"a good file in a directory others may write", inOpenDir, files.ErrMode},
	} {
		t.Run(c.name, func(t *testing.T) {
			link := filepath.Join(t.TempDir(), "gateway.yaml")
			if err := os.Symlink(c.target, link); err != nil {
				t.Fatal(err)
			}
			_, err := Load(link, nil)
			if c.want == nil {
				if err != nil {
					t.Errorf("Load through a link: %v", err)
				}
				return
			}
			refusedFor(t, err, c.want, link)
		})
	}
}

// TestAConfigMapIsRead: a mounted ConfigMap names the file through a link to
// a link to a timestamped directory, owned by root with mode 0644 when the
// cluster is; here the account running the test owns it.
func TestAConfigMapIsRead(t *testing.T) {
	good := placed(t, 0o755, 0o644)
	mount := t.TempDir()
	stamped := filepath.Join(mount, "..stamped")
	if err := os.Rename(filepath.Dir(good), stamped); err != nil {
		t.Fatal(err)
	}
	for _, l := range [][2]string{{filepath.Base(stamped), "..data"}, {"..data/gateway.yaml", "gateway.yaml"}} {
		if err := os.Symlink(l[0], filepath.Join(mount, l[1])); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(filepath.Join(mount, "gateway.yaml"), nil); err != nil {
		t.Errorf("Load of a ConfigMap's file: %v", err)
	}
}

// wantOwner makes the loader judge each file and directory as owned by want's
// answer for it, so a file this account owns can read as another's.
func wantOwner(t *testing.T, want func(fs.FileInfo) int) {
	t.Helper()
	ownerWanted = want
	t.Cleanup(func() { ownerWanted = func(fs.FileInfo) int { return os.Geteuid() } })
}

// TestAConfigurationAnotherAccountOwnsIsRefused: another account may rewrite
// a file or a directory it owns whatever the mode says. The seam names an
// account other than this one; as root, a file this process owns is root's
// and taken, so the case needs another account.
func TestAConfigurationAnotherAccountOwnsIsRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root every file this test makes is root's; TestAConfigurationGivenAwayIsRefused covers the check")
	}
	me, other := os.Geteuid(), os.Geteuid()+1
	for _, c := range []struct {
		name  string
		owner func(fs.FileInfo) int
	}{
		{"the file", func(info fs.FileInfo) int {
			if info.IsDir() {
				return me
			}
			return other
		}},
		{"the directory", func(info fs.FileInfo) int {
			if info.IsDir() {
				return other
			}
			return me
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := placed(t, 0o700, 0o600)
			wantOwner(t, c.owner)
			_, err := Load(path, nil)
			refusedFor(t, err, files.ErrOwner, path)
		})
	}
	path := placed(t, 0o700, 0o600)
	if _, err := Load(path, nil); err != nil {
		t.Errorf("the same layout read as its owner: %v", err)
	}
}

// TestAConfigurationGivenAwayIsRefused needs an account that can give a file
// away, which only root is.
func TestAConfigurationGivenAwayIsRefused(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("giving a file to another account needs root; TestAConfigurationAnotherAccountOwnsIsRefused covers the check without it")
	}
	for _, target := range []string{"the file", "the directory"} {
		path := placed(t, 0o755, 0o644)
		chown := path
		if target == "the directory" {
			chown = filepath.Dir(path)
		}
		if err := os.Chown(chown, 1, -1); err != nil {
			t.Fatalf("chown: %v", err)
		}
		_, err := Load(path, nil)
		refusedFor(t, err, files.ErrOwner, path)
	}
}

// statInfo is a FileInfo whose owner is whatever the test hands it.
type statInfo struct{ sys any }

func (statInfo) Name() string       { return "gateway.yaml" }
func (statInfo) Size() int64        { return 0 }
func (statInfo) Mode() fs.FileMode  { return 0o644 }
func (statInfo) ModTime() time.Time { return time.Time{} }
func (statInfo) IsDir() bool        { return false }
func (i statInfo) Sys() any         { return i.sys }

// TestRootOrThePlaneOwnsTheConfiguration: a container mounts the file
// read-only and root's, so root is taken beside the plane's own account; any
// other account, or none named, is refused.
func TestRootOrThePlaneOwnsTheConfiguration(t *testing.T) {
	wantOwner(t, func(fs.FileInfo) int { return 501 })
	for _, c := range []struct {
		name string
		sys  any
		want error
	}{
		{"the plane's account", &syscall.Stat_t{Uid: 501}, nil},
		{"root", &syscall.Stat_t{Uid: 0}, nil},
		{"another account", &syscall.Stat_t{Uid: 502}, files.ErrOwner},
		{"the account below the plane's", &syscall.Stat_t{Uid: 500}, files.ErrOwner},
		{"no owner named", nil, files.ErrOwnerUnknown},
	} {
		err := checkConfigOwner(statInfo{c.sys})
		if (c.want == nil) != (err == nil) || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	wantOwner(t, func(fs.FileInfo) int { return -1 })
	if err := checkConfigOwner(statInfo{&syscall.Stat_t{Uid: 0}}); !errors.Is(err, files.ErrOwnerUnknown) {
		t.Errorf("a plane whose account is unknown took root's file: %v", err)
	}
}

// atStep runs each of act, in order, when the loader reaches step.
func atStep(t *testing.T, step string, act ...func() error) {
	t.Helper()
	configSteps = func(reached string) {
		if reached != step {
			return
		}
		for _, a := range act {
			if err := a(); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { configSteps = nil })
}

// TestADirectorySwappedWhileReadIsRefused: a directory others may write, put
// at the path once the judged one was opened, is refused, not read.
func TestADirectorySwappedWhileReadIsRefused(t *testing.T) {
	path := placed(t, 0o700, 0o600)
	dir := filepath.Dir(path)
	open := filepath.Dir(placed(t, 0o777, 0o600))
	atStep(t, stepDirOpened,
		func() error { return os.Rename(dir, dir+".judged") },
		func() error { return os.Rename(open, dir) })
	_, err := Load(path, nil)
	if err == nil || !strings.Contains(err.Error(), "changed while it was read") {
		t.Errorf("Load after the directory was swapped = %v, want a refusal of a changed directory", err)
	}
}

// TestAFileSwappedWhileReadIsRefused: a link into a directory others may
// write, put at the file's name once it was named, is refused, not followed.
func TestAFileSwappedWhileReadIsRefused(t *testing.T) {
	path := placed(t, 0o700, 0o600)
	sub := filepath.Join(filepath.Dir(path), "open")
	atStep(t, stepFileNamed,
		func() error { return os.Mkdir(sub, 0o700) },
		func() error { return os.WriteFile(filepath.Join(sub, "gateway.yaml"), []byte(document), 0o600) },
		func() error { return os.Chmod(sub, 0o777) }, //nolint:gosec // G302: a directory the checks refuse, on purpose
		func() error { return os.Remove(path) },
		func() error { return os.Symlink("open/gateway.yaml", path) })
	_, err := Load(path, nil)
	if err == nil || !strings.Contains(err.Error(), "changed while it was read") {
		t.Errorf("Load after the file was swapped = %v, want a refusal of a changed file", err)
	}
}
