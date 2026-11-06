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
  - Every mutating command takes --dry-run; run it first and read the plan.
  - admin-key generate, rotate and delete prompt on a terminal; pass --yes in scripts.
  - secret set never prompts without a terminal; pass --stdin.
  - Unknown commands exit non-zero; bare invoke exits 0 with this guide.
  - Full flag reference: polypus help
`)
	return b.String()
}

// appendGuideGroups lists the commands under each help group that has any, in
// groupTitles order, so help and the guide share one set of titles.
func appendGuideGroups(b *strings.Builder, root *cobra.Command) {
	for _, g := range groupTitles {
		if !root.ContainsGroup(g.ID) && !groupUsedBelow(root, g.ID) {
			continue
		}
		appendGuideGroup(b, root, g.ID, g.Title)
	}
}

// groupUsedBelow reports whether any command under root sits in group id.
func groupUsedBelow(root *cobra.Command, id string) bool {
	for _, cmd := range polypusCommands(root) {
		if cmd != root && cmd.GroupID == id {
			return true
		}
	}
	return false
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
				fmt.Fprintf(b, "    %-24s %s\n", line, child.Short)
			}
			continue
		}
		if cmd.GroupID == groupID {
			fmt.Fprintf(b, "    %-24s %s\n", cmd.Name(), cmd.Short)
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
