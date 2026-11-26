package hyperplot

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"testing"
)

// parsed is what checkSVG learns about a rendered chart.
type parsed struct {
	ids     map[string]bool
	classes map[string]int // element count per class token
	tips    map[string]any // decoded <metadata class="hp-tips"> JSON
	texts   []string       // character data of <text> elements
}

// checkSVG asserts that b is well-formed XML with unique ids and, when
// present, a valid JSON tooltip payload. It returns what it found.
func checkSVG(tb testing.TB, b []byte) parsed {
	tb.Helper()
	p, err := parseSVG(b)
	if err != nil {
		tb.Fatalf("%v\n%s", err, b)
	}
	return p
}

func parseSVG(b []byte) (parsed, error) {
	p := parsed{ids: map[string]bool{}, classes: map[string]int{}}
	dec := xml.NewDecoder(bytes.NewReader(b))
	var stack []string
	var meta, text bytes.Buffer
	inMeta, inText := false, false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return p, errors.New("not well-formed XML: " + err.Error())
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			stack = append(stack, tok.Name.Local)
			for _, a := range tok.Attr {
				switch a.Name.Local {
				case "id":
					if p.ids[a.Value] {
						return p, errors.New("duplicate id " + a.Value)
					}
					p.ids[a.Value] = true
				case "class":
					for _, c := range bytes.Fields([]byte(a.Value)) {
						p.classes[string(c)]++
					}
					if tok.Name.Local == "metadata" && a.Value == "hp-tips" {
						inMeta = true
					}
				case "style":
					return p, errors.New("style attribute found; output must be CSP-friendly")
				}
			}
			if tok.Name.Local == "text" {
				inText = true
				text.Reset()
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			if tok.Name.Local == "metadata" && inMeta {
				inMeta = false
				if err := json.Unmarshal(meta.Bytes(), &p.tips); err != nil {
					return p, errors.New("bad tooltip JSON: " + err.Error())
				}
			}
			if tok.Name.Local == "text" && inText {
				inText = false
				p.texts = append(p.texts, text.String())
			}
		case xml.CharData:
			if inMeta {
				meta.Write(tok)
			}
			if inText {
				text.Write(tok)
			}
		}
	}
	if len(stack) != 0 {
		return p, errors.New("unclosed elements")
	}
	return p, nil
}

// mustRender renders c or fails the test.
func mustRender(tb testing.TB, c *Chart) []byte {
	tb.Helper()
	b, err := c.Render()
	if err != nil {
		tb.Fatalf("Render: %v", err)
	}
	return b
}
