package health

import (
	"context"
	"fmt"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
)

// dumpBudget bounds the diagnostics. It is a fresh budget, because a dump runs
// after a failure, often once the deploy's own deadline has passed.
const dumpBudget = 30 * time.Second

// DumpGateway prints gateway and Switchyard container logs and state for CI debugging.
func DumpGateway() {
	ctx, cancel := context.WithTimeout(context.Background(), dumpBudget)
	defer cancel()
	dumpContainer(ctx, "polypus-gateway")
	dumpContainer(ctx, "polypus-switchyard")
}

func dumpContainer(ctx context.Context, name string) {
	fmt.Printf("polypus deploy: %s diagnostics (last 120 log lines):\n", name)
	out, err := execx.OutputContext(ctx, "docker", []string{"logs", name, "--tail", "120"}, nil)
	fmt.Printf("%s\n", out)
	if err != nil {
		fmt.Printf("docker logs %s: %v\n", name, err)
	}
	state, err := execx.OutputContext(ctx, "docker", []string{"inspect", name, "--format", "State={{json .State}}"}, nil)
	if err != nil {
		fmt.Printf("docker inspect %s: %v\n", name, err)
		return
	}
	fmt.Printf("%s\n", state)
}
