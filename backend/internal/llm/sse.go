package llm

import (
	"bufio"
	"io"
	"strings"
)

// maxEventBytes bounds one server-sent event. A whole chunk is one line, and a
// fragmented tool-call argument can be long, so the default 64 KiB scanner limit
// would be too small.
const maxEventBytes = 1 << 20

// sseReader yields the data payload of each server-sent event.
//
// It follows the event-stream rules the OpenAI shape relies on: a blank line
// dispatches the event, several data lines in one event are joined with
// newlines, a leading colon marks a comment, and any other field is ignored.
type sseReader struct {
	scanner *bufio.Scanner
}

func newSSEReader(body io.Reader) *sseReader {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxEventBytes)
	return &sseReader{scanner: scanner}
}

// Next returns the next event payload. ok is false once the stream ends, which
// includes a stream that stops between events.
func (r *sseReader) Next() (data string, ok bool, err error) {
	var payload []string
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line == "" {
			if len(payload) == 0 {
				// Keep-alive, or an event with no data lines.
				continue
			}
			return strings.Join(payload, "\n"), true, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		value, found := strings.CutPrefix(line, "data:")
		if !found {
			// event:, id: and retry: are not part of the OpenAI shape.
			continue
		}
		payload = append(payload, strings.TrimPrefix(value, " "))
	}
	if err := r.scanner.Err(); err != nil {
		return "", false, err
	}
	if len(payload) > 0 {
		// The stream ended without the blank line that would dispatch this event.
		return strings.Join(payload, "\n"), true, nil
	}
	return "", false, nil
}
