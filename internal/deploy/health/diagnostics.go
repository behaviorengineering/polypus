package health

import (
	"context"
	"fmt"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/compose"
	"github.com/behaviorengineering/polypus/internal/deploy/execx"
)

// dumpBudget bounds the diagnostics. It is a fresh budget, because a dump runs
// after a failure, often once the deploy's own deadline has passed.
const dumpBudget = 30 * time.Second

// DumpGateway prints the gateway and Switchyard logs and state in cc's project,
// for CI debugging.
func DumpGateway(cc compose.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), dumpBudget)
	defer cancel()
	for _, service := range []string{"gateway", "switchyard"} {
		fmt.Printf("polypus deploy: %s diagnostics (last 120 log lines):\n", service)
		out, err := execx.OutputContext(ctx, "docker", cc.Args("logs", "--no-color", "--tail", "120", service), nil)
		fmt.Printf("%s\n", out)
		if err != nil {
			fmt.Printf("docker compose logs %s: %v\n", service, err)
		}
	}
	state, err := execx.OutputContext(ctx, "docker", cc.Args("ps", "--all", "gateway", "switchyard"), nil)
	if err != nil {
		fmt.Printf("docker compose ps: %v\n", err)
		return
	}
	fmt.Printf("%s\n", state)
}
