package dianxiaomi

import (
	"html"
	"sort"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

// Only display copy is cleaned. Identifiers, URLs and the shared product model
// remain unchanged, so exporting Medusa from the same products preserves emoji.
func cleanCopy(fields map[string]string) {
	for _, key := range []string{"*产品标题", "*英文标题", "*变种属性名称一", "*变种属性值一", "变种属性名称二", "变种属性值二", "包装清单", "敏感属性值", "产地"} {
		fields[key] = stripEmoji(fields[key])
	}
	fields["产品描述"] = cleanDescription(fields["产品描述"])
}

func isEmoji(r rune) bool {
	i := sort.Search(len(emojiRanges), func(i int) bool { return emojiRanges[i][1] >= r })
	return i < len(emojiRanges) && emojiRanges[i][0] <= r
}

// Decode entities only for classification, preserving original spelling of
// retained text (including &amp; and numeric entities).
func stripEmoji(s string) string {
	type unit struct {
		r   rune
		raw string
	}
	var units []unit
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if r == '&' {
			if end := strings.IndexByte(s[:min(len(s), 40)], ';'); end >= 0 {
				decoded := html.UnescapeString(s[:end+1])
				if utf8.RuneCountInString(decoded) == 1 {
					r, _ = utf8.DecodeRuneInString(decoded)
					n = end + 1
				}
			}
		}
		units = append(units, unit{r, s[:n]})
		s = s[n:]
	}
	var out strings.Builder
	for i := 0; i < len(units); {
		r := units[i].r
		end := i + 1
		remove := isEmoji(r)
		if r == '#' || r == '*' || (r >= '0' && r <= '9') {
			if end < len(units) && units[end].r == 0xfe0f {
				end++
			}
			remove = end < len(units) && units[end].r == 0x20e3
			if remove {
				end++
			}
		}
		// Ordinary copyright/registered/trademark symbols are not decorative emoji.
		if r == '©' || r == '®' || r == '™' {
			remove = end < len(units) && units[end].r == 0xfe0f
		}
		if !remove {
			out.WriteString(units[i].raw)
			i++
			continue
		}
		for end < len(units) {
			r = units[end].r
			if r == 0xfe0e || r == 0xfe0f || (r >= 0xe0020 && r <= 0xe007f) || (r >= 0x1f3fb && r <= 0x1f3ff) {
				end++
				continue
			}
			if r == 0x200d && end+1 < len(units) && isEmoji(units[end+1].r) {
				end++
				break
			}
			break
		}
		i = end
	}
	return out.String()
}

func cleanDescription(s string) string {
	z := xhtml.NewTokenizer(strings.NewReader(s))
	var out strings.Builder
	for {
		typ := z.Next()
		raw := string(z.Raw())
		if typ == xhtml.ErrorToken {
			out.WriteString(raw)
			return out.String()
		}
		if typ == xhtml.TextToken {
			raw = stripEmoji(raw)
		}
		// Preserve tags and attributes verbatim, in particular image and link URLs.
		out.WriteString(raw)
	}
}
