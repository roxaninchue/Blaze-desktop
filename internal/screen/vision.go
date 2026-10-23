package screen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/ocr"
)

// Vision-capable fallbacks for providers whose default model can't see.
var visionFallback = map[string]string{
	"groq":    "meta-llama/llama-4-scout-17b-16e-instruct",
	"mistral": "mistral-small-latest",
	"gemini":  "gemini-2.5-flash",
}

type visionTarget struct{ provider, model string }

// VisionTargets lists the models from the user's route that can look at an image.
func VisionTargets(cfg *config.Config) []visionTarget {
	var out []visionTarget
	seen := map[string]bool{}
	add := func(p, m string) {
		k := p + "|" + m
		if m == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, visionTarget{p, m})
	}
	ts := llm.Targets(cfg, false)
	for _, t := range ts {
		if llm.Info(t.Provider, t.Model).Vision {
			add(t.Provider, t.Model)
		}
	}
	for _, t := range ts {
		if m, ok := visionFallback[t.Provider]; ok {
			add(t.Provider, m)
		}
	}
	return out
}

var vclient = &http.Client{Timeout: 60 * time.Second}

// Describe sends the screenshot and the question to the first vision model that answers.
// With Private vision on, the screenshot never leaves the computer: it is read here
// (OCR, size, colours) and only that text comes back.
func Describe(ctx context.Context, cfg *config.Config, jpeg []byte, question string) (string, string, error) {
	if cfg.PrivateVision {
		f, err := os.CreateTemp("", "blaze-look-*.jpg")
		if err != nil {
			return "", "", err
		}
		name := f.Name()
		defer os.Remove(name)
		_, werr := f.Write(jpeg)
		f.Close()
		if werr != nil {
			return "", "", werr
		}
		return "Private vision is on, so the screenshot was read on this computer and not sent anywhere. It gives text and colours only; for layout or pictures, the window's text from read_screen helps most.\n" + ocr.Describe(ctx, name), "this computer (Private vision)", nil
	}
	targets := VisionTargets(cfg)
	if len(targets) == 0 {
		return "", "", errors.New("none of your models can look at images. Add a free Google Gemini key in Settings › Providers (Gemini models can see)")
	}
	url := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)
	prompt := "You are looking at a screenshot of the user's screen (downscaled). " + question +
		"\nDescribe only what is relevant: exact visible text that matters (errors, values, labels), where things are, and what state the UI is in. Be concise. Never repeat anything that looks like a password, key or card number."
	var errs []string
	for _, t := range targets {
		p, ok := config.ProviderByID(t.provider)
		if !ok || p.BaseURL == "" {
			continue
		}
		body := map[string]any{
			"model": t.model, "max_tokens": 900, "stream": false,
			"messages": []map[string]any{{"role": "user", "content": []map[string]any{
				{"type": "text", "text": prompt},
				{"type": "image_url", "image_url": map[string]any{"url": url}},
			}}},
		}
		b, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(p.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range llm.AuthHeaders(p.ID, cfg.Key(p.ID)) {
			req.Header.Set(k, v)
		}
		resp, err := vclient.Do(req)
		if err != nil {
			errs = append(errs, p.Name+": "+err.Error())
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			msg := string(raw)
			if len(msg) > 200 {
				msg = msg[:200]
			}
			errs = append(errs, fmt.Sprintf("%s %s: HTTP %d %s", p.Name, t.model, resp.StatusCode, msg))
			continue
		}
		var r struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage *llm.Usage `json:"usage"`
		}
		if json.Unmarshal(raw, &r) != nil || len(r.Choices) == 0 || strings.TrimSpace(r.Choices[0].Message.Content) == "" {
			errs = append(errs, p.Name+": empty answer")
			continue
		}
		text := strings.TrimSpace(r.Choices[0].Message.Content)
		llm.RecordUsage(p.ID, t.model, len(prompt)+len(jpeg)/3, &llm.Result{Text: text, Usage: r.Usage})
		return text, config.Name(p.ID) + " · " + t.model, nil
	}
	return "", "", errors.New("no vision model could look at the screenshot: " + strings.Join(errs, "; "))
}
