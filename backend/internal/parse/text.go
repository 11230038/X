package parse

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

// textParser reads a file that is already text.
//
// It has no dependencies and no configuration, and it is the reason the service
// can read anything at all with nothing else installed: a markdown file needs a
// decoder, not a service.
type textParser struct{}

var _ Parser = textParser{}

func (textParser) Name() string { return EngineText }

func (textParser) Supports(category string) bool { return category == CategoryMarkdown }

func (p textParser) Parse(ctx context.Context, req Request) (Result, error) {
	raw, err := readChars(ctx, req.Content, req.MaxChars)
	if err != nil {
		// Cancellation is not a document that failed to parse: the caller gave
		// up, and it must be able to tell that apart from a broken file.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{}, ctxErr
		}
		return Result{}, &Error{
			Code:    CodeParseFailed,
			Engine:  p.Name(),
			Message: "the file could not be read",
			Cause:   err,
		}
	}
	text := normalizeText(raw)
	if !utf8.ValidString(text) {
		// Replacing the undecodable bytes would store text that is not what the
		// user wrote, and every offset after the replacement would point at the
		// wrong place. Refusing is the honest answer, and it is one the user can
		// act on.
		return Result{}, &Error{
			Code:    CodeParseFailed,
			Engine:  p.Name(),
			Message: "the file is not UTF-8 text; convert it to UTF-8 and upload it again",
		}
	}
	return Result{Text: text, Engine: p.Name()}, nil
}

// readChars reads at most maxChars+1 characters from source.
//
// One character past the limit and not up to it: the registry decides whether the
// document was too long, and it can only decide that if the engine shows it that
// there was more. The byte ceiling derived from the character limit is what keeps
// a stream that never ends from exhausting memory.
func readChars(ctx context.Context, source io.Reader, maxChars int) (string, error) {
	if source == nil {
		return "", errors.New("no content")
	}
	ceiling := byteCeiling(maxChars)
	raw, err := io.ReadAll(io.LimitReader(source, ceiling))
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	text := string(raw)
	if int64(len(raw)) == ceiling {
		// The read stopped because of the byte ceiling rather than because the
		// file ended, so a character cut in half at the end is this reader's
		// doing and not the file's. Dropping it keeps a document that is too
		// long from being refused as though it were not UTF-8 at all — which is
		// what a Chinese or emoji document over the limit would be reported as
		// otherwise. Enough whole characters remain: the ceiling is four bytes
		// per character, so what is left is at least the one past the limit.
		text = trimPartialRune(text)
		// And the answer is counted in characters, like the limit that produced
		// it, rather than being however many fitted in four bytes each: what
		// comes back should not depend on how wide the file's characters are.
		// The addition is guarded because a limit of MaxInt means no limit, and
		// overflowing it into a negative count would return nothing at all.
		if maxChars < math.MaxInt {
			text, _ = limitChars(text, maxChars+1)
		}
	}
	return text, nil
}

// trimPartialRune removes a character that is only partly there at the end of
// text. Text that ends in a complete character, or in bytes that are invalid for
// some other reason, is returned unchanged: this only takes back a cut it might
// have made.
func trimPartialRune(text string) string {
	for index := len(text) - 1; index >= 0 && len(text)-index <= utf8.UTFMax; index-- {
		if text[index]&0xC0 == 0x80 {
			continue
		}
		if !utf8.FullRune([]byte(text[index:])) {
			return text[:index]
		}
		return text
	}
	return text
}

// normalizeText makes the file's own spelling of line endings uniform, and drops
// a byte order mark if one is there.
//
// Both are about offsets. A carriage return that is stored becomes a character
// every offset past it counts, and the text a user is shown is not what they
// wrote; a byte order mark in front of the first character does the same to
// every offset in the document. Normalising here, once, means the stored text is
// the text every later stage measures.
func normalizeText(text string) string {
	text = strings.TrimPrefix(text, "\ufeff")
	if !strings.ContainsRune(text, '\r') {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}
