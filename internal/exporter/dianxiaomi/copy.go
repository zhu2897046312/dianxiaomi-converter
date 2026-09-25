package dianxiaomi

import (
	"hash/fnv"
	"html"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

// Only display copy is cleaned. Identifiers, URLs and the shared product model
// remain unchanged, so exporting Medusa from the same products preserves emoji.
func cleanCopy(fields map[string]string, descriptionImages []string, seed string) {
	for _, key := range []string{"*产品标题", "*英文标题", "*变种属性名称一", "*变种属性值一", "变种属性名称二", "变种属性值二", "包装清单", "敏感属性值", "产地"} {
		fields[key] = stripEmoji(fields[key])
	}
	fields["产品描述"] = cleanDescription(fields["产品描述"], descriptionImages, seed)
}

func cleanProductCode(s string) string {
	var out strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			out.WriteRune(r)
		}
	}
	return out.String()
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

var imageAttributePattern = regexp.MustCompile(`(?i)(\b(?:src|data-src|srcset|data-srcset)\s*=\s*)(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
var attributeURLPattern = regexp.MustCompile(`(?i)https?://[^\s,]+`)

func gifURL(s string) bool {
	u, err := url.Parse(html.UnescapeString(s))
	return err == nil && strings.EqualFold(pathExtension(u.Path), ".gif")
}

func pathExtension(path string) string {
	if dot := strings.LastIndexByte(path, '.'); dot >= 0 && dot > strings.LastIndexByte(path, '/') {
		return path[dot:]
	}
	return ""
}

func replacementPool(images []string) []string {
	var all, still []string
	seen := map[string]bool{}
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" || seen[image] {
			continue
		}
		seen[image] = true
		all = append(all, image)
		if !gifURL(image) {
			still = append(still, image)
		}
	}
	if len(still) > 0 {
		return still
	}
	return all
}

func chooseDescriptionImage(images []string, seed, old string, occurrence int) string {
	if len(images) == 0 {
		return old
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(old))
	_, _ = h.Write([]byte{byte(occurrence), byte(occurrence >> 8), byte(occurrence >> 16), byte(occurrence >> 24)})
	return images[h.Sum64()%uint64(len(images))]
}

func replaceGIFAttributes(raw string, images []string, seed string, occurrence *int) string {
	return imageAttributePattern.ReplaceAllStringFunc(raw, func(attribute string) string {
		parts := imageAttributePattern.FindStringSubmatch(attribute)
		value, quote := parts[2], `"`
		if value == "" {
			value, quote = parts[3], `'`
		}
		if value == "" {
			value, quote = parts[4], ""
		}
		changed := false
		value = attributeURLPattern.ReplaceAllStringFunc(value, func(candidate string) string {
			if !gifURL(candidate) {
				return candidate
			}
			changed = true
			replacement := chooseDescriptionImage(images, seed, candidate, *occurrence)
			*occurrence++
			return replacement
		})
		if !changed {
			return attribute
		}
		return parts[1] + quote + value + quote
	})
}

func cleanDescription(s string, images []string, seed string) string {
	z := xhtml.NewTokenizer(strings.NewReader(s))
	images = replacementPool(images)
	occurrence := 0
	var out strings.Builder
	for {
		typ := z.Next()
		raw := string(z.Raw())
		if typ == xhtml.ErrorToken {
			if z.Err() != io.EOF {
				return s
			}
			out.WriteString(raw)
			return out.String()
		}
		if typ == xhtml.TextToken {
			raw = stripEmoji(raw)
		} else if typ == xhtml.StartTagToken || typ == xhtml.SelfClosingTagToken {
			t := z.Token()
			if t.Data == "img" || t.Data == "source" {
				raw = replaceGIFAttributes(raw, images, seed, &occurrence)
			}
		}
		// Other tags and all non-image URL attributes remain byte-for-byte unchanged.
		out.WriteString(raw)
	}
}
