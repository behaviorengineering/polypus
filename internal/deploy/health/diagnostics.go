package health

import (
	"context"
	"io"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/compose"
	"github.com/behaviorengineering/polypus/internal/deploy/progress"
	"github.com/behaviorengineering/polypus/internal/deploy/stack"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// dumpBudget bounds the diagnostics. It is a fresh budget, because a dump runs
// after a failure, often once the deploy's own deadline has passed.
const dumpBudget = 30 * time.Second

// DumpGateway writes the gateway and Switchyard logs and state in cc's project to
// w (nil writes to os.Stdout), for CI debugging. It keeps ctx's values and trace
// but not its cancel or deadline. It returns only a failure to write.
func DumpGateway(ctx context.Context, w io.Writer, cc compose.Client) error {
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, "health.DumpGateway", "context required")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dumpBudget)
	defer cancel()
	out := progress.New(w)
	for _, service := range []string{stack.ServiceGateway, stack.ServiceSwitchyard} {
		out.Printf("polypus deploy: %s diagnostics (last 120 log lines):\n", service)
		logs, err := cc.CombinedOutput(ctx, cc.Args("logs", "--no-color", "--tail", "120", service))
		out.Printf("%s\n", logs)
		if err != nil {
			out.Printf("docker compose logs %s: %v\n", service, err)
		}
	}
	state, err := cc.CombinedOutput(ctx, cc.Args("ps", "--all", stack.ServiceGateway, stack.ServiceSwitchyard))
	if err != nil {
		out.Printf("docker compose ps: %v\n", err)
		return out.Err()
	}
	out.Printf("%s\n", state)
	return out.Err()
}
