package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func agentOperatingGuide(root *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, `polypus %s — OpenAI-compatible inference gateway (loopback)

ROLE & BOUNDARIES
  Routes speech and chat traffic to configured MLX or remote backends.
  Consumer stacks may probe /health before speech or model jobs when enabled.

AGENT OPERATING GUIDE
  Read AGENTS.md and ai-copilots/skills/polypus-operator/SKILL.md before changing config.
  Listening requires explicit polypus serve (never bare polypus).

COMMANDS BY RISK & LIFECYCLE
`, version)
	appendGuideGroup(&b, root, groupInspect, "Inspect & Validate")
	appendGuideGroup(&b, root, groupSetup, "Setup & secrets")
	appendGuideGroup(&b, root, groupMutate, "Execute & Mutate")
	b.WriteString(`
AUTOMATION RULES FOR AGENTS
  - Confirm /health before running downstream model or speech diagnostics.
  - Unknown commands exit non-zero; bare invoke exits 0 with this guide.
  - Full flag reference: polypus help
`)
	return b.String()
}

func appendGuideGroup(b *strings.Builder, root *cobra.Command, groupID string, title string) {
	fmt.Fprintf(b, "  %s\n", title)
	for _, cmd := range root.Commands() {
		if skipGuideCommand(cmd) {
			continue
		}
		children := visibleSubcommands(cmd)
		if len(children) > 0 {
			for _, child := range children {
				if child.GroupID != groupID {
					continue
				}
				line := cmd.Name() + " " + child.Name()
				fmt.Fprintf(b, "    %-20s %s\n", line, child.Short)
			}
			continue
		}
		if cmd.GroupID == groupID {
			fmt.Fprintf(b, "    %-20s %s\n", cmd.Name(), cmd.Short)
		}
	}
}

func visibleSubcommands(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, child := range cmd.Commands() {
		if skipGuideCommand(child) {
			continue
		}
		out = append(out, child)
	}
	return out
}

func skipGuideCommand(cmd *cobra.Command) bool {
	return cmd == nil || cmd.Hidden || cmd.Name() == "help"
}
