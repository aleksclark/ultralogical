// ultra-env-agent is the user-side provider process. In production it is
// exposed through an outbound cloudflared tunnel; Phase 5 loopback mode is
// used by CI and local onboarding tests.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8099", "control API listen address")
	token := flag.String("token", "", "registration token")
	flag.Parse()
	if *token == "" {
		fmt.Fprintln(os.Stderr, "--token is required")
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+*token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "provider": "tunnel_local", "connected_at": time.Now().UTC()})
	})
	fmt.Printf("ultra env-agent listening on %s; expose with: cloudflared tunnel --url http://%s\n", *listen, *listen)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		panic(err)
	}
}
