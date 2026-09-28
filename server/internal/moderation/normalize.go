package moderation

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// normalize folds text to the form the banned-term filter matches against.
//
// A plain lower-cased strings.Contains is defeated by anything a motivated user
// types in ten seconds: full-width or accented look-alikes (ｆｏｏ, fóo), a
// zero-width space in the middle of the word, digit substitutions (f00), or just
// punctuation between the letters (f.o.o). Each of those is the same word to
// every human reader, so matching has to see them as the same word too.
//
// The steps, in order, and why each is needed:
//
//   - NFKC folds compatibility forms — full-width, ligatures, superscripts — onto
//     their plain equivalents, so ｆｏｏ and ﬀ stop being distinct alphabets.
//   - Combining marks are dropped after decomposition, so fóo folds to foo.
//   - Format characters (zero-width space/joiner, soft hyphen, bidi marks) are
//     removed: they are invisible, so they carry no meaning worth preserving and
//     exist here only to break a substring match.
//   - Confusable Cyrillic/Greek letters that are visually identical to Latin ones
//     are mapped to Latin. Deliberately limited to the unambiguous homoglyphs —
//     a broad transliteration would mangle ordinary Russian text, which this
//     server carries as a first-class language.
//   - Common digit-for-letter substitutions are undone (0→o, 1→i, 3→e, 4→a, 5→s,
//     7→t, @→a, $→s).
//   - Everything that is not a letter or digit is dropped entirely, which is what
//     makes f.o.o and f o o match. This is the step that widens false positives
//     (the Scunthorpe problem), and it is acceptable precisely because this filter
//     is advisory: it records an abuse event for review, it does not block a send.
//
// Both the message and the configured terms pass through this, so the comparison
// is always like-for-like.
func normalize(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFKD.String(strings.ToLower(norm.NFKC.String(s))) {
		switch {
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Cf, r):
			// Combining mark or invisible format character: contributes nothing.
			continue
		case unicode.IsLetter(r):
			if latin, ok := homoglyphs[r]; ok {
				b.WriteRune(latin)
				continue
			}
			b.WriteRune(r)
		case unicode.IsDigit(r):
			if letter, ok := leetDigits[r]; ok {
				b.WriteRune(letter)
				continue
			}
			b.WriteRune(r)
		default:
			if letter, ok := leetSymbols[r]; ok {
				b.WriteRune(letter)
			}
			// Any other punctuation, space or symbol is a separator: dropped.
		}
	}
	return b.String()
}

// homoglyphs maps Cyrillic and Greek letters that are visually indistinguishable
// from a Latin letter onto that letter. Only the unambiguous ones: mapping, say,
// Cyrillic "и" to "u" would corrupt Russian text for no gain, because nobody
// disguises a Latin word with it.
var homoglyphs = map[rune]rune{
	'а': 'a', 'в': 'b', 'е': 'e', 'к': 'k', 'м': 'm', 'н': 'h', 'о': 'o',
	'р': 'p', 'с': 'c', 'т': 't', 'у': 'y', 'х': 'x',
	'ѕ': 's', 'і': 'i', 'ј': 'j', 'ԁ': 'd', 'ԛ': 'q', 'ԝ': 'w',
	'α': 'a', 'β': 'b', 'ε': 'e', 'ι': 'i', 'κ': 'k', 'ο': 'o', 'ρ': 'p',
	'τ': 't', 'υ': 'y', 'χ': 'x', 'ν': 'v',
}

// leetDigits undoes digit-for-letter substitution.
var leetDigits = map[rune]rune{
	'0': 'o', '1': 'i', '3': 'e', '4': 'a', '5': 's', '7': 't',
}

// leetSymbols undoes symbol-for-letter substitution.
var leetSymbols = map[rune]rune{
	'@': 'a', '$': 's', '!': 'i',
}
