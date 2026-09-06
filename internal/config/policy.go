package config

// RouterPolicy controls breach guardrails for backend URLs.
type RouterPolicy struct {
	RejectNonLoopbackBackends bool `yaml:"reject_non_loopback_backends"`
}

// DefaultRouterPolicy returns case-mode defaults (fail closed on non-loopback locals).
func DefaultRouterPolicy() RouterPolicy {
	return RouterPolicy{
		RejectNonLoopbackBackends: true,
	}
}

type routerPolicyFile struct {
	RejectNonLoopbackBackends *bool `yaml:"reject_non_loopback_backends"`
}

func (f routerPolicyFile) merge() RouterPolicy {
	p := DefaultRouterPolicy()
	if f.RejectNonLoopbackBackends != nil {
		p.RejectNonLoopbackBackends = *f.RejectNonLoopbackBackends
	}
	return p
}

// RejectNonLoopback reports whether local backends must bind loopback hosts.
func (p RouterPolicy) RejectNonLoopback() bool {
	return p.RejectNonLoopbackBackends
}
