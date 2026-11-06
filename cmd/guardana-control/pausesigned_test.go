package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/policystate"
)

// signedInitialized is a pause file signed with the operator key at keyFile
// for t1/A at serial 7.
func signedInitialized(t *testing.T, keyFile string) string {
	t.Helper()
	path := pauseDir(t)
	code, stdout, stderr := invoke(t, "pause", "init", "--key", keyFile, "--tenant", "t1", "--project", "A", "--serial", "7", path)
	if code != exitOK || !strings.Contains(stdout, "at serial 7") {
		t.Fatalf("pause init --key answered %d: %q %q", code, stdout, stderr)
	}
	return path
}

// TestSignedPauseCommandsSignEveryChangeAtTheNextSerial: init, add and
// remove with --key each leave a file signed by the key at a serial one
// above the last, for the tenant and project init named, and list names the
// signer.
func TestSignedPauseCommandsSignEveryChangeAtTheNextSerial(t *testing.T) {
	keyFile, pub, _ := operatorKey(t)
	path := signedInitialized(t, keyFile)
	code, id, stderr := invoke(t, "pause", "add", "--global", "--key", keyFile, path)
	id = strings.TrimSpace(id)
	if code != exitOK || !drawnID.MatchString(id) {
		t.Fatalf("pause add --key answered %d: %q %q", code, id, stderr)
	}
	want := pause.Header{KeyID: policykey.KeyID(pub), Tenant: "t1", Project: "A", Serial: 8}
	holdsSigned(t, path, want, id)
	listsSigned(t, path, "signed by key "+want.KeyID+" for tenant t1 project A at serial 8;", id+" global")
	if code, _, stderr := invoke(t, "pause", "remove", "--key", keyFile, path, id); code != exitOK {
		t.Fatalf("pause remove --key answered %d: %q", code, stderr)
	}
	if l, err := pause.List(path); err != nil || l.Signed == nil || l.Signed.Serial != 9 || len(l.Entries) != 0 {
		t.Errorf("after the removal the file holds %+v, %v", l, err)
	}
}

// holdsSigned fails unless the file at path is signed under want and holds
// the one entry id.
func holdsSigned(t *testing.T, path string, want pause.Header, id string) {
	t.Helper()
	l, err := pause.List(path)
	if err != nil || l.Signed == nil || l.Signed.Header != want || len(l.Entries) != 1 || l.Entries[0].ID != id {
		t.Fatalf("the file holds %+v, %v; want %+v with the entry %s", l, err, want, id)
	}
}

// listsSigned fails unless pause list of path opens with first and holds
// entry.
func listsSigned(t *testing.T, path, first, entry string) {
	t.Helper()
	code, stdout, _ := invoke(t, "pause", "list", path)
	if code != exitOK || !strings.HasPrefix(stdout, first) || !strings.Contains(stdout, entry) {
		t.Errorf("list answered %d: %q", code, stdout)
	}
}

// TestASignedPauseFileRefusesAnUnsignedWrite: an add or a removal without
// --key is refused naming the flag, and writes nothing; one with --key to an
// unsigned file is refused naming the signed init.
func TestASignedPauseFileRefusesAnUnsignedWrite(t *testing.T) {
	keyFile, _, _ := operatorKey(t)
	path := signedInitialized(t, keyFile)
	before := fileBytes(t, path)
	expectRefusal(t, "pause add", "sign the change with --key <operator key file>", "pause", "add", "--global", path)
	expectRefusal(t, "pause remove", "sign the change with --key <operator key file>", "pause", "remove", path, "P1")
	if fileBytes(t, path) != before {
		t.Error("a refused unsigned write changed the signed file")
	}
	unsigned := initialized(t)
	expectRefusal(t, "pause add", "a signed pause file starts with pause init --key", "pause", "add", "--global", "--key", keyFile, unsigned)
	expectRefusal(t, "pause add", "--key", "pause", "add", "--global", "--key", filepath.Join(t.TempDir(), "missing"), path)
}

// TestPauseInitTakesTheSignedFlagsTogether: --tenant, --project and
// --serial make a signed file only with --key, and --key needs the tenant
// and the project.
func TestPauseInitTakesTheSignedFlagsTogether(t *testing.T) {
	keyFile, _, _ := operatorKey(t)
	for _, args := range [][]string{
		{"--tenant", "t1", "--project", "A"},
		{"--serial", "2"},
		{"--key", keyFile, "--tenant", "t1"},
		{"--key", keyFile, "--project", "A"},
	} {
		code, stdout, stderr := invoke(t, append(append([]string{"pause", "init"}, args...), pauseDir(t))...)
		if code != exitUsage || stdout != "" || stderr == "" {
			t.Errorf("init %v answered %d with %q and %q; want the usage status", args, code, stdout, stderr)
		}
	}
	expectRefusal(t, "pause init", "serial", "pause", "init", "--key", keyFile, "--tenant", "t1", "--project", "A", "--serial", "0", pauseDir(t))
}

func TestPauseFloorInitMakesThePlanesFloorOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "floor")
	code, stdout, stderr := invoke(t, "pause", "floor", "init", "--tenant", "t1", "--project", "A", dir)
	if code != exitOK || stdout != "tenant: t1\nproject: A\nfloor: no serial\n" {
		t.Fatalf("pause floor init answered %d: %q %q", code, stdout, stderr)
	}
	if f, err := policystate.ReadPause(dir, "t1", "A"); err != nil || f.HasSerial() {
		t.Errorf("the floor = %+v, %v", f, err)
	}
	expectRefusal(t, "pause floor init", "floor file already", "pause", "floor", "init", "--tenant", "t1", "--project", "A", dir)
	if code, _, _ := invoke(t, "pause", "floor", "init", "--tenant", "t1", dir); code != exitUsage {
		t.Errorf("floor init with no project answered %d", code)
	}
}
