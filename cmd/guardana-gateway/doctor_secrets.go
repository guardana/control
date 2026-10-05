package main

import (
	"context"
	"fmt"
	"os"

	"github.com/guardana/control/internal/secretscan"
)

// secrets says how many configured values the plane withholds an upstream's
// answer for, and names by its key, never its value, each value too short to
// scan and each credential short enough to withhold answers that merely hold
// it (ADR-0042). The set is the one run builds, so its refusal is run's too.
func (d *examination) secrets(context.Context) (string, string, string) {
	listed := d.cfg.Secrets(os.LookupEnv)
	set, err := secretscan.New(listed)
	if err != nil {
		return verdictFail, "secrets", err.Error()
	}
	for _, sec := range listed {
		// A credential is matched whatever its length, so a short one withholds
		// every answer that merely holds it.
		if sec.Kind == secretscan.Credential && len(sec.Value) < secretscan.MinMaybeBytes {
			writeLine(d.out, fmt.Sprintf("       %-28s shorter than %d bytes, matched anyway: an answer holding it is withheld",
				oneLine(sec.Key), secretscan.MinMaybeBytes))
		}
	}
	short := set.NotScanned()
	for _, key := range short {
		writeLine(d.out, fmt.Sprintf("       %-28s shorter than %d bytes, not scanned", oneLine(key), secretscan.MinMaybeBytes))
	}
	return verdictOK, "secrets", fmt.Sprintf("upstream answers are scanned for %d configured value(s); %d value(s) shorter than %d bytes are not",
		len(listed)-len(short), len(short), secretscan.MinMaybeBytes)
}
