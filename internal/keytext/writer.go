package keytext

import (
	"io"
	"strings"
)

// Writer returns a writer that passes each run of text between the newlines
// and tabs of every write through Printable before it reaches w. It is for
// output laid out in lines and columns that repeats what it was given, as the
// flag package names a flag as it was given.
func Writer(w io.Writer) io.Writer { return cellWriter{w} }

type cellWriter struct{ w io.Writer }

func (c cellWriter) Write(p []byte) (int, error) {
	lines := strings.Split(string(p), "\n")
	for i, line := range lines {
		cells := strings.Split(line, "\t")
		for j, cell := range cells {
			cells[j] = Printable(cell)
		}
		lines[i] = strings.Join(cells, "\t")
	}
	if _, err := io.WriteString(c.w, strings.Join(lines, "\n")); err != nil {
		return 0, err
	}
	return len(p), nil
}
