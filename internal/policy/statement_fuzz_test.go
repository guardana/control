package policy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/guardana/control/internal/policy"
)

// FuzzStatementBody signs any body with the freshness key and verifies it, so
// only the body's checks can refuse. VerifyStatement must not panic, must
// return a statement or exactly one of its refusals, must leave the body as
// it was, and must accept only what a reading written here from ADR-0038
// accepts.
func FuzzStatementBody(f *testing.F) {
	f.Add([]byte(statementBody))
	f.Add([]byte(statementBody + strings.Repeat(" ", 4096-len(statementBody))))
	for _, c := range badBodies() {
		f.Add([]byte(c.body))
	}
	keys := freshKeys()
	f.Fuzz(func(t *testing.T, body []byte) {
		held := bytes.Clone(body)
		env := signed(string(body))
		st, err := policy.VerifyStatement(env, keys)
		if !bytes.Equal(env.Payload, held) {
			t.Fatal("VerifyStatement changed the body")
		}
		if err != nil {
			matched := 0
			for _, s := range statementSentinels() {
				if errors.Is(err, s) {
					matched++
				}
			}
			if matched != 1 || st != (policy.Statement{}) {
				t.Fatalf("refusal %v matches %d sentinels, statement %v", err, matched, st)
			}
			return
		}
		if problem := statementProblem(body, st); problem != "" {
			t.Fatalf("accepted a body the reading refuses: %s: %q", problem, body)
		}
	})
}

// statementProblem is why the reading refuses body, or why st is not what it
// says, or "".
func statementProblem(body []byte, st policy.Statement) string {
	if len(body) > 4096 || !utf8.Valid(body) {
		return "over 4096 bytes or invalid UTF-8"
	}
	got, problem := objectMembers(body)
	if problem != "" {
		return problem
	}
	want := map[string]any{
		"kind":     "agent-policy-freshness/v1alpha1",
		"bundleId": st.BundleID(),
		"serial":   json.Number(strconv.FormatInt(st.Serial(), 10)),
		"digest":   st.Digest(),
		"issuedAt": st.IssuedAt().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if len(got) != len(want) {
		return "not exactly the five members"
	}
	for name, value := range want {
		if got[name] != value {
			return "member " + name + " is not what the statement says"
		}
	}
	if st.Serial() < 1 || st.BundleID() == "" || !strings.HasPrefix(st.Digest(), "sha256:") || len(st.Digest()) != 71 || st.IssuedAt().IsZero() {
		return "a value out of its form"
	}
	return ""
}

// objectMembers reads body as one object whose members are each named once.
func objectMembers(body []byte) (map[string]any, string) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, "not an object"
	}
	got := map[string]any{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, "a malformed member"
		}
		name, _ := tok.(string)
		if _, twice := got[name]; twice {
			return nil, "a member named twice"
		}
		var value any
		if err := dec.Decode(&value); err != nil {
			return nil, "a malformed value"
		}
		got[name] = value
	}
	if _, err := dec.Token(); err != nil {
		return nil, "an unclosed object"
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, "more after the object"
	}
	return got, ""
}
