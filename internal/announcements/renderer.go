package announcements

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxAnnouncementBodyBytes  = 64 * 1024
	maxAnnouncementTitleRunes = 160
	maxExcerptRunes           = 240
)

var orderedListPrefix = regexp.MustCompile(`^[0-9]{1,9}\. `)

type renderedMarkdown struct {
	html  string
	plain string
}

type markdownRenderer struct{}

func (markdownRenderer) render(source string) (renderedMarkdown, error) {
	if !validMarkdownSource(source) {
		return renderedMarkdown{}, ErrInvalidRequest
	}
	source = strings.ReplaceAll(source, "\r\n", "\n")
	lines := strings.Split(source, "\n")
	var out strings.Builder
	plainParts := make([]string, 0, len(lines))
	for index := 0; index < len(lines); {
		line := lines[index]
		if strings.TrimSpace(line) == "" {
			index++
			continue
		}
		if strings.HasPrefix(line, "```") {
			end, block, text, ok := renderFence(lines, index)
			if !ok {
				return renderedMarkdown{}, ErrInvalidRequest
			}
			out.WriteString(block)
			if text != "" {
				plainParts = append(plainParts, text)
			}
			index = end
			continue
		}
		if level, content, heading := parseHeading(line); heading {
			rendered, plain, err := renderInline(content, true)
			if err != nil || strings.TrimSpace(plain) == "" {
				return renderedMarkdown{}, ErrInvalidRequest
			}
			out.WriteString("<h")
			out.WriteByte(byte('0' + level))
			out.WriteByte('>')
			out.WriteString(rendered)
			out.WriteString("</h")
			out.WriteByte(byte('0' + level))
			out.WriteString(">\n")
			plainParts = append(plainParts, plain)
			index++
			continue
		}
		if startsUnsupportedHeading(line) || isThematicBreak(line) || startsIndentedCode(line) {
			return renderedMarkdown{}, ErrInvalidRequest
		}
		if strings.HasPrefix(line, ">") {
			end, block, text, err := renderQuote(lines, index)
			if err != nil {
				return renderedMarkdown{}, err
			}
			out.WriteString(block)
			plainParts = append(plainParts, text)
			index = end
			continue
		}
		if _, _, unordered := parseUnorderedItem(line); unordered {
			end, block, text, err := renderList(lines, index, false)
			if err != nil {
				return renderedMarkdown{}, err
			}
			out.WriteString(block)
			plainParts = append(plainParts, text...)
			index = end
			continue
		}
		if _, ordered := parseOrderedItem(line); ordered {
			end, block, text, err := renderList(lines, index, true)
			if err != nil {
				return renderedMarkdown{}, err
			}
			out.WriteString(block)
			plainParts = append(plainParts, text...)
			index = end
			continue
		}

		paragraph := make([]string, 0, 4)
		for index < len(lines) {
			candidate := lines[index]
			if strings.TrimSpace(candidate) == "" || strings.HasPrefix(candidate, "```") ||
				strings.HasPrefix(candidate, ">") || isBlockListItem(candidate) {
				break
			}
			if _, _, heading := parseHeading(candidate); heading || startsUnsupportedHeading(candidate) ||
				isThematicBreak(candidate) || startsIndentedCode(candidate) {
				break
			}
			paragraph = append(paragraph, candidate)
			index++
		}
		if len(paragraph) == 0 {
			return renderedMarkdown{}, ErrInvalidRequest
		}
		out.WriteString("<p>")
		for lineIndex, candidate := range paragraph {
			rendered, plain, err := renderInline(candidate, true)
			if err != nil {
				return renderedMarkdown{}, err
			}
			if lineIndex > 0 {
				out.WriteString("<br>\n")
			}
			out.WriteString(rendered)
			if strings.TrimSpace(plain) != "" {
				plainParts = append(plainParts, plain)
			}
		}
		out.WriteString("</p>\n")
	}
	plain := normalizePlain(strings.Join(plainParts, " "))
	return renderedMarkdown{html: out.String(), plain: truncateRunes(plain, maxExcerptRunes)}, nil
}

func validMarkdownSource(source string) bool {
	if len(source) > maxAnnouncementBodyBytes || !utf8.ValidString(source) || strings.Contains(source, "\x00") {
		return false
	}
	for index, value := range source {
		if value == '\r' {
			if index+1 >= len(source) || source[index+1] != '\n' {
				return false
			}
			continue
		}
		if unicode.IsControl(value) && value != '\n' && value != '\t' {
			return false
		}
	}
	return true
}

func validTitle(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxAnnouncementTitleRunes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	return true
}

func renderFence(lines []string, start int) (int, string, string, bool) {
	opener := strings.TrimSpace(lines[start])
	if !strings.HasPrefix(opener, "```") || strings.Contains(opener[3:], "`") {
		return start, "", "", false
	}
	for _, value := range opener[3:] {
		if unicode.IsControl(value) || value == '<' || value == '>' {
			return start, "", "", false
		}
	}
	content := make([]string, 0, 8)
	for index := start + 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "```" {
			plain := strings.Join(content, "\n")
			return index + 1, "<pre><code>" + html.EscapeString(plain) + "</code></pre>\n", plain, true
		}
		content = append(content, lines[index])
	}
	return start, "", "", false
}

func parseHeading(line string) (int, string, bool) {
	count := 0
	for count < len(line) && line[count] == '#' {
		count++
	}
	if count < 2 || count > 4 || len(line) <= count || line[count] != ' ' {
		return 0, "", false
	}
	return count, strings.TrimSpace(line[count+1:]), true
}

func startsUnsupportedHeading(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	count := 0
	for count < len(line) && line[count] == '#' {
		count++
	}
	return len(line) > count && line[count] == ' ' && (count < 2 || count > 4)
}

func startsIndentedCode(line string) bool {
	return strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")
}

func isThematicBreak(line string) bool {
	compact := strings.ReplaceAll(strings.TrimSpace(line), " ", "")
	if len(compact) < 3 {
		return false
	}
	for _, marker := range []byte{'-', '*', '_'} {
		if strings.Trim(compact, string(marker)) == "" {
			return true
		}
	}
	return false
}

func renderQuote(lines []string, start int) (int, string, string, error) {
	parts := make([]string, 0, 4)
	plainParts := make([]string, 0, 4)
	index := start
	for index < len(lines) && strings.HasPrefix(lines[index], ">") {
		line := strings.TrimPrefix(lines[index], ">")
		if strings.HasPrefix(line, ">") {
			return start, "", "", ErrInvalidRequest
		}
		line = strings.TrimPrefix(line, " ")
		rendered, plain, err := renderInline(line, true)
		if err != nil {
			return start, "", "", err
		}
		parts = append(parts, rendered)
		plainParts = append(plainParts, plain)
		index++
	}
	return index, "<blockquote><p>" + strings.Join(parts, "<br>\n") + "</p></blockquote>\n", normalizePlain(strings.Join(plainParts, " ")), nil
}

func parseUnorderedItem(line string) (byte, string, bool) {
	if len(line) < 2 || line[1] != ' ' || (line[0] != '-' && line[0] != '*' && line[0] != '+') {
		return 0, "", false
	}
	return line[0], line[2:], true
}

func parseOrderedItem(line string) (string, bool) {
	prefix := orderedListPrefix.FindString(line)
	if prefix == "" {
		return "", false
	}
	return line[len(prefix):], true
}

func isBlockListItem(line string) bool {
	_, _, unordered := parseUnorderedItem(line)
	_, ordered := parseOrderedItem(line)
	return unordered || ordered
}

func renderList(lines []string, start int, ordered bool) (int, string, []string, error) {
	index := start
	items := make([]string, 0, 4)
	plain := make([]string, 0, 4)
	for index < len(lines) {
		var content string
		var ok bool
		if ordered {
			content, ok = parseOrderedItem(lines[index])
		} else {
			_, content, ok = parseUnorderedItem(lines[index])
		}
		if !ok {
			break
		}
		rendered, text, err := renderInline(content, true)
		if err != nil || strings.TrimSpace(text) == "" {
			return start, "", nil, ErrInvalidRequest
		}
		items = append(items, "<li>"+rendered+"</li>")
		plain = append(plain, text)
		index++
	}
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	return index, "<" + tag + ">\n" + strings.Join(items, "\n") + "\n</" + tag + ">\n", plain, nil
}

func renderInline(source string, allowLinks bool) (string, string, error) {
	var out strings.Builder
	var plain strings.Builder
	for index := 0; index < len(source); {
		switch {
		case source[index] == '<' || source[index] == '>':
			return "", "", ErrInvalidRequest
		case strings.HasPrefix(source[index:], "!["):
			return "", "", ErrInvalidRequest
		case source[index] == '\\' && index+1 < len(source):
			_, size := utf8.DecodeRuneInString(source[index+1:])
			value := source[index+1 : index+1+size]
			out.WriteString(html.EscapeString(value))
			plain.WriteString(value)
			index += 1 + size
		case source[index] == '`':
			end := strings.IndexByte(source[index+1:], '`')
			if end < 0 {
				return "", "", ErrInvalidRequest
			}
			end += index + 1
			value := source[index+1 : end]
			out.WriteString("<code>" + html.EscapeString(value) + "</code>")
			plain.WriteString(value)
			index = end + 1
		case allowLinks && source[index] == '[':
			labelEnd := strings.Index(source[index+1:], "](")
			if labelEnd < 0 {
				out.WriteByte('[')
				plain.WriteByte('[')
				index++
				continue
			}
			labelEnd += index + 1
			urlStart := labelEnd + 2
			urlEnd := strings.IndexByte(source[urlStart:], ')')
			if urlEnd < 0 {
				return "", "", ErrInvalidRequest
			}
			urlEnd += urlStart
			href := source[urlStart:urlEnd]
			if !validHTTPLink(href) {
				return "", "", ErrInvalidRequest
			}
			labelHTML, labelPlain, err := renderInline(source[index+1:labelEnd], false)
			if err != nil || labelPlain == "" {
				return "", "", ErrInvalidRequest
			}
			out.WriteString(`<a href="` + html.EscapeString(href) + `" rel="noopener noreferrer">` + labelHTML + `</a>`)
			plain.WriteString(labelPlain)
			index = urlEnd + 1
		case strings.HasPrefix(source[index:], "**") || strings.HasPrefix(source[index:], "__"):
			delim := source[index : index+2]
			end := strings.Index(source[index+2:], delim)
			if end < 0 {
				out.WriteString(html.EscapeString(delim))
				plain.WriteString(delim)
				index += 2
				continue
			}
			end += index + 2
			innerHTML, innerPlain, err := renderInline(source[index+2:end], false)
			if err != nil || innerPlain == "" {
				return "", "", ErrInvalidRequest
			}
			out.WriteString("<strong>" + innerHTML + "</strong>")
			plain.WriteString(innerPlain)
			index = end + 2
		case source[index] == '*' || source[index] == '_':
			delim := source[index]
			end := strings.IndexByte(source[index+1:], delim)
			if end < 0 {
				out.WriteByte(delim)
				plain.WriteByte(delim)
				index++
				continue
			}
			end += index + 1
			innerHTML, innerPlain, err := renderInline(source[index+1:end], false)
			if err != nil || innerPlain == "" {
				return "", "", ErrInvalidRequest
			}
			out.WriteString("<em>" + innerHTML + "</em>")
			plain.WriteString(innerPlain)
			index = end + 1
		default:
			_, size := utf8.DecodeRuneInString(source[index:])
			value := source[index : index+size]
			out.WriteString(html.EscapeString(value))
			plain.WriteString(value)
			index += size
		}
	}
	return out.String(), plain.String(), nil
}

func validHTTPLink(value string) bool {
	if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\t\r\n <>\\") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "http" || scheme == "https"
}

func normalizePlain(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
