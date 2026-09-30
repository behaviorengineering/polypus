//go:build integration

package integration

// Mode selects which hermetic integration harness configuration to boot.
type Mode int

const (
	// ModeCloudflare is the default shared harness (mock or live Workers AI).
	ModeCloudflare Mode = iota
	// ModeMLX uses mlx_local speech backends against a mock OpenAI speech server.
	ModeMLX
	// ModeRouter exercises named router composition via a mock Switchyard.
	ModeRouter
)
