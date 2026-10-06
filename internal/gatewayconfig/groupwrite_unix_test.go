//go:build unix

package gatewayconfig

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/files"
)

// accountsAre makes the loader read the account and group databases from
// passwd and group, written to fresh files.
func accountsAre(t *testing.T, passwd, group string) {
	t.Helper()
	dir := t.TempDir()
	p, g := filepath.Join(dir, "passwd"), filepath.Join(dir, "group")
	for path, body := range map[string]string{p: passwd, g: group} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	passwdFile, groupFile = p, g
	t.Cleanup(func() { passwdFile, groupFile = "/etc/passwd", "/etc/group" })
}

// gidOf is the group that owns path's file.
func gidOf(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	gid, ok := statGroup(info)
	if !ok {
		t.Fatal("no group to read")
	}
	return gid
}

// privateGroupOf makes the group of path's file this account's own: its
// primary group, with nobody else in it.
func privateGroupOf(t *testing.T, path string) {
	t.Helper()
	gid := gidOf(t, path)
	accountsAre(t,
		fmt.Sprintf("# accounts\nme:x:%d:%d::/home/me:/bin/sh\nother:x:%d:%d::/home/other:/bin/sh\n", os.Geteuid(), gid, os.Geteuid()+1, gid+1),
		fmt.Sprintf("me:x:%d:\nother:x:%d:other\n", gid, gid+1))
}

// sharedGroupOf makes the group of path's file this account's primary group
// with another account listed in it.
func sharedGroupOf(t *testing.T, path string) {
	t.Helper()
	gid := gidOf(t, path)
	accountsAre(t,
		fmt.Sprintf("me:x:%d:%d::/home/me:/bin/sh\nother:x:%d:%d::/home/other:/bin/sh\n", os.Geteuid(), gid, os.Geteuid()+1, gid+1),
		fmt.Sprintf("staff:x:%d:me,other\n", gid))
}

// TestAGroupIsTheOwnersOwnOnlyWithNoOtherMember: a group is the owner's own
// when the owner is its one member, by primary group or by being listed in
// it. Another account with it as its primary group, another account listed,
// a group with no member, an owner or a group the databases do not hold, and
// a database that defers to another or cannot be read are each refused.
func TestAGroupIsTheOwnersOwnOnlyWithNoOtherMember(t *testing.T) {
	const uid, gid = 1000, 1000
	for _, c := range []struct {
		name, passwd, group string
		want                string
	}{
		{"its primary group", "me:x:1000:1000::/home/me:/bin/sh\n", "me:x:1000:\n", ""},
		{"listed in it alone", "me:x:1000:100::/home/me:/bin/sh\n", "me:x:1000:me\n", ""},
		{"both", "me:x:1000:1000::/home/me:/bin/sh\n", "me:x:1000:me\n", ""},
		{"another account's primary group too", "me:x:1000:1000::/h:/s\nyou:x:1001:1000::/h:/s\n", "me:x:1000:\n", `account "you" has it as its primary group`},
		{"another account listed", "me:x:1000:1000::/h:/s\n", "me:x:1000:me,you\n", `account "you" is a member`},
		{"no member", "me:x:1000:100::/h:/s\n", "me:x:1000:\n", "no member"},
		{"an owner not in the accounts", "you:x:1001:1001::/h:/s\n", "me:x:1000:\n", "uid 1000 is not in"},
		{"a group not in the groups", "me:x:1000:1000::/h:/s\n", "other:x:1001:\n", "gid 1000 is not in"},
		{"accounts deferred elsewhere", "me:x:1000:1000::/h:/s\n+::::::\n", "me:x:1000:\n", "defers to another database"},
		{"groups deferred elsewhere", "me:x:1000:1000::/h:/s\n", "me:x:1000:\n+:::\n", "defers to another database"},
		{"an account of too few fields", "me:x:1000\n", "me:x:1000:\n", "a record of 3 fields"},
		{"a uid that is no number", "me:x:one:1000::/h:/s\n", "me:x:1000:\n", `uid "one"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			accountsAre(t, c.passwd, c.group)
			err := ownGroup(uid, gid)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("ownGroup = %v, want its own group", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("ownGroup = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
	passwdFile = filepath.Join(t.TempDir(), "none")
	t.Cleanup(func() { passwdFile = "/etc/passwd" })
	if err := ownGroup(uid, gid); err == nil {
		t.Error("ownGroup with no account database = nil, want a refusal")
	}
}

// TestAGroupNeverOutranksTheOtherRules: a group that is the owner's own makes
// its write bit no refusal, and nothing more: others' write bit is still
// refused, and so is a group the platform cannot name.
func TestAGroupNeverOutranksTheOtherRules(t *testing.T) {
	path := placed(t, 0o700, 0o666)
	privateGroupOf(t, path)
	_, err := Load(path, nil)
	refusedFor(t, err, files.ErrMode, path)
	good := placed(t, 0o700, 0o664)
	privateGroupOf(t, good)
	groupOf = func(fs.FileInfo) (int, bool) { return 0, false }
	t.Cleanup(func() { groupOf = statGroup })
	_, err = Load(good, nil)
	refusedFor(t, err, files.ErrMode, good)
}
