package docscheck

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/keytext"
)

// plantedSecret is one finding of a shape a default gitleaks rule reports,
// as the three texts an allowlist regex can be matched against: the secret,
// the text the rule matched, and the line holding it.
type plantedSecret struct {
	rule, secret, match, line string
}

const (
	plantAlnum  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	plantBase32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	plantDigits = "0123456789"
	plantLower  = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// plantBody returns n characters of set, stepping through it from seed by a
// stride that shares no factor with the set sizes above.
func plantBody(set string, n, seed int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = set[(seed+i*7)%len(set)]
	}
	return string(b)
}

// plantedSecrets builds the planted findings at run time, with every rule
// prefix split, so that no scanner reads this file as holding one.
func plantedSecrets() []plantedSecret {
	token := func(rule, secret string) plantedSecret {
		return plantedSecret{rule, secret, secret, "export VALUE='" + secret + "'"}
	}
	seed := make([]byte, 32)
	next := byte(11)
	for i := range seed {
		seed[i] = next
		next += 37
	}
	keyBlock := "-----BEGIN " + "PRIVATE KEY-----\n" +
		base64.StdEncoding.EncodeToString(append([]byte(keytext.PKCS8Prefix), seed...)) +
		"\n-----END " + "PRIVATE KEY-----"
	generic := plantBody(plantAlnum, 32, 23)
	genericMatch := "api" + "_key = \"" + generic + "\""
	return []plantedSecret{
		token("github-pat", "gh"+"p_"+plantBody(plantAlnum, 36, 3)),
		token("aws-access-token", "AK"+"IA"+plantBody(plantBase32, 16, 5)),
		token("gitlab-pat", "glp"+"at-"+plantBody(plantAlnum, 20, 11)),
		token("slack-bot-token", "xox"+"b-"+plantBody(plantDigits, 11, 1)+"-"+plantBody(plantDigits, 12, 4)+"-"+plantBody(plantAlnum, 24, 29)),
		token("stripe-access-token", "sk"+"_live_"+plantBody(plantAlnum, 24, 13)),
		token("npm-access-token", "np"+"m_"+plantBody(plantLower, 36, 17)),
		token("gcp-api-key", "AI"+"za"+plantBody(plantAlnum+"_-", 35, 19)),
		token("jwt", "ey"+"J"+plantBody(plantAlnum, 30, 31)+".ey"+"J"+plantBody(plantAlnum, 40, 37)+"."+plantBody(plantAlnum+"_-", 43, 41)),
		{"private-key", keyBlock, keyBlock, keyBlock},
		{"generic-api-key", generic, genericMatch, "  " + genericMatch + ",  # service"},
	}
}

// plantedRegexProblem names a planted secret re excepts, read against all
// three texts whatever the allowlist's regexTarget says, so a regex is judged
// for every target it could be given.
func plantedRegexProblem(re *regexp.Regexp) string {
	for _, p := range plantedSecrets() {
		for _, text := range []string{p.secret, p.match, p.line} {
			if re.MatchString(text) {
				return fmt.Sprintf("regex %q excepts the planted %s secret", re, p.rule)
			}
		}
	}
	return ""
}

// plantedStopwordProblem names a planted secret word hides. gitleaks excepts a
// finding whose secret holds a stopword, ignoring case.
func plantedStopwordProblem(word string) string {
	for _, p := range plantedSecrets() {
		if strings.Contains(strings.ToLower(p.secret), strings.ToLower(word)) {
			return fmt.Sprintf("stopword %q excepts the planted %s secret", word, p.rule)
		}
	}
	return ""
}

func TestGitleaksRefusesEveryOneCharacterStopword(t *testing.T) {
	for _, c := range "abcdefghijklmnopqrstuvwxyz0123456789_-" {
		config := "[extend]\nuseDefault = true\n[[allowlists]]\nstopwords = ['" + string(c) + "']\n"
		problems := gitleaksConfigProblems(config)
		if !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, "excepts the planted") }) {
			t.Errorf("stopword %q: want a planted secret it excepts, got %q", c, problems)
		}
	}
}
