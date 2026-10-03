package tui

import (
	"strings"
	"unicode"

	"github.com/yongjohnlee80/golib/highlight"
)

func yamlSyntaxHighlighter() highlight.Highlighter {
	return highlight.HighlighterFunc(func(line string, previous highlight.State) ([]highlight.Span, highlight.State) {
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if previous > 0 && (strings.TrimSpace(line) == "" || indent >= int(previous)) {
			return []highlight.Span{{Start: 0, End: len(line), Style: highlight.String}}, previous
		}
		var spans []highlight.Span
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" || trimmed == "..." {
			return []highlight.Span{{Start: indent, End: len(line), Style: highlight.RegionMarker}}, 0
		}
		if strings.HasPrefix(trimmed, "#") {
			return []highlight.Span{{Start: indent, End: len(line), Style: highlight.Comment}}, 0
		}
		colon, comment, quote := -1, -1, byte(0)
		for index := 0; index < len(line); index++ {
			current := line[index]
			if quote != 0 {
				if current == quote && (index == 0 || line[index-1] != '\\') {
					quote = 0
				}
				continue
			}
			if current == '\'' || current == '"' {
				quote = current
				continue
			}
			if current == '#' && (index == 0 || unicode.IsSpace(rune(line[index-1]))) {
				comment = index
				break
			}
			if current == ':' && colon < 0 && (index+1 == len(line) || unicode.IsSpace(rune(line[index+1]))) {
				colon = index
			}
		}
		end := len(line)
		if comment >= 0 {
			end = comment
		}
		if colon >= indent {
			spans = append(spans, highlight.Span{Start: indent, End: colon, Style: highlight.Attribute})
			valueStart := colon + 1
			for valueStart < end && unicode.IsSpace(rune(line[valueStart])) {
				valueStart++
			}
			value := strings.TrimSpace(line[valueStart:end])
			if value != "" {
				style := highlight.Normal
				switch {
				case strings.HasPrefix(value, "\""), strings.HasPrefix(value, "'"):
					style = highlight.String
				case value == "true" || value == "false" || value == "null" || value == "~":
					style = highlight.Constant
				case value[0] >= '0' && value[0] <= '9', value[0] == '-':
					style = highlight.DecVal
				}
				if style != highlight.Normal {
					spans = append(spans, highlight.Span{Start: valueStart, End: end, Style: style})
				}
				if value == "|" || value == ">" || strings.HasPrefix(value, "|-") || strings.HasPrefix(value, ">-") {
					if comment >= 0 {
						spans = append(spans, highlight.Span{Start: comment, End: len(line), Style: highlight.Comment})
					}
					return spans, highlight.State(indent + 1)
				}
			}
		}
		if comment >= 0 {
			spans = append(spans, highlight.Span{Start: comment, End: len(line), Style: highlight.Comment})
		}
		return spans, 0
	})
}
