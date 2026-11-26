// Command basic serves one static hyperplot chart: the README quick start.
//
//	go run ./examples/basic   # http://localhost:8080
package main

import (
	"html/template"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/ironystock/hyperplot"
)

var page = template.Must(template.New("page").Parse(`<!doctype html>
<title>Deploys</title>
<link rel="stylesheet" href="/hyperplot.css">
<main>{{.}}</main>`))

func main() {
	http.HandleFunc("/hyperplot.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = io.WriteString(w, hyperplot.Stylesheet())
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		chart := hyperplot.Chart{
			Title: "Deploys per day",
			Marks: []hyperplot.Mark{hyperplot.Bar{
				Name: "deploys",
				X:    hyperplot.Categories{"Mon", "Tue", "Wed", "Thu", "Fri"},
				Y:    []float64{12, 19, 7, 15, 22},
			}},
		}
		svg, err := chart.HTML() // template.HTML: every label is escaped
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := page.Execute(w, svg); err != nil {
			log.Print(err)
		}
	})
	// The timeouts drop clients that send a request or read a response
	// too slowly, so one stalled client cannot hold a connection forever.
	srv := &http.Server{
		Addr:              "localhost:8080",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
