package plot

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// stdTips mirrors the tooltip payload for encoding with encoding/json.
type stdTips struct {
	Mode   string      `json:"mode"`
	Xs     []float64   `json:"xs"`
	Bw     float64     `json:"bw"`
	Top    float64     `json:"top"`
	Bottom float64     `json:"bottom"`
	Rows   []stdTipRow `json:"rows"`
}

type stdTipRow struct {
	T string  `json:"t"`
	K string  `json:"k"`
	V [][]any `json:"v"`
}

func stdRows(rows []tipRow) []stdTipRow {
	out := make([]stdTipRow, len(rows))
	for i, r := range rows {
		v := make([][]any, len(r.v))
		for j, e := range r.v {
			v[j] = []any{e.slot, e.name, e.value}
		}
		out[i] = stdTipRow{r.t, r.k, v}
	}
	return out
}

// fffdEscape is the six-byte JSON escape for U+FFFD, built from parts so the
// source holds no escape sequence an editor might turn into the character.
var fffdEscape = []byte(`\u` + "fffd")

// normFFFD spells every raw U+FFFD as its JSON escape. encoding/json writes
// the replacement for invalid UTF-8 as the escape before Go 1.27 and as the
// raw character from Go 1.27 on; both decode to the same string.
func normFFFD(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte(string(utf8.RuneError)), fffdEscape)
}

func TestTipsJSONMatchesEncodingJSON(t *testing.T) {
	rows := []tipRow{
		{"Mar 3, 2026 14:00", "2026-03-03T14:00:00Z", []tipEntry{{1, "p50", "42 ms"}, {2, `<b>"x"</b> & \ y`, missing}}},
		{"tab\tnew\nline\r\x00\x1f\b\f\xe2\x80\xa8\xe2\x80\xa9\x7f", "<k>&\x01\t\xe2\x80\xa8", []tipEntry{{8, "日本語 \U0001F600", "−1.5"}}},
		{"bad \xff utf8", "", nil},
	}
	ax := tipsAxis{mode: "x", xs: []float64{0, 1.5, 123.25, -4, 1e6}, bw: 0, top: 8, bottom: 298.5, rows: rows}
	want, err := json.Marshal(stdTips{ax.mode, ax.xs, ax.bw, ax.top, ax.bottom, stdRows(ax.rows)})
	if err != nil {
		t.Fatal(err)
	}
	if got := ax.appendJSON(nil); !bytes.Equal(normFFFD(got), normFFFD(want)) {
		t.Errorf("axis payload:\n got %s\nwant %s", got, want)
	}

	xy := tipsXY{pts: [][2]float64{{1, 2}, {3.5, 4.25}}, rows: rows[:1]}
	want, err = json.Marshal(struct {
		Mode string       `json:"mode"`
		Pts  [][2]float64 `json:"pts"`
		Rows []stdTipRow  `json:"rows"`
	}{"xy", xy.pts, stdRows(xy.rows)})
	if err != nil {
		t.Fatal(err)
	}
	if got := xy.appendJSON(nil); !bytes.Equal(normFFFD(got), normFFFD(want)) {
		t.Errorf("xy payload:\n got %s\nwant %s", got, want)
	}
	empty := tipsXY{}
	if got := string(empty.appendJSON(nil)); got != `{"mode":"xy","pts":[],"rows":[]}` {
		t.Errorf("empty xy payload = %s", got)
	}
}

func FuzzJSONString(f *testing.F) {
	for _, s := range []string{"", "plain", `<script>"&'\`, "\x00\xff\xfe", "\xe2\x80\xa8\xef\xbf\xbe\xef\xbf\xbf", "\U0001F600"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := appendJSONString(nil, s)
		var back string
		if err := json.Unmarshal(got, &back); err != nil {
			t.Fatalf("%q encodes to invalid JSON %s: %v", s, got, err)
		}
		want, _ := json.Marshal(s)
		var stdBack string
		_ = json.Unmarshal(want, &stdBack)
		if back != stdBack {
			t.Fatalf("%q decodes to %q, encoding/json gives %q", s, back, stdBack)
		}
		if !strings.ContainsAny(s, "\xef\xbf\xbe\xef\xbf\xbf") && !bytes.Equal(normFFFD(got), normFFFD(want)) {
			t.Fatalf("%q: got %s, encoding/json %s", s, got, want)
		}
		if strings.ContainsAny(string(got), "<>&\xef\xbf\xbe\xef\xbf\xbf") {
			t.Fatalf("%q: output %s holds a character unsafe in XML text", s, got)
		}
	})
}
