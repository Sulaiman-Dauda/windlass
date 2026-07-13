// Example Windlass plugin: a complete, working extension in ~40 lines.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := os.Getenv("WINDLASS_PLUGIN_ADDR")
	if addr == "" {
		log.Fatal("WINDLASS_PLUGIN_ADDR not set — this binary is a Windlass plugin")
	}

	start := time.Now()
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<h1>Hello from a Windlass plugin</h1><p>Up %s</p>", time.Since(start).Round(time.Second))
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"plugin": "hello",
			"uptime": time.Since(start).Seconds(),
		})
	})

	log.Printf("hello plugin listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
