package ai

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Prompt is one request to a language model. System is fixed per feature
// (and cached by the provider); User carries the documents and the question.
type Prompt struct {
	System    string
	User      string
	MaxTokens int
}

// LLM writes text. Apexx uses it only where seconds are acceptable (context
// of chunks, answers in assistants, panel copilots), never on the path of a
// storefront page, and always with a deterministic fallback.
type LLM interface {
	Model() string
	Complete(ctx context.Context, p Prompt) (string, Usage, error)
}

// ErrNoModel means no language model is configured; callers fall back to
// their deterministic answer.
var ErrNoModel = errors.New("ai: no language model configured")

// Claude calls the Claude API with the official SDK.
type Claude struct {
	client          anthropic.Client
	model           string
	inPerM, outPerM int64 // micro-dollars per million tokens
}

// NewClaude returns a client for model with an explicit key and base URL,
// so nothing is inherited from the process environment by accident.
func NewClaude(key, baseURL, model string) *Claude {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	in, out := Price(model)
	return &Claude{client: anthropic.NewClient(option.WithAPIKey(key), option.WithBaseURL(baseURL), option.WithMaxRetries(2)), model: model, inPerM: in, outPerM: out}
}

// Price is what a model costs per million input and output tokens, in
// micro-dollars (list prices, November 2026).
func Price(model string) (in, out int64) {
	switch {
	case strings.Contains(model, "haiku"):
		return 100_000, 500_000
	case strings.Contains(model, "sonnet"):
		return 2_000_000, 10_000_000
	case strings.Contains(model, "opus"):
		return 4_000_000, 20_000_000
	}
	return 4_000_000, 20_000_000
}

func (c *Claude) Model() string { return c.model }

func (c *Claude) Complete(ctx context.Context, p Prompt) (string, Usage, error) {
	if p.MaxTokens <= 0 {
		p.MaxTokens = 1024
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: int64(p.MaxTokens),
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(p.User))},
	}
	if p.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: p.System, CacheControl: anthropic.NewCacheControlEphemeralParam()}}
	}
	resp, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return "", Usage{}, err
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	in := resp.Usage.InputTokens + resp.Usage.CacheCreationInputTokens + resp.Usage.CacheReadInputTokens
	u := Usage{Tokens: int(in + resp.Usage.OutputTokens),
		CostMicros: (resp.Usage.InputTokens*c.inPerM + resp.Usage.CacheCreationInputTokens*c.inPerM*5/4 +
			resp.Usage.CacheReadInputTokens*c.inPerM/10 + resp.Usage.OutputTokens*c.outPerM) / 1_000_000}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return "", u, errors.New("ai: the model declined")
	}
	return strings.TrimSpace(b.String()), u, nil
}
