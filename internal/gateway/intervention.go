package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
)

// setIntervention records code on the span (when ctx is set) and the response header
// (when w is set).
func setIntervention(ctx context.Context, w http.ResponseWriter, code observability.Intervention) {
	code = observability.Intervention(strings.TrimSpace(string(code)))
	if code == "" {
		return
	}
	if w != nil {
		joinInterventionHeader(w, string(code))
	}
	if ctx != nil {
		observability.RecordIntervention(ctx, code)
	}
}

func recordInterventionFromError(ctx context.Context, err error) {
	if code := derrors.Field(err, derrors.FieldIntervention); code != "" {
		setIntervention(ctx, nil, observability.Intervention(code))
	}
}

func joinInterventionHeader(w http.ResponseWriter, code string) {
	code = strings.TrimSpace(code)
	if code == "" || w == nil {
		return
	}
	prev := w.Header().Get(config.InterventionHeader)
	w.Header().Set(config.InterventionHeader, observability.JoinInterventions(prev, observability.Intervention(code)))
}
