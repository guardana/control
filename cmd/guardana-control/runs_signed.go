package main

import (
	"errors"
	"flag"
	"fmt"
	"time"

	ondisk "github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/runs"
	"github.com/guardana/control/internal/runs/signedruns"
	"github.com/guardana/control/internal/trust"
)

// runsSignHint is what an operator adds to write a signed layout.
const runsSignHint = "--key <operator key file> --roster <roster file> --trust-root <trust root public key file>"

// runSignFlags is what a signed write of a runs directory is told: the
// operator's key, and the roster and trust root its records, and the records
// it signs over, are checked against.
type runSignFlags struct {
	key, roster, trustRoot *string
}

func runsSignFlags(flags *flag.FlagSet) runSignFlags {
	return runSignFlags{
		key:       flags.String("key", "", "the operator's private key file, which signs the record for a plane under the signed authority"),
		roster:    flags.String("roster", "", "with --key: the trust roster file the plane reads, which every record signed or signed over is checked against"),
		trustRoot: flags.String("trust-root", "", "with --key: the trust root public key file the roster is verified under"),
	}
}

// given reports whether any of the three flags was given; problem says what
// is wrong with what was.
func (f runSignFlags) given() (bool, string) {
	set := 0
	for _, v := range []*string{f.key, f.roster, f.trustRoot} {
		if *v != "" {
			set++
		}
	}
	switch set {
	case 0:
		return false, ""
	case 3:
		return true, ""
	}
	return true, "a signed write takes " + runsSignHint + ", all three"
}

// authority reads the operator key into a signer, and the roster verified
// under the trust root and usable at now into the judge every record signed
// or signed over is held to. The key is kept for the command's life and
// cleared by release.
func (f runSignFlags) authority(now time.Time) (runs.Signer, runs.Judge, func(), error) {
	root, err := readPublicKey("--trust-root", *f.trustRoot)
	if err != nil {
		return runs.Signer{}, nil, nil, err
	}
	raw, err := ondisk.ReadRegular(*f.roster, trust.MaxFileBytes, 0)
	if err != nil {
		return runs.Signer{}, nil, nil, fmt.Errorf("--roster: %w", err)
	}
	env, err := trust.ParseFile(raw)
	if err != nil {
		return runs.Signer{}, nil, nil, fmt.Errorf("--roster: %w", err)
	}
	doc, err := trust.VerifyDocument(env, root)
	if err == nil {
		err = doc.UsableAt(now)
	}
	if err != nil {
		return runs.Signer{}, nil, nil, fmt.Errorf("--roster: %w", err)
	}
	key, err := policykey.ReadPrivate(*f.key)
	if err != nil {
		return runs.Signer{}, nil, nil, fmt.Errorf("--key: %w", err)
	}
	s := runs.Signer{KeyID: policykey.KeyID(publicHalf(key)), Sign: func(payloadType string, body []byte) ([]byte, error) {
		return bundle.SignBytesAs(payloadType, body, key)
	}}
	return s, signedruns.Roster{Document: doc}, func() { clear(key) }, nil
}

// runsHint is what an operator does about a write the directory's layout
// refused: a signed layout takes only a signed write.
func runsHint(err error) error {
	if errors.Is(err, runs.ErrSigned) {
		return fmt.Errorf("%w; sign it with %s", err, runsSignHint)
	}
	return err
}
