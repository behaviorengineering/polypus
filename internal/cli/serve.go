package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway"
)

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	host := fs.String("host", "", "gateway listen host (default POLYPUS_HOST or 127.0.0.1)")
	port := fs.Int("port", 0, "gateway listen port (default POLYPUS_PORT or 1320)")
	backend := fs.String("backend", "", "speech backend base URL (default POLYPUS_BACKEND_URL)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	opts := config.LoadServeOptions()
	if *host != "" {
		opts.Host = *host
	}
	if *port > 0 {
		opts.Port = *port
	}
	if *backend != "" {
		opts.BackendURL = *backend
	}

	fmt.Fprintf(os.Stderr, "polypus gateway: http://%s/\n", opts.ListenAddr())
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus serve: router config: %v\n", err)
		return 1
	}
	for _, id := range rcfg.BackendIDs() {
		b := rcfg.Backends[id]
		fmt.Fprintf(os.Stderr, "  backend %s: %s (%v)\n", id, b.BaseURL, b.Capabilities)
	}
	if err := gateway.ListenAndServe(opts); err != nil {
		fmt.Fprintf(os.Stderr, "polypus serve: %v\n", err)
		return 1
	}
	return 0
}
