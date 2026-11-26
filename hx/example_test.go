package hx_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/ironystock/hyperplot/hx"
)

// renderChart stands in for hyperplot's Chart.Render.
func renderChart(width, height float64) []byte {
	return fmt.Appendf(nil, `<svg class="hp" viewBox="0 0 %g %g"></svg>`, width, height)
}

// A polling handler: render, tag, and answer 304 when the client already
// has this exact chart.
func ExampleCheckPTag() {
	handler := func(w http.ResponseWriter, r *http.Request) {
		svg := renderChart(640, 320)
		if hx.CheckPTag(w, r, hx.Tag(svg)) {
			return // 304, htmx skips the swap
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(svg)
	}

	// First poll: no HX-PTag yet.
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/charts/latency", nil))
	tag := w.Header().Get("HX-PTag")
	fmt.Println(w.Code, tag)

	// Next poll: hx-ptag echoes the tag, and nothing changed.
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/charts/latency", nil)
	r.Header.Set("HX-PTag", tag)
	w = httptest.NewRecorder()
	handler(w, r)
	fmt.Println(w.Code, w.Body.Len())
	// Output:
	// 200 "hp-b3d5ad02449d8d41"
	// 304 0
}

func ExampleTag() {
	fmt.Println(hx.Tag([]byte("<svg/>")))
	// Output: "hp-675c6e4be5c05a62"
}

// A streaming handler sends a fresh chart every second until the client
// disconnects. The page connects with
//
//	<div hx-sse:connect="/stream/throughput" hx-swap="innerMorph"></div>
func ExampleNewStream() {
	handler := func(w http.ResponseWriter, r *http.Request) {
		stream, err := hx.NewStream(w, r)
		if err != nil {
			// Not an event stream. HX-Reswap: none keeps the chart the
			// page already shows, and a non-2xx status makes a
			// reconnecting hx-sse back off instead of retrying at its
			// base delay, as it would after a 204.
			log.Print(err)
			w.Header().Set("HX-Reswap", "none")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				if err := stream.Send(renderChart(640, 320)); err != nil {
					return // client disconnected
				}
			}
		}
	}
	http.HandleFunc("/stream/throughput", handler)
}

// Send splits multi-line HTML into data lines; the client joins them back.
func ExampleStream_Send() {
	w := httptest.NewRecorder()
	stream, _ := hx.NewStream(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/stream", nil))
	_ = stream.Send([]byte("<svg>\n<g/>\n</svg>"))
	fmt.Print(w.Body.String())
	// Output:
	// data: <svg>
	// data: <g/>
	// data: </svg>
}

// Ids let the server replay what a reconnecting client missed.
func ExampleStream_LastEventID() {
	updates := []string{"<p>1</p>", "<p>2</p>", "<p>3</p>"}

	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/stream", nil)
	r.Header.Set("Last-Event-ID", "1") // the client saw update 1, then lost the connection
	w := httptest.NewRecorder()
	stream, _ := hx.NewStream(w, r)

	start := 0
	if id := stream.LastEventID(); id != "" {
		_, _ = fmt.Sscan(id, &start)
		start++
	}
	for i := start; i < len(updates); i++ {
		_ = stream.SendID(fmt.Sprint(i), []byte(updates[i]))
	}
	fmt.Print(w.Body.String())
	// Output:
	// id: 2
	// data: <p>3</p>
}

// A named event becomes a DOM event; matching hx-sse:close ends the stream
// without a reconnect.
func ExampleStream_Event() {
	w := httptest.NewRecorder()
	stream, _ := hx.NewStream(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/stream", nil))
	_ = stream.Event("done", nil)
	fmt.Print(w.Body.String())
	// Output:
	// event: done
	// data:
}

// One response updates the chart (main swap) and a KPI tile elsewhere on
// the page.
func ExamplePartial() {
	svg := renderChart(640, 320)
	tile := []byte(`<span class="kpi">12.5 ms</span>`)
	fmt.Printf("%s\n%s\n", svg, hx.Partial("#kpi-p99", "innerMorph", tile))
	// Output:
	// <svg class="hp" viewBox="0 0 640 320"></svg>
	// <hx-partial hx-target="#kpi-p99" hx-swap="innerMorph"><span class="kpi">12.5 ms</span></hx-partial>
}

// Size reads the container width that the hx-plot client extension
// sends with each request that targets an hx-plot:autosize element, falling back
// to the default. The handler keeps its own height: the container is as
// tall as the chart inside it, so its height must not feed back.
func ExampleSize() {
	for _, query := range []string{"hp-w=812", "hp-w=12", "hp-w=NaN", ""} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/charts/latency?"+query, nil)
		width, _ := hx.Size(r, 640, 0)
		svg := renderChart(width, 320)
		fmt.Printf("%s\n", svg)
	}
	// Output:
	// <svg class="hp" viewBox="0 0 812 320"></svg>
	// <svg class="hp" viewBox="0 0 50 320"></svg>
	// <svg class="hp" viewBox="0 0 640 320"></svg>
	// <svg class="hp" viewBox="0 0 640 320"></svg>
}

// With hx-plot:autosize="both", the client also sends the container's
// content height as hp-h. Use it only when CSS sets that height, as with
//
//	<div style="height: 40vh" hx-plot:autosize="both" …>
//
// Both values are clamped to [hx.MinSize, hx.MaxSize].
func ExampleSize_both() {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/charts/latency?hp-w=812&hp-h=9000", nil)
	w, h := hx.Size(r, 640, 320)
	fmt.Println(w, h)
	// Output: 812 4096
}

// A drill-down handler reads the select gesture that the extension adds
// to a request triggered by htmx:plot:select (hx-trigger="htmx:plot:select"),
// or that a plain link sends with the same parameters. hp-x is the row key
// the chart carries, so it can be matched or parsed exactly.
func ExampleSelected() {
	q := "hp-chart=lt&hp-x=2026-03-03T14%3A10%3A00Z&hp-series=p50"
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/detail?"+q, nil)
	sel, ok := hx.Selected(r)
	if !ok {
		fmt.Println("no selection: show the overview")
		return
	}
	t, err := sel.Time()
	if err != nil {
		fmt.Println("not a time: ignore it")
		return
	}
	fmt.Println(sel.Chart, sel.Series, t.Format(time.Kitchen))
	// Output: lt p50 2:10PM
}

// A zoom handler tells a brush from a poll of the same URL by the
// presence of hp-x0 and hp-x1, orders the range, and fixes the chart's x
// domain to it. Values that do not parse are ignored (docs/spec.md §5.3).
// A plain datetime-local from/to form sends the same parameters without
// an offset; TimesIn reads them in the chart's time zone.
func ExampleBrushed() {
	q := "hp-chart=lt&hp-x0=2026-03-03T14%3A15%3A00Z&hp-x1=2026-03-03T14%3A05%3A00Z"
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/charts/latency?"+q, nil)
	domain := [2]time.Time{} // zero: the data's extent
	if b, ok := hx.Brushed(r); ok {
		if t0, t1, err := b.Times(); err == nil {
			domain = [2]time.Time{t0, t1}
		}
	}
	fmt.Println(domain[0].Format(time.RFC3339), domain[1].Format(time.RFC3339))
	// Output: 2026-03-03T14:05:00Z 2026-03-03T14:15:00Z
}

// On a linear axis the brush values are numbers. Floats rejects NaN,
// infinities and anything that is not a JSON number; clamping the range
// to what the data covers is the application's choice.
func ExampleBrush_Floats() {
	for _, q := range []string{"hp-x0=7.5&hp-x1=2", "hp-x0=NaN&hp-x1=2", "hp-x0=2"} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/charts/size?"+q, nil)
		b, ok := hx.Brushed(r)
		if !ok {
			fmt.Println("no brush")
			continue
		}
		x0, x1, err := b.Floats()
		if err != nil {
			fmt.Println("ignored:", err)
			continue
		}
		fmt.Println(max(x0, 0), min(x1, 100))
	}
	// Output:
	// 2 7.5
	// ignored: hyperplot: "NaN" is not a number
	// no brush
}

// One URL can serve the full page and, for htmx, just the fragment.
func ExampleIsPartial() {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/dashboard?region=eu", nil)
	r.Header.Set("HX-Request", "true")
	r.Header.Set("HX-Request-Type", "partial")
	if hx.IsPartial(r) {
		fmt.Println("render #charts only")
	} else {
		fmt.Println("render the whole page")
	}
	// Output: render #charts only
}
