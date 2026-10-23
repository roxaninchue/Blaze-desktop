package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Stack is what a project is built with, for "rebuild this in your stack".
type Stack struct {
	Framework string `json:"framework"`         // react | next | vue | nuxt | svelte | sveltekit | astro | solid | angular | html
	Styling   string `json:"styling,omitempty"` // tailwind | css-modules | styled-components | sass | css
	TS        bool   `json:"ts,omitempty"`
	Build     string `json:"build,omitempty"` // the npm build script, when there is one
	Out       string `json:"out,omitempty"`   // where the build writes the site
}

// Describe is one line for a prompt.
func (s Stack) Describe() string {
	f := map[string]string{"react": "React", "next": "Next.js", "vue": "Vue", "nuxt": "Nuxt", "svelte": "Svelte", "sveltekit": "SvelteKit", "astro": "Astro", "solid": "SolidJS", "angular": "Angular", "html": "plain HTML/CSS"}[s.Framework]
	if f == "" {
		f = s.Framework
	}
	out := f
	if s.TS {
		out += " + TypeScript"
	}
	if s.Styling != "" && s.Styling != "css" {
		out += " with " + map[string]string{"tailwind": "Tailwind CSS", "css-modules": "CSS Modules", "styled-components": "styled-components", "sass": "Sass"}[s.Styling]
	}
	return out
}

// DetectStack reads package.json (and a few files) to name the project's stack.
func DetectStack(folder string) Stack {
	st := Stack{Framework: "html", Styling: "css"}
	b, err := os.ReadFile(filepath.Join(folder, "package.json"))
	if err != nil {
		return st
	}
	var pj struct {
		Scripts map[string]string `json:"scripts"`
		Deps    map[string]string `json:"dependencies"`
		Dev     map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(b, &pj) != nil {
		return st
	}
	has := func(n string) bool { _, a := pj.Deps[n]; _, d := pj.Dev[n]; return a || d }
	switch {
	case has("next"):
		st.Framework, st.Out = "next", "out"
	case has("nuxt"):
		st.Framework, st.Out = "nuxt", ".output/public"
	case has("@sveltejs/kit"):
		st.Framework, st.Out = "sveltekit", "build"
	case has("svelte"):
		st.Framework, st.Out = "svelte", "dist"
	case has("astro"):
		st.Framework, st.Out = "astro", "dist"
	case has("vue"):
		st.Framework, st.Out = "vue", "dist"
	case has("solid-js"):
		st.Framework, st.Out = "solid", "dist"
	case has("@angular/core"):
		st.Framework, st.Out = "angular", "dist"
	case has("react"):
		st.Framework, st.Out = "react", "dist"
		if has("react-scripts") {
			st.Out = "build"
		}
	}
	switch {
	case has("tailwindcss"):
		st.Styling = "tailwind"
	case has("styled-components"):
		st.Styling = "styled-components"
	case has("sass"):
		st.Styling = "sass"
	}
	st.TS = has("typescript")
	if s, ok := pj.Scripts["build"]; ok && strings.TrimSpace(s) != "" {
		st.Build = "build"
	}
	return st
}
