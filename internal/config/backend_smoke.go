package config

// BackendSmoke is a backend's optional smoke: block. polypus deploy reads it to
// choose the post-deploy probes, so each environment keeps its choices in its own
// gateway YAML instead of in code. The gateway itself only accepts the key.
type BackendSmoke struct {
	// Skip leaves the backend out of every post-deploy probe.
	Skip bool `yaml:"skip"`
	// Chat lists the model ids (as the backend names them, no backend prefix) the
	// chat probes send. Empty lets deploy pick from models.allow.
	Chat []string `yaml:"chat"`
	// SystemOne is the model id the SystemOne probe sends. Empty lets deploy pick
	// from models.systemone_allow.
	SystemOne string `yaml:"systemone"`
}
