package docs

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// dashboardServer holds the strict policy every example copies for every
// HTML response it sends (examples/README.md, "What every example has in
// common").
const dashboardServer = "examples/dashboard/server.go"

var (
	// policyDecl is where a source file declares a policy: Go's
	// `const contentSecurityPolicy = ...` (or the gallery's exportPolicy),
	// Python's `CSP = (...)` and Node's `export const CSP = ...`. The value
	// is the string literals that follow, joined (see policyValue).
	policyDecl = regexp.MustCompile(`(?m)^(?:export )?(?:const )?(contentSecurityPolicy|exportPolicy|CSP) = `)
	// policyUse is where a source file sends a policy: the value after a
	// quoted "Content-Security-Policy" header name and a comma or colon.
	// It must be one of the file's declared names, not a literal.
	policyUse = regexp.MustCompile(`["']Content-Security-Policy["']\s*[,:]\s*([^\s,)}]+)`)
	// policyMeta is a policy written straight into a <meta> tag
	// (standalone-svg's index.html); a template's content="{{.}}" is filled
	// from a declared policy instead.
	policyMeta = regexp.MustCompile(`<meta http-equiv="Content-Security-Policy" content="([^"{]*)">`)
	// htmxSource is the one script host a policy may name: the pinned htmx
	// release that TestExamplesPinHtmx pins the tags of.
	htmxSource = regexp.MustCompile(`^https://cdn\.jsdelivr\.net/npm/htmx\.org@\d+\.\d+\.\d+/dist/$`)
)

// policyValue returns the string literals that follow a declaration at
// src[i:], joined: Go and Node concatenate them with +, Python juxtaposes
// them inside parentheses. It stops at the first token that is not a
// literal, a +, a parenthesis or white space.
func policyValue(src string, i int) string {
	var b strings.Builder
	for i < len(src) {
		switch c := src[i]; c {
		case ' ', '\t', '\n', '\r', '+', '(':
			i++
		case '"', '\'', '`':
			end := strings.IndexByte(src[i+1:], c)
			if end < 0 {
				return b.String()
			}
			b.WriteString(src[i+1 : i+1+end])
			i += end + 2
		default:
			return b.String()
		}
	}
	return b.String()
}

// policies returns the policies src declares, by name, and the problems
// with how src sends them: a header set from a literal or from a name src
// does not declare.
func policies(src string) (decls map[string]string, uses int, problems []string) {
	decls = map[string]string{}
	for _, m := range policyDecl.FindAllStringSubmatchIndex(src, -1) {
		decls[src[m[2]:m[3]]] = policyValue(src, m[1])
	}
	for _, m := range policyUse.FindAllStringSubmatch(src, -1) {
		uses++
		if _, ok := decls[m[1]]; !ok {
			problems = append(problems, fmt.Sprintf("sends the policy %s, which is not a declared policy constant (contentSecurityPolicy, CSP)", m[1]))
		}
	}
	return decls, uses, problems
}

// policyProblems returns what is wrong with policy, directive by
// directive: every source must be 'self' or 'none', apart from data: in
// img-src and the pinned htmx release in script-src. That rules out
// 'unsafe-inline', 'unsafe-eval', nonces and hashes, wildcards, bare
// schemes such as https:, and any other host. default-src is required,
// and base-uri 'none' wherever scripts may run.
func policyProblems(policy string) []string {
	var problems []string
	seen := map[string]bool{}
	scripts := []string{"'none'"} // script-src, or default-src without one
	for _, d := range strings.Split(policy, ";") {
		fields := strings.Fields(d)
		if len(fields) == 0 {
			continue
		}
		name, sources := fields[0], fields[1:]
		if seen[name] {
			problems = append(problems, name+" appears twice")
		}
		seen[name] = true
		if name == "script-src" || (name == "default-src" && !seen["script-src"]) {
			scripts = sources
		}
		if len(sources) == 0 {
			problems = append(problems, name+" has no sources")
		}
		for _, s := range sources {
			switch {
			case s == "'self'" || s == "'none'":
			case s == "data:" && name == "img-src":
			case htmxSource.MatchString(s) && name == "script-src":
			default:
				problems = append(problems, fmt.Sprintf("%s allows %s", name, s))
			}
		}
		if slices.Contains(sources, "'none'") && len(sources) > 1 {
			problems = append(problems, name+" mixes 'none' with other sources")
		}
	}
	if !seen["default-src"] {
		problems = append(problems, "has no default-src")
	}
	// A <base> tag would move every relative script URL to another host,
	// so a policy that lets scripts run must forbid it. (base-uri does
	// not fall back to default-src.)
	if !slices.Equal(scripts, []string{"'none'"}) && !strings.Contains(policy, "base-uri 'none'") {
		problems = append(problems, "allows scripts but does not set base-uri 'none'")
	}
	return problems
}

// withScriptSrc returns policy with its script-src sources replaced.
func withScriptSrc(t *testing.T, policy, sources string) string {
	t.Helper()
	re := regexp.MustCompile(`script-src [^;]*`)
	if !re.MatchString(policy) {
		t.Fatalf("%s: the policy has no script-src: %q", dashboardServer, policy)
	}
	return re.ReplaceAllLiteralString(policy, "script-src "+sources)
}

// TestExamplesPinCSP checks the Content-Security-Policy of every example
// against the dashboard's, the way TestExamplesPinHtmx checks the htmx
// tags. Each example's own tests compare the header with the example's
// own constant, so a weakened constant would pass them; this test reads
// the constants. A policy sent as a header must be exactly the
// dashboard's, or the dashboard's with script-src 'self' (a page that
// loads hx-plot.js and no htmx) or script-src 'none' (a page with no
// script at all). A policy in a <meta> tag (a static export) may differ,
// since a <meta> cannot carry frame-ancestors, but must be as strict
// (policyProblems).
func TestExamplesPinCSP(t *testing.T) {
	decls, _, _ := policies(readRepoFile(t, dashboardServer))
	ref := decls["contentSecurityPolicy"]
	if ref == "" {
		t.Fatalf("%s: no contentSecurityPolicy constant", dashboardServer)
	}
	if p := policyProblems(ref); len(p) > 0 {
		t.Fatalf("%s: the reference policy itself: %s", dashboardServer, strings.Join(p, "; "))
	}
	allowed := []string{ref, withScriptSrc(t, ref, "'self'"), withScriptSrc(t, ref, "'none'")}

	sent, declared, metas := 0, 0, 0
	walkExamples(t, func(rel string, src []byte) {
		if strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(rel[strings.LastIndex(rel, "/")+1:], "test_") || strings.HasSuffix(rel, ".test.mjs") {
			return // tests may hold weak policies on purpose
		}
		decls, uses, problems := policies(string(src))
		sent += uses
		for _, p := range problems {
			t.Errorf("%s: %s", rel, p)
		}
		for name, policy := range decls {
			declared++
			for _, p := range policyProblems(policy) {
				t.Errorf("%s: %s %s", rel, name, p)
			}
			if name != "exportPolicy" && !slices.Contains(allowed, policy) {
				t.Errorf("%s: %s is\n\t%q\nwant the dashboard's (%s), or it with script-src 'self' or 'none':\n\t%q", rel, name, policy, dashboardServer, ref)
			}
		}
		for _, m := range policyMeta.FindAllStringSubmatch(string(src), -1) {
			metas++
			for _, p := range policyProblems(m[1]) {
				t.Errorf("%s: <meta> policy %s", rel, p)
			}
		}
	})
	// 15 servers send the policy (some in several places); the gallery
	// and standalone-svg also write one into a <meta>.
	if sent < 15 || declared < 16 || metas < 1 {
		t.Fatalf("found %d policy headers, %d declared policies and %d <meta> policies; are the patterns out of date?", sent, declared, metas)
	}
}

// TestPolicyChecks checks that the CSP checks reject the ways a policy
// can be weakened or sent around the constant.
func TestPolicyChecks(t *testing.T) {
	const strict = "default-src 'self'; script-src 'self' https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/; " +
		"style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	for _, ok := range []string{strict, "default-src 'none'; img-src 'self'; style-src 'self'"} {
		if p := policyProblems(ok); len(p) > 0 {
			t.Errorf("policyProblems(%q) = %v, want none", ok, p)
		}
	}
	for _, weak := range []string{
		strings.Replace(strict, "script-src 'self'", "script-src 'self' 'unsafe-inline'", 1),
		strings.Replace(strict, "script-src 'self'", "script-src 'self' 'unsafe-eval'", 1),
		strings.Replace(strict, "script-src 'self'", "script-src 'self' https:", 1),
		strings.Replace(strict, "script-src 'self'", "script-src *", 1),
		strings.Replace(strict, "script-src 'self'", "script-src 'self' 'nonce-abc'", 1),
		strings.Replace(strict, "htmx.org@4.0.2/dist/", "", 1),
		strings.Replace(strict, "style-src 'self'", "style-src 'self' 'unsafe-inline'", 1),
		strings.Replace(strict, "connect-src 'self'", "connect-src *", 1),
		strings.Replace(strict, "img-src 'self' data:", "img-src 'self' data: blob:", 1),
		strings.Replace(strict, "default-src 'self'", "default-src https:", 1),
		strings.Replace(strict, "default-src 'self'; ", "", 1),
		strings.Replace(strict, "base-uri 'none'", "base-uri 'self' https:", 1),
		strings.Replace(strict, "base-uri 'none'; ", "", 1),
		"default-src 'self'; img-src 'self'", // scripts from 'self', no base-uri
		strings.Replace(strict, "style-src 'self'", "style-src 'self' data:", 1),
	} {
		if len(policyProblems(weak)) == 0 {
			t.Errorf("policyProblems(%q) found nothing", weak)
		}
	}

	// Each language's way of declaring and sending the policy.
	for _, tc := range []struct {
		name, src string
		want      string // the declared policy
		problems  int
	}{
		{"go", "const contentSecurityPolicy = \"default-src 'self'; \" +\n\t\"base-uri 'none'\"\n\nfunc f() { h.Set(\"Content-Security-Policy\", contentSecurityPolicy) }", "default-src 'self'; base-uri 'none'", 0},
		{"python", "CSP = (\"default-src 'self'; \"\n       \"base-uri 'none'\")\n\nself.send_header('Content-Security-Policy', CSP)", "default-src 'self'; base-uri 'none'", 0},
		{"node", "export const CSP = \"default-src 'self'; \" +\n  \"base-uri 'none'\"\nres.writeHead(200, { 'Content-Security-Policy': CSP })", "default-src 'self'; base-uri 'none'", 0},
		{"go literal", "const contentSecurityPolicy = \"default-src 'self'\"\nh.Set(\"Content-Security-Policy\", \"default-src *\")", "default-src 'self'", 1},
		{"python other name", "CSP = \"default-src 'self'\"\nself.send_header('Content-Security-Policy', LOOSE)", "default-src 'self'", 1},
	} {
		decls, uses, problems := policies(tc.src)
		var got string
		for _, v := range decls {
			got = v
		}
		if got != tc.want || uses == 0 || len(problems) != tc.problems {
			t.Errorf("%s: policies() = %q, %d uses, problems %v; want %q and %d problems", tc.name, got, uses, problems, tc.want, tc.problems)
		}
	}
}
