package docs

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// specNumberPattern returns the regular expression for the number form
// that spec.md §1.1 gives on a line of its own.
func specNumberPattern(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, "docs", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(line, "^-?(0|") && strings.HasSuffix(line, "$") {
			return line
		}
	}
	t.Fatal("spec.md §1.1 has no number pattern line")
	return ""
}

// TestSpecNumberPattern checks the §1.1 pattern against Go's encoding/json,
// which writes floats exactly in the number form, and against the forms
// the spec names as mistakes.
func TestSpecNumberPattern(t *testing.T) {
	re := regexp.MustCompile(specNumberPattern(t))
	for _, v := range []float64{
		0, 1, -1, 2.5, 0.001, 24, 98, 228, 1e21, 1e-7, -1e-7, 1.5e300, 5e-324,
		1772546400000, 1772546400000.5, 123456789.125, 0.1, -0.5, math.MaxFloat64,
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if !re.Match(b) {
			t.Errorf("%s (encoding/json of %v) does not match the number pattern", b, v)
		}
	}
	for _, s := range []string{
		"2.0", "24.0", "1772546400000.0", "0.50", "024", "00", "-01", "..", "e+e+",
		"1E+21", "1e21", "1e+021", "1e-07", ".5", "5.", "+1", "-", "", " 1",
		"NaN", "Infinity", "1_0", "0x10",
	} {
		if re.MatchString(s) {
			t.Errorf("%q matches the number pattern, but is not the number form", s)
		}
	}
}

// geometryAttr finds a root geometry attribute's value in a reference chart.
var geometryAttr = regexp.MustCompile(`\b(data-hp-plot|data-hp-x|data-hp-y)="([^"]*)"`)

// TestConformanceGeometryNumberForm checks that the fixtures' `match`
// assertions on the geometry attributes are built from the spec's number
// pattern, accept the reference output, and reject a server that writes
// the numbers in a default float format or as junk. Looser patterns let
// such output pass every server assertion although spec §1.1 forbids it.
func TestConformanceGeometryNumberForm(t *testing.T) {
	num := strings.TrimSuffix(strings.TrimPrefix(specNumberPattern(t), "^"), "$")
	two := num + " " + num
	want := map[string][]string{
		"data-hp-plot": {"^" + two + " " + two + "$"},
		"data-hp-x":    {"^time " + two + "$", "^linear " + two + "$"},
		"data-hp-y":    {"^linear " + two + "$"},
	}
	paths, err := filepath.Glob(filepath.Join(root, "testdata", "conformance", "*", "checks.json"))
	if err != nil {
		t.Fatal(err)
	}
	checked := map[string]int{}
	for _, path := range paths {
		fixture := filepath.Base(filepath.Dir(path))
		var checks struct {
			Server []struct {
				Sel   string            `json:"sel"`
				Match map[string]string `json:"match"`
			} `json:"server"`
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &checks); err != nil {
			t.Fatalf("%s: %v", fixture, err)
		}
		chart, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "chart.svg"))
		ref := map[string]string{}
		for _, m := range geometryAttr.FindAllStringSubmatch(string(chart), -1) {
			if _, ok := ref[m[1]]; !ok {
				ref[m[1]] = m[2]
			}
		}
		for _, a := range checks.Server {
			for attr, pat := range a.Match {
				forms, ok := want[attr]
				if !ok {
					continue
				}
				checked[attr]++
				ok = false
				for _, f := range forms {
					ok = ok || pat == f
				}
				if !ok {
					t.Errorf("%s: match %s = %q is not built from the number pattern of spec §1.1", fixture, attr, pat)
					continue
				}
				re := regexp.MustCompile(pat)
				v, found := ref[attr]
				if !found {
					t.Errorf("%s: chart.svg has no %s", fixture, attr)
					continue
				}
				if !re.MatchString(v) {
					t.Errorf("%s: the reference %s=%q fails its own match", fixture, attr, v)
				}
				for _, bad := range badGeometry(v) {
					if re.MatchString(bad) {
						t.Errorf("%s: match %s accepts %q, which is not in the number form", fixture, attr, bad)
					}
				}
			}
		}
	}
	for attr := range want {
		if checked[attr] < 2 {
			t.Errorf("only %d fixture(s) check %s with match; want the line-time, bars-band and scatter-xy ones", checked[attr], attr)
		}
	}
}

// badGeometry returns variants of a geometry attribute value with one
// number at a time written in a form spec §1.1 forbids.
func badGeometry(v string) []string {
	fields := strings.Fields(v)
	var out []string
	for i, f := range fields {
		if f == "time" || f == "linear" {
			continue
		}
		forms := []string{"..", "e+e+", "-", "0" + strings.TrimPrefix(f, "-")}
		switch {
		case strings.Contains(f, "e"): // 1e+21 + "0" is still canonical
		case strings.Contains(f, "."):
			forms = append(forms, f+"0") // a trailing zero in a fraction
		default:
			forms = append(forms, f+".0") // Python's str(24.0)
		}
		for _, bad := range forms {
			g := append([]string(nil), fields...)
			g[i] = bad
			out = append(out, strings.Join(g, " "))
		}
	}
	return out
}

// pythonCommand returns a Python 3.8+ interpreter, or "" when there is
// none. On Windows, python3 may be the Microsoft Store stub, which exits
// with an error, so each candidate is run once.
func pythonCommand(ctx context.Context) string {
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, path, "-c", "import sys; sys.exit(0 if sys.version_info >= (3, 8) else 1)")
		if cmd.Run() == nil {
			return path
		}
	}
	return ""
}

// pythonBlocks returns the ```python code blocks of a Markdown file whose
// first line contains marker ("" for all).
func pythonBlocks(t *testing.T, file, marker string) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		t.Fatal(err)
	}
	var blocks []string
	var cur []string
	in := false
	for _, line := range strings.Split(string(src), "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case !in && trim == "```python":
			in, cur = true, nil
		case in && strings.HasPrefix(trim, "```"):
			in = false
			if len(cur) > 0 && strings.Contains(cur[0], marker) {
				blocks = append(blocks, strings.Join(cur, "\n")+"\n")
			}
		case in:
			cur = append(cur, line)
		}
	}
	return blocks
}

// pythonCheck runs against the spec's Appendix B helpers (hp_wire.py) and
// the guide's server (server.py). It prints one line per failure.
const pythonCheck = `import json
import threading
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timedelta, timezone
from http.server import ThreadingHTTPServer

import hp_wire
import server

fails = []


def check(cond, msg):
    if not cond:
        fails.append(msg)


FFFD = chr(0xFFFD)
LONE = 'p' + chr(0xD800) + 'x'  # json.loads of an escaped lone surrogate gives this
ESC = b'p\xffx'.decode('utf-8', 'surrogateescape')  # an OS string with a bad byte

# Spec 3.5, 3.6, Appendix A: dropped in XML, U+FFFD in the JSON and in key input.
check(hp_wire.xml_attr(LONE) == 'px', 'xml_attr(LONE) = %s' % ascii(hp_wire.xml_attr(LONE)))
for name in (LONE, ESC):
    fixed = name[0] + FFFD + name[2]
    try:
        s = hp_wire.tips_json({'mode': 'x', 'rows': [{'t': '', 'k': name, 'v': [[1, name, '1']]}]})
        s.encode()
        row = json.loads(s)['rows'][0]
        check(row['k'] == fixed and row['v'][0][1] == fixed, 'tips_json: %s' % ascii(s))
    except Exception as e:
        check(False, 'tips_json(%s) raised %r' % (ascii(name), e))
    try:
        check(server.fnv1a(name) == server.fnv1a(fixed), 'fnv1a(%s) is not the hash of its U+FFFD form' % ascii(name))
    except Exception as e:
        check(False, 'fnv1a(%s) raised %r' % (ascii(name), e))
check(server.fnv1a('p50') == '2e119f3a', 'fnv1a(p50)')
check(server.keys(['p50', 'p50']) == ['2e119f3a', '4e3e42de'], 'keys(p50, p50)')

now = datetime.now(timezone.utc)
try:
    svg, table = server.chart('c', 'T', [LONE, ESC], server.load(now - timedelta(minutes=5), now),
                              now - timedelta(minutes=5), now, 640)
    (svg + table).encode()
except Exception as e:
    check(False, 'a chart with lone surrogates in its series names raised %r' % e)

# Ranges are bounded before they reach datetime code: the routes answer, never drop.
httpd = ThreadingHTTPServer(('127.0.0.1', 0), server.Handler)
threading.Thread(target=httpd.serve_forever, daemon=True).start()
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def status(path):
    try:
        with opener.open('http://127.0.0.1:%d%s' % (httpd.server_address[1], path), timeout=20) as r:
            r.read()
            return r.status
    except urllib.error.HTTPError as e:
        return e.code
    except Exception as e:
        return repr(e)


recent = lambda m: hp_wire.time_value(now - timedelta(minutes=m))
for path, want in [
    ('/chart', 200),
    ('/chart?hp-x0=0001-01-01T00:00:00Z&hp-x1=0001-01-01T01:00:00Z', 204),
    ('/chart?hp-x0=9999-12-31T22:00:00Z&hp-x1=9999-12-31T23:59:59Z', 204),
    ('/chart?from=1960-01-01T00:00:00Z&to=1960-01-01T01:00:00Z', 204),
    ('/chart?from=9999-12-31T22:00:00Z&to=9999-12-31T23:00:00Z', 204),
    ('/?from=9999-12-31T22:00:00Z&to=9999-12-31T23:59:59Z', 200),
    ('/?from=0001-01-01T00:00:00Z&to=0001-01-01T01:00:00Z', 200),
    ('/chart?hp-x0=%s&hp-x1=%s' % (recent(60), recent(30)), 200),
    ('/?from=%s&to=%s' % (recent(60), recent(30)), 200),
    # A select key is bounded too: year 1 and 9999 are multiples of STEP.
    ('/detail?hp-x=0001-01-01T00%3A00%3A00Z', 204),
    ('/detail?hp-x=9999-12-31T23%3A59%3A50Z', 204),
    ('/detail?hp-x=' + hp_wire.time_value(datetime.fromtimestamp(int(now.timestamp()) // 10 * 10 - 60, timezone.utc)), 200),
]:
    got = status(path)
    check(got == want, 'GET %s: %s, want %d' % (path, got, want))
httpd.shutdown()

# The server cases of the shared fixture, read with parse_number and
# parse_time_value: the helpers the guide tells servers to copy must pass
# the fixture every server is held to.
with open('params.json', encoding='utf-8') as f:
    params = json.load(f)
for case in params['server']:
    b = case.get('brush')
    if not b or not b['ok']:
        continue
    q = urllib.parse.parse_qs(case['query'], keep_blank_values=True)
    x0, x1 = q['hp-x0'][0], q['hp-x1'][0]
    where = 'params.json %s' % case['query']
    check((x0, x1) == (b['x0'], b['x1']), '%s: raw values %r' % (where, (x0, x1)))

    def run(fn):
        try:
            return sorted([fn(x0), fn(x1)])
        except ValueError:
            return 'error'

    if 'floats' in b:
        got = run(hp_wire.parse_number)
        check(got == b['floats'], '%s: floats %r, want %r' % (where, got, b['floats']))
    if 'times' in b:
        got = run(hp_wire.parse_time_value)
        if got != 'error':
            got = [hp_wire.time_value(t) for t in got]
        check(got == b['times'], '%s: times %r, want %r' % (where, got, b['times']))
    if 'timesIn' in b:
        try:  # Python 3.9+, and Windows needs the tzdata package
            from zoneinfo import ZoneInfo
            zone = ZoneInfo(b['timesIn']['zone'])
        except Exception:
            continue
        got = run(lambda s: hp_wire.parse_time_value(s, zone))
        if got != 'error':
            got = [hp_wire.time_value(t) for t in got]
        check(got == b['timesIn']['times'], '%s: timesIn %r' % (where, got))

for f in fails:
    print('FAIL', f)
raise SystemExit(1 if fails else 0)
`

// TestPythonHelpers runs the Python in spec.md Appendix B and in
// implementing-a-server.md, which the guide says to copy: lone surrogates
// in a series name must not make them raise (spec §3.5); a brush or
// bookmark range, or a select key, that parses but lies outside the data
// (year 1, 9999) must get a 204 rather than a request that ends with no
// response; and the parsers must pass the server brush cases of
// testdata/conformance/params.json. It needs Python 3.8 or later and is
// skipped without one.
func TestPythonHelpers(t *testing.T) {
	if testing.Short() {
		t.Skip("runs Python")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	py := pythonCommand(ctx)
	if py == "" {
		t.Skip("no Python 3.8+ on PATH")
	}
	wire := pythonBlocks(t, "docs/spec.md", "")
	if len(wire) != 1 {
		t.Fatalf("spec.md has %d python blocks, want 1 (Appendix B)", len(wire))
	}
	parts := pythonBlocks(t, "docs/implementing-a-server.md", "# server.py, part")
	if len(parts) != 5 {
		t.Fatalf("implementing-a-server.md has %d server.py parts, want 5", len(parts))
	}
	params, err := os.ReadFile(filepath.Join(root, "testdata", "conformance", "params.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, src := range map[string]string{
		"hp_wire.py":  wire[0],
		"server.py":   strings.Join(parts, "\n"),
		"check.py":    pythonCheck,
		"params.json": string(params),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(ctx, py, "check.py")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONIOENCODING=utf-8")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
