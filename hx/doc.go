// Package hx provides small, dependency-free server helpers for keeping
// hyperplot charts live with htmx 4: polling tags, server-sent event
// streams, multi-target partial responses, and client-reported chart
// sizes. The helpers work with any HTML fragment, not only charts.
//
// Every pattern below swaps with innerMorph. hyperplot output is
// deterministic and gives series, bars and dots stable ids, so idiomorph
// updates the existing SVG nodes in place instead of replacing them, and
// the CSS transitions of a chart rendered with Animate animate the change.
//
// The snippets assume htmx 4.0.2 and the extensions loaded by script tag
// (htmx 4 has no hx-ext attribute; an extension is active once its script
// has run):
//
//	<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
//	<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-ptag.js"></script>
//	<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-sse.min.js"></script>
//
// # Polling with ptag and innerMorph
//
// The element polls its URL. With the ptag extension loaded, htmx stores
// the HX-PTag response header on the element and sends it back as the
// HX-PTag request header on the next poll. When nothing changed, the
// server answers 304 and htmx skips the swap, so an idle chart costs one
// empty response per interval.
//
//	<div hx-get="/charts/latency"
//	     hx-trigger="every 2s, hp:resize"
//	     hx-swap="innerMorph"
//	     hx-ptag='"hp-1a2b3c4d5e6f7a8b"'
//	     data-hp-autosize>
//	  {{.Chart}}
//	</div>
//
// The optional hx-ptag attribute seeds the tag of the server-rendered
// first paint, so even the first poll can be a 304. [CheckPTag] accepts
// the tag with or without quotes, so hx-ptag="hp-1a2b3c4d5e6f7a8b" works
// as well. data-hp-autosize and the hp:resize trigger come from the
// hyperplot client extension, which re-requests the chart when its
// container is resized and reports the new size (see [Size]).
//
// The handler renders, tags the bytes, and lets [CheckPTag] decide. It
// keeps the height: a wrapper without a CSS height is as tall as the
// chart inside it, so using its reported height would grow the chart on
// every poll. On an error it answers 204, because htmx 4 swaps every
// other status, error bodies included, over the chart:
//
//	func latency(w http.ResponseWriter, r *http.Request) {
//		width, _ := hx.Size(r, 640, 0)
//		svg, err := latencyChart(width, 320).Render() // latencyChart returns a *hyperplot.Chart
//		if err != nil {
//			log.Print(err)
//			w.WriteHeader(http.StatusNoContent) // keep the last good chart
//			return
//		}
//		if hx.CheckPTag(w, r, hx.Tag(svg)) {
//			return // 304: the client already shows this chart
//		}
//		w.Header().Set("Content-Type", "text/html; charset=utf-8")
//		w.Write(svg)
//	}
//
// # Streaming with hx-sse and innerMorph
//
// hx-sse:connect opens a persistent GET request when the element loads.
// Each unnamed SSE message is swapped into the element using its hx-swap;
// named events are dispatched as DOM events instead. If the stream ends or
// fails, hx-sse reconnects and sends the last event id it saw as the
// Last-Event-ID header. It waits the reconnect delay (500 ms, or the value
// set with [Stream.Retry], give or take 30%) and doubles the wait, up to
// a minute, only while reconnects fail: a network error or a non-2xx
// status. Any 2xx answer resets the wait to the base delay, so a handler
// that keeps returning soon after it starts, or that answers 204, has
// every open tab reconnecting at the base rate for as long as the problem
// lasts.
//
//	<div hx-sse:connect="/stream/throughput"
//	     hx-swap="innerMorph"
//	     hx-sse:close="done">
//	  {{.Chart}}
//	</div>
//
// The handler opens a [Stream] and sends a fresh chart on every tick until
// the client goes away. A frame that fails to render is skipped, so the
// client keeps its last chart and its connection. A handler that cannot
// stream at all answers 503 with "HX-Reswap: none": htmx swaps nothing on
// the first request, and a reconnect that gets a non-2xx status backs off:
//
//	func throughput(w http.ResponseWriter, r *http.Request) {
//		stream, err := hx.NewStream(w, r)
//		if err != nil {
//			log.Print(err)
//			w.Header().Set("HX-Reswap", "none") // not a stream: keep the chart
//			w.WriteHeader(http.StatusServiceUnavailable) // and back off
//			return
//		}
//		tick := time.NewTicker(time.Second)
//		defer tick.Stop()
//		for {
//			select {
//			case <-r.Context().Done():
//				return
//			case <-tick.C:
//				svg, err := throughputChart().Render()
//				if err != nil {
//					log.Print(err)
//					continue // skip the frame; returning would reconnect
//				}
//				if err := stream.Send(svg); err != nil {
//					return // client disconnected
//				}
//			}
//		}
//	}
//
// Use [Stream.SendID] and [Stream.LastEventID] to replay missed updates
// after a reconnect (ids travel back as an HTTP header, so they must be
// printable ASCII), [Stream.Retry] to tune the reconnect delay, and
// [Stream.Comment] as a keep-alive when updates are rare. To end a stream
// for good, send a named event matching hx-sse:close (here
// stream.Event("done", nil)); merely returning from the handler makes the
// client reconnect after the base delay.
//
// While the tab is hidden, hx-sse pauses the connection by default
// (sse.pauseOnBackground) and reconnects when the tab is visible again.
// The request context is then canceled, so the loop above exits cleanly.
//
// # Filters
//
// A filter control re-requests the whole chart area, and the server
// re-renders every chart for the selected slice. Because innerMorph keeps
// the chart elements (their ids derive from the chart ID and series
// names), the charts animate to the new data rather than flashing, and a
// series pinned with Slot keeps its color when others are filtered out.
//
//	<form hx-get="/dashboard"
//	      hx-trigger="change"
//	      hx-target="#charts"
//	      hx-swap="innerMorph">
//	  <select name="region">
//	    <option>all</option><option>eu</option><option>us</option>
//	  </select>
//	</form>
//	<div id="charts">…</div>
//
// The same URL usually also serves the full page. [IsPartial] tells the
// two apart: htmx sends "HX-Request-Type: partial" when it targets an
// element like #charts, and "full" for boosted navigation, a body target,
// or hx-select. Because one URL then has two bodies, send a Vary header
// naming both request headers, so no cache serves the fragment for the
// page:
//
//	w.Header().Add("Vary", hx.HeaderRequest+", "+hx.HeaderRequestType)
//	if hx.IsPartial(r) {
//		renderCharts(w, region) // just the contents of #charts
//		return
//	}
//	renderPage(w, region)
//
// # Multiple targets with hx-partial
//
// One response can update a chart and the KPI tiles around it. htmx 4
// removes every <hx-partial> element from the response, swaps its content
// into its hx-target after the main swap, and swaps whatever is left into
// the request target as usual. [Partial] builds the element with escaped
// attributes:
//
//	w.Write(svg) // main swap: the chart
//	w.Write(hx.Partial("#kpi-p99", "innerMorph", p99Tile))
//	w.Write(hx.Partial("#kpi-errors", "innerMorph", errorsTile))
//
// Partials also work inside SSE messages, so one Send can update several
// elements:
//
//	var msg bytes.Buffer
//	msg.Write(svg)
//	msg.Write(hx.Partial("#kpi-p99", "innerMorph", p99Tile))
//	stream.Send(msg.Bytes())
//
// If a message contains only partials, the connecting element is left
// unchanged.
package hx
