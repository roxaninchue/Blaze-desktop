package router

// The provider catalog. Blaze already knows the big free tiers (Gemini, Groq,
// Cerebras, OpenRouter, Mistral, NVIDIA…); BlazeRouter adds the long tail of free
// OpenAI-compatible providers ported from OmniRoute's free-tier catalog
// (open-sse/config/freeModelCatalog.data.ts and providers/registry, MIT licence,
// (c) diegosouzapw; see THIRD_PARTY_NOTICES.md). Only API-key providers that speak
// the OpenAI format at a fixed address are taken, and none whose terms OmniRoute
// flags "avoid". Every provider registered here uses Blaze's shared key store
// (~/.blaze, the same as Blaze CLI), so a key is saved once for everything.

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

//go:embed omniroute_catalog.json
var catalogJSON []byte

// FreeInfo is what the catalog knows about a provider's free access.
type FreeInfo struct {
	Type    string `json:"type"`    // recurring-daily, recurring-monthly, recurring-uncapped, one-time-initial…
	Monthly int64  `json:"monthly"` // documented recurring tokens per month (0 = no published cap)
	Credit  int64  `json:"credit"`  // one-time signup credit in tokens
	ToS     string `json:"tos"`     // ok, caution, ambiguous (OmniRoute's advisory reading)
	Note    string `json:"note,omitempty"`
}

type catModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type catProvider struct {
	ID       string     `json:"id"`
	Base     string     `json:"base"`
	FreeType string     `json:"free_type"`
	Monthly  int64      `json:"monthly_tokens"`
	Credit   int64      `json:"credit_tokens"`
	ToS      string     `json:"tos"`
	Models   []catModel `json:"models"`
}

var (
	free    = map[string]FreeInfo{}
	names   = map[string]string{}
	keyPage = map[string]string{}
	// Free-tier notes for the providers Blaze already had (from the same catalog).
	builtinFree = map[string]FreeInfo{
		"gemini":     {Type: "recurring-uncapped", ToS: "ok", Note: "Flash family; per-project rate limits"},
		"groq":       {Type: "recurring-daily", Monthly: 30000000, ToS: "caution", Note: "five per-model daily caps"},
		"cerebras":   {Type: "one-time-initial", ToS: "ok", Note: "one-time $5 credit (needs a card)"},
		"openrouter": {Type: "recurring-daily", ToS: "ok", Note: ":free models, 50 requests/day (1000 after a $10 top-up)"},
		"mistral":    {Type: "recurring-monthly", Monthly: 1000000000, ToS: "ok", Note: "1B tokens/month on the free plan"},
		"nvidia":     {Type: "recurring-uncapped", ToS: "caution", Note: "rate limited, no token cap"},
		"deepseek":   {Type: "one-time-initial", Credit: 5000000, ToS: "ok"},
		"ollama":     {Type: "local", ToS: "ok", Note: "runs on this computer"},
	}
)

// Extra providers OmniRoute documents that need a hand-written entry.
var manual = []struct {
	p     config.Provider
	f     FreeInfo
	ms    []catModel
	label string
}{
	{config.Provider{ID: "github-models", Name: "GitHub Models", BaseURL: "https://models.github.ai/inference/", DefaultModel: "openai/gpt-4.1-mini", KeyHint: "github_pat_…", KeyPage: "https://github.com/settings/personal-access-tokens", Env: "GITHUB_TOKEN", Free: true, QuotaPage: "https://github.com/marketplace/models"},
		FreeInfo{Type: "recurring-daily", Monthly: 18000000, ToS: "ok", Note: "a GitHub token with the models scope"},
		[]catModel{{"openai/gpt-4.1-mini", "GPT-4.1 mini"}, {"openai/gpt-4.1", "GPT-4.1"}, {"openai/gpt-4o", "GPT-4o"}, {"meta/Llama-4-Scout-17B-16E-Instruct", "Llama 4 Scout"}, {"deepseek/DeepSeek-V3-0324", "DeepSeek V3"}, {"mistral-ai/mistral-small-2503", "Mistral Small"}}, ""},
	{config.Provider{ID: "cloudflare-ai", Name: "Cloudflare Workers AI", BaseURL: "https://api.cloudflare.com/client/v4/accounts/{account}/ai/v1/", DefaultModel: "@cf/meta/llama-3.3-70b-instruct-fp8-fast", KeyHint: "API token", KeyPage: "https://dash.cloudflare.com/profile/api-tokens", Env: "CLOUDFLARE_API_TOKEN", Free: true, QuotaPage: "https://dash.cloudflare.com/"},
		FreeInfo{Type: "recurring-daily", Monthly: 30000000, ToS: "caution", Note: "10k neurons/day; needs your account id"},
		[]catModel{{"@cf/meta/llama-3.3-70b-instruct-fp8-fast", "Llama 3.3 70B (fast)"}, {"@cf/qwen/qwen2.5-coder-32b-instruct", "Qwen 2.5 Coder 32B"}, {"@cf/zai-org/glm-4.7-flash", "GLM 4.7 Flash"}, {"@cf/moonshotai/kimi-k2.6", "Kimi K2.6"}, {"@cf/google/gemma-4-26b-a4b-it", "Gemma 4 26B"}, {"@cf/qwen/qwq-32b", "QwQ 32B"}}, ""},
	{config.Provider{ID: "siliconflow", Name: "SiliconFlow", BaseURL: "https://api.siliconflow.com/v1/", DefaultModel: "Qwen/Qwen3-8B", KeyHint: "sk-…", KeyPage: "https://cloud.siliconflow.com/account/ak", Env: "SILICONFLOW_API_KEY", Free: true},
		FreeInfo{Type: "recurring-uncapped", ToS: "caution", Note: "permanently free small models"},
		[]catModel{{"Qwen/Qwen3-8B", "Qwen3 8B"}, {"THUDM/GLM-4-9B-0414", "GLM-4 9B"}, {"deepseek-ai/DeepSeek-R1-0528-Qwen3-8B", "DeepSeek R1 Qwen3 8B"}}, ""},
	{config.Provider{ID: "nara", Name: "NaraRouter", BaseURL: "https://router.bynara.id/v1/", DefaultModel: "", KeyHint: "…", KeyPage: "https://router.bynara.id/", Env: "NARA_API_KEY", Free: true},
		FreeInfo{Type: "recurring-daily", Monthly: 210000000, ToS: "caution", Note: "7M tokens/day bucket"}, nil, ""},
}

// Nice names and key pages for the ported providers (OmniRoute only keeps ids).
var nice = map[string][2]string{
	"agnes": {"Agnes AI", "https://apihub.agnes-ai.com/"}, "agnes-cn": {"Agnes AI (China)", "https://api.agnes-ai.cn/"},
	"api-airforce": {"API Airforce", "https://api.airforce/"}, "baichuan": {"Baichuan", "https://platform.baichuan-ai.com/"},
	"baidu": {"Baidu Qianfan", "https://console.bce.baidu.com/qianfan/"}, "bazaarlink": {"BazaarLink", "https://bazaarlink.ai/"},
	"bluesminds": {"BluesMinds", "https://bluesminds.com/"}, "bytez": {"Bytez", "https://bytez.com/api"},
	"cohere": {"Cohere", "https://dashboard.cohere.com/api-keys"}, "deepinfra": {"DeepInfra", "https://deepinfra.com/dash/api_keys"},
	"doubao": {"Doubao (Volcengine)", "https://console.volcengine.com/ark"}, "freemodel-dev": {"FreeModel.dev", "https://freemodel.dev/"},
	"glm": {"Z.AI (GLM)", "https://z.ai/manage-apikey/apikey-list"}, "glm-cn": {"Zhipu GLM (China)", "https://open.bigmodel.cn/usercenter/apikeys"},
	"huggingface": {"Hugging Face", "https://huggingface.co/settings/tokens"}, "hyperbolic": {"Hyperbolic", "https://app.hyperbolic.xyz/settings"},
	"inference-net": {"Inference.net", "https://inference.net/dashboard"}, "kilo-gateway": {"Kilo Gateway", "https://app.kilo.ai/"},
	"llm7": {"LLM7", "https://token.llm7.io/"}, "modelscope": {"ModelScope", "https://modelscope.cn/my/myaccesstoken"},
	"monsterapi": {"MonsterAPI", "https://monsterapi.ai/"}, "morph": {"Morph", "https://morphllm.com/dashboard"},
	"nous-research": {"Nous Research", "https://portal.nousresearch.com/"}, "novita": {"Novita AI", "https://novita.ai/settings/key-management"},
	"nscale": {"Nscale", "https://console.nscale.com/"}, "ollama-cloud": {"Ollama Cloud", "https://ollama.com/settings/keys"},
	"predibase": {"Predibase", "https://app.predibase.com/"}, "publicai": {"PublicAI", "https://publicai.co/"},
	"qoder": {"Qoder", "https://qoder.com/"}, "reka": {"Reka", "https://platform.reka.ai/apikeys"},
	"sambanova": {"SambaNova", "https://cloud.sambanova.ai/apis"}, "scaleway": {"Scaleway", "https://console.scaleway.com/iam/api-keys"},
	"sensenova": {"SenseNova", "https://console.sensecore.cn/"}, "stepfun": {"StepFun", "https://platform.stepfun.com/"},
	"tencent": {"Tencent Hunyuan", "https://console.cloud.tencent.com/hunyuan"},
}

func init() {
	var c struct {
		Providers []catProvider         `json:"providers"`
		Known     map[string][]catModel `json:"known"`
	}
	_ = json.Unmarshal(catalogJSON, &c)
	for id, f := range builtinFree {
		free[id] = f
	}
	var regs []config.Provider
	for _, m := range manual {
		regs = append(regs, m.p)
		free[m.p.ID] = m.f
		addModels(m.p.ID, m.ms)
	}
	for _, p := range c.Providers {
		if p.ID == "freemodel-dev" || p.FreeType == "" {
			continue
		}
		name, page := p.ID, ""
		if n, ok := nice[p.ID]; ok {
			name, page = n[0], n[1]
		}
		def := ""
		if len(p.Models) > 0 {
			def = p.Models[0].ID
		}
		env := strings.ToUpper(strings.ReplaceAll(p.ID, "-", "_")) + "_API_KEY"
		regs = append(regs, config.Provider{ID: p.ID, Name: name, BaseURL: p.Base, DefaultModel: def, KeyHint: "…", KeyPage: page, Env: env, Free: true, QuotaPage: page})
		free[p.ID] = FreeInfo{Type: p.FreeType, Monthly: p.Monthly, Credit: p.Credit, ToS: p.ToS}
		addModels(p.ID, p.Models)
	}
	config.Register(regs...)
	// BlazeRouter itself, so Blaze's own chats can go through it (address set while it runs).
	config.Register(config.Provider{ID: config.RouterID, Name: "BlazeRouter", DefaultModel: "auto", KeyHint: "(local)", Free: true})
	// Free models OmniRoute lists for providers Blaze already knew: add any Blaze's table lacks.
	for id, ms := range c.Known {
		addModels(id, ms)
	}
}

// addModels adds catalog models Blaze's built-in table doesn't have yet (tools unknown).
func addModels(provider string, ms []catModel) {
	have := map[string]bool{}
	for _, m := range llm.Catalog[provider] {
		have[m.ID] = true
	}
	for _, m := range ms {
		if have[m.ID] || !llm.IsChat(m.ID) {
			continue
		}
		have[m.ID] = true
		llm.Catalog[provider] = append(llm.Catalog[provider], llm.ModelInfo{ID: m.ID, Name: m.Name, Tools: llm.Unknown, Free: true, Known: true})
	}
}

// Free is the catalog's free-tier note for a provider.
func Free(id string) (FreeInfo, bool) {
	f, ok := free[id]
	return f, ok
}

// routable lists the providers BlazeRouter can send to: every provider except the
// keyless chat-only one and BlazeRouter itself, free tiers first.
func routable() []config.Provider {
	var out []config.Provider
	for _, p := range config.All() {
		if p.ID == config.RouterID || p.ID == config.KeylessID {
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Free && !out[j].Free })
	return out
}

// Templated is true for a provider whose address needs a value from the user first
// (Cloudflare's account id).
func Templated(id string) bool {
	for _, p := range config.All() {
		if p.ID == id {
			return strings.Contains(p.BaseURL, "{")
		}
	}
	return false
}

// rawBase is the provider's address as listed (with any {placeholder}).
func rawBase(id string) string {
	for _, p := range config.All() {
		if p.ID == id {
			return p.BaseURL
		}
	}
	return ""
}
