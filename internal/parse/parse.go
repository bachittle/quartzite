// Package parse handles the small amount of text analysis Quartzite does
// server-side: frontmatter, excerpts, and stripping markup for search.
//
// Rendering markdown is deliberately not here. Viewers are sandboxed HTML
// documents, so the markdown viewer renders in the browser and the backend
// never needs a markdown library.
package parse

import (
	"strings"
	"unicode"
)

// Frontmatter splits a leading YAML block from the body. It understands the
// subset that actually appears in a vault: scalars, inline lists, and dash
// lists. Anything more exotic is passed over rather than erroring.
func Frontmatter(src string) (map[string]any, string) {
	s := strings.TrimPrefix(src, "\uFEFF")
	if !strings.HasPrefix(s, "---") {
		return nil, src
	}
	rest := s[3:]
	if i := strings.IndexAny(rest, "\r\n"); i < 0 {
		return nil, src
	} else {
		rest = rest[i+1:]
	}
	end := findCloser(rest)
	if end.start < 0 {
		return nil, src
	}
	block, body := rest[:end.start], rest[end.next:]

	meta := map[string]any{}
	var listKey string
	var list []string
	flush := func() {
		if listKey != "" {
			meta[listKey] = append([]string(nil), list...)
			listKey, list = "", nil
		}
	}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "- ") && listKey != "" {
			list = append(list, unquote(strings.TrimSpace(line)[2:]))
			continue
		}
		flush()
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch {
		case val == "":
			listKey = key
		case strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]"):
			inner := strings.TrimSuffix(strings.TrimPrefix(val, "["), "]")
			var items []string
			for _, p := range strings.Split(inner, ",") {
				if p = strings.TrimSpace(p); p != "" {
					items = append(items, unquote(p))
				}
			}
			meta[key] = items
		default:
			meta[key] = unquote(val)
		}
	}
	flush()
	return meta, body
}

type closer struct{ start, next int }

// findCloser locates the terminating --- of a frontmatter block.
func findCloser(s string) closer {
	offset := 0
	for {
		line := s[offset:]
		nl := strings.IndexByte(line, '\n')
		var cur string
		if nl < 0 {
			cur = line
		} else {
			cur = line[:nl]
		}
		if strings.TrimSpace(strings.TrimRight(cur, "\r")) == "---" {
			if nl < 0 {
				return closer{offset, len(s)}
			}
			return closer{offset, offset + nl + 1}
		}
		if nl < 0 {
			return closer{-1, -1}
		}
		offset += nl + 1
		if offset >= len(s) {
			return closer{-1, -1}
		}
	}
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// StringField reads a scalar frontmatter value.
func StringField(meta map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := meta[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// ListField reads a list frontmatter value, tolerating a comma-separated scalar.
func ListField(meta map[string]any, keys ...string) []string {
	for _, k := range keys {
		v, ok := meta[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case []string:
			return t
		case string:
			var out []string
			for _, p := range strings.Split(t, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			return out
		}
	}
	return nil
}

// Excerpt produces a short plain-text preview, stripping the markup that would
// otherwise dominate a one-line summary.
func Excerpt(body string, max int) string {
	s := body
	if strings.Contains(s, "<") {
		s = stripTags(s)
	}
	var b strings.Builder
	skipLine := func(l string) bool {
		t := strings.TrimSpace(l)
		return t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") ||
			strings.HasPrefix(t, "---") || strings.HasPrefix(t, "|")
	}
	for _, line := range strings.Split(s, "\n") {
		if skipLine(line) {
			continue
		}
		b.WriteString(strings.TrimSpace(line))
		b.WriteByte(' ')
		if b.Len() > max*2 {
			break
		}
	}
	out := strings.Join(strings.Fields(cleanMarkup(b.String())), " ")
	if len(out) > max {
		cut := out[:max]
		if i := strings.LastIndexByte(cut, ' '); i > max/2 {
			cut = cut[:i]
		}
		out = cut + "…"
	}
	return out
}

// StripCode removes fenced blocks and inline code spans so that link and tag
// extraction do not treat documentation *about* syntax as uses of it. Without
// this, a note explaining [[wikilinks]] manufactures broken links.
func StripCode(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	lines := strings.Split(s, "\n")
	inFence := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			b.WriteByte('\n')
			continue
		}
		if inFence {
			b.WriteByte('\n')
			continue
		}
		// Drop inline code spans, keeping surrounding text intact.
		out := line
		for {
			i := strings.IndexByte(out, '`')
			if i < 0 {
				break
			}
			j := strings.IndexByte(out[i+1:], '`')
			if j < 0 {
				out = out[:i] + out[i+1:]
				break
			}
			out = out[:i] + " " + out[i+1+j+1:]
		}
		b.WriteString(out)
		b.WriteByte('\n')
	}
	return b.String()
}

func cleanMarkup(s string) string {
	r := strings.NewReplacer("[[", "", "]]", "", "**", "", "__", "", "`", "", "> ", "")
	return r.Replace(s)
}

// stripTags removes HTML tags plus the contents of script and style elements.
func stripTags(s string) string {
	for _, tag := range []string{"script", "style"} {
		for {
			lo := strings.Index(strings.ToLower(s), "<"+tag)
			if lo < 0 {
				break
			}
			hi := strings.Index(strings.ToLower(s[lo:]), "</"+tag+">")
			if hi < 0 {
				s = s[:lo]
				break
			}
			s = s[:lo] + s[lo+hi+len(tag)+3:]
		}
	}
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	out := b.String()
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, out)
}
