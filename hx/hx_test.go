package hx

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"testing/iotest"
)

func TestIsRequest(t *testing.T) {
	tests := []struct {
		name   string
		header map[string]string
		want   bool
	}{
		{"no headers", nil, false},
		{"htmx", map[string]string{"HX-Request": "true"}, true},
		{"case-insensitive value", map[string]string{"HX-Request": "True"}, true},
		{"surrounding space", map[string]string{"HX-Request": " true "}, true},
		{"false", map[string]string{"HX-Request": "false"}, false},
		{"empty", map[string]string{"HX-Request": ""}, false},
		{"lowercase header name", map[string]string{"hx-request": "true"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			for k, v := range tt.header {
				r.Header.Set(k, v)
			}
			if got := IsRequest(r); got != tt.want {
				t.Errorf("IsRequest = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsPartial(t *testing.T) {
	tests := []struct {
		name   string
		header map[string]string
		want   bool
	}{
		{"no headers", nil, false},
		{"partial", map[string]string{"HX-Request": "true", "HX-Request-Type": "partial"}, true},
		{"full", map[string]string{"HX-Request": "true", "HX-Request-Type": "full"}, false},
		{"case-insensitive value", map[string]string{"HX-Request": "true", "HX-Request-Type": "Partial"}, true},
		{"type without HX-Request", map[string]string{"HX-Request-Type": "partial"}, false},
		{"htmx without type", map[string]string{"HX-Request": "true"}, false},
		{"garbage", map[string]string{"HX-Request": "true", "HX-Request-Type": "partially"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			for k, v := range tt.header {
				r.Header.Set(k, v)
			}
			if got := IsPartial(r); got != tt.want {
				t.Errorf("IsPartial = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTag(t *testing.T) {
	format := regexp.MustCompile(`^"hp-[0-9a-f]{16}"$`)
	inputs := [][]byte{nil, {}, []byte("a"), []byte("<svg/>"), []byte(strings.Repeat("x", 1<<16))}
	for _, in := range inputs {
		got := Tag(in)
		if !format.MatchString(got) {
			t.Errorf("Tag(%.20q) = %s, want format %s", in, got, format)
		}
		if again := Tag(append([]byte(nil), in...)); again != got {
			t.Errorf("Tag not deterministic: %s then %s", got, again)
		}
	}

	// Known FNV-1a 64 vectors pin the algorithm, so tags stay stable across
	// releases (a changed tag would invalidate every client's ptag).
	vectors := map[string]string{
		"":       `"hp-cbf29ce484222325"`, // the FNV-64 offset basis
		"a":      `"hp-af63dc4c8601ec8c"`,
		"foobar": `"hp-85944171f73967e8"`,
	}
	for in, want := range vectors {
		if got := Tag([]byte(in)); got != want {
			t.Errorf("Tag(%q) = %s, want %s", in, got, want)
		}
	}

	if Tag([]byte("<svg>1</svg>")) == Tag([]byte("<svg>2</svg>")) {
		t.Error("different content produced the same tag")
	}
}

func TestCheckPTag(t *testing.T) {
	const tag = `"hp-0123456789abcdef"`
	tests := []struct {
		name     string
		tag      string
		reqTag   string // "" means no header
		wantHit  bool
		wantHdr  string
		wantCode int
	}{
		{"first request", tag, "", false, tag, http.StatusOK},
		{"exact match", tag, tag, true, tag, http.StatusNotModified},
		{"request unquoted", tag, `hp-0123456789abcdef`, true, tag, http.StatusNotModified},
		{"request weak", tag, `W/"hp-0123456789abcdef"`, true, tag, http.StatusNotModified},
		{"request padded", tag, "  " + tag + " ", true, tag, http.StatusNotModified},
		{"tag unquoted", `v42`, `"v42"`, true, `v42`, http.StatusNotModified},
		{"tag weak", `W/"v42"`, `v42`, true, `W/"v42"`, http.StatusNotModified},
		{"changed", tag, `"hp-fedcba9876543210"`, false, tag, http.StatusOK},
		{"case matters", `"V42"`, `"v42"`, false, `"V42"`, http.StatusOK},
		{"empty quotes do not match", `""`, `""`, false, "", http.StatusOK},
		{"empty tag disables", "", `""`, false, "", http.StatusOK},
		{"lone quote is opaque", `"`, `"`, true, `"`, http.StatusNotModified},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/chart", nil)
			r.Header.Set("HX-Request", "true")
			if tt.reqTag != "" {
				r.Header.Set("HX-PTag", tt.reqTag)
			}
			w := httptest.NewRecorder()
			w.Header().Set("Content-Type", "text/html")

			hit := CheckPTag(w, r, tt.tag)
			if !hit {
				_, _ = w.WriteString("<svg/>")
			}
			res := w.Result()

			if hit != tt.wantHit {
				t.Errorf("CheckPTag = %v, want %v", hit, tt.wantHit)
			}
			if got := res.Header.Get("HX-PTag"); got != tt.wantHdr {
				t.Errorf("HX-PTag response header = %q, want %q", got, tt.wantHdr)
			}
			if res.StatusCode != tt.wantCode {
				t.Errorf("status = %d, want %d", res.StatusCode, tt.wantCode)
			}
			if hit {
				if w.Body.Len() != 0 {
					t.Errorf("304 has a body: %q", w.Body.String())
				}
				if ct := res.Header.Get("Content-Type"); ct != "" {
					t.Errorf("304 kept Content-Type %q", ct)
				}
			}
		})
	}
}

// TestCheckPTagRoundTrip follows the hx-ptag client across three polls:
// the client echoes whatever HX-PTag the previous response carried.
func TestCheckPTagRoundTrip(t *testing.T) {
	content := []byte("<svg>v1</svg>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if CheckPTag(w, r, Tag(content)) {
			return
		}
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	var stored string // what hx-ptag keeps on the element
	poll := func() int {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("HX-Request", "true")
		if stored != "" {
			req.Header.Set("HX-PTag", stored)
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if v := res.Header.Get("HX-PTag"); v != "" {
			stored = v
		}
		return res.StatusCode
	}

	if code := poll(); code != http.StatusOK {
		t.Fatalf("first poll: status %d, want 200", code)
	}
	if code := poll(); code != http.StatusNotModified {
		t.Fatalf("unchanged poll: status %d, want 304", code)
	}
	content = []byte("<svg>v2</svg>")
	if code := poll(); code != http.StatusOK {
		t.Fatalf("changed poll: status %d, want 200", code)
	}
	if stored != Tag(content) {
		t.Fatalf("stored tag %s, want %s", stored, Tag(content))
	}
}

func TestPartial(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		swap    string
		content string
		want    string
	}{
		{
			"basic", "#kpi", "innerMorph", "<b>42</b>",
			`<hx-partial hx-target="#kpi" hx-swap="innerMorph"><b>42</b></hx-partial>`,
		},
		{
			"default swap", "#kpi", "", "x",
			`<hx-partial hx-target="#kpi">x</hx-partial>`,
		},
		{
			"no target", "", "outerMorph", "x",
			`<hx-partial hx-swap="outerMorph">x</hx-partial>`,
		},
		{
			"empty content", "#a", "innerHTML", "",
			`<hx-partial hx-target="#a" hx-swap="innerHTML"></hx-partial>`,
		},
		{
			"escaped attributes",
			`#a" onclick="alert(1)`, `innerHTML'><script>`, "ok",
			`<hx-partial hx-target="#a&#34; onclick=&#34;alert(1)" hx-swap="innerHTML&#39;&gt;&lt;script&gt;">ok</hx-partial>`,
		},
		{
			"ampersand", `[data-x="a&b"]`, "innerMorph", "",
			`<hx-partial hx-target="[data-x=&#34;a&amp;b&#34;]" hx-swap="innerMorph"></hx-partial>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(Partial(tt.target, tt.swap, []byte(tt.content)))
			if got != tt.want {
				t.Errorf("Partial =\n  %s\nwant\n  %s", got, tt.want)
			}
		})
	}
}

// TestPartialWellFormed checks that hostile attribute values cannot break
// out of their attributes: the result parses as one element whose
// attributes decode back to the inputs.
func TestPartialWellFormed(t *testing.T) {
	targets := []string{`#a`, `"><script>alert(1)</script>`, `a'b"c<d>e&f`, `#a > .b ~ [x='y']`}
	for _, target := range targets {
		b := Partial(target, `innerMorph "x"`, []byte("<p>hi</p>"))
		var el struct {
			XMLName xml.Name
			Target  string `xml:"hx-target,attr"`
			Swap    string `xml:"hx-swap,attr"`
			Inner   string `xml:",innerxml"`
		}
		if err := xml.Unmarshal(b, &el); err != nil {
			t.Fatalf("Partial(%q) is not well-formed: %v\n%s", target, err, b)
		}
		if el.XMLName.Local != "hx-partial" || el.Target != target || el.Swap != `innerMorph "x"` || el.Inner != "<p>hi</p>" {
			t.Errorf("Partial(%q) round trip = %+v", target, el)
		}
	}
}

func TestSize(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantW, wantH float64
	}{
		{"missing", "", 640, 320},
		{"both", "hp-w=800&hp-h=400", 800, 400},
		{"width only", "hp-w=800", 800, 320},
		{"height only", "hp-h=200", 640, 200},
		{"fractional", "hp-w=800.5&hp-h=99.25", 800.5, 99.25},
		{"padded", "hp-w=%20700%20", 700, 320},
		{"clamped low", "hp-w=10&hp-h=49.9", MinSize, MinSize},
		{"clamped high", "hp-w=100000&hp-h=4097", MaxSize, MaxSize},
		{"bounds", "hp-w=50&hp-h=4096", 50, 4096},
		{"zero ignored", "hp-w=0&hp-h=0", 640, 320},
		{"negative ignored", "hp-w=-300&hp-h=-1", 640, 320},
		{"NaN ignored", "hp-w=NaN&hp-h=nan", 640, 320},
		{"Inf ignored", "hp-w=Inf&hp-h=-Inf", 640, 320},
		{"huge exponent ignored", "hp-w=1e400", 640, 320},
		{"garbage ignored", "hp-w=wide&hp-h=12px", 640, 320},
		{"empty ignored", "hp-w=&hp-h=", 640, 320},
		{"first value wins", "hp-w=300&hp-w=900", 300, 320},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/chart?"+tt.query, nil)
			w, h := Size(r, 640, 320)
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("Size(%q) = %v, %v; want %v, %v", tt.query, w, h, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestSizeDefaultsUnclamped(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/chart", nil)
	if w, h := Size(r, 0, 10000); w != 0 || h != 10000 {
		t.Errorf("defaults changed: got %v, %v; want 0, 10000", w, h)
	}
	nan := math.NaN()
	if w, _ := Size(r, nan, 0); !math.IsNaN(w) {
		t.Errorf("NaN default changed to %v", w)
	}
}

func TestSizeForm(t *testing.T) {
	t.Run("urlencoded body", func(t *testing.T) {
		form := url.Values{"hp-w": {"720"}, "hp-h": {"360"}}
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/chart", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if w, h := Size(r, 640, 320); w != 720 || h != 360 {
			t.Errorf("Size = %v, %v; want 720, 360", w, h)
		}
	})
	t.Run("form still readable", func(t *testing.T) {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/chart", strings.NewReader("hp-w=720&region=eu"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		if w, _ := Size(r, 640, 320); w != 720 {
			t.Errorf("Size w = %v, want 720", w)
		}
		if got := r.FormValue("region"); got != "eu" {
			t.Errorf("FormValue(region) after Size = %q, want eu", got)
		}
	})
	t.Run("parsed multipart form", func(t *testing.T) {
		body := "--B\r\nContent-Disposition: form-data; name=\"hp-w\"\r\n\r\n512\r\n--B--\r\n"
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/chart", strings.NewReader(body))
		r.Header.Set("Content-Type", "multipart/form-data; boundary=B")
		if err := r.ParseMultipartForm(1 << 10); err != nil {
			t.Fatal(err)
		}
		if w, h := Size(r, 640, 320); w != 512 || h != 320 {
			t.Errorf("Size = %v, %v; want 512, 320", w, h)
		}
	})
	t.Run("body wins over query", func(t *testing.T) {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/chart?hp-w=100", strings.NewReader("hp-w=200"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if w, _ := Size(r, 640, 320); w != 200 {
			t.Errorf("Size w = %v, want 200", w)
		}
	})
	t.Run("GET leaves the form unparsed", func(t *testing.T) {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/chart?hp-w=300", nil)
		if w, _ := Size(r, 640, 320); w != 300 {
			t.Errorf("Size w = %v, want 300", w)
		}
		if r.Form != nil {
			t.Error("Size parsed the form of a GET request")
		}
	})
}

// countingBody records how many bytes were read from a request body and
// whether it was closed.
type countingBody struct {
	r      io.Reader
	n      int
	closed bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.n += n
	return n, err
}

func (b *countingBody) Close() error {
	b.closed = true
	return nil
}

// Size runs on every chart request, so it must not make the server read,
// buffer or spool a body it doesn't need: a multipart upload, or an
// urlencoded body larger than maxSizeBody. Such a body is left for the
// handler, intact, and the query string still applies.
func TestSizeLeavesLargeBodies(t *testing.T) {
	junk := strings.Repeat("x", 1<<20)
	multipart := "--B\r\nContent-Disposition: form-data; name=\"hp-w\"\r\n\r\n512\r\n" +
		"--B\r\nContent-Disposition: form-data; name=\"junk\"; filename=\"junk\"\r\n\r\n" + junk + "\r\n--B--\r\n"
	big := "hp-w=512&junk=" + junk
	tests := []struct {
		name, ctype, body string
		length            int64 // ContentLength; -1 when unknown
		maxRead           int   // bytes Size may read
	}{
		{"multipart", "multipart/form-data; boundary=B", multipart, int64(len(multipart)), 0},
		{"multipart, unknown length", "multipart/form-data; boundary=B", multipart, -1, 0},
		{"large urlencoded", "application/x-www-form-urlencoded", big, int64(len(big)), 0},
		{"large urlencoded, unknown length", "application/x-www-form-urlencoded", big, -1, maxSizeBody + 1},
		{"JSON", "application/json", `{"hp-w":512}`, 12, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &countingBody{r: strings.NewReader(tt.body)}
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/chart?hp-w=300", nil)
			r.Body = src
			r.ContentLength = tt.length
			r.Header.Set("Content-Type", tt.ctype)

			if w, h := Size(r, 640, 320); w != 300 || h != 320 {
				t.Errorf("Size = %v, %v; want the query's 300, 320", w, h)
			}
			if src.n > tt.maxRead {
				t.Errorf("Size read %d body bytes, want at most %d", src.n, tt.maxRead)
			}
			if r.MultipartForm != nil {
				t.Error("Size parsed a multipart body")
			}
			rest, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(rest) != tt.body {
				t.Errorf("handler reads %d body bytes after Size, want the whole %d", len(rest), len(tt.body))
			}
			if err := r.Body.Close(); err != nil || !src.closed {
				t.Errorf("closing the body after Size: err %v, original closed %v", err, src.closed)
			}
		})
	}
}

// A body that fails mid-read is handed back to the handler with the
// bytes Size already read, followed by the same error.
func TestSizeBodyReadError(t *testing.T) {
	errBoom := errors.New("boom")
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/chart?hp-w=300", nil)
	r.Body = io.NopCloser(io.MultiReader(strings.NewReader("hp-w=512"), iotest.ErrReader(errBoom)))
	r.ContentLength = -1
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w, _ := Size(r, 640, 320); w != 300 {
		t.Errorf("Size w = %v, want the query's 300", w)
	}
	got, err := io.ReadAll(r.Body)
	if string(got) != "hp-w=512" || !errors.Is(err, errBoom) {
		t.Errorf("handler reads %q, %v; want %q, %v", got, err, "hp-w=512", errBoom)
	}
}

// The largest body Size parses itself is maxSizeBody bytes.
func TestSizeBodyLimit(t *testing.T) {
	for _, n := range []int{maxSizeBody, maxSizeBody + 1} {
		body := "hp-w=512&pad=" + strings.Repeat("x", n-len("hp-w=512&pad="))
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/chart", strings.NewReader(body))
		r.ContentLength = -1
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		want := 512.0
		if n > maxSizeBody {
			want = 640
		}
		if w, _ := Size(r, 640, 320); w != want {
			t.Errorf("%d-byte body: Size w = %v, want %v", n, w, want)
		}
	}
}
