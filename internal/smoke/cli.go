package smoke

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// RunCLI parses flags and runs smoke channels, printing JSONL results to stdout.
// Exit codes: 0 pass, 1 fail, 2 usage.
func RunCLI(args []string) int {
	fs := flag.NewFlagSet("polypus-smoke", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baseURL := fs.String("base-url", envOr("POLYPUS_BASE_URL", "http://127.0.0.1:1320"), "Polypus gateway base URL")
	channels := fs.String("channels", "all", "comma-separated channels or 'all' (chat,tts,stt,systemone)")
	chatModel := fs.String("chat-model", "", "chat model id")
	ttsModel := fs.String("tts-model", "", "TTS model id")
	sttModel := fs.String("stt-model", "", "STT model id")
	systemOneModel := fs.String("systemone-model", "", "systemone model id")
	requireCF := fs.Bool("require-cf", false, "fail when CF_AI_API_KEY is unset (CI)")
	list := fs.Bool("list", false, "print default models and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *list {
		fmt.Printf("chat=%s\n", DefaultChatModel)
		fmt.Printf("tts=%s\n", DefaultTTSModel)
		fmt.Printf("stt=%s\n", DefaultSTTModel)
		fmt.Printf("systemone=%s\n", DefaultSystemOneModel)
		return 0
	}

	opts := Options{
		BaseURL:        *baseURL,
		ChatModel:      *chatModel,
		TTSModel:       *ttsModel,
		STTModel:       *sttModel,
		SystemOneModel: *systemOneModel,
		RequireCF:      *requireCF,
	}
	ch := strings.TrimSpace(*channels)
	if ch != "" && !strings.EqualFold(ch, "all") {
		opts.Channels = splitCSV(ch)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	results, err := Run(ctx, opts)
	for _, r := range results {
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(r)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus-smoke: %v\n", err)
		return 1
	}
	return 0
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
