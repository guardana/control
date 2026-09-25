package trailfile

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/guardana/control/adapters/otel"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

type digest = [sha256.Size]byte

// index maps the digest of each event id a file holds to the digest of the
// line that carries it. Digests keep an entry the same size however long the
// id is. An event with no id is not in it.
type index map[digest]digest

type entry struct{ id, line digest }

// readIndex reads r as whole evidence lines and indexes them. A line that is
// not one event, and an event id on two different lines, are ErrDamaged: a
// writer of this package leaves neither.
func readIndex(r io.Reader) (index, error) {
	reader := bufio.NewReaderSize(r, evidence.MaxLineBytes+1)
	ids := index{}
	for number := 1; ; number++ {
		line, err := reader.ReadSlice('\n')
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			return nil, fmt.Errorf("%w: line %d is over %d bytes", ErrDamaged, number, evidence.MaxLineBytes)
		case errors.Is(err, io.EOF) && len(line) == 0:
			return ids, nil
		case err != nil:
			return nil, fmt.Errorf("reading line %d: %w", number, err)
		}
		events, err := evidence.DecodeJSONL(bytes.NewReader(line), 1)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d is not one evidence event: %w", ErrDamaged, number, err)
		}
		id := events[0].GetEventId()
		if id == "" {
			continue
		}
		key, sum := sha256.Sum256([]byte(id)), sha256.Sum256(line)
		if held, ok := ids[key]; ok && held != sum {
			return nil, fmt.Errorf("%w: line %d carries the event id of an earlier line with other content", ErrDamaged, number)
		}
		ids[key] = sum
	}
}

// fresh returns the lines of events that neither the index nor an earlier
// event of the same append holds, and their entries. An event id held with
// another line refuses the whole append.
func (ids index) fresh(events []*controlv1.Event, lines [][]byte) ([]byte, []entry, error) {
	var buf []byte
	var added []entry
	pending := map[digest]digest{}
	for i, line := range lines {
		id := events[i].GetEventId()
		if id == "" {
			buf = append(buf, line...)
			continue
		}
		key, sum := sha256.Sum256([]byte(id)), sha256.Sum256(line)
		held, ok := ids[key]
		if !ok {
			held, ok = pending[key]
		}
		switch {
		case ok && held == sum:
			continue
		case ok:
			return nil, nil, fmt.Errorf("%w: %w: event %d of the append", ErrConflict, otel.ErrSinkRefused, i)
		}
		pending[key] = sum
		added = append(added, entry{id: key, line: sum})
		buf = append(buf, line...)
	}
	return buf, added, nil
}

func (ids index) add(entries []entry) {
	for _, e := range entries {
		ids[e.id] = e.line
	}
}
