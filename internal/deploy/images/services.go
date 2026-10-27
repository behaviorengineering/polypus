package images

// Services says which optional parts of the deploy stack run, and so which image
// pins a deploy needs. The gateway and Tailscale always run.
type Services struct {
	// Routers runs Switchyard (the gateway YAML has routers:).
	Routers bool
	// Observability runs the OTLP collector, Phoenix and HyperDX.
	Observability bool
}

// pinKeys are the images.env keys the services need.
func (s Services) pinKeys() []string {
	keys := []string{polypusImageKey, "TAILSCALE_IMAGE"}
	if s.Routers {
		keys = append(keys, switchyardImageKey)
	}
	if s.Observability {
		keys = append(keys, "PHOENIX_IMAGE", "HYPERDX_IMAGE", "OTELCOL_IMAGE")
	}
	return keys
}

// probeKeys are the pins checked against GHCR: the images Polypus publishes.
func (s Services) probeKeys() []string {
	if s.Routers {
		return []string{polypusImageKey, switchyardImageKey}
	}
	return []string{polypusImageKey}
}
