package reaction_test

import (
	"testing"

	"github.com/guardana/control/internal/brand"
)

// Each case is a way to write a refused read, or a near miss that is not one.
func TestImpureReads(t *testing.T) {
	policykey := `import "` + brand.ModulePath + `/internal/policykey"; `
	for _, c := range []struct {
		name, src string
		want      []string
	}{
		{"clock", `import "time"; var _ = time.Now()`, []string{"names time.Now"}},
		{"clock as a value", `import clock "time"; var f = clock.Since`, []string{"names time.Since"}},
		{"zone", `import "time"; func f(t time.Time) { _ = t.Local() }`, []string{"names the method Local"}},
		{"zone through an alias's method value", `import "time"; type T = time.Time; var _ = T.Local`, []string{"names the method Local"}},
		{"deadline", `import "context"; var _, _ = context.WithTimeout(nil, 0)`, []string{"names context.WithTimeout"}},
		{"input", `import "fmt"; var _, _ = fmt.Scanln()`, []string{"names fmt.Scanln"}},
		{"output through fmt", `import "fmt"; func f() { fmt.Println("x") }`, []string{"names fmt.Println"}},
		{"key generation", `import "crypto/ed25519"; var _, _, _ = ed25519.GenerateKey(nil)`, []string{"names crypto/ed25519.GenerateKey"}},
		{"file", `import "os"; var _, _ = os.ReadFile("x")`, []string{"imports os"}},
		{"file, blank", `import _ "os"`, []string{"imports os"}},
		{"process", `import "os/exec"; var _ = exec.Command("x")`, []string{"imports os/exec"}},
		{"randomness", `import "crypto/rand"; var _ = rand.Reader`, []string{"imports crypto/rand"}},
		{"renamed randomness", `import r "math/rand/v2"; var _ = r.Int()`, []string{"imports math/rand/v2"}},
		{"a seed of the hash", `import "hash/maphash"; var _ = maphash.MakeSeed()`, []string{"imports hash/maphash"}},
		{"lattice key generation", `import "crypto/mlkem"; var _, _ = mlkem.GenerateKey768()`, []string{"imports crypto/mlkem"}},
		{"curve key generation", `import ("crypto/ecdsa"; "crypto/elliptic"); var _, _ = ecdsa.GenerateKey(elliptic.P256(), nil)`,
			[]string{"imports crypto/ecdsa", "imports crypto/elliptic"}},
		{"exchange key generation", `import "crypto/ecdh"; var _, _ = ecdh.X25519().GenerateKey(nil)`, []string{"imports crypto/ecdh"}},
		{"RSA key generation", `import "crypto/rsa"; var _, _ = rsa.GenerateKey(nil, 2048)`, []string{"imports crypto/rsa"}},
		{"an archive by path", `import "archive/zip"; var _, _ = zip.OpenReader("x")`, []string{"imports archive/zip"}},
		{"an executable by path", `import "debug/elf"; var _, _ = elf.Open("x")`, []string{"imports debug/elf"}},
		{"templates by path", `import "text/template"; var _, _ = template.ParseFiles("x")`, []string{"imports text/template"}},
		{"the system's roots", `import "crypto/x509"; var _, _ = x509.SystemCertPool()`, []string{"imports crypto/x509"}},
		{"the system's types", `import "mime"; var _ = mime.TypeByExtension(".x")`, []string{"imports mime"}},
		{"a package directory", `import "go/build"; var _, _ = build.ImportDir(".", 0)`, []string{"imports go/build"}},
		{"the system log", `import "log/syslog"; var _, _ = syslog.Dial("udp", "x", 0, "x")`, []string{"imports log/syslog"}},
		{"output through log", `import "log"; func f() { log.Print("x") }`, []string{"imports log"}},
		{"the build", `import "runtime/debug"; var _ = debug.ReadBuildInfo`, []string{"imports runtime/debug"}},
		{"cgo", `import "C"; var _ = C.time(nil)`, []string{"imports C"}},
		{"reflection by method name", `import ("reflect"; "time"); func f(t time.Time) { reflect.ValueOf(t).MethodByName("Lo"+"cal") }`,
			[]string{"imports reflect"}},
		{"network", `import "net/http"; var _ = http.Get`, []string{"imports net/http"}},
		{"the module's file helpers", `import _ "` + brand.ModulePath + `/internal/files"`, []string{"imports " + brand.ModulePath + "/internal/files"}},
		{"a statement file through policykey", policykey + `var _, _ = policykey.ReadStatement("x")`,
			[]string{"names " + brand.ModulePath + "/internal/policykey.ReadStatement, which is not listed"}},
		{"a key pair written through policykey", policykey + `var _ = policykey.WriteKeyPair("x", nil)`,
			[]string{"names " + brand.ModulePath + "/internal/policykey.WriteKeyPair, which is not listed"}},
		{"dot import of the clock", `import . "time"; var _ = Now()`, []string{"imports time with a dot"}},
		{"dot import of anything", `import . "strings"; var _ = ToUpper("x")`, []string{"imports strings with a dot"}},
		{"a package not on the list", `import clock "example.invalid/m/clock"; var _ = clock.Now()`, []string{"imports example.invalid/m/clock"}},

		{"a time from a number", `import "time"; var _ = time.Unix(0, 0)`, nil},
		{"a duration", `import "time"; var _ = time.Minute`, nil},
		{"an error", `import "fmt"; var _ = fmt.Errorf("x")`, nil},
		{"a key id through policykey", policykey + `var _ = policykey.KeyID(nil)`, nil},
		{"a guarded module package", `import "` + brand.ModulePath + `/internal/canon"; var _ = canon.ValidDigest`, nil},
		{"a name in a string", `var _ = "time.Now"`, nil},
	} {
		problems := impureReads(t, "x.go", []byte("package x; "+c.src))
		if len(problems) != len(c.want) {
			t.Errorf("%s: impureReads = %q, want %q", c.name, problems, c.want)
			continue
		}
		for i, want := range c.want {
			if problems[i] != "x.go:1: "+want {
				t.Errorf("%s: problem %q, want %q", c.name, problems[i], "x.go:1: "+want)
			}
		}
	}
}
