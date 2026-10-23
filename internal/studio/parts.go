package studio

// 3D mockups (three-d-design skill): a Three.js page keeps every part's numbers in one
// JSON block,
//
//	<script type="application/json" id="blaze-parts">{"units": "mm", "parts": [{"id": "seat", …}]}</script>
//
// and builds the scene from it. A part picked in the Design tab is edited here: only the
// changed keys of that one part are rewritten, so the diff stays a line or two and the
// rest of the block keeps its order and layout.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var partsBlock = regexp.MustCompile(`(?is)<script\b[^>]*\bid\s*=\s*["']blaze-parts["'][^>]*>(.*?)</script>`)

// partKeys are the part fields the Design tab may set. Each value is a JSON literal.
var partKeys = map[string]bool{
	"color": true, "position": true, "rotation": true, "scale": true, "size": true,
	"radius": true, "height": true, "label": true, "material": true, "visible": true,
	"opacity": true, "roughness": true, "metalness": true,
}

// HasParts reports whether a page is a 3D mockup built from a blaze-parts block.
func HasParts(src string) bool { return partsBlock.MatchString(src) }

// ApplyPart sets props (key -> JSON literal, e.g. "color": `"#c0392b"`, "position": `[0, 420, 0]`)
// on the part with this id. An empty value removes the key.
func ApplyPart(src, id string, props map[string]string) (string, error) {
	if id == "" || len(props) == 0 {
		return src, ErrNotFound
	}
	loc := partsBlock.FindStringSubmatchIndex(src)
	if loc == nil {
		return src, ErrNotFound
	}
	bs, be := loc[2], loc[3]
	block := src[bs:be]
	var top struct {
		Parts []json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal([]byte(block), &top); err != nil {
		return src, fmt.Errorf("the blaze-parts block isn't valid JSON: %w", err)
	}
	var raw string
	for _, p := range top.Parts {
		var head struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(p, &head) == nil && head.ID == id {
			raw = string(p)
			break
		}
	}
	if raw == "" {
		return src, ErrNotFound
	}
	at := strings.Index(block, raw)
	if at < 0 || strings.Count(block, raw) != 1 {
		return src, ErrTooComplex
	}
	obj := raw
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !partKeys[k] {
			return src, fmt.Errorf("a part has no %q to set", k)
		}
		v := strings.TrimSpace(props[k])
		if v != "" && !json.Valid([]byte(v)) {
			return src, fmt.Errorf("%s: %q isn't a JSON value", k, v)
		}
		var err error
		if obj, err = setKey(obj, k, compact(v)); err != nil {
			return src, err
		}
	}
	block = block[:at] + obj + block[at+len(raw):]
	if !json.Valid([]byte(block)) {
		return src, ErrTooComplex
	}
	return src[:bs] + block + src[be:], nil
}

// compact writes arrays the way people do in these blocks: [0, 420, 0].
func compact(v string) string {
	if v == "" {
		return v
	}
	var b bytes.Buffer
	if json.Compact(&b, []byte(v)) != nil {
		return v
	}
	out := b.String()
	if strings.HasPrefix(out, "[") && !strings.ContainsAny(out[1:], "{[\"") { // a flat list of numbers
		out = strings.ReplaceAll(out, ",", ", ")
	}
	return out
}

// setKey replaces, removes or appends one top-level key of a JSON object written as text.
func setKey(obj, key, val string) (string, error) {
	start, end, ok := findValue(obj, key)
	if ok {
		if val == "" { // remove `"key": value` and one neighbouring comma
			ks := strings.LastIndex(obj[:start], `"`+key+`"`)
			if ks < 0 {
				return obj, ErrTooComplex
			}
			rest := obj[end:]
			if t := strings.TrimLeft(rest, " \t\r\n"); strings.HasPrefix(t, ",") {
				return obj[:ks] + strings.TrimLeft(t[1:], " \t"), nil
			}
			pre := strings.TrimRight(obj[:ks], " \t\r\n")
			pre = strings.TrimSuffix(pre, ",")
			return pre + rest, nil
		}
		return obj[:start] + val + obj[end:], nil
	}
	if val == "" {
		return obj, nil
	}
	close := strings.LastIndexByte(obj, '}')
	if close < 0 {
		return obj, ErrTooComplex
	}
	body := strings.TrimRight(obj[:close], " \t\r\n")
	sep := ", "
	if strings.HasSuffix(body, "{") {
		sep = ""
	}
	return body + sep + `"` + key + `": ` + val + obj[len(body):close] + "}", nil
}

// findValue finds the text span of a top-level key's value in a JSON object.
func findValue(obj, key string) (int, int, bool) {
	dec := json.NewDecoder(strings.NewReader(obj))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return 0, 0, false
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return 0, 0, false
		}
		k, _ := t.(string)
		before := int(dec.InputOffset())
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return 0, 0, false
		}
		after := int(dec.InputOffset())
		if k == key {
			s := strings.Index(obj[before:after], string(v))
			if s < 0 {
				return 0, 0, false
			}
			return before + s, before + s + len(v), true
		}
	}
	return 0, 0, false
}
