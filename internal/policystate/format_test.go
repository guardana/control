package policystate_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policystate"
)

// floorBody is a floor file for idA holding serial 5, with the members given
// in place of the plain ones.
func floorBody(members ...string) string {
	plain := []string{
		`"schema_version":"1.0"`, `"bundle_id":"bundle-a"`, `"serial":5`, `"digest":"` + d5 + `"`,
		`"issued_at":"2026-09-11T10:00:00Z"`, `"latest_issued_at":"2026-09-11T11:00:00Z"`,
		`"reset_reason":null`, `"reset_from":null`,
	}
	for _, m := range members {
		name, _, _ := strings.Cut(m, ":")
		replaced := false
		for i, p := range plain {
			if strings.HasPrefix(p, name+":") {
				plain[i], replaced = m, true
			}
		}
		if !replaced {
			plain = append(plain, m)
		}
	}
	return "{" + strings.Join(plain, ",") + "}\n"
}

const resetFrom3 = `"reset_from":{"serial":3,"digest":"` + d3 + `","issued_at":"2026-09-11T09:00:00Z","latest_issued_at":"2026-09-11T09:00:00Z"}`

func TestTheFloorFileIsReadStrictly(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want error
	}{
		"the plain file":                    {floorBody(), nil},
		"a later minor":                     {floorBody(`"schema_version":"1.7"`), nil},
		"no serial yet":                     {floorBody(`"serial":null`, `"digest":null`, `"issued_at":null`, `"latest_issued_at":null`), nil},
		"a reset":                           {floorBody(`"reset_reason":"withdrawn"`, resetFrom3), nil},
		"a reset from no serial":            {floorBody(`"reset_reason":"withdrawn"`, `"reset_from":{"serial":null,"digest":null,"issued_at":null,"latest_issued_at":null}`), nil},
		"another major":                     {floorBody(`"schema_version":"2.0"`), policystate.ErrSchemaVersion},
		"a version with no minor":           {floorBody(`"schema_version":"1"`), policystate.ErrSchemaVersion},
		"an unknown member":                 {floorBody(`"note":"x"`), policystate.ErrUnknownField},
		"a member in another case":          {strings.Replace(floorBody(), `"serial"`, `"Serial"`, 1), policystate.ErrUnknownField},
		"a repeated member":                 {strings.Replace(floorBody(), `"serial":5`, `"serial":5,"serial":9`, 1), policystate.ErrDuplicateField},
		"a missing member":                  {strings.Replace(floorBody(), `,"reset_from":null`, ``, 1), policystate.ErrMissingField},
		"another bundle id":                 {floorBody(`"bundle_id":"bundle-b"`), policystate.ErrNameMismatch},
		"a serial of zero":                  {floorBody(`"serial":0`), policystate.ErrMalformed},
		"a serial as a string":              {floorBody(`"serial":"5"`), policystate.ErrMalformed},
		"a serial with a fraction":          {floorBody(`"serial":5.0`), policystate.ErrMalformed},
		"a serial past the safe range":      {floorBody(`"serial":9007199254740992`), policystate.ErrMalformed},
		"a serial with no digest":           {floorBody(`"digest":null`), policystate.ErrMalformed},
		"no serial with a digest":           {floorBody(`"serial":null`, `"issued_at":null`, `"latest_issued_at":null`), policystate.ErrMalformed},
		"a digest in upper case":            {floorBody(`"digest":"` + strings.ToUpper(d5) + `"`), policystate.ErrMalformed},
		"issued_at with a fraction":         {floorBody(`"issued_at":"2026-09-11T10:00:00.5Z"`), policystate.ErrMalformed},
		"issued_at with an offset":          {floorBody(`"issued_at":"2026-09-11T10:00:00+00:00"`), policystate.ErrMalformed},
		"latest before issued":              {floorBody(`"latest_issued_at":"2026-09-11T09:59:59Z"`), policystate.ErrMalformed},
		"a reason, the file was missing":    {floorBody(`"reset_reason":"withdrawn"`), nil},
		"a prior value with no reason":      {floorBody(resetFrom3), policystate.ErrMalformed},
		"an empty reason":                   {floorBody(`"reset_reason":""`, resetFrom3), policystate.ErrMalformed},
		"a prior value missing a member":    {floorBody(`"reset_reason":"withdrawn"`, `"reset_from":{"serial":3,"digest":"`+d3+`","issued_at":"2026-09-11T09:00:00Z"}`), policystate.ErrMissingField},
		"a prior value with another member": {floorBody(`"reset_reason":"withdrawn"`, strings.Replace(resetFrom3, `"serial":3`, `"serial":3,"bundle_id":"bundle-a"`, 1)), policystate.ErrUnknownField},
		"a prior value that is a string":    {floorBody(`"reset_reason":"withdrawn"`, `"reset_from":"3"`), policystate.ErrMalformed},
		"data after the object":             {floorBody() + "{}", policystate.ErrMalformed},
		"an array":                          {"[" + floorBody() + "]", policystate.ErrMalformed},
		"invalid UTF-8 in a string":         {floorBody(`"reset_reason":"with`+"\xff"+`drawn"`, resetFrom3), policystate.ErrMalformed},
		"an unpaired surrogate":             {floorBody(`"reset_reason":"with\ud800drawn"`, resetFrom3), policystate.ErrMalformed},
	} {
		t.Run(name, func(t *testing.T) {
			dir, s := raised(t)
			writeFile(t, filepath.Join(dir, fileA), tc.body)
			f, err := s.Floor(t.Context(), idA)
			if !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
				t.Errorf("Floor: %s, %v; want %v", describe(f), err, tc.want)
			}
		})
	}
}

func TestTheFloorFileIsReadUpToItsBound(t *testing.T) {
	for name, tc := range map[string]struct {
		size int
		want error
	}{
		"exactly the bound": {8192, nil},
		"one byte over":     {8193, policystate.ErrTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			dir, s := raised(t)
			body := floorBody()
			writeFile(t, filepath.Join(dir, fileA), body+strings.Repeat(" ", tc.size-len(body)))
			if f, err := s.Floor(t.Context(), idA); !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
				t.Errorf("Floor of %d bytes: %s, %v; want %v", tc.size, describe(f), err, tc.want)
			}
		})
	}
}

func TestTheMarkerIsReadStrictly(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want error
	}{
		"a later minor":            {`{"schema_version":"1.3","kind":"plane","bundle_ids":["bundle-a"]}`, nil},
		"ids beyond the files":     {`{"schema_version":"1.0","kind":"plane","bundle_ids":["bundle-a","bundle-b"]}`, nil},
		"another major":            {`{"schema_version":"2.0","kind":"plane","bundle_ids":["bundle-a"]}`, policystate.ErrNotStateDir},
		"a kind of no meaning":     {`{"schema_version":"1.0","kind":"runs","bundle_ids":["bundle-a"]}`, policystate.ErrNotStateDir},
		"an unknown member":        {`{"schema_version":"1.0","kind":"plane","bundle_ids":["bundle-a"],"note":"x"}`, policystate.ErrNotStateDir},
		"a repeated member":        {`{"schema_version":"1.0","kind":"signer","kind":"plane","bundle_ids":["bundle-a"]}`, policystate.ErrNotStateDir},
		"no kind":                  {`{"schema_version":"1.0","bundle_ids":["bundle-a"]}`, policystate.ErrNotStateDir},
		"no ids":                   {`{"schema_version":"1.0","kind":"plane"}`, policystate.ErrNotStateDir},
		"no ids listed":            {`{"schema_version":"1.0","kind":"plane","bundle_ids":[]}`, policystate.ErrNotStateDir},
		"ids that are not a list":  {`{"schema_version":"1.0","kind":"plane","bundle_ids":"bundle-a"}`, policystate.ErrNotStateDir},
		"ids out of order":         {`{"schema_version":"1.0","kind":"plane","bundle_ids":["bundle-b","bundle-a"]}`, policystate.ErrNotStateDir},
		"an id twice":              {`{"schema_version":"1.0","kind":"plane","bundle_ids":["bundle-a","bundle-a"]}`, policystate.ErrNotStateDir},
		"an id a bundle cannot be": {`{"schema_version":"1.0","kind":"plane","bundle_ids":["bundle-a"," x"]}`, policystate.ErrNotStateDir},
		"an id that is null":       {`{"schema_version":"1.0","kind":"plane","bundle_ids":["bundle-a",null]}`, policystate.ErrNotStateDir},
		"the file's id unlisted":   {`{"schema_version":"1.0","kind":"plane","bundle_ids":["bundle-b"]}`, policystate.ErrForeignFile},
		"an empty file":            {``, policystate.ErrNotStateDir},
	} {
		t.Run(name, func(t *testing.T) {
			dir := initDir(t, policystate.KindPlane)
			writeFile(t, filepath.Join(dir, "floors.meta"), tc.body)
			s, err := policystate.Open(dir, policystate.KindPlane)
			if !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
				t.Errorf("Open: %v, want %v", err, tc.want)
			}
			if err == nil {
				_ = s.Close()
			}
		})
	}
}

func TestAMarkerChangedOnceOpenIsReadAgain(t *testing.T) {
	dir, s := raised(t)
	writeFile(t, filepath.Join(dir, "floors.meta"), `{"schema_version":"1.0","kind":"signer","bundle_ids":["bundle-a"]}`)
	if _, err := s.Raise(t.Context(), statement(t, idA, 6, d6, "11:30:00"), noon(t)); !errors.Is(err, policystate.ErrWrongKind) {
		t.Errorf("Raise once the marker names the signer: %v, want ErrWrongKind", err)
	}
	if _, err := s.Floor(t.Context(), idA); !errors.Is(err, policystate.ErrWrongKind) {
		t.Errorf("Floor once the marker names the signer: %v, want ErrWrongKind", err)
	}
}
