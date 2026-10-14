package reading

// The two readings of text the pairing rules are built on, and neither of them is
// this layer's own: whether a document is Chinese or English, and how much two
// materials look like the same book.
//
// Both are pure functions over strings, which is why they are here rather than
// among the rules in pairing.go: what language a text is in cannot be looked up
// (no column in this build records one, §0.3), and what makes two files look alike
// is a statement about names and lengths rather than about pairings. Keeping them
// apart is what lets the language rule be pinned by a table of texts instead of by
// a shelf of materials.

import (
	"strings"
	"unicode"
)

const (
	// The two languages this build can tell apart. A material in a third one is
	// not a candidate for anything, because "different from Chinese" and "English"
	// are not the same claim.
	languageChinese = "zh"
	languageEnglish = "en"

	// languageWindow is how much of a document is read to decide what language it
	// is written in, counted in characters from the beginning. It is a window and
	// not the whole text because a book says what language it is in on every page,
	// and it is the opening because that is where a translation prints its title.
	languageWindow int64 = 2048

	// languageMinRunes is how many characters of one script a window needs before
	// it is allowed to count as evidence. Below it the window is one that does not
	// say, which is a different answer from either language.
	languageMinRunes = 32

	// languageKanaShare is how much kana it takes to refuse a text. Japanese is
	// written with Han characters and would otherwise be read as Chinese, so kana
	// at a sixteenth of the Han count is already enough to make the window silent
	// rather than Chinese.
	languageKanaShare = 16

	// The two weights the recommendation's order is made of: what the two filenames
	// have in common, and how close the two texts are in length. They are this
	// build's, and they are a recipe for an order rather than a verdict — which is
	// exactly what the requirement leaves to the reader to confirm (§2.10).
	pairingNameWeight   = 0.6
	pairingLengthWeight = 0.4
)

// pairingScore is how strongly one material suggests another as its counterpart.
//
// The two terms are what the design names as the recommendation's ingredients —
// a similar title or filename, and a text of the same order of magnitude. The
// language is not a term: it is a condition, and a material in the same language
// never reaches this function.
func pairingScore(oneName, otherName string, oneChars, otherChars int64) float64 {
	return pairingNameWeight*nameSimilarity(oneName, otherName) +
		pairingLengthWeight*lengthCloseness(oneChars, otherChars)
}

// nameSimilarity is what two filenames have in common, as the Jaccard index of
// their words: one for the same words, zero for none.
//
// Words rather than characters, because the two editions of one book are usually
// named the same way in a file library — `SICP-zh.md` beside `SICP-en.md` — and
// the one word they do not share is the one that says which language each is in.
func nameSimilarity(one, other string) float64 {
	first, second := nameTokens(one), nameTokens(other)
	if len(first) == 0 || len(second) == 0 {
		return 0
	}
	shared := 0
	for token := range first {
		if _, ok := second[token]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(first)+len(second)-shared)
}

// nameTokens splits a filename into the words it is made of: runs of letters and
// digits, lowercased. A run of Han characters comes back as one word, which is
// what makes 《算法笔记》 and 算法笔记.md share one.
func nameTokens(name string) map[string]struct{} {
	tokens := make(map[string]struct{})
	var current []rune
	flush := func() {
		if len(current) > 0 {
			tokens[strings.ToLower(string(current))] = struct{}{}
			current = nil
		}
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

// lengthCloseness is how nearly two texts are the same size, as the smaller over
// the larger: one for two texts of the same length, falling off as one of them
// grows.
//
// It is a term in the score and not a filter, because a translation is rarely the
// same length as its original and "同一量级" is a statement about magnitude rather
// than about equality.
func lengthCloseness(one, other int64) float64 {
	if one <= 0 || other <= 0 {
		return 0
	}
	if one > other {
		one, other = other, one
	}
	return float64(one) / float64(other)
}

// languageOf reads one document's language off its own text.
//
// Nothing in this build records what language a material is in. The reference
// implementation reads `dc:language` out of the EPUB's OPF; the materials here
// are documents, not packages, and no column holds a language (§0.3). So the
// language is derived from the text, and derived narrowly: this function answers
// for the two scripts it can tell apart and returns the empty string for
// everything else. "Chinese, or English, or I cannot tell" is an honest answer
// where "some language" would be a guess, and the pairing rules only ever need to
// know whether two materials differ.
func languageOf(text string) string {
	var han, latin, kana int
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
		case isKana(r):
			kana++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	// Japanese is written with Han characters, so it would be read as Chinese
	// without this refusal, and a Japanese book offered as the Chinese half of a
	// pair is a wrong answer rather than a missing one.
	if kana > 0 && kana*languageKanaShare >= han {
		return ""
	}
	if han >= languageMinRunes && han >= latin {
		return languageChinese
	}
	if latin >= languageMinRunes && latin > han {
		return languageEnglish
	}
	return ""
}

// isKana reports whether a character is kana. The two scripts are listed rather
// than asked for as one, because the standard library has no single name for
// them.
func isKana(r rune) bool {
	return unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}
