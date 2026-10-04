package testreport_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/guardana/control/internal/testreport"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSummarizeRecordedStreams(t *testing.T) {
	cases := []struct {
		name    string
		want    testreport.Summary
		wantErr error
	}{
		{"passing", testreport.Summary{Events: 18, Packages: 1}, nil},
		{"skipping", testreport.Summary{Events: 32, Packages: 1, Skipped: 3}, nil},
		{"notests", testreport.Summary{Events: 3, Packages: 1}, nil},
		{"failing", testreport.Summary{Events: 30, Packages: 1, FailedPackages: 1, FailedTests: 3}, testreport.ErrFailed},
		{"panicking", testreport.Summary{Events: 27, Packages: 1, FailedPackages: 1, FailedTests: 1}, testreport.ErrFailed},
		{"broken", testreport.Summary{Events: 6, Packages: 1, FailedPackages: 1, BuildFailures: 1}, testreport.ErrFailed},
		{"all", testreport.Summary{
			Events: 116, Packages: 6, FailedPackages: 3, FailedTests: 4, BuildFailures: 1, Skipped: 3,
		}, testreport.ErrFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := testreport.Summarize(bytes.NewReader(readFixture(t, tc.name+".jsonl")), &out)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("Summarize: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("Summarize error = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("Summary = %+v, want %+v", got, tc.want)
			}
			if want := string(readFixture(t, tc.name+".want")); out.String() != want {
				t.Errorf("transcript:\n%s\nwant:\n%s", out.String(), want)
			}
		})
	}
}

func TestSummarizeRefusesWhatDoesNotProveAPass(t *testing.T) {
	passing := string(readFixture(t, "passing.jsonl"))
	withoutResult := passing[:strings.LastIndex(strings.TrimSuffix(passing, "\n"), "\n")+1]
	cases := []struct {
		name  string
		input string
		want  error
	}{
		{"empty", "", testreport.ErrNoEvents},
		{"garbage", "ok  \texample.com/fixture/passing\t0.1s\n", testreport.ErrNotEvent},
		{"garbage without a newline", "PASS", testreport.ErrNotEvent},
		{"null", "null\n", testreport.ErrNotEvent},
		{"array", "[1]\n", testreport.ErrNotEvent},
		{"no action", `{"Package":"example.com/p"}` + "\n", testreport.ErrNotEvent},
		{"unknown action", `{"Action":"retry","Package":"example.com/p"}` + "\n", testreport.ErrNotEvent},
		{"no package", `{"Action":"pass"}` + "\n", testreport.ErrNotEvent},
		{"build output without an import path", `{"Action":"build-output","Output":"x\n"}` + "\n", testreport.ErrNotEvent},
		{"trailing data", `{"Action":"pass","Package":"example.com/p"} {}` + "\n", testreport.ErrNotEvent},
		{"a pass followed by garbage", passing + "panic: boom\n", testreport.ErrNotEvent},
		{"a pass with a blank line", passing + "\n", testreport.ErrNotEvent},
		{"a package without a result", withoutResult, testreport.ErrUnfinished},
		{"build output alone", `{"ImportPath":"example.com/p","Action":"build-output","Output":"x\n"}` + "\n", testreport.ErrNoPackages},
		{"a failed test in a passing package", `{"Action":"fail","Package":"example.com/p","Test":"TestX"}
{"Action":"pass","Package":"example.com/p"}
`, testreport.ErrFailed},
		{"a failed build beside a passing package", `{"ImportPath":"example.com/q","Action":"build-fail"}
{"Action":"pass","Package":"example.com/p"}
`, testreport.ErrFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testreport.Summarize(strings.NewReader(tc.input), &bytes.Buffer{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Summarize error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSummarizeShowsWhatItRefuses(t *testing.T) {
	var out bytes.Buffer
	if _, err := testreport.Summarize(strings.NewReader("panic: boom\n"), &out); err == nil {
		t.Fatal("Summarize accepted a line that is not an event")
	}
	if out.String() != "panic: boom\n" {
		t.Errorf("transcript = %q, want the refused line itself", out.String())
	}

	passing := string(readFixture(t, "passing.jsonl"))
	unfinished := passing[:strings.LastIndex(strings.TrimSuffix(passing, "\n"), "\n")+1]
	out.Reset()
	if _, err := testreport.Summarize(strings.NewReader(unfinished), &out); err == nil {
		t.Fatal("Summarize accepted a package without a result")
	}
	want := "-test.shuffle 1791112380631026000\nPASS\nok  \texample.com/fixture/passing\t0.289s\ntest: 0 skipped\n"
	if out.String() != want {
		t.Errorf("transcript = %q, want %q", out.String(), want)
	}
}

func TestSummarizeWritesNothingForAnEmptyStream(t *testing.T) {
	var out bytes.Buffer
	if _, err := testreport.Summarize(strings.NewReader(""), &out); err == nil {
		t.Fatal("Summarize accepted an empty stream")
	}
	if out.Len() != 0 {
		t.Errorf("transcript = %q, want nothing that could read as a result", out.String())
	}
}

func TestSummarizeNamesAPassingPackageWithoutAResultLine(t *testing.T) {
	var out bytes.Buffer
	got, err := testreport.Summarize(strings.NewReader(`{"Action":"pass","Package":"example.com/p"}`+"\n"), &out)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got != (testreport.Summary{Events: 1, Packages: 1}) {
		t.Errorf("Summary = %+v", got)
	}
	if want := "ok  \texample.com/p\ntest: 0 skipped\n"; out.String() != want {
		t.Errorf("transcript = %q, want %q", out.String(), want)
	}
}

func TestSummarizeShowsATestThatNeverFinished(t *testing.T) {
	input := `{"Action":"run","Package":"example.com/p","Test":"TestHangs"}
{"Action":"output","Package":"example.com/p","Test":"TestHangs","Output":"=== RUN   TestHangs\n"}
{"Action":"output","Package":"example.com/p","Test":"TestHangs","Output":"panic: test timed out after 1s\n"}
{"Action":"output","Package":"example.com/p","Output":"FAIL\texample.com/p\t1.0s\n"}
{"Action":"fail","Package":"example.com/p"}
`
	var out bytes.Buffer
	if _, err := testreport.Summarize(strings.NewReader(input), &out); !errors.Is(err, testreport.ErrFailed) {
		t.Fatalf("Summarize error = %v, want ErrFailed", err)
	}
	want := "panic: test timed out after 1s\nFAIL\texample.com/p\t1.0s\ntest: 0 skipped\n"
	if out.String() != want {
		t.Errorf("transcript = %q, want %q", out.String(), want)
	}
}

func TestSummarizeReadsALineLongerThanAScannerBuffer(t *testing.T) {
	long := strings.Repeat("x", 200_000)
	input := `{"Action":"output","Package":"example.com/p","Test":"TestX","Output":"` + long + `\n"}
{"Action":"fail","Package":"example.com/p","Test":"TestX"}
{"Action":"fail","Package":"example.com/p"}
`
	var out bytes.Buffer
	got, err := testreport.Summarize(strings.NewReader(input), &out)
	if !errors.Is(err, testreport.ErrFailed) {
		t.Fatalf("Summarize error = %v, want ErrFailed", err)
	}
	if got.Events != 3 {
		t.Errorf("Events = %d, want 3", got.Events)
	}
	if want := long + "\ntest: 0 skipped\n"; out.String() != want {
		t.Errorf("transcript holds %d bytes, want %d", out.Len(), len(want))
	}
}

type failingWriter struct{}

var errWrite = errors.New("write refused")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestSummarizeReportsAWriteFailure(t *testing.T) {
	_, err := testreport.Summarize(bytes.NewReader(readFixture(t, "passing.jsonl")), failingWriter{})
	if !errors.Is(err, errWrite) {
		t.Fatalf("Summarize error = %v, want the writer's", err)
	}
}

var errRead = errors.New("read refused")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errRead }

func TestSummarizeReportsAReadFailureAfterAPass(t *testing.T) {
	r := io.MultiReader(bytes.NewReader(readFixture(t, "passing.jsonl")), failingReader{})
	got, err := testreport.Summarize(r, &bytes.Buffer{})
	if !errors.Is(err, errRead) {
		t.Fatalf("Summarize error = %v, want the reader's", err)
	}
	if got.Packages != 1 {
		t.Errorf("Packages = %d, want the passing package read before the failure", got.Packages)
	}
}

func FuzzSummarize(f *testing.F) {
	for _, name := range []string{"passing", "skipping", "notests", "failing", "panicking", "broken", "all"} {
		b, err := fs.ReadFile(os.DirFS("testdata"), name+".jsonl")
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(""))
	f.Add([]byte("null\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		var out bytes.Buffer
		got, err := testreport.Summarize(bytes.NewReader(input), &out)
		if err != nil {
			return
		}
		if got.Packages == 0 || got.FailedPackages != 0 || got.FailedTests != 0 || got.BuildFailures != 0 {
			t.Fatalf("Summarize passed %+v", got)
		}
		if !strings.HasSuffix(out.String(), " skipped\n") {
			t.Fatalf("a pass without the skip count: %q", out.String())
		}
	})
}
