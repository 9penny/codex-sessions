package sessionindex

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	hanUnigramPrefix = "zh1"
	hanBigramPrefix  = "zh2"
	hanBoundaryToken = "zhboundary"
)

// cjkSearchTerms returns deterministic, search-only tokens for contiguous Han text. Runs
// become overlapping bigrams followed by unigrams; boundary tokens prevent phrase matching
// across punctuation. The fixed-width hexadecimal encoding keeps the unicode61 tokenizer
// from merging these terms with readable transcript text.
func cjkSearchTerms(text string) string {
	var terms []string
	var run []rune
	flush := func() {
		if len(run) == 0 {
			return
		}
		for i := 0; i+1 < len(run); i++ {
			terms = append(terms, encodeHanBigram(run[i], run[i+1]))
		}
		terms = append(terms, hanBoundaryToken)
		for _, r := range run {
			terms = append(terms, encodeHanUnigram(r))
		}
		terms = append(terms, hanBoundaryToken)
		run = run[:0]
	}
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			run = append(run, r)
		} else {
			flush()
		}
	}
	flush()
	return strings.Join(terms, " ")
}

// CJKQueryExpression returns an FTS5 expression for one contiguous Han query run. It uses
// the exact same tokenization as cjkSearchTerms and scopes the match to the search-only field.
func CJKQueryExpression(run string) string {
	runes := []rune(run)
	for _, r := range runes {
		if !unicode.Is(unicode.Han, r) {
			return ""
		}
	}
	var terms []string
	if len(runes) == 1 {
		terms = append(terms, encodeHanUnigram(runes[0]))
	} else {
		for i := 0; i+1 < len(runes); i++ {
			terms = append(terms, encodeHanBigram(runes[i], runes[i+1]))
		}
	}
	if len(terms) == 0 {
		return ""
	}
	phrase := strings.Join(terms, " ")
	return `{search_terms ai_search_terms}:"` + phrase + `"`
}

// CJKNeedlesFromQuery reconstructs the readable Han runs embedded in an FTS query so snippet
// generation can highlight source text rather than exposing hexadecimal search-only tokens.
func CJKNeedlesFromQuery(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var needles []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			needles = append(needles, string(current))
			current = nil
		}
	}
	for _, field := range fields {
		switch {
		case strings.HasPrefix(field, hanBigramPrefix) && len(field) == len(hanBigramPrefix)+12:
			left, right, ok := decodeHanPair(field[len(hanBigramPrefix):])
			if !ok {
				flush()
				continue
			}
			if len(current) == 0 {
				current = []rune{left, right}
			} else if current[len(current)-1] == left {
				current = append(current, right)
			} else {
				flush()
				current = []rune{left, right}
			}
		case strings.HasPrefix(field, hanUnigramPrefix) && len(field) == len(hanUnigramPrefix)+6:
			flush()
			if r, ok := decodeHanRune(field[len(hanUnigramPrefix):]); ok {
				needles = append(needles, string(r))
			}
		default:
			flush()
		}
	}
	flush()
	seen := make(map[string]bool, len(needles))
	unique := needles[:0]
	for _, needle := range needles {
		if !seen[needle] {
			seen[needle] = true
			unique = append(unique, needle)
		}
	}
	return unique
}

func encodeHanUnigram(r rune) string {
	return fmt.Sprintf("%s%06x", hanUnigramPrefix, r)
}

func encodeHanBigram(left, right rune) string {
	return fmt.Sprintf("%s%06x%06x", hanBigramPrefix, left, right)
}

func decodeHanPair(encoded string) (rune, rune, bool) {
	left, ok := decodeHanRune(encoded[:6])
	if !ok {
		return 0, 0, false
	}
	right, ok := decodeHanRune(encoded[6:])
	return left, right, ok
}

func decodeHanRune(encoded string) (rune, bool) {
	var value uint32
	if _, err := fmt.Sscanf(encoded, "%06x", &value); err != nil || !unicode.Is(unicode.Han, rune(value)) {
		return 0, false
	}
	return rune(value), true
}
