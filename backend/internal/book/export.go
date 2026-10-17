package book

import (
	"fmt"
	"strings"
)

// buildExport renders generated chapters as one Markdown document.
//
// Only ready chapters are passed in. A pending or failed chapter is a gap the
// book does not have text for, and writing a heading with nothing under it
// would make the export look broken rather than partial.
func buildExport(summary Summary, chapters []Chapter) Export {
	var document strings.Builder
	document.WriteString("# ")
	document.WriteString(oneLine(summary.Title))
	document.WriteString("\n\n")

	source := sourceKindLabel(summary.SourceKind)
	if summary.SourceTitle != "" {
		source += "《" + oneLine(summary.SourceTitle) + "》"
	}
	document.WriteString("> 来源：")
	document.WriteString(source)
	document.WriteString("\n")

	for _, chapter := range chapters {
		document.WriteString("\n---\n\n")
		document.WriteString(fmt.Sprintf("## 第 %d 章 %s\n\n", chapter.Ordinal, oneLine(chapter.Title)))
		document.WriteString(strings.TrimSpace(chapter.Content))
		document.WriteString("\n")
	}

	return Export{
		Filename: exportFilename(summary.Title),
		Content:  document.String(),
	}
}

// exportFilename turns a book title into a file name.
//
// The characters are Windows' illegal set rather than one platform's, because
// the download is offered to whoever is reading, and a name that saves on Linux
// and not on Windows is a bug one reader finds. The fallback is used when the
// title consisted entirely of characters that had to go.
func exportFilename(title string) string {
	var name strings.Builder
	for _, character := range title {
		switch {
		case character < 0x20:
			continue
		case strings.ContainsRune(`<>:"/\|?*`, character):
			name.WriteRune('_')
		default:
			name.WriteRune(character)
		}
	}
	cleaned := strings.Trim(name.String(), " .")
	cleaned, _ = clipRunes(cleaned, 120)
	cleaned = strings.TrimSpace(cleaned)
	// A name made only of substitutes is not a name: a title that was all
	// punctuation would otherwise download as "___ .md".
	if strings.Trim(cleaned, "_") == "" {
		cleaned = "book"
	}
	return cleaned + ".md"
}

// oneLine keeps a title from breaking the Markdown around it.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
