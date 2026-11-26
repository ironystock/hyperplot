package hyperplot

import (
	"strconv"
	"strings"
)

// Palette is one set of color tokens: the surface, ink and the eight
// categorical series colors. Values are CSS colors such as "#257de0",
// "rgb(37 125 224)", "rebeccapurple", "var(--brand, #0b6bcb)" or
// "color-mix(in srgb, #0b6bcb 80%, white)". [Theme.CSS] silently
// replaces a value that is not shaped like a color (including any
// function other than the color functions, var(), calc(), min(), max()
// and clamp(), so never url()) with the default theme's value, so a
// palette taken from user input can neither inject CSS nor make viewers'
// browsers fetch a URL. If a color does not show, check it against that
// rule.
type Palette struct {
	// Surface is the background the chart sits on. Dot and end-dot rings
	// use it so that points stand out from lines.
	Surface string
	// Text is the primary ink, for titles and reference lines.
	Text string
	// TextMuted is the secondary ink, for tick labels, legend names and
	// direct labels.
	TextMuted string
	// Grid colors the gridlines.
	Grid string
	// Axis colors the baseline, one step darker than Grid.
	Axis string
	// Series holds the categorical colors for slots 1 through 8, in
	// order. The order is part of the color-vision safety of the palette:
	// adjacent slots are chosen to stay distinguishable.
	Series [8]string
}

// Theme is a pair of palettes: Light, and Dark for readers who select a
// dark color scheme. Dark is a separately chosen palette, not an
// inversion of Light. The zero Theme is not useful; start from
// [DefaultTheme] and change what you need.
type Theme struct {
	// Light is the default palette.
	Light Palette
	// Dark applies under prefers-color-scheme: dark (unless the page sets
	// data-theme="light" on its root element) and whenever the root
	// element has data-theme="dark".
	Dark Palette
}

// DefaultTheme returns hyperplot's validated default palette.
func DefaultTheme() Theme {
	return Theme{
		Light: Palette{
			Surface:   "#ffffff",
			Text:      "#191919",
			TextMuted: "#666666",
			Grid:      "#e7e7e7",
			Axis:      "#c2c2c2",
			Series: [8]string{
				"#257de0", "#f47230", "#00b894", "#f2a600",
				"#ee7fac", "#008a17", "#6a4ce2", "#ec4647",
			},
		},
		Dark: Palette{
			Surface:   "#191919",
			Text:      "#efefef",
			TextMuted: "#9c9c9c",
			Grid:      "#303030",
			Axis:      "#474747",
			Series: [8]string{
				"#338ced", "#e76624", "#00a888", "#d28e05",
				"#e24d8b", "#0aac23", "#9887ef", "#eb6263",
			},
		},
	}
}

// Stylesheet returns the CSS of the default theme. Serve it once per page,
// or embed it in a <style> element; charts render legibly without it, but
// in monochrome.
func Stylesheet() string { return DefaultTheme().CSS() }

// cssValue strips everything but the characters CSS color values need,
// so that a theme value can never break out of its declaration.
func cssValue(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("#(),.%/ -+", r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// colorFuncs are the CSS functions a theme color may use: color
// functions, plus var() and the math functions their arguments may need.
// Anything else, notably url(), image-set() and other functions that
// load resources, is rejected.
var colorFuncs = map[string]bool{
	"rgb": true, "rgba": true, "hsl": true, "hsla": true, "hwb": true,
	"lab": true, "lch": true, "oklab": true, "oklch": true, "color": true,
	"color-mix": true, "light-dark": true, "var": true,
	"calc": true, "min": true, "max": true, "clamp": true,
}

// cssColor returns s, reduced to the characters CSS colors need (see
// cssValue), if it is shaped like a CSS color: a #hex color, a keyword
// (a named or system color, transparent, currentColor), or a call of one
// of colorFuncs whose arguments call only colorFuncs (and may be #hex
// colors). Otherwise it
// returns fallback. A theme color can thus neither break out of its
// declaration nor make the browser fetch anything.
func cssColor(s, fallback string) string {
	s = cssValue(s)
	if s == "" {
		return fallback
	}
	if s[0] == '#' {
		hex := s[1:]
		switch len(hex) {
		case 3, 4, 6, 8:
			for _, c := range hex {
				isHex := c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
				if !isHex {
					return fallback
				}
			}
			return s
		}
		return fallback
	}
	depth, start := 0, 0 // start is where the current identifier began
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-':
			continue
		case c == '(':
			if !colorFuncs[strings.ToLower(s[start:i])] {
				return fallback
			}
			depth++
		case c == ')':
			if depth--; depth < 0 {
				return fallback
			}
		case c == '#':
			// A #hex argument, as in var(--brand, #0b6bcb) or
			// light-dark(#fff, #000). It cannot end the declaration or
			// load anything; outside a call it would be a second word.
			if depth == 0 || !hexArg(s[i+1:]) {
				return fallback
			}
		}
		start = i + 1
	}
	if depth != 0 || !strings.Contains(s, "(") && strings.ContainsAny(s, " ,/%+.") {
		return fallback // unbalanced, or several bare words
	}
	return s
}

// hexArg reports whether s starts with 3, 4, 6 or 8 hex digits followed by
// the end of a function argument.
func hexArg(s string) bool {
	n := 0
	for n < len(s) && (s[n] >= '0' && s[n] <= '9' || s[n] >= 'a' && s[n] <= 'f' || s[n] >= 'A' && s[n] <= 'F') {
		n++
	}
	return (n == 3 || n == 4 || n == 6 || n == 8) && n < len(s) && strings.IndexByte(" ,)/", s[n]) >= 0
}

// vars returns the custom property declarations for pl. A value that is
// not a CSS color is replaced by the same token of def.
func (pl Palette) vars(def Palette) string {
	var b strings.Builder
	decl := func(name, value, fallback string) {
		b.WriteString("--hp-" + name + ":" + cssColor(value, fallback) + ";")
	}
	decl("surface", pl.Surface, def.Surface)
	decl("text", pl.Text, def.Text)
	decl("text-muted", pl.TextMuted, def.TextMuted)
	decl("grid", pl.Grid, def.Grid)
	decl("axis", pl.Axis, def.Axis)
	for i, c := range pl.Series {
		decl("series-"+strconv.Itoa(i+1), c, def.Series[i])
	}
	return b.String()
}

// CSS returns the theme's stylesheet. It declares the --hp-* custom
// properties on .hp (charts), .hp-spark (sparklines), .hp-tip (the
// client's tooltip) and .hp-table (data tables), switches to the dark palette for a dark color
// scheme or data-theme="dark", and styles marks, text, tooltips and
// tables. It also honors prefers-reduced-motion and forced-colors.
func (t Theme) CSS() string {
	var b strings.Builder
	w := func(s ...string) {
		for _, x := range s {
			b.WriteString(x)
		}
		b.WriteByte('\n')
	}
	scopes := []string{".hp", ".hp-spark", ".hp-tip", ".hp-table"}
	under := func(prefix string) string {
		parts := make([]string, len(scopes))
		for i, s := range scopes {
			parts[i] = prefix + " " + s
		}
		return strings.Join(parts, ",")
	}
	// Tokens. A standalone SVG file is its own document root, which the
	// svg.hp:root selectors cover.
	def := DefaultTheme()
	dark := t.Dark.vars(def.Dark)
	w(strings.Join(scopes, ","), "{", t.Light.vars(def.Light), "}")
	w(`@media (prefers-color-scheme:dark){`, under(`:root:where(:not([data-theme="light"]))`),
		`,svg.hp:root:not([data-theme="light"]){`, dark, "}}")
	w(under(`:root[data-theme="dark"]`), `,svg.hp:root[data-theme="dark"]{`, dark, "}")

	// Chart frame and text.
	// touch-action lets a sideways finger drag reach the client as
	// pointer events (the touch scrub) while vertical scrolling and pinch
	// zoom still work; without it the browser takes the drag as a pan and
	// cancels the pointer. docs/spec.md §3.4 makes it a MUST for any
	// replacement stylesheet.
	w(".hp{color:var(--hp-text);max-width:100%;height:auto;overflow:visible;touch-action:pan-y pinch-zoom}")
	w(".hp-spark{color:var(--hp-text);max-width:100%;height:auto;overflow:visible}")
	w(".hp:focus{outline:none}")
	w(".hp:focus-visible{outline:2px solid var(--hp-series-1);outline-offset:2px;border-radius:2px}")
	w(".hp text{fill:var(--hp-text);fill-opacity:1}")
	w(".hp .hp-tick,.hp .hp-axis-label,.hp .hp-legend-label,.hp .hp-label,.hp .hp-rule-label{fill:var(--hp-text-muted)}")
	w(".hp .hp-tick{font-variant-numeric:tabular-nums}")
	// A halo in the surface color keeps labels legible where a line or
	// dot passes behind them (a spike through a rule label, say).
	w(".hp .hp-label,.hp .hp-rule-label{paint-order:stroke;stroke:var(--hp-surface);stroke-width:3px;stroke-linejoin:round}")
	w(".hp .hp-bg{fill:var(--hp-surface)}")
	w(".hp .hp-grid{stroke:var(--hp-grid);stroke-opacity:1}")
	w(".hp .hp-baseline{stroke:var(--hp-axis);stroke-opacity:1}")
	w(".hp .hp-rule{stroke:var(--hp-text);stroke-opacity:.5}")
	w(".hp .hp-dot,.hp .hp-end,.hp-spark .hp-end{stroke:var(--hp-surface)}")
	// The active point's indicators: the crosshair (mode x), the band
	// highlight (mode band) and the brushed range (rect.hp-brush, drawn by
	// the client). With a keyboard they are the chart's focus state, so
	// each has an edge in the muted ink at 3:1 or more against the surface
	// (WCAG 1.4.11): a 1.5 px stroke whose worst-case antialiasing still
	// covers 75% of a pixel, in screen pixels whatever the chart's scale.
	// The washes alone, about 1.1:1, were barely visible. Then the
	// crosshair cursor over charts inside an hx-plot:brush container.
	const edge = "stroke:var(--hp-text-muted);stroke-opacity:1;stroke-width:1.5px;vector-effect:non-scaling-stroke;"
	w(".hp .hp-crosshair{", edge, "pointer-events:none}")
	w(".hp .hp-band-hl{fill:var(--hp-text);fill-opacity:.06;", edge, "pointer-events:none}")
	w(".hp .hp-brush{fill:currentColor;fill-opacity:.1;", edge, "pointer-events:none}")
	w(`:is([hx-plot\:brush],[data-hx-plot\:brush]) .hp{cursor:crosshair}`)

	// Series colors. Marks paint with currentColor.
	for i := 1; i <= 8; i++ {
		n := strconv.Itoa(i)
		w(".hp-s", n, "{color:var(--hp-series-", n, ")}")
	}

	// Motion: animate morph updates only for readers who allow motion.
	w("@media (prefers-reduced-motion:no-preference){",
		".hp-anim path,.hp-anim circle,.hp-anim rect,.hp-anim text{",
		"transition:d .35s ease-out,x .35s ease-out,y .35s ease-out,cx .35s ease-out,cy .35s ease-out,r .35s ease-out,opacity .35s ease-out}}")
	// Keep the frame while a refetch is in flight, dimmed only once the
	// request has taken half a second: htmx marks a polling wrapper (an
	// element without hx-indicator) with htmx-request on every poll, and
	// a fast poll, often a bodiless 304, must not make the chart pulse.
	w(".htmx-request .hp,.hp.htmx-request{opacity:.6;transition:opacity .2s .5s}")

	// Tooltip (built by the client script).
	w(".hp-tip{z-index:1000;box-sizing:border-box;max-width:min(320px,calc(100vw - 16px));padding:8px 10px;",
		"border-radius:6px;background:var(--hp-surface);color:var(--hp-text);",
		"box-shadow:0 0 0 1px color-mix(in srgb,var(--hp-text) 14%,transparent),0 4px 16px rgb(0 0 0/.14);",
		"font:12px/1.4 system-ui,-apple-system,'Segoe UI',sans-serif;font-variant-numeric:tabular-nums;pointer-events:none}")
	// Long headings (a band category the axis truncates) and long values
	// (repeated observations) wrap inside the card rather than run past
	// it. The series name keeps its width up to 60% of the row and is
	// ellipsized beyond, so it never shrinks away and leaves the row
	// identified by color alone.
	w(".hp-tip-t{color:var(--hp-text-muted);margin-bottom:4px;overflow-wrap:anywhere}")
	w(".hp-tip-row{display:flex;align-items:center;gap:6px}")
	w(".hp-tip-v{font-weight:600;color:var(--hp-text);min-width:0;overflow-wrap:anywhere}")
	w(".hp-tip-n{flex:none;max-width:60%;color:var(--hp-text-muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}")
	w(".hp-tip .hp-key{flex:none;width:12px;height:2px;border-radius:1px;background:currentColor}")

	// Data table.
	w(".hp-table{border-collapse:collapse;font-variant-numeric:tabular-nums;color:var(--hp-text);background:var(--hp-surface);font-size:.875em}")
	w(".hp-table caption{text-align:left;font-weight:600;padding:0 0 6px}")
	w(".hp-table th,.hp-table td{padding:4px 12px;text-align:right;border-bottom:1px solid var(--hp-grid)}")
	w(".hp-table thead th{color:var(--hp-text-muted);font-weight:600;border-bottom-color:var(--hp-axis)}")
	w(".hp-table th:first-child,.hp-table td:first-child{text-align:left;padding-left:0}")
	w(".hp-table tbody th{font-weight:400}")
	// Visually hidden text for screen readers, such as "no data" beside
	// the dash of a missing value.
	w(".hp-sr{position:absolute;width:1px;height:1px;margin:-1px;padding:0;border:0;overflow:hidden;clip-path:inset(50%);white-space:nowrap}")

	// Forced colors (Windows high contrast): text, grid and frame take
	// the system colors, but data marks and their keys keep the series
	// palette (forced-color-adjust:none), the usual practice for data
	// visualization: with every series in CanvasText, the legend and
	// grouped bars could no longer tell series apart. Tooltip text is set
	// to CanvasText explicitly, because the tooltip opts out of forcing
	// to keep its keys and its own color tokens would follow the page
	// theme rather than the system one. The band highlight gets a
	// Highlight outline and a stronger wash: at the usual 6% it is about
	// 1:1 against Canvas, and in band mode it is the only mark of the
	// active category on the plot.
	w("@media (forced-colors:active){",
		".hp,.hp-spark{color:CanvasText}",
		".hp .hp-marks,.hp .hp-key,.hp .hp-end,.hp-spark .hp-end{forced-color-adjust:none}",
		".hp text,.hp .hp-tick,.hp .hp-axis-label,.hp .hp-legend-label,.hp .hp-label,.hp .hp-rule-label{fill:CanvasText}",
		".hp .hp-grid{stroke:GrayText}.hp .hp-baseline,.hp .hp-rule,.hp .hp-crosshair{stroke:CanvasText}",
		".hp .hp-dot,.hp .hp-end,.hp-spark .hp-end,.hp .hp-label,.hp .hp-rule-label{stroke:Canvas}.hp .hp-bg{fill:Canvas}",
		".hp .hp-band-hl{fill:Highlight;fill-opacity:.25;stroke:Highlight;stroke-width:2px}",
		".hp .hp-brush{fill:Highlight;fill-opacity:.25;stroke:Highlight}",
		".hp-tip{border:1px solid CanvasText;forced-color-adjust:none;background:Canvas;color:CanvasText}",
		".hp-tip-t,.hp-tip-v,.hp-tip-n{color:CanvasText}}")
	return b.String()
}
