package cli

import (
	"fmt"
	"strings"

	"github.com/behaviorengineering/polypus/internal/buildinfo"
	"github.com/spf13/cobra"
)

func agentOperatingGuide(root *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, `polypus %s: OpenAI-compatible inference gateway (loopback)

ROLE & BOUNDARIES
  Routes chat, vision, embeddings, speech, SystemOne and batch traffic to configured
  local (for example MLX) or remote backends.
  Consumer stacks may probe /health before speech or model jobs when enabled.

AGENT OPERATING GUIDE
  Read AGENTS.md and ai-copilots/skills/polypus-operator/SKILL.md before changing config.
  Listening requires explicit polypus serve (never bare polypus).

COMMANDS BY RISK & LIFECYCLE
`, buildinfo.Version)
	appendGuideGroups(&b, root)
	b.WriteString(`
AUTOMATION RULES FOR AGENTS
  - Confirm /health before running downstream model or speech diagnostics.
  - Every mutating command except serve takes --dry-run; run it first and read the plan.
  - admin-key generate, rotate and delete prompt on a terminal; pass --yes in scripts.
  - secret set never prompts without a terminal; pass --stdin.
  - Unknown commands exit non-zero; bare invoke exits 0 with this guide.
  - Full flag reference: polypus help
`)
	return b.String()
}

// appendGuideGroups lists the runnable leaf commands under each help group that
// has any, in groupTitles order, so help and the guide share one set of titles.
// A leaf is printed by its full path below root (deploy secret set), so an agent
// can copy the line and run it.
func appendGuideGroups(b *strings.Builder, root *cobra.Command) {
	leaves := guideLeaves(root)
	for _, g := range groupTitles {
		var lines []*cobra.Command
		for _, leaf := range leaves {
			if leaf.GroupID == g.ID {
				lines = append(lines, leaf)
			}
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(b, "  %s\n", g.Title)
		for _, leaf := range lines {
			fmt.Fprintf(b, "    %-24s %s\n", strings.TrimPrefix(leaf.CommandPath(), root.CommandPath()+" "), leaf.Short)
		}
	}
}

// guideLeaves walks root's visible commands down to the runnable ones with no
// visible subcommands, in Cobra's order.
func guideLeaves(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		children := visibleSubcommands(cmd)
		if len(children) == 0 {
			if cmd != root && cmd.Runnable() {
				out = append(out, cmd)
			}
			return
		}
		for _, child := range children {
			walk(child)
		}
	}
	walk(root)
	return out
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
