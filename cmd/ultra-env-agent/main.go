// ultra-env-agent runs environments on the user's own machine and exposes them
// to the platform through an outbound tunnel.
//
// The agent owns a real local-Docker provider and serves an authenticated
// control API. The user publishes that API with any outbound tunnel, so the
// platform never needs inbound access to the user's network:
//
//	ultra-env-agent --token <registration-token> --secret <signing-secret>
//	cloudflared tunnel --url http://127.0.0.1:8099
//
// Authentication is mutual: the platform presents the org-scoped token and
// signs every control request, so a leaked tunnel URL alone is useless.
//
// Configuration:
//
//	--listen   control API listen address (default 127.0.0.1:8099)
//	--token    org-scoped registration token (required)
//	--secret   shared signing secret for control requests (required)
//	--image    Bezalel image to run (default ultracore/bezalel:local)
//	--advertise
//	           public base URL environments are reachable at, when the tunnel
//	           rewrites addresses
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aleksclark/ultracore/envprovider/localdocker"
	"github.com/aleksclark/ultracore/envprovider/tunnel"
	"github.com/aleksclark/ultracore/secrets"
)

func main() {
	log := slog.New(secrets.NewRedactingHandler(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(log); err != nil {
		log.Error("ultra-env-agent exited", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	listen := flag.String("listen", "127.0.0.1:8099", "control API listen address")
	token := flag.String("token", "", "org-scoped registration token")
	secret := flag.String("secret", "", "shared signing secret for control requests")
	image := flag.String("image", "ultracore/bezalel:local", "Bezalel image to run")
	advertise := flag.String("advertise", "", "public base URL environments are reachable at")
	flag.Parse()

	if *token == "" {
		return errors.New("--token is required")
	}
	// Refusing to start without a signing secret is deliberate: an agent that
	// accepted any caller holding the tunnel URL would turn a leaked URL into
	// remote execution on the user's machine.
	if *secret == "" {
		return errors.New("--secret is required; an unsigned control API would make a leaked tunnel URL sufficient to run commands")
	}
	secrets.DefaultRedactor.Register(*token)
	secrets.DefaultRedactor.Register(*secret)

	provider, err := localdocker.New(localdocker.Config{Image: *image})
	if err != nil {
		return err
	}
	defer func() { _ = provider.Close() }()

	agent := &tunnel.Agent{
		Provider: provider, Token: *token, Secret: *secret, Log: log,
		Endpoint: rewriteEndpoint(*advertise),
	}
	server := &http.Server{
		Addr: *listen, Handler: agent.Handler(), ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("ultra-env-agent listening", "addr", *listen)
	// The operator instruction goes to stdout so it stays visible when the
	// structured log is captured elsewhere.
	if _, err := fmt.Fprintf(os.Stdout,
		"ultra-env-agent listening on %s; publish it with: cloudflared tunnel --url http://%s\n",
		*listen, *listen); err != nil {
		return err
	}
	return server.ListenAndServe()
}

// rewriteEndpoint maps a locally-published environment endpoint onto the
// address the platform can reach through the tunnel. Without it the platform
// would receive a loopback address that means nothing on its side.
func rewriteEndpoint(advertise string) func(string) string {
	if advertise == "" {
		return nil
	}
	base := strings.TrimSuffix(advertise, "/")
	return func(local string) string {
		if index := strings.Index(local, "://"); index >= 0 {
			if slash := strings.Index(local[index+3:], "/"); slash >= 0 {
				return base + local[index+3+slash:]
			}
		}
		return base
	}
}
