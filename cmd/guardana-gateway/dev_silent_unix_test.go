//go:build unix

package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
)

// silentVetoDocument allows reads, and asks the decision point about read_web,
// which it may veto.
const silentVetoDocument = `{"apiVersion":"agent-policy/v1alpha1",
  "bundle":{"id":"scenario-fixture","version":"1","serial":1,"maxStaleSeconds":600},
  "rules":[
    {"id":"allow-reads","effect":"ALLOW","when":{"action":{"effect":["READ"]}}},
    {"id":"web-vetoed","effect":"DENY","when":{"action":{"name":["read_web"]},"external":{"denies":true}}}
  ]}`

// vetoDemo is a demo whose policy reads external.
func vetoDemo(t *testing.T) demo {
	t.Helper()
	d := writeDemo(t, newLiveUpstream(t).url, "5ms", "")
	if err := os.WriteFile(d.policy, []byte(silentVetoDocument), 0o600); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestASilentDecisionPointTakesAQuestionAndNeverAnswers: the plane's
// decision point is a loopback listener dev bound, which takes a connection
// and an evaluation request and writes nothing back; dev's printed scenario
// command keeps the flag, and halting the plane closes the listener.
func TestASilentDecisionPointTakesAQuestionAndNeverAnswers(t *testing.T) {
	t.Parallel()
	_, controlBin := builtBinaries(t)
	ctx := context.Background()
	control, err := findSibling(ctx, brand.CLI, controlBin, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d := vetoDemo(t)
	in := devInputs{config: d.config, document: []byte(silentVetoDocument), control: control, silent: true}
	var log syncBuffer
	dp, err := startDevPlane(ctx, in, filepath.Join(t.TempDir(), "state"), &log)
	if err != nil {
		t.Fatalf("startDevPlane: %v\n%s", err, log.String())
	}
	halted := false
	t.Cleanup(func() {
		if !halted {
			_, _ = dp.halt()
		}
	})
	addr, ok := strings.CutPrefix(dp.cfg.PDP.Identifier, "http://")
	if !ok || dp.silent == nil || addr != dp.silent.Addr().String() || !dp.cfg.PDP.AllowPlaintext {
		t.Fatalf("pdp.identifier %q, plaintext %v, silent listener %v", dp.cfg.PDP.Identifier, dp.cfg.PDP.AllowPlaintext, dp.silent)
	}
	conn := askUnanswered(t, addr)
	defer func() { _ = conn.Close() }()
	var out bytes.Buffer
	dp.describe(&out, "page: http://127.0.0.1:1/", devOptions{config: d.config, policy: d.policy, decisionPoint: silentDecisionPoint})
	if !strings.Contains(out.String(), "\nscenario_command: "+brand.Gateway+" dev --config "+shellWord(d.config)+" --policy "+shellWord(d.policy)+" --decision-point=silent --scenario <file>\n") {
		t.Errorf("the printed scenario command drops the flag:\n%s", out.String())
	}
	halted = true
	if _, err := dp.halt(); err != nil {
		t.Fatalf("halt: %v", err)
	}
	if _, err := dp.silent.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("after halt, the silent listener accepts: %v", err)
	}
}

// askUnanswered dials addr, writes an evaluation request and requires that
// not one byte comes back within 300ms. It returns the connection open, so
// the question stays asked.
func askUnanswered(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("dialling the silent decision point: %v", err)
	}
	body := `{"subject":{"type":"agent","id":"a"},"action":{"name":"read_web"},"resource":{"type":"order","id":"o"}}`
	request := "POST /access/v1/evaluation HTTP/1.1\r\nHost: " + addr + "\r\nContent-Type: application/json\r\nContent-Length: " +
		strconv.Itoa(len(body)) + "\r\n\r\n" + body
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("writing the question: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	var ne net.Error
	if n != 0 || !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("the silent decision point gave %d bytes %q and %v, want none before the deadline", n, buf[:n], err)
	}
	return conn
}

// TestDevDecidesAgainstASilentDecisionPointAsATimeout: a question the
// silent decision point holds is decided as PDP_TIMEOUT, which a scenario
// expecting PDP_UNAVAILABLE fails on; without the flag the plane refuses a
// bundle that reads external and names the key it lacks.
func TestDevDecidesAgainstASilentDecisionPointAsATimeout(t *testing.T) {
	t.Parallel()
	const codes = `["RULE_ALLOW", "RULE_UNDETERMINED", "PDP_TIMEOUT"]`
	scenarioText := `{"kind": "agent-scenario/v1alpha1", "about": "A veto nobody answers blocks the read.",
	  "plane": {"mode": "APPROVE", "bundle": {"id": "scenario-fixture"}},
	  "steps": [{"call": {"tool": "read_web", "args": {"id": "p-1"},
	    "answer": {"kind": "blocked", "codes": ` + codes + `},
	    "decided": {"verdict": "INDETERMINATE", "codes": ` + codes + `, "obligations": []},
	    "trail": {"request": "new", "kinds": ["ACTION_PROPOSED", "POLICY_DECIDED", "ACTION_BLOCKED"]}}}]}`
	dir := t.TempDir()
	timeout, unavailable := filepath.Join(dir, "timeout.json"), filepath.Join(t.TempDir(), "timeout.json")
	if err := os.WriteFile(timeout, []byte(scenarioText), 0o600); err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(scenarioText, `"codes": `+codes+`, "obligations"`, `"codes": ["RULE_ALLOW", "RULE_UNDETERMINED", "PDP_UNAVAILABLE"], "obligations"`, 1)
	if mutated == scenarioText {
		t.Fatal("the mutant changed nothing")
	}
	if err := os.WriteFile(unavailable, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}
	d := vetoDemo(t)
	base := []string{"--config", d.config, "--policy", d.policy}
	t.Run("silent", func(t *testing.T) {
		t.Parallel()
		code, stdout, stderr := runDevToEnd(t, time.Minute, append(base, "--decision-point=silent", "--scenario", timeout)...)
		if code != exitOK || !strings.HasSuffix(stdout, "\ntimeout.json passed\n") {
			t.Fatalf("exit %d, want 0 and passed:\n%s\n%s", code, stdout, stderr)
		}
	})
	t.Run("unavailable expected", func(t *testing.T) {
		t.Parallel()
		code, stdout, stderr := runDevToEnd(t, time.Minute, append(base, "--decision-point=silent", "--scenario", unavailable)...)
		if code != exitDiffered || !strings.Contains(stdout, "\ntimeout.json step[0].decided.codes: want ") {
			t.Fatalf("exit %d, want 1 on decided.codes:\n%s\n%s", code, stdout, stderr)
		}
	})
	t.Run("without the flag", func(t *testing.T) {
		t.Parallel()
		code, stdout, stderr := runDevToEnd(t, time.Minute, append(base, "--scenario", timeout)...)
		if code != exitCouldNotRun || !strings.Contains(stdout, "timeout.json could not run: its plane did not start: ") ||
			!strings.Contains(stdout, "set pdp.identifier") {
			t.Fatalf("exit %d, want 2 with the plane refusing a bundle that reads external:\n%s\n%s", code, stdout, stderr)
		}
	})
}
