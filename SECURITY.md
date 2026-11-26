# Security policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public issues,
discussions or pull requests.**

Report them privately with GitHub's private vulnerability reporting:

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability**
   (direct link: <https://github.com/ironystock/hyperplot/security/advisories/new>).
3. Describe the issue, the affected version or commit, and how to reproduce
   it. A minimal `hyperplot.Chart` literal or HTTP request is ideal.

What to expect:

- We aim to acknowledge a report within **5 business days** and to give an
  initial assessment within **14 days**.
- We will work with you on a fix and a disclosure date, and credit you in the
  advisory unless you prefer otherwise.
- hyperplot is maintained by volunteers, so timelines are best effort, but
  security reports always take priority.

## Supported versions

hyperplot has not had a release yet. Until v1.0.0, only the **latest minor
release** (and `main`) receives security fixes. After v1.0.0, the latest
minor release of the current major version will be supported.

| Version | Supported |
|---|---|
| `main` | Yes |
| Latest `v0.x` release | Yes (once released) |
| Older releases | No |

## Scope

hyperplot renders text that often comes from users or from data you don't
control: series names, categories, axis labels, titles, point labels, and
formatted values. Its security model is built around that.

**In scope** (please report):

- **Escaping failures.** Any input to `Chart`, `Sparkline`, `Table`, or the
  `hx` helpers that produces SVG or HTML containing unescaped markup, an
  attribute break-out, or a script-capable element. This includes text inside
  `<title>`, `<desc>`, `<text>`, the tooltip JSON in `<metadata>`, and the
  `<table>` output.
- **Malformed output.** Input that makes `Render` produce SVG that is not
  well-formed XML.
- **SSE field injection.** Input to `hx.Stream` that injects extra SSE fields
  or events (for example through `\r` or `\n` in data, event names or ids).
- **Attribute injection** through `hx.Partial` target or swap values.
- **Client-side injection.** The client extension inserting server-provided
  text as HTML instead of with `textContent`.
- **Denial of service** through small inputs that cause disproportionate CPU
  or memory use in rendering, or a panic in a public function.

**Out of scope:**

- Charts that display misleading data because the data you passed is
  misleading.
- Content you insert into your own pages next to hyperplot output, or custom
  `Format` / `TimeFormat` functions that return attacker-controlled text: the
  output of those functions is still escaped, but its meaning is your
  responsibility.
- Vulnerabilities in htmx or its extensions; please report those to the
  [htmx project](https://github.com/bigskysoftware/htmx).
- Missing hardening headers (CSP, CORS) in the example dashboard, which is a
  demo and not meant for production use.

## Guidance for users

- You do not need to pre-escape labels: hyperplot escapes all text. Don't
  escape it yourself as well, or users will see `&amp;lt;` in charts.
- Treat `Chart.HTML()` and `Chart.Table()` output as trusted HTML only because
  hyperplot generated it; don't splice untrusted strings into it afterwards.
- Charts work under a strict Content Security Policy: serve
  `hyperplot.Stylesheet()` as a same-origin stylesheet and the client as a
  same-origin script. See the [htmx guide](docs/htmx.md) for CSP details.
- Bound the size of user-controlled inputs (number of points, label length)
  before rendering, as you would for any server-side work.
