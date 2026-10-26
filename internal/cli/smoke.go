package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/buildinfo"
	"github.com/behaviorengineering/polypus/internal/smoke"
	"github.com/spf13/cobra"
)

// smokeRunBudget bounds one polypus-smoke run across every channel.
const smokeRunBudget = 10 * time.Minute

// RunSmoke dispatches polypus-smoke subcommands. Returns a process exit code:
// 0 pass, 1 a probe failed, 2 usage.
func RunSmoke(args []string) int {
	return execute(newSmokeRoot(), args)
}

func newSmokeRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "polypus-smoke",
		Short:         "L1 probes against a running Polypus gateway",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       buildinfo.Version,
		// Bare invoke prints the guide; probes cost money, so they only run from
		// the explicit run command.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &exitError{code: 2, msg: fmt.Sprintf("polypus-smoke: unknown command %q (see polypus-smoke help)", args[0])}
			}
			_, err := fmt.Fprint(cmd.OutOrStdout(), smokeOperatingGuide(cmd))
			return err
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	addCLIGroups(root)
	root.AddCommand(newSmokeVersionCmd())
	root.AddCommand(newSmokeModelsCmd())
	root.AddCommand(newSmokeRunCmd())
	return root
}

func newSmokeVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Build identity",
		GroupID: groupInspect,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "polypus-smoke %s\n", buildinfo.Version)
			return err
		},
	}
}

func newSmokeModelsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "models",
		Short:   "Print the default probe model per channel",
		GroupID: groupInspect,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "chat=%s\ntts=%s\nstt=%s\nsystemone=%s\nbatch=%s\n",
				smoke.DefaultChatModel, smoke.DefaultTTSModel, smoke.DefaultSTTModel,
				smoke.DefaultSystemOneModel, smoke.DefaultBatchModel)
			return err
		},
	}
}

func newSmokeRunCmd() *cobra.Command {
	var opts smoke.Options
	var channels string
	cmd := &cobra.Command{
		Use:     "run",
		Short:   "Run the probes (paid calls) and print one JSON result per channel",
		GroupID: groupMutate,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if ch := strings.TrimSpace(channels); ch != "" && !strings.EqualFold(ch, "all") {
				opts.Channels = splitCSV(ch)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), smokeRunBudget)
			defer cancel()
			results, runErr := smoke.Run(ctx, opts)
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, r := range results {
				if err := enc.Encode(r); err != nil {
					return &exitError{code: 1, msg: "polypus-smoke: write result: " + err.Error(), err: err}
				}
			}
			if runErr != nil {
				return &exitError{code: 1, msg: "polypus-smoke: " + runErr.Error(), err: runErr}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.BaseURL, "base-url", "", "Polypus gateway base URL (default $POLYPUS_BASE_URL, else http://127.0.0.1:1320)")
	f.StringVar(&channels, "channels", "all", "comma-separated channels or 'all' (chat,tts,stt,systemone; batch is opt-in)")
	f.StringVar(&opts.ChatModel, "chat-model", "", "chat model id")
	f.StringVar(&opts.TTSModel, "tts-model", "", "TTS model id")
	f.StringVar(&opts.STTModel, "stt-model", "", "STT model id")
	f.StringVar(&opts.SystemOneModel, "systemone-model", "", "systemone model id")
	f.StringVar(&opts.BatchModel, "batch-model", "", "batch chat model id (Workers AI batch allow-list)")
	f.BoolVar(&opts.RequireCF, "require-cf", false, "fail TypeSafe billing skips instead of skip (CI)")
	return cmd
}

func smokeOperatingGuide(root *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, `polypus-smoke %s: L1 probes against a running Polypus gateway

ROLE & BOUNDARIES
  Sends one cheap request per channel (chat, TTS, STT, systemone; batch on request)
  to a gateway that is already serving. Every probe is a paid upstream call.

AGENT OPERATING GUIDE
  Probes run only from polypus-smoke run (never bare polypus-smoke).
  Start the gateway first (go tool task serve) and confirm /health.

COMMANDS BY RISK & LIFECYCLE
`, buildinfo.Version)
	appendGuideGroup(&b, root, groupInspect, "Inspect & Validate")
	appendGuideGroup(&b, root, groupMutate, "Execute & Mutate")
	b.WriteString(`
AUTOMATION RULES FOR AGENTS
  - Unknown commands exit 2; bare invoke exits 0 with this guide.
  - run exits 1 when any probe fails and prints one JSON line per channel.
  - Full flag reference: polypus-smoke help run
`)
	return b.String()
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
