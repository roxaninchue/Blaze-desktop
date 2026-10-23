package router

// Format translation (OmniRoute's translator, simplified): clients that speak
// Anthropic's Messages API (Claude Code, the Anthropic SDKs) or Gemini's
// generateContent API can use BlazeRouter too. Their request becomes an OpenAI chat
// request, goes through the same routing, and the answer is turned back into their
// format, streamed when they asked for a stream. Upstream every provider is spoken
// to in the OpenAI format, as Blaze already does.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"blaze-desktop/internal/llm"
)

func str(v any) string {
	s, _ := v.(string)
	return s
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func jsonText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------------- Anthropic Messages ----------------

// fromAnthropic turns a Messages API request into an OpenAI chat request.
func fromAnthropic(in map[string]any) map[string]any {
	out := map[string]any{"model": in["model"]}
	var msgs []any
	switch s := in["system"].(type) {
	case string:
		if s != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": s})
		}
	case []any:
		var b strings.Builder
		for _, p := range s {
			if t := str(obj(p)["text"]); t != "" {
				if b.Len() > 0 {
					b.WriteString("\n\n")
				}
				b.WriteString(t)
			}
		}
		if b.Len() > 0 {
			msgs = append(msgs, map[string]any{"role": "system", "content": b.String()})
		}
	}
	for _, mm := range list(in["messages"]) {
		m := obj(mm)
		role := str(m["role"])
		if c, ok := m["content"].(string); ok {
			msgs = append(msgs, map[string]any{"role": role, "content": c})
			continue
		}
		var parts []any
		var calls []any
		var text strings.Builder
		hasImage := false
		for _, bb := range list(m["content"]) {
			b := obj(bb)
			switch str(b["type"]) {
			case "text":
				text.WriteString(str(b["text"]))
				parts = append(parts, map[string]any{"type": "text", "text": str(b["text"])})
			case "image":
				src := obj(b["source"])
				url := str(src["url"])
				if str(src["type"]) == "base64" {
					url = "data:" + str(src["media_type"]) + ";base64," + str(src["data"])
				}
				if url != "" {
					hasImage = true
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
				}
			case "tool_use":
				calls = append(calls, map[string]any{"id": str(b["id"]), "type": "function",
					"function": map[string]any{"name": str(b["name"]), "arguments": jsonText(orEmpty(b["input"]))}})
			case "tool_result":
				var res string
				switch c := b["content"].(type) {
				case string:
					res = c
				case []any:
					var sb strings.Builder
					for _, x := range c {
						sb.WriteString(str(obj(x)["text"]))
					}
					res = sb.String()
				}
				if e, _ := b["is_error"].(bool); e {
					res = "Error: " + res
				}
				msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": str(b["tool_use_id"]), "content": res})
			}
		}
		if len(calls) > 0 {
			msgs = append(msgs, map[string]any{"role": "assistant", "content": text.String(), "tool_calls": calls})
			continue
		}
		if len(parts) == 0 {
			continue
		}
		if hasImage {
			msgs = append(msgs, map[string]any{"role": role, "content": parts})
		} else {
			msgs = append(msgs, map[string]any{"role": role, "content": text.String()})
		}
	}
	out["messages"] = msgs
	if v, ok := in["max_tokens"]; ok {
		out["max_tokens"] = v
	}
	for _, k := range []string{"temperature", "top_p"} {
		if v, ok := in[k]; ok {
			out[k] = v
		}
	}
	if v, ok := in["stop_sequences"]; ok {
		out["stop"] = v
	}
	var tools []any
	for _, tt := range list(in["tools"]) {
		t := obj(tt)
		if str(t["name"]) == "" || (str(t["type"]) != "" && str(t["type"]) != "custom" && t["input_schema"] == nil) {
			continue // Anthropic's server tools (web search, bash…) have no OpenAI equivalent
		}
		params := obj(t["input_schema"])
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t["name"], "description": str(t["description"]), "parameters": params}})
	}
	if len(tools) > 0 {
		out["tools"] = tools
		switch tc := obj(in["tool_choice"]); str(tc["type"]) {
		case "any":
			out["tool_choice"] = "required"
		case "tool":
			out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": str(tc["name"])}}
		case "none":
			out["tool_choice"] = "none"
		}
	}
	return out
}

func orEmpty(v any) any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

func stopReason(res *llm.Result) string {
	switch {
	case len(res.ToolCalls) > 0:
		return "tool_use"
	case res.Finish == "length" || res.Finish == "max_tokens":
		return "max_tokens"
	}
	return "end_turn"
}

func argsObj(s string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil && v != nil {
		return v
	}
	if m, ok := llm.ParseArgs(s); ok {
		return m
	}
	return map[string]any{}
}

func msgID() string { return fmt.Sprintf("msg_br%x", time.Now().UnixNano()) }

func usageNums(res *llm.Result, chars int) (int, int) {
	in, out := llm.Estimate(chars), len(res.Text)/4+1
	if res.Usage != nil {
		if res.Usage.Prompt > 0 {
			in = res.Usage.Prompt
		}
		if res.Usage.Completion > 0 {
			out = res.Usage.Completion
		}
	}
	return in, out
}

// toAnthropic is a whole Messages API answer.
func toAnthropic(res *llm.Result, model string, chars int) map[string]any {
	var content []any
	if res.Text != "" {
		content = append(content, map[string]any{"type": "text", "text": res.Text})
	}
	for _, c := range res.ToolCalls {
		content = append(content, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Function.Name, "input": argsObj(c.Function.Arguments)})
	}
	if content == nil {
		content = []any{}
	}
	in, out := usageNums(res, chars)
	return map[string]any{"id": msgID(), "type": "message", "role": "assistant", "model": model, "content": content,
		"stop_reason": stopReason(res), "stop_sequence": nil, "usage": map[string]any{"input_tokens": in, "output_tokens": out}}
}

// anthropicStream writes Messages API stream events.
type anthropicStream struct {
	emit    func(event string, data any)
	model   string
	textOn  bool
	index   int
	started bool
}

func (a *anthropicStream) start() {
	a.started = true
	a.emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": msgID(), "type": "message", "role": "assistant",
		"model": a.model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}})
}

func (a *anthropicStream) text(s string) {
	if !a.textOn {
		a.textOn = true
		a.emit("content_block_start", map[string]any{"type": "content_block_start", "index": a.index, "content_block": map[string]any{"type": "text", "text": ""}})
	}
	a.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": a.index, "delta": map[string]any{"type": "text_delta", "text": s}})
}

func (a *anthropicStream) finish(res *llm.Result, chars int) {
	if a.textOn {
		a.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": a.index})
		a.index++
	}
	for _, c := range res.ToolCalls {
		a.emit("content_block_start", map[string]any{"type": "content_block_start", "index": a.index, "content_block": map[string]any{"type": "tool_use", "id": c.ID, "name": c.Function.Name, "input": map[string]any{}}})
		args := c.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		a.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": a.index, "delta": map[string]any{"type": "input_json_delta", "partial_json": args}})
		a.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": a.index})
		a.index++
	}
	_, out := usageNums(res, chars)
	a.emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason(res), "stop_sequence": nil}, "usage": map[string]any{"output_tokens": out}})
	a.emit("message_stop", map[string]any{"type": "message_stop"})
}

// anthropicError is the Messages API's error shape.
func anthropicError(code int, msg string) map[string]any {
	t := "api_error"
	switch code {
	case 400:
		t = "invalid_request_error"
	case 401:
		t = "authentication_error"
	case 404:
		t = "not_found_error"
	case 429:
		t = "rate_limit_error"
	case 503, 529:
		t = "overloaded_error"
	}
	return map[string]any{"type": "error", "error": map[string]any{"type": t, "message": msg}}
}

// ---------------- Gemini generateContent ----------------

// fromGemini turns a generateContent request into an OpenAI chat request.
func fromGemini(in map[string]any, model string) map[string]any {
	out := map[string]any{"model": model}
	var msgs []any
	if si := obj(in["systemInstruction"]); si != nil {
		var b strings.Builder
		for _, p := range list(si["parts"]) {
			b.WriteString(str(obj(p)["text"]))
		}
		if b.Len() > 0 {
			msgs = append(msgs, map[string]any{"role": "system", "content": b.String()})
		}
	}
	callIDs := map[string][]string{} // function name -> ids waiting for a response
	n := 0
	for _, cc := range list(in["contents"]) {
		c := obj(cc)
		role := "user"
		if str(c["role"]) == "model" {
			role = "assistant"
		}
		var text strings.Builder
		var parts, calls []any
		hasImage := false
		for _, pp := range list(c["parts"]) {
			p := obj(pp)
			switch {
			case p["text"] != nil:
				text.WriteString(str(p["text"]))
				parts = append(parts, map[string]any{"type": "text", "text": str(p["text"])})
			case p["inlineData"] != nil:
				d := obj(p["inlineData"])
				hasImage = true
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + str(d["mimeType"]) + ";base64," + str(d["data"])}})
			case p["functionCall"] != nil:
				fc := obj(p["functionCall"])
				n++
				id := fmt.Sprintf("call_%d", n)
				if s := str(fc["id"]); s != "" {
					id = s
				}
				name := str(fc["name"])
				callIDs[name] = append(callIDs[name], id)
				calls = append(calls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": jsonText(orEmpty(fc["args"]))}})
			case p["functionResponse"] != nil:
				fr := obj(p["functionResponse"])
				name := str(fr["name"])
				id := str(fr["id"])
				if q := callIDs[name]; id == "" && len(q) > 0 {
					id, callIDs[name] = q[0], q[1:]
				}
				if id == "" {
					n++
					id = fmt.Sprintf("call_%d", n)
				}
				msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": id, "content": jsonText(fr["response"])})
			}
		}
		switch {
		case len(calls) > 0:
			msgs = append(msgs, map[string]any{"role": "assistant", "content": text.String(), "tool_calls": calls})
		case hasImage:
			msgs = append(msgs, map[string]any{"role": role, "content": parts})
		case len(parts) > 0:
			msgs = append(msgs, map[string]any{"role": role, "content": text.String()})
		}
	}
	out["messages"] = msgs
	if gc := obj(in["generationConfig"]); gc != nil {
		for k, v := range map[string]string{"temperature": "temperature", "topP": "top_p", "maxOutputTokens": "max_tokens", "stopSequences": "stop"} {
			if x, ok := gc[k]; ok {
				out[v] = x
			}
		}
	}
	var tools []any
	for _, tt := range list(in["tools"]) {
		for _, ff := range list(obj(tt)["functionDeclarations"]) {
			f := obj(ff)
			params := obj(f["parameters"])
			if params == nil {
				params = obj(f["parametersJsonSchema"])
			}
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": f["name"], "description": str(f["description"]), "parameters": lowerTypes(params)}})
		}
	}
	if len(tools) > 0 {
		out["tools"] = tools
	}
	return out
}

// lowerTypes turns Gemini's upper-case schema types (STRING, OBJECT) into JSON Schema's.
func lowerTypes(v any) map[string]any {
	m := obj(v)
	if m == nil {
		return nil
	}
	out := map[string]any{}
	for k, x := range m {
		switch {
		case k == "type":
			if s, ok := x.(string); ok {
				x = strings.ToLower(s)
			}
		case obj(x) != nil:
			if k == "properties" {
				props := map[string]any{}
				for pk, pv := range obj(x) {
					props[pk] = lowerTypes(pv)
				}
				x = props
			} else {
				x = lowerTypes(x)
			}
		}
		out[k] = x
	}
	return out
}

func geminiFinish(res *llm.Result) string {
	if res.Finish == "length" || res.Finish == "max_tokens" {
		return "MAX_TOKENS"
	}
	return "STOP"
}

// toGemini is a whole generateContent answer (also one stream chunk when final).
func toGemini(res *llm.Result, model string, chars int, final bool) map[string]any {
	var parts []any
	if res.Text != "" {
		parts = append(parts, map[string]any{"text": res.Text})
	}
	for _, c := range res.ToolCalls {
		parts = append(parts, map[string]any{"functionCall": map[string]any{"name": c.Function.Name, "args": argsObj(c.Function.Arguments)}})
	}
	if parts == nil {
		parts = []any{map[string]any{"text": ""}}
	}
	cand := map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "index": 0}
	out := map[string]any{"candidates": []any{cand}, "modelVersion": model}
	if final {
		cand["finishReason"] = geminiFinish(res)
		in, o := usageNums(res, chars)
		out["usageMetadata"] = map[string]any{"promptTokenCount": in, "candidatesTokenCount": o, "totalTokenCount": in + o}
	}
	return out
}

func geminiError(code int, msg string) map[string]any {
	status := "INTERNAL"
	switch code {
	case 400:
		status = "INVALID_ARGUMENT"
	case 401:
		status = "UNAUTHENTICATED"
	case 404:
		status = "NOT_FOUND"
	case 429:
		status = "RESOURCE_EXHAUSTED"
	case 503:
		status = "UNAVAILABLE"
	}
	return map[string]any{"error": map[string]any{"code": code, "message": msg, "status": status}}
}
