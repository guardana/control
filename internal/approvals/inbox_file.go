package approvals

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// answerFileMode is the mode an answer file is filed with. Nobody but its
// owner may write it, so no other member of the inbox's group can empty an
// answer, a rejection among them; the plane's account reads it.
const answerFileMode fs.FileMode = 0o644

// File files raw under name in the inbox, where name is an answer file name.
// The file is written whole under a temporary name and linked into place,
// so the plane never reads half an answer and nothing ever replaces one: a
// name the inbox holds already is refused with ErrAnswerFiled. An inbox that
// others may write and is not sticky is refused as OpenInbox refuses it.
func (in *Inbox) File(name string, raw []byte) error {
	if in == nil || in.root == nil {
		return ErrClosed
	}
	id, label, ok := cutAnswerName(name)
	if !ok || checkApprovalID(id) != nil || !answerLabel(label) {
		return fmt.Errorf("%w: %q is not an answer file name", ErrRecordName, cause(name))
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.judge(); err != nil {
		return err
	}
	tmp := tmpPrefix + rand.Text() + tmpSuffix
	f, err := in.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, answerFileMode)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	// The mode is set on the descriptor: the umask may have taken the read
	// bit the plane's account needs.
	if err = errors.Join(err, f.Chmod(answerFileMode), f.Sync(), f.Close()); err != nil {
		return errors.Join(err, in.root.Remove(tmp))
	}
	err = in.root.Link(tmp, name)
	if errors.Is(err, fs.ErrExist) {
		return errors.Join(ErrAnswerFiled, in.root.Remove(tmp))
	}
	if err != nil {
		return errors.Join(err, in.root.Remove(tmp))
	}
	d, err := in.root.Open(".")
	if err != nil {
		return errors.Join(err, in.root.Remove(tmp))
	}
	return errors.Join(in.root.Remove(tmp), d.Sync(), d.Close())
}
