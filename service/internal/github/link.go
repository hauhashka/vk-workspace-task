package github

import (
	"net/url"
	"strings"
)

// NextLink извлекает адрес rel="next" из заголовка Link (RFC 8288)
// и разрешает его относительно адреса запроса. Пустая строка - следующей страницы нет.
func NextLink(header, base string) string {
	for _, part := range splitLinks(header) {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "<") {
			continue
		}
		end := strings.Index(part, ">")
		if end < 0 {
			continue
		}
		target := part[1:end]
		for _, param := range strings.Split(part[end+1:], ";") {
			key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "rel") {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), `"`)
			for _, rel := range strings.Fields(value) {
				if strings.EqualFold(rel, "next") {
					return resolve(base, target)
				}
			}
		}
	}
	return ""
}

// splitLinks делит значение по запятым, не заходя внутрь <...>.
func splitLinks(header string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range header {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, header[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, header[start:])
}

func resolve(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}
