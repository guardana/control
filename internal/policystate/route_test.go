package policystate_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/policystate"
)

// The file names of the route ids these tests use: the hex SHA-256 of the
// name domain and the id, computed apart from the code under test. The last
// is the route file of a route id spelled as idA.
const (
	routeFileA      = "ba3be677aeeda338eb5c3213a33273c3a4490c874945abd0ca9f211ccc66a5a1.route.json"
	routeFileB      = "9f819d584ea720ed05c646443e2998657380751045941cb6c5266d5b429302b0.route.json"
	routeFileBundle = "335d546b06e3a2b4d6a99d8201111624f4db827ceb758d2592688099f1ef0a86.route.json"
	routeMarker     = "routes.meta"
)

const (
	routeA = "route-a"
	routeB = "route-b"
)

const (
	routeMarkerA = `{"schema_version":"1.0","kind":"route","route_ids":["route-a"]}` + "\n"
	routeEmptyA  = `{"schema_version":"1.0","route_id":"route-a","serial":null,"digest":null}` + "\n"
	routeAt5     = `{"schema_version":"1.0","route_id":"route-a","serial":5,"digest":"` + d5 + `"}` + "\n"
	routeAt7     = `{"schema_version":"1.0","route_id":"route-a","serial":7,"digest":"` + d7 + `"}` + "\n"
)

// routeDir is a route directory holding routeA's floor with no serial yet.
func routeDir(t testing.TB) string {
	t.Helper()
	dir := newDir(t)
	if err := policystate.InitRoute(t.Context(), dir, routeA); err != nil {
		t.Fatalf("InitRoute(%s): %v", dir, err)
	}
	return dir
}

// routeRaisedTo5 is a route directory whose routeA floor holds serial 5.
func routeRaisedTo5(t testing.TB) string {
	t.Helper()
	dir := routeDir(t)
	if _, err := policystate.RaiseRoute(t.Context(), dir, routeA, 5, d5); err != nil {
		t.Fatalf("RaiseRoute to 5: %v", err)
	}
	return dir
}

func describeRoute(f policystate.RouteFloor) string {
	return fmt.Sprintf("%s serial %d %q", f.RouteID, f.Serial, f.Digest)
}

// past is a modification time no write in these tests leaves.
var past = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

// snapshot is a directory's entries, each with its bytes and modification
// time, the directory's own time under ".".
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range append(entries, nil) {
		name := "."
		if e != nil {
			name = e.Name()
		}
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		body := ""
		if info.Mode().IsRegular() {
			body = readFile(t, filepath.Join(dir, name))
		}
		out[name] = info.ModTime().UTC().Format(time.RFC3339Nano) + " " + body
	}
	return out
}

// agePast sets every entry of dir, and dir itself, to the past time.
func agePast(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(dir, e.Name()), past, past); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(dir, past, past); err != nil {
		t.Fatal(err)
	}
}

func sameSnapshot(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("%s changed the directory:\n%v\nto\n%v", what, before, after)
	}
}

func TestInitRouteMakesAFloorWithNoSerial(t *testing.T) {
	dir := routeDir(t)
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the directory: %v, %v; want mode 0700", info, err)
	}
	if got := readFile(t, filepath.Join(dir, routeMarker)); got != routeMarkerA {
		t.Errorf("the marker holds %s, want %s", got, routeMarkerA)
	}
	if got := readFile(t, filepath.Join(dir, routeFileA)); got != routeEmptyA {
		t.Errorf("the route file holds %s, want %s", got, routeEmptyA)
	}
	f, err := policystate.ReadRoute(dir, routeA)
	if err != nil || f.HasSerial() || f.RouteID != routeA || f.Digest != "" {
		t.Errorf("ReadRoute: %s, %v; want %s with no serial", describeRoute(f), err, routeA)
	}
	if err := policystate.InitRoute(t.Context(), dir, routeB); err != nil {
		t.Fatalf("InitRoute of a second id: %v", err)
	}
	want := `{"schema_version":"1.0","kind":"route","route_ids":["route-a","route-b"]}` + "\n"
	if got := readFile(t, filepath.Join(dir, routeMarker)); got != want {
		t.Errorf("the marker holds %s, want %s", got, want)
	}
	if _, err := os.Lstat(filepath.Join(dir, routeFileB)); err != nil {
		t.Errorf("the second id's file: %v", err)
	}
}

func TestInitRouteNeverReplacesAFloor(t *testing.T) {
	dir := routeRaisedTo5(t)
	agePast(t, dir)
	before := snapshot(t, dir)
	if err := policystate.InitRoute(t.Context(), dir, routeA); !errors.Is(err, policystate.ErrExists) {
		t.Errorf("InitRoute of an id with a floor: %v, want ErrExists", err)
	}
	sameSnapshot(t, "a refused InitRoute", before, snapshot(t, dir))
	if err := os.Remove(filepath.Join(dir, routeFileA)); err != nil {
		t.Fatal(err)
	}
	if err := policystate.InitRoute(t.Context(), dir, routeA); !errors.Is(err, policystate.ErrFloorRemoved) {
		t.Errorf("InitRoute of an id whose file was removed: %v, want ErrFloorRemoved", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, routeFileA)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused InitRoute made the file: %v", err)
	}
}

func TestInitRouteRefusesAnIDPastTheBound(t *testing.T) {
	dir := newDir(t)
	for i := range policystate.MaxRouteIDs {
		if err := policystate.InitRoute(t.Context(), dir, fmt.Sprintf("route-%02d", i)); err != nil {
			t.Fatalf("InitRoute of id %d: %v", i, err)
		}
	}
	err := policystate.InitRoute(t.Context(), dir, "route-zz")
	if !errors.Is(err, policystate.ErrTooManyBundleIDs) {
		t.Errorf("InitRoute of id %d: %v, want ErrTooManyBundleIDs", policystate.MaxRouteIDs+1, err)
	}
	if strings.Contains(readFile(t, filepath.Join(dir, routeMarker)), "route-zz") {
		t.Error("the refused id is listed")
	}
	if f, err := policystate.ReadRoute(dir, "route-31"); err != nil || f.RouteID != "route-31" {
		t.Errorf("ReadRoute of the last id listed: %s, %v", describeRoute(f), err)
	}
}

// raiseTaken raises routeA to serial and digest, which must be taken, and
// holds the file to body.
func raiseTaken(t *testing.T, dir string, serial int64, digest, body string) {
	t.Helper()
	f, err := policystate.RaiseRoute(t.Context(), dir, routeA, serial, digest)
	if err != nil || f.Serial != serial || f.Digest != digest || f.RouteID != routeA {
		t.Errorf("RaiseRoute to %d: %s, %v", serial, describeRoute(f), err)
	}
	if got := readFile(t, filepath.Join(dir, routeFileA)); got != body {
		t.Errorf("the route file holds %s, want %s", got, body)
	}
}

func TestRaiseRouteTakesAHigherSerialAndTheSameRoute(t *testing.T) {
	dir := routeDir(t)
	raiseTaken(t, dir, 5, d5, routeAt5)

	agePast(t, dir)
	before := snapshot(t, dir)
	raiseTaken(t, dir, 5, d5, routeAt5)
	sameSnapshot(t, "a raise to the floor it holds", before, snapshot(t, dir))

	raiseTaken(t, dir, 7, d7, routeAt7)
	if got, err := policystate.ReadRoute(dir, routeA); err != nil || got.Serial != 7 || got.Digest != d7 {
		t.Errorf("ReadRoute after the raise: %s, %v", describeRoute(got), err)
	}
}

func TestRaiseRouteRefusesARouteTheFloorRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		serial int64
		digest string
		want   error
	}{
		"one below the floor":                {4, d5, policystate.ErrRouteBelowFloor},
		"below the floor with its digest":    {1, d3, policystate.ErrRouteBelowFloor},
		"the floor's serial, another digest": {5, d6, policystate.ErrRouteForked},
		"serial zero":                        {0, d5, policystate.ErrRouteInvalid},
		"serial zero and no digest":          {0, "", policystate.ErrRouteInvalid},
		"a negative serial":                  {-6, d6, policystate.ErrRouteInvalid},
		"past the JSON-safe serial":          {1 << 53, d6, policystate.ErrRouteInvalid},
		"an upper case digest":               {6, strings.ToUpper(d6), policystate.ErrRouteInvalid},
		"a digest of another algorithm":      {6, "sha512:" + d6[len("sha256:"):], policystate.ErrRouteInvalid},
		"no digest":                          {6, "", policystate.ErrRouteInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			dir := routeRaisedTo5(t)
			agePast(t, dir)
			before := snapshot(t, dir)
			f, err := policystate.RaiseRoute(t.Context(), dir, routeA, tc.serial, tc.digest)
			if !errors.Is(err, tc.want) || f != (policystate.RouteFloor{}) {
				t.Errorf("RaiseRoute(%d, %s): %s, %v; want %v and no floor", tc.serial, tc.digest, describeRoute(f), err, tc.want)
			}
			sameSnapshot(t, "a refused raise", before, snapshot(t, dir))
		})
	}
}

func TestRaiseRouteTakesTheLastJSONSafeSerial(t *testing.T) {
	dir := routeRaisedTo5(t)
	if f, err := policystate.RaiseRoute(t.Context(), dir, routeA, 1<<53-1, d6); err != nil || f.Serial != 9007199254740991 {
		t.Fatalf("RaiseRoute to 2^53-1: %s, %v", describeRoute(f), err)
	}
	want := `{"schema_version":"1.0","route_id":"route-a","serial":9007199254740991,"digest":"` + d6 + `"}` + "\n"
	if got := readFile(t, filepath.Join(dir, routeFileA)); got != want {
		t.Errorf("the route file holds %s, want %s", got, want)
	}
}

func TestARouteIDNoRouteCarriesIsRefused(t *testing.T) {
	dir := routeDir(t)
	for name, id := range map[string]string{
		"empty":                   "",
		"a control character":     "route-\x01a",
		"a line break":            "route-a\nroute-b",
		"one byte over the bound": strings.Repeat("r", 1025),
	} {
		if err := policystate.InitRoute(t.Context(), dir, id); !errors.Is(err, policystate.ErrRouteInvalid) {
			t.Errorf("InitRoute of %s: %v, want ErrRouteInvalid", name, err)
		}
		if _, err := policystate.ReadRoute(dir, id); !errors.Is(err, policystate.ErrRouteInvalid) {
			t.Errorf("ReadRoute of %s: %v, want ErrRouteInvalid", name, err)
		}
		if _, err := policystate.RaiseRoute(t.Context(), dir, id, 6, d6); !errors.Is(err, policystate.ErrRouteInvalid) {
			t.Errorf("RaiseRoute of %s: %v, want ErrRouteInvalid", name, err)
		}
	}
	if got := readFile(t, filepath.Join(dir, routeMarker)); got != routeMarkerA {
		t.Errorf("the marker became %s", got)
	}
	longest := strings.Repeat("r", 1024)
	if err := policystate.InitRoute(t.Context(), dir, longest); err != nil {
		t.Fatalf("InitRoute of an id at the bound: %v", err)
	}
	if f, err := policystate.RaiseRoute(t.Context(), dir, longest, 6, d6); err != nil || f.RouteID != longest {
		t.Errorf("RaiseRoute of an id at the bound: %v", err)
	}
}

// refusedBoth holds ReadRoute and RaiseRoute of id in dir to want.
func refusedBoth(t *testing.T, dir, id string, want error) {
	t.Helper()
	if f, err := policystate.ReadRoute(dir, id); !errors.Is(err, want) {
		t.Errorf("ReadRoute: %s, %v; want %v", describeRoute(f), err, want)
	}
	if f, err := policystate.RaiseRoute(t.Context(), dir, id, 6, d6); !errors.Is(err, want) {
		t.Errorf("RaiseRoute: %s, %v; want %v", describeRoute(f), err, want)
	}
}

// absent holds path to standing nowhere.
func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused call made %s: %v", path, err)
	}
}

func TestAnAbsentRouteFloorIsRefusedAndNeverMade(t *testing.T) {
	t.Run("a removed file", func(t *testing.T) {
		dir := routeRaisedTo5(t)
		if err := os.Remove(filepath.Join(dir, routeFileA)); err != nil {
			t.Fatal(err)
		}
		refusedBoth(t, dir, routeA, policystate.ErrNoFloor)
		absent(t, filepath.Join(dir, routeFileA))
	})
	t.Run("an id the marker does not list", func(t *testing.T) {
		dir := routeRaisedTo5(t)
		refusedBoth(t, dir, routeB, policystate.ErrNoFloor)
		absent(t, filepath.Join(dir, routeFileB))
		if got := readFile(t, filepath.Join(dir, routeMarker)); got != routeMarkerA {
			t.Errorf("the raise listed the id: %s", got)
		}
	})
	t.Run("a file of an id the marker does not list", func(t *testing.T) {
		dir := routeRaisedTo5(t)
		writeFile(t, filepath.Join(dir, routeFileB), `{"schema_version":"1.0","route_id":"route-b","serial":null,"digest":null}`+"\n")
		refusedBoth(t, dir, routeB, policystate.ErrForeignFile)
		refusedBoth(t, dir, routeA, policystate.ErrForeignFile)
	})
	t.Run("a removed directory", func(t *testing.T) {
		dir := routeRaisedTo5(t)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		refusedBoth(t, dir, routeA, policystate.ErrNotStateDir)
		absent(t, dir)
	})
	t.Run("a removed marker", func(t *testing.T) {
		dir := routeRaisedTo5(t)
		if err := os.Remove(filepath.Join(dir, routeMarker)); err != nil {
			t.Fatal(err)
		}
		refusedBoth(t, dir, routeA, policystate.ErrNotStateDir)
		absent(t, filepath.Join(dir, routeMarker))
	})
}

func TestReadRouteLeavesTheFloorAsItWas(t *testing.T) {
	dir := routeRaisedTo5(t)
	agePast(t, dir)
	before := snapshot(t, dir)
	f, err := policystate.ReadRoute(dir, routeA)
	if err != nil || f.Serial != 5 || f.Digest != d5 || f.RouteID != routeA {
		t.Errorf("ReadRoute: %s, %v; want serial 5", describeRoute(f), err)
	}
	sameSnapshot(t, "ReadRoute", before, snapshot(t, dir))
}

// TestEachKindKeepsToItsOwnDirectory: a route directory is not a plane's or
// a signer's, and the reverse, whichever side opens it.
func TestEachKindKeepsToItsOwnDirectory(t *testing.T) {
	route := routeRaisedTo5(t)
	for _, kind := range []policystate.Kind{policystate.KindPlane, policystate.KindSigner} {
		floors := initDir(t, kind)
		before := snapshot(t, floors)
		if _, err := policystate.ReadRoute(floors, idA); !errors.Is(err, policystate.ErrWrongKind) {
			t.Errorf("ReadRoute of a %s directory: %v, want ErrWrongKind", kind, err)
		}
		if _, err := policystate.RaiseRoute(t.Context(), floors, idA, 6, d6); !errors.Is(err, policystate.ErrWrongKind) {
			t.Errorf("RaiseRoute of a %s directory: %v, want ErrWrongKind", kind, err)
		}
		if err := policystate.InitRoute(t.Context(), floors, routeA); !errors.Is(err, policystate.ErrWrongKind) {
			t.Errorf("InitRoute in a %s directory: %v, want ErrWrongKind", kind, err)
		}
		sameSnapshot(t, "a refused route call", before, snapshot(t, floors))

		agePast(t, route)
		routeBefore := snapshot(t, route)
		if _, err := policystate.Open(route, kind); !errors.Is(err, policystate.ErrWrongKind) {
			t.Errorf("Open(%s) of a route directory: %v, want ErrWrongKind", kind, err)
		}
		if err := policystate.Init(t.Context(), route, kind, idA); !errors.Is(err, policystate.ErrWrongKind) {
			t.Errorf("Init(%s) in a route directory: %v, want ErrWrongKind", kind, err)
		}
		empty := floorAt3(t, "09:00:00")
		if _, err := policystate.Reset(t.Context(), route, kind, empty, "into a route directory"); !errors.Is(err, policystate.ErrWrongKind) {
			t.Errorf("Reset(%s) in a route directory: %v, want ErrWrongKind", kind, err)
		}
		sameSnapshot(t, "a refused floor call", routeBefore, snapshot(t, route))
	}
	if _, err := policystate.Open(route, policystate.KindRoute); !errors.Is(err, policystate.ErrKind) {
		t.Errorf("Open as a route kind: %v, want ErrKind", err)
	}
	if err := policystate.Init(t.Context(), newDir(t), policystate.KindRoute, idA); !errors.Is(err, policystate.ErrKind) {
		t.Errorf("Init as a route kind: %v, want ErrKind", err)
	}
	if _, err := policystate.Reset(t.Context(), route, policystate.KindRoute, floorAt3(t, "09:00:00"), "r"); !errors.Is(err, policystate.ErrKind) {
		t.Errorf("Reset as a route kind: %v, want ErrKind", err)
	}
}

// TestAPlaneOrSignerDirectoryStillReads: the files a plane's or a signer's
// directory held before route floors existed, written out here, still open
// and read as they did.
func TestAPlaneOrSignerDirectoryStillReads(t *testing.T) {
	for _, kind := range []string{"plane", "signer"} {
		dir := newDir(t)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "floors.meta"), `{"schema_version":"1.0","kind":"`+kind+`","bundle_ids":["bundle-a"]}`+"\n")
		writeFile(t, filepath.Join(dir, fileA), floorBody())
		s, err := policystate.Open(dir, policystate.Kind(kind))
		if err != nil {
			t.Fatalf("Open(%s): %v", kind, err)
		}
		f, err := s.Floor(t.Context(), idA)
		if err != nil || f.Serial() != 5 || f.Digest() != d5 {
			t.Errorf("%s: Floor: %s, %v; want serial 5", kind, describe(f), err)
		}
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}
}

// TestARouteFileHasANameOfItsOwn: a route id spelled as a bundle id names
// another file than the bundle id's, and neither directory takes the other
// kind's file.
func TestARouteFileHasANameOfItsOwn(t *testing.T) {
	dir := newDir(t)
	if err := policystate.InitRoute(t.Context(), dir, idA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, routeFileBundle)); err != nil {
		t.Errorf("the route file of %s: %v", idA, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, fileA)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the route floor took the bundle id's file name: %v", err)
	}

	writeFile(t, filepath.Join(dir, fileA), emptyBodyA)
	if _, err := policystate.ReadRoute(dir, idA); !errors.Is(err, policystate.ErrForeignFile) {
		t.Errorf("ReadRoute beside a plane's floor file: %v, want ErrForeignFile", err)
	}
	if _, err := policystate.RaiseRoute(t.Context(), dir, idA, 6, d6); !errors.Is(err, policystate.ErrForeignFile) {
		t.Errorf("RaiseRoute beside a plane's floor file: %v, want ErrForeignFile", err)
	}
	if err := policystate.InitRoute(t.Context(), dir, routeB); !errors.Is(err, policystate.ErrForeignFile) {
		t.Errorf("InitRoute beside a plane's floor file: %v, want ErrForeignFile", err)
	}

	plane := initDir(t, policystate.KindPlane)
	writeFile(t, filepath.Join(plane, routeFileA), routeEmptyA)
	if _, err := policystate.Open(plane, policystate.KindPlane); !errors.Is(err, policystate.ErrForeignFile) {
		t.Errorf("Open beside a route file: %v, want ErrForeignFile", err)
	}
}

func TestTheRouteFilesAreReadStrictly(t *testing.T) {
	for name, tc := range map[string]struct {
		file, body string
		want       error
	}{
		"a later minor":             {routeFileA, strings.Replace(routeAt5, `"1.0"`, `"1.3"`, 1), nil},
		"another major":             {routeFileA, strings.Replace(routeAt5, `"1.0"`, `"2.0"`, 1), policystate.ErrSchemaVersion},
		"an unknown member":         {routeFileA, strings.Replace(routeAt5, `}`, `,"note":"x"}`, 1), policystate.ErrUnknownField},
		"a missing member":          {routeFileA, strings.Replace(routeAt5, `,"digest":"`+d5+`"`, "", 1), policystate.ErrMissingField},
		"a member twice":            {routeFileA, strings.Replace(routeAt5, `}`, `,"serial":9}`, 1), policystate.ErrDuplicateField},
		"a digest with no serial":   {routeFileA, strings.Replace(routeAt5, `"serial":5`, `"serial":null`, 1), policystate.ErrMalformed},
		"a serial with no digest":   {routeFileA, strings.Replace(routeAt5, `"`+d5+`"`, "null", 1), policystate.ErrMalformed},
		"serial zero":               {routeFileA, strings.Replace(routeAt5, `"serial":5`, `"serial":0`, 1), policystate.ErrMalformed},
		"serial zero, digest empty": {routeFileA, strings.Replace(routeAt5, `"serial":5,"digest":"`+d5+`"`, `"serial":0,"digest":""`, 1), policystate.ErrMalformed},
		"a serial in quotes":        {routeFileA, strings.Replace(routeAt5, `"serial":5`, `"serial":"5"`, 1), policystate.ErrMalformed},
		"a fractional serial":       {routeFileA, strings.Replace(routeAt5, `"serial":5`, `"serial":5.0`, 1), policystate.ErrMalformed},
		"another route id":          {routeFileA, strings.Replace(routeAt5, routeA, routeB, 1), policystate.ErrNameMismatch},
		"a marker naming a plane":   {routeMarker, strings.Replace(routeMarkerA, `"route"`, `"plane"`, 1), policystate.ErrNotStateDir},
		"a marker of bundle ids":    {routeMarker, strings.Replace(routeMarkerA, "route_ids", "bundle_ids", 1), policystate.ErrUnknownField},
		"a marker out of order":     {routeMarker, strings.Replace(routeMarkerA, `["route-a"]`, `["route-a","route-0"]`, 1), policystate.ErrMalformed},
		"a marker listing none":     {routeMarker, strings.Replace(routeMarkerA, `["route-a"]`, `[]`, 1), policystate.ErrMalformed},
		"a marker of another major": {routeMarker, strings.Replace(routeMarkerA, `"1.0"`, `"2.0"`, 1), policystate.ErrSchemaVersion},
	} {
		t.Run(name, func(t *testing.T) {
			dir := routeRaisedTo5(t)
			writeFile(t, filepath.Join(dir, tc.file), tc.body)
			f, err := policystate.ReadRoute(dir, routeA)
			if tc.want == nil {
				if err != nil || f.Serial != 5 {
					t.Errorf("ReadRoute: %s, %v; want serial 5", describeRoute(f), err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("ReadRoute: %s, %v; want %v", describeRoute(f), err, tc.want)
			}
			if _, err := policystate.RaiseRoute(t.Context(), dir, routeA, 6, d6); !errors.Is(err, tc.want) {
				t.Errorf("RaiseRoute: %v; want %v", err, tc.want)
			}
			if got := readFile(t, filepath.Join(dir, tc.file)); got != tc.body {
				t.Errorf("a refused raise rewrote %s as %s", tc.file, got)
			}
		})
	}
}
