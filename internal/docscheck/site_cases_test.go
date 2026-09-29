package docscheck

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func wantProblem(t *testing.T, name string, problems []string, want string) {
	t.Helper()
	if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) }) {
		t.Errorf("%s: want a problem containing %q, got %q", name, want, problems)
	}
}

func wantNone(t *testing.T, name string, problems []string) {
	t.Helper()
	if len(problems) != 0 {
		t.Errorf("%s: a correct fixture reported %q", name, problems)
	}
}

func summed(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	var sums strings.Builder
	for _, name := range sortedKeys(files) {
		fsys[name] = &fstest.MapFile{Data: []byte(files[name])}
		sum := sha256.Sum256([]byte(files[name]))
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	fsys["SHA256SUMS"] = &fstest.MapFile{Data: []byte(sums.String())}
	return fsys
}

func digestOf(fsys fstest.MapFS) string {
	sum := sha256.Sum256(fsys["SHA256SUMS"].Data)
	return hex.EncodeToString(sum[:])
}

func TestBrandProblems(t *testing.T) {
	good := summed(map[string]string{"a.css": "a{}", "fonts/b.txt": "b"})
	pinned := digestOf(good)
	wantNone(t, "a correct copy", brandProblems(good, pinned, 2))

	resummed := summed(map[string]string{"a.css": "a{color:red}", "fonts/b.txt": "b"})
	extra := maps.Clone(good)
	extra["c.svg"] = &fstest.MapFile{Data: []byte("<svg/>")}
	missing := maps.Clone(good)
	delete(missing, "fonts/b.txt")
	edited := maps.Clone(good)
	edited["a.css"] = &fstest.MapFile{Data: []byte("a{color:red}")}
	malformed := summed(map[string]string{"a.css": "a{}", "fonts/b.txt": "b"})
	malformed["SHA256SUMS"] = &fstest.MapFile{Data: []byte(strings.Replace(string(good["SHA256SUMS"].Data), "  a.css", " a.css", 1))}
	emptySums := fstest.MapFS{"SHA256SUMS": &fstest.MapFile{Data: []byte("")}}
	for name, c := range map[string]struct {
		fsys   fstest.MapFS
		pinned string
		want   string
	}{
		"an edit re-summed":     {resummed, pinned, "not the pinned version"},
		"an edit not re-summed": {edited, pinned, "a.css does not match its line"},
		"an extra file":         {extra, pinned, "c.svg is not listed"},
		"a missing file":        {missing, pinned, "fonts/b.txt, which is missing"},
		"a malformed line":      {malformed, digestOf(malformed), "line 1 is malformed"},
		"an empty directory":    {fstest.MapFS{}, pinned, "reading SHA256SUMS"},
		"an empty sums file":    {emptySums, digestOf(emptySums), "want 2 each"},
	} {
		wantProblem(t, name, brandProblems(c.fsys, c.pinned, 2), c.want)
	}
	wantProblem(t, "a floor of zero", brandProblems(fstest.MapFS{"SHA256SUMS": &fstest.MapFile{}}, pinned, 0), "at least one")
}

func TestManifestProblems(t *testing.T) {
	files := map[string][]byte{"index.html": nil, ".assetsignore": nil}
	wantNone(t, "the pinned set", manifestProblems(files, []string{".assetsignore", "index.html"}))
	wantProblem(t, "an extra file", manifestProblems(files, []string{"index.html"}), ".assetsignore is not in the pinned manifest")
	wantProblem(t, "a missing file", manifestProblems(files, []string{".assetsignore", "index.html", "og.png"}), "names og.png, which is missing")
	wantProblem(t, "no file", manifestProblems(map[string][]byte{}, nil), "holds no file")
}

func TestHostProblems(t *testing.T) {
	base := map[string][]byte{
		"index.html": []byte(`<html><head><link rel="canonical" href="https://example.org/"/><link rel="stylesheet" href="/a.css"/></head>` +
			`<body><a href="https://example.org/x">x</a><svg><path marker-end="url(#m)"/></svg></body></html>`),
		"a.css": []byte(`@font-face{src:url("fonts/x.woff2")} .a{fill:url( #g )}`),
		"b.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`),
	}
	wantNone(t, "a page that loads only its own files", hostProblems(base))
	for name, c := range map[string]struct{ file, text, want string }{
		"a protocol-relative link": {"index.html", `<a href="//evil.example/">x</a>`, "protocol-relative"},
		"an upper-case SRC":        {"index.html", `<img SRC="https://evil.example/p.png"/>`, "a remote source"},
		"a srcset":                 {"index.html", `<img src="a.png" srcset="a.png 2x"/>`, "a srcset"},
		"a spaced quoted url":      {"a.css", `.a{background:url( 'https://evil.example/x.png' )}`, "is not a path of the site"},
		"an @import":               {"a.css", `@import "b.css";`, "an @import"},
		"an xlink:href":            {"b.svg", `<image xlink:href="p.png"/>`, "an xlink:href"},
		"a <use href>":             {"b.svg", `<use href="#a"/>`, "a <use> with a reference"},
		"a meta refresh":           {"index.html", `<meta http-equiv="refresh" content="0;url=/x"/>`, "a meta refresh"},
		"a preconnect":             {"index.html", `<link rel="preconnect" href="/"/>`, "a connection hint"},
		"a script element":         {"b.svg", `<script>1</script>`, "a script element"},
		"an event handler":         {"index.html", `<body onload="x()">`, "an event handler"},
		"a javascript: URL":        {"index.html", `<a href="javascript:void(0)">x</a>`, "a javascript: URL"},
		"a remote stylesheet":      {"index.html", `<link rel="stylesheet" href="https://cdn.example/a.css"/>`, `a <link rel="stylesheet">`},
		"a data url":               {"a.css", `.a{background:url(data:image/png;base64,AA)}`, "is not a path of the site"},
		"an <image href>":          {"b.svg", `<image href="p.png"/>`, "an <image> or <feImage> reference"},
		"a <feImage href>":         {"b.svg", `<filter><feImage href="p.png"/></filter>`, "an <image> or <feImage> reference"},
		"a ping":                   {"index.html", `<a href="/" ping="/count">x</a>`, "a ping attribute"},
		"an image-set()":           {"a.css", `.a{background:image-set("a.png" 1x)}`, "an image-set()"},
		"an entity-encoded source": {"index.html", `<img src="&#104;ttps://evil.example/p.png"/>`, "a remote source"},
	} {
		files := maps.Clone(base)
		if c.file == "index.html" {
			files[c.file] = []byte(strings.Replace(string(base[c.file]), "</body>", c.text+"</body>", 1))
		} else {
			files[c.file] = append(append([]byte{}, files[c.file]...), c.text...)
		}
		wantProblem(t, name, hostProblems(files), c.want)
	}
	wantProblem(t, "no stylesheet", hostProblems(map[string][]byte{"index.html": nil, "b.svg": nil}), "no .css file was read")
	wantProblem(t, "a page that does not parse", hostProblems(map[string][]byte{"index.html": []byte("<html><br></html>"), "a.css": nil, "b.svg": nil}), "index.html: does not parse")
}

const goodHeaders = "/*\n" +
	"  X-Frame-Options: DENY\n" +
	"  X-Content-Type-Options: nosniff\n" +
	"  Referrer-Policy: strict-origin-when-cross-origin\n" +
	"  Permissions-Policy: geolocation=(), microphone=(), camera=(), interest-cohort=()\n" +
	"  Strict-Transport-Security: max-age=31536000; includeSubDomains\n" +
	"  Content-Security-Policy: default-src 'none'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self'; connect-src 'none'; script-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'\n"

func TestHeaderProblems(t *testing.T) {
	wantNone(t, "the pinned headers", headerProblems([]byte(goodHeaders)))
	reordered := strings.Replace(goodHeaders, "default-src 'none'; style-src 'self' 'unsafe-inline';", "style-src 'self' 'unsafe-inline'; default-src 'none';", 1)
	wantNone(t, "directives in another order", headerProblems([]byte(reordered)))
	for name, c := range map[string]struct{ text, want string }{
		"a script source":       {strings.Replace(goodHeaders, "script-src 'none'", "script-src 'self'", 1), "script-src is \"'self'\""},
		"a host in a directive": {strings.Replace(goodHeaders, "font-src 'self'", "font-src 'self' https://fonts.example", 1), "font-src"},
		"a data: image source":  {strings.Replace(goodHeaders, "img-src 'self';", "img-src 'self' data:;", 1), `img-src is "'self' data:"`},
		"a directive dropped":   {strings.Replace(goodHeaders, "; connect-src 'none'", "", 1), "connect-src is \"\""},
		"a directive twice":     {strings.Replace(goodHeaders, "base-uri 'none'", "base-uri 'none'; base-uri 'self'", 1), "names base-uri twice"},
		"a second policy line":  {goodHeaders + "  Content-Security-Policy: default-src *\n", "Content-Security-Policy is set twice"},
		"a policy split in two": {strings.Replace(goodHeaders, "; img-src", ";\n  img-src", 1), "is not `Name: value`"},
		"a cross-origin block":  {goodHeaders + "\n/schemas/*\n  Access-Control-Allow-Origin: *\n", "want exactly [/*]"},
		"a header missing":      {strings.Replace(goodHeaders, "  X-Frame-Options: DENY\n", "", 1), "X-Frame-Options is not set"},
		"a header changed":      {strings.Replace(goodHeaders, "max-age=31536000", "max-age=60", 1), "Strict-Transport-Security is"},
		"a header of its own":   {goodHeaders + "  Access-Control-Allow-Origin: *\n", "not a header this site sends"},
	} {
		wantProblem(t, name, headerProblems([]byte(c.text)), c.want)
	}
}

func TestParseXHTMLIsStrict(t *testing.T) {
	if _, err := parseXHTML([]byte(`<!DOCTYPE html><html><body><p>a&nbsp;&amp;b<br/></p></body></html>`)); err != nil {
		t.Errorf("a polyglot page: %v", err)
	}
	for name, page := range map[string]string{
		"an open void element":  `<html><body><br></body></html>`,
		"a bare ampersand":      `<html><body>a & b</body></html>`,
		"an unknown entity":     `<html><body>&nosuch;</body></html>`,
		"an unquoted attribute": `<html lang=en></html>`,
		"two roots":             `<html></html><html></html>`,
	} {
		if _, err := parseXHTML([]byte(page)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}
