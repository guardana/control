package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/adapters/otel"
)

// TestCollectTakesNothingWithoutItsCredential: a request without the
// credential, or with another, is answered 401 and writes nothing; the same
// request with it is taken.
func TestCollectTakesNothingWithoutItsCredential(t *testing.T) {
	out := filepath.Join(t.TempDir(), "trail")
	c := startCollect(t, out)
	body := otlpFixture(t, "export_logs_request.json")
	for _, auth := range []string{"", "Bearer " + collectToken[1:], "Bearer " + collectToken + "q", "Basic " + collectToken, collectToken} {
		if status := c.postAs(t, body, auth); status != http.StatusUnauthorized {
			t.Errorf("Authorization %q answered %d, want 401", auth, status)
		}
	}
	if got := liveSegment(t, out); len(got) != 0 {
		t.Errorf("a request without the credential wrote %q", got)
	}
	if status := c.post(t, body); status != http.StatusOK {
		t.Errorf("the request with the credential answered %d", status)
	}
	c.stop(t)
}

// TestCollectRefusesACredentialFileItCannotTrust: a credential file the group
// may read, one that is not there, and one too short to be a credential are
// refused before anything is listened on, and no refusal prints the file's
// content; the file beside each, narrowed or long enough, is taken.
func TestCollectRefusesACredentialFileItCannotTrust(t *testing.T) {
	short := strings.Repeat("w", 31)
	cases := map[string]struct {
		body string
		mode os.FileMode
		want string
	}{
		"readable by the group": {collectToken, 0o640, "mode"},
		"too short":             {short, 0o600, "32 to 512 bytes"},
		"a space in it":         {strings.Repeat("w", 20) + " " + strings.Repeat("w", 20), 0o600, "not printable ASCII"},
	}
	for name, c := range cases {
		path := filepath.Join(t.TempDir(), "credential")
		if err := os.WriteFile(path, []byte(c.body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, c.mode); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "trail")
		args := []string{"collect", "--listen", "127.0.0.1:0", "--dir", out, "--tenant", "tenant-1", "--project", "proj-1",
			"--credential-file", path}
		// A collect that took the file would serve until this deadline and
		// end 0, which fails here rather than hanging the suite.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var stdout, stderr syncBuffer
		status := run(ctx, args, &stdout, &stderr)
		cancel()
		if status != exitFail || !strings.Contains(stderr.String(), c.want) || strings.Contains(stderr.String(), c.body) {
			t.Errorf("%s: answered %d with %q, want %d naming %q and not the content", name, status, stderr.String(), exitFail, c.want)
		}
		if _, err := os.Stat(out); err == nil {
			t.Errorf("%s: a refused collect made its trail directory", name)
		}
	}
	missing := []string{"collect", "--listen", "127.0.0.1:0", "--dir", filepath.Join(t.TempDir(), "trail"), "--tenant", "tenant-1",
		"--project", "proj-1", "--credential-file", filepath.Join(t.TempDir(), "absent")}
	var stdout, stderr bytes.Buffer
	if status := run(context.Background(), missing, &stdout, &stderr); status != exitFail {
		t.Errorf("a credential file that is not there answered %d", status)
	}
	c := startCollect(t, filepath.Join(t.TempDir(), "trail"))
	c.stop(t)
}

// TestCollectRefusesAnotherProjectsEvents: a collect bound to another project
// or tenant than the fixture's answers its request 400, which the exporter
// quarantines on, and writes nothing.
func TestCollectRefusesAnotherProjectsEvents(t *testing.T) {
	for _, bind := range [][2]string{{"tenant-1", "proj-2"}, {"tenant-2", "proj-1"}} {
		out := filepath.Join(t.TempDir(), "trail")
		ctx, cancel := context.WithCancel(context.Background())
		c := &collecting{stdout: &syncBuffer{}, stderr: &syncBuffer{}, cancel: cancel, done: make(chan int, 1)}
		args := []string{"collect", "--listen", "127.0.0.1:0", "--dir", out, "--tenant", bind[0], "--project", bind[1],
			"--credential-file", credentialFile(t, collectToken)}
		go func() { c.done <- run(ctx, args, c.stdout, c.stderr) }()
		_, url, _ := strings.Cut(waitFor(t, c.stdout, "listening on "), "listening on ")
		c.url = strings.TrimSpace(url)
		if status := c.post(t, otlpFixture(t, "export_logs_request.json")); status != http.StatusBadRequest {
			t.Errorf("bound to %v: the fixture answered %d, want 400", bind, status)
		}
		if got := liveSegment(t, out); len(got) != 0 {
			t.Errorf("bound to %v: the trail holds %q", bind, got)
		}
		c.stop(t)
	}
}

// TestCollectAtItsBoundAnswers503AndPrunesNothing: a collect whose bound is
// one byte short of the fixture answers it 503, which the exporter sends
// again, and writes nothing; a bound of the fixture's size exactly takes it.
func TestCollectAtItsBoundAnswers503AndPrunesNothing(t *testing.T) {
	size := int64(len(otlpFixture(t, "events.jsonl")))
	for bound, want := range map[int64]int{size - 1: http.StatusServiceUnavailable, size: http.StatusOK} {
		out := filepath.Join(t.TempDir(), "trail")
		c := startCollect(t, out, "--segment-bytes", strconv.FormatInt(bound/2, 10), "--max-bytes", strconv.FormatInt(bound, 10))
		if status := c.post(t, otlpFixture(t, "export_logs_request.json")); status != want {
			t.Errorf("a bound of %d bytes answered %d, want %d", bound, status, want)
		}
		if got := liveSegment(t, out); (want == http.StatusOK) != (len(got) > 0) {
			t.Errorf("a bound of %d bytes: the trail holds %d bytes", bound, len(got))
		}
		c.stop(t)
	}
}

// TestTheCollectorsDefaultsAreThePagesNumbers: the collector guide says a
// segment is sealed at 64 MiB and the log takes 1 GiB unless told, and a
// credential is 32 to 512 bytes.
func TestTheCollectorsDefaultsAreThePagesNumbers(t *testing.T) {
	if defaultSegmentBytes != 64<<20 || defaultMaxBytes != 1<<30 {
		t.Errorf("the defaults are %d and %d bytes, and the pages say 64 MiB and 1 GiB", defaultSegmentBytes, defaultMaxBytes)
	}
	if otel.MinCredentialBytes != 32 || otel.MaxCredentialBytes != 512 {
		t.Errorf("a credential is %d to %d bytes, and the pages say 32 to 512", otel.MinCredentialBytes, otel.MaxCredentialBytes)
	}
}
