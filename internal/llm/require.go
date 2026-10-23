package llm

import "context"

type requireKey struct{}

// WithRequireTool marks a request whose user plainly asked for an action, so it is
// sent with tool_choice "required" (v0.19.13, like CLI 0.14.15): a model can't answer
// "run the tests" with output it made up.
func WithRequireTool(ctx context.Context) context.Context {
	return context.WithValue(ctx, requireKey{}, true)
}

// RequireTool reports the mark.
func RequireTool(ctx context.Context) bool {
	v, _ := ctx.Value(requireKey{}).(bool)
	return v
}

// ToolChoice is the tool_choice to send for ctx.
func ToolChoice(ctx context.Context) string {
	if RequireTool(ctx) {
		return "required"
	}
	return "auto"
}
