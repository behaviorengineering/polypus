package config

// RouterPolicy controls breach guardrails for backend URLs and cloud opt-in.
type RouterPolicy struct {
	RejectNonLoopbackBackends bool `yaml:"reject_non_loopback_backends"`
	RequireCloudOptIn         bool `yaml:"require_cloud_opt_in"`
}

// DefaultRouterPolicy returns case-mode defaults (fail closed).
func DefaultRouterPolicy() RouterPolicy {
	return RouterPolicy{
		RejectNonLoopbackBackends: true,
		RequireCloudOptIn:         true,
	}
}

type routerPolicyFile struct {
	RejectNonLoopbackBackends *bool `yaml:"reject_non_loopback_backends"`
	RequireCloudOptIn         *bool `yaml:"require_cloud_opt_in"`
}

func (f routerPolicyFile) merge() RouterPolicy {
	p := DefaultRouterPolicy()
	if f.RejectNonLoopbackBackends != nil {
		p.RejectNonLoopbackBackends = *f.RejectNonLoopbackBackends
	}
	if f.RequireCloudOptIn != nil {
		p.RequireCloudOptIn = *f.RequireCloudOptIn
	}
	return p
}

// CloudOptInRequired reports whether remote backends need INFERENCE_CLOUD_CASE=1.
func (p RouterPolicy) CloudOptInRequired() bool {
	return p.RequireCloudOptIn
}

// RejectNonLoopback reports whether local backends must bind loopback hosts.
func (p RouterPolicy) RejectNonLoopback() bool {
	return p.RejectNonLoopbackBackends
}
