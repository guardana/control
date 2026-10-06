package gatewayconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/guardana/control/internal/files"
)

// groupWritable is the permission bit that lets the file's group write it.
const groupWritable fs.FileMode = 0o020

// maxAccountDBBytes bounds a read of the account or the group database.
const maxAccountDBBytes = 1 << 20

// The account and group databases a group's members are read from. Accounts
// served from elsewhere, by a directory service or a name service, are not
// in them, and a group of such an account cannot be shown to be its own.
var (
	passwdFile = "/etc/passwd"
	groupFile  = "/etc/group"
)

// groupOf reads the group that owns info's file, where the platform names
// one.
var groupOf = statGroup

// othersMayWrite refuses an entry whose mode lets an account other than its
// owner write it: the others' write bit, or the group's unless the group is
// the owner's own.
func othersMayWrite(info fs.FileInfo) error {
	perm := info.Mode().Perm()
	if perm&worldWritable != 0 {
		return fmt.Errorf("%w: mode %04o; others may write it", files.ErrMode, perm)
	}
	if perm&groupWritable == 0 {
		return nil
	}
	uid, uidNamed := ownerOf(info)
	gid, gidNamed := groupOf(info)
	if !uidNamed || !gidNamed {
		return fmt.Errorf("%w: mode %04o; the group may write it, and its group cannot be read", files.ErrMode, perm)
	}
	if err := ownGroup(uid, gid); err != nil {
		return fmt.Errorf("%w: mode %04o; the group may write it, and gid %d is not its owner's own: %w", files.ErrMode, perm, gid, err)
	}
	return nil
}

// ownGroup returns nil when gid is a group whose one member is the account
// uid: no other account has it as its primary group or is listed in it, and
// uid is one way or the other. This is the user private group a umask of 002
// is meant for, and the rule Debian's sshd takes for a group-writable file.
func ownGroup(uid, gid int) error {
	name, primary, err := primaryMembers(uid, gid)
	if err != nil {
		return err
	}
	listed, err := listedMembers(name, gid)
	if err != nil {
		return err
	}
	if primary+listed == 0 {
		return errors.New("the group has no member")
	}
	return nil
}

// primaryMembers is the name of the account uid and how many accounts have
// gid as their primary group, refusing when one of them is not uid.
func primaryMembers(uid, gid int) (string, int, error) {
	users, err := readAccountDB(passwdFile, 7)
	if err != nil {
		return "", 0, err
	}
	name, members := "", 0
	for _, u := range users {
		uu, err := strconv.Atoi(u[2])
		if err != nil {
			return "", 0, fmt.Errorf("%s: uid %q: %w", passwdFile, u[2], err)
		}
		ug, err := strconv.Atoi(u[3])
		if err != nil {
			return "", 0, fmt.Errorf("%s: gid %q: %w", passwdFile, u[3], err)
		}
		if uu == uid && name == "" {
			name = u[0]
		}
		if ug != gid {
			continue
		}
		if uu != uid {
			return "", 0, fmt.Errorf("account %s has it as its primary group", strconv.Quote(u[0]))
		}
		members++
	}
	if name == "" {
		return "", 0, fmt.Errorf("uid %d is not in %s", uid, passwdFile)
	}
	return name, members, nil
}

// listedMembers is how many accounts the group database lists in gid,
// refusing when the group is not there or lists an account but name.
func listedMembers(name string, gid int) (int, error) {
	groups, err := readAccountDB(groupFile, 4)
	if err != nil {
		return 0, err
	}
	found, members := false, 0
	for _, g := range groups {
		if gg, err := strconv.Atoi(g[2]); err != nil || gg != gid {
			continue
		}
		found = true
		if g[3] == "" {
			continue
		}
		for _, m := range strings.Split(g[3], ",") {
			if m != name {
				return 0, fmt.Errorf("account %s is a member", strconv.Quote(m))
			}
			members++
		}
	}
	if !found {
		return 0, fmt.Errorf("gid %d is not in %s", gid, groupFile)
	}
	return members, nil
}

// readAccountDB reads the colon-separated records of the database at path,
// each with at least fields fields. A record that defers to another database,
// as "+" and "-" lines do, cannot be read here, so it refuses the database.
func readAccountDB(path string, fields int) ([][]string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: a system database at a fixed path
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxAccountDBBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxAccountDBBytes {
		return nil, fmt.Errorf("%s: over %d bytes", path, maxAccountDBBytes)
	}
	var out [][]string
	for line := range bytes.Lines(raw) {
		l := strings.TrimRight(string(line), "\r\n")
		switch {
		case l == "" || strings.HasPrefix(l, "#"):
			continue
		case strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-"):
			return nil, fmt.Errorf("%s: a record defers to another database", path)
		}
		f := strings.Split(l, ":")
		if len(f) < fields {
			return nil, fmt.Errorf("%s: a record of %d fields, want %d", path, len(f), fields)
		}
		out = append(out, f)
	}
	return out, nil
}
