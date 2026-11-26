package svg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNum(t *testing.T) {
	tests := []struct {
		v    float64
		want string
	}{
		{0, "0"},
		{math.Copysign(0, -1), "0"},
		{-0.001, "0"},
		{1.5, "1.5"},
		{2, "2"},
		{1.006, "1.01"},
		{-12.345, "-12.35"},
		{100, "100"},
		{0.1 + 0.2, "0.3"},
		{math.NaN(), "0"},
		{math.Inf(1), "0"},
	}
	for _, tt := range tests {
		if got := Num(tt.v); got != tt.want {
			t.Errorf("Num(%v) = %q, want %q", tt.v, got, tt.want)
		}
	}
}

// TestNumMatchesFormatFloat checks the integer fast path of AppendNum
// against the strconv formulation it replaces.
func TestNumMatchesFormatFloat(t *testing.T) {
	ref := func(v float64) string {
		v = math.Round(v*100) / 100
		if v == 0 {
			return "0"
		}
		s := strconv.FormatFloat(v, 'f', 2, 64)
		s = strings.TrimRight(s, "0")
		return strings.TrimSuffix(s, ".")
	}
	vals := []float64{0.005, -0.005, 0.015, 1.005, 2.675, 123.456, -98.7, 0.1, 0.2, 0.3, 1e6 + 0.01, 4503599627370.49, -4503599627370.49, 1e14, 1e20, -3e30}
	for i := range 2000 {
		vals = append(vals, float64(i)*0.37-300, math.Sqrt(float64(i))*17.123)
	}
	for _, v := range vals {
		if got, want := Num(v), ref(v); got != want {
			t.Errorf("Num(%v) = %q, want %q", v, got, want)
		}
	}
}

func FuzzNum(f *testing.F) {
	f.Add(1.005)
	f.Add(-123456.789)
	f.Fuzz(func(t *testing.T, v float64) {
		s := Num(v)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			if s != "0" {
				t.Fatalf("Num(%v) = %q", v, s)
			}
			return
		}
		back, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("Num(%v) = %q does not parse: %v", v, s, err)
		}
		if math.Abs(back-v) > 0.005+1e-9*math.Abs(v) {
			t.Fatalf("Num(%v) = %q is off by %v", v, s, back-v)
		}
	})
}

func TestWriterEscapes(t *testing.T) {
	var w Writer
	w.Open("svg", A("xmlns", "http://www.w3.org/2000/svg"), A("data-x", `a"b<c>&d`+"\n"), A("empty", ""))
	w.Elem("text", "1 < 2 & \"ok\"\x00\x1b", N("x", 1.234))
	w.Close("svg")
	want := `<svg xmlns="http://www.w3.org/2000/svg" data-x="a&quot;b&lt;c&gt;&amp;d&#10;"><text x="1.23">1 &lt; 2 &amp; "ok"</text></svg>`
	if got := w.String(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestPath(t *testing.T) {
	var p Path
	p.M(0, 0).L(10.5, 20).H(30).V(0).Z()
	if got, want := p.String(), "M0,0L10.5,20H30V0Z"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPathArc(t *testing.T) {
	tests := []struct {
		name string
		path func(p *Path)
		want string
	}{
		{"sweep", func(p *Path) { p.M(0, 10).A(4, 4, 0, false, true, 4, 6) }, "M0,10A4,4,0,0,1,4,6"},
		{"large arc", func(p *Path) { p.A(2.5, 3, 30, true, false, -1.234, 0) }, "A2.5,3,30,1,0,-1.23,0"},
		{"zero radius", func(p *Path) { p.A(0, 0, 0, false, false, 1, 2) }, "A0,0,0,0,0,1,2"},
		{"non-finite", func(p *Path) { p.A(math.NaN(), 1, 0, true, true, math.Inf(1), 2) }, "A0,1,0,1,1,0,2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p Path
			tt.path(&p)
			if got := p.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFlag(t *testing.T) {
	var w Writer
	w.Void("svg", Flag("data-hp"), A("empty", ""), A("id", "x"))
	if got, want := w.String(), `<svg data-hp="" id="x"/>`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// FuzzWellFormed checks that arbitrary text and attribute values always
// produce markup that an XML parser accepts and round-trips.
func FuzzWellFormed(f *testing.F) {
	for _, s := range []string{"", "plain", `<script>"&'`, "\x00\xff\xfe", "line\nbreak\r\ttab", "\U0001F600 emoji"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var w Writer
		w.Open("svg", A("data-v", s))
		w.Text(s)
		w.Close("svg")
		dec := xml.NewDecoder(bytes.NewReader(w.Bytes()))
		var attr, text string
		for {
			tok, err := dec.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("not well-formed for %q: %v\n%s", s, err, w.Bytes())
			}
			switch tok := tok.(type) {
			case xml.StartElement:
				for _, a := range tok.Attr {
					attr = a.Value
				}
			case xml.CharData:
				text += string(tok)
			}
		}
		// Valid, XML-legal input must round-trip exactly.
		if utf8.ValidString(s) && allXMLChars(s) {
			if attr != s || text != s {
				t.Errorf("round trip of %q: attr %q, text %q", s, attr, text)
			}
		}
	})
}

func allXMLChars(s string) bool {
	for _, r := range s {
		if !xmlChar(r) || r == '\r' {
			return false // XML normalizes \r in text, so exclude it
		}
	}
	return true
}
