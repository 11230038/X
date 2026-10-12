package ws

import (
	"errors"
	"strconv"
	"testing"
)

func TestDecodingACommand(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		// versionMismatch means the frame should be refused as unreadable rather
		// than reported as a bad command.
		versionMismatch bool
		want            command
	}{
		{
			name: "the version this build speaks is enough",
			raw:  `{"protocol_version":"2.0","type":"ping"}`,
			want: command{ProtocolVersion: ProtocolVersion, Type: CommandPing},
		},
		{
			name: "a command carries what it acts on",
			raw:  `{"protocol_version":"2.0","type":"subscribe","turn_id":"t1","after_seq":4}`,
			want: command{ProtocolVersion: ProtocolVersion, Type: CommandSubscribe, TurnID: "t1", AfterSeq: seq(4)},
		},
		{
			name: "a field this build does not know is ignored",
			raw:  `{"protocol_version":"2.0","type":"ping","colour":"green"}`,
			want: command{ProtocolVersion: ProtocolVersion, Type: CommandPing},
		},
		{
			name:            "another version is not read at all",
			raw:             `{"protocol_version":"1.0","type":"ping"}`,
			versionMismatch: true,
		},
		{
			name:            "a frame that names no version is not read either",
			raw:             `{"type":"ping"}`,
			versionMismatch: true,
		},
		{
			name: "something that is not a frame at all",
			raw:  `not json at all`,
		},
		{
			name: "a frame that is JSON but names no command",
			raw:  `{"protocol_version":"2.0"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decode([]byte(test.raw))
			if test.versionMismatch {
				if !errors.Is(err, errProtocolVersion) {
					t.Fatalf("decode(%s) error = %v, want a version mismatch", test.raw, err)
				}
				return
			}
			if test.want.Type == "" {
				if err == nil {
					t.Fatalf("decode(%s) = %+v, want an error", test.raw, got)
				}
				// A frame that is otherwise fine must not be reported as a
				// version problem: the client would rewrite the wrong thing.
				if errors.Is(err, errProtocolVersion) {
					t.Errorf("decode(%s) blamed the version: %v", test.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode(%s) error = %v", test.raw, err)
			}
			if !sameCommand(got, test.want) {
				t.Errorf("decode(%s) = %+v, want %+v", test.raw, got, test.want)
			}
		})
	}
}

// sameCommand compares two commands by what they mean. The watermark travels as
// a pointer, so a plain == would compare addresses and never agree.
func sameCommand(a, b command) bool {
	if a.AfterSeq == nil || b.AfterSeq == nil {
		if a.AfterSeq != b.AfterSeq {
			return false
		}
	} else if *a.AfterSeq != *b.AfterSeq {
		return false
	}
	a.AfterSeq, b.AfterSeq = nil, nil
	return a == b
}

func TestAMissingWatermarkMeansTheBeginning(t *testing.T) {
	// The zero value is already the useful default, so a client that omits
	// after_seq is asking for everything and should get it.
	if got := (command{}).watermark(); got != 0 {
		t.Errorf("watermark() = %d, want 0 for a command that named none", got)
	}
	if got := (command{AfterSeq: seq(0)}).watermark(); got != 0 {
		t.Errorf("watermark() = %d, want the zero a client named explicitly", got)
	}
	if got := (command{AfterSeq: seq(9)}).watermark(); got != 9 {
		t.Errorf("watermark() = %d, want the watermark the client named", got)
	}
}

func TestARepeatedCommandIsAnsweredFromMemory(t *testing.T) {
	remembered := newAcks()
	ack := ackFrame{ProtocolVersion: ProtocolVersion, Frame: FrameCommandAck, Command: CommandCancel, TurnID: "t1", Status: "accepted"}
	remembered.remember("c1", CommandCancel, "t1", ack)

	got, found := remembered.recall("c1", CommandCancel, "t1")
	if !found || got != ack {
		t.Errorf("recall() = %+v, %v, want the ack that was remembered", got, found)
	}
}

func TestACommandIDIsOnlyOneCommand(t *testing.T) {
	remembered := newAcks()
	ack := ackFrame{Command: CommandCancel, TurnID: "t1", Status: "accepted"}
	remembered.remember("c1", CommandCancel, "t1", ack)

	// The same id on a different command, or about a different turn, is a
	// different command: a client that reuses an id must not be answered with
	// someone else's outcome.
	if _, found := remembered.recall("c1", CommandSubmitReply, "t1"); found {
		t.Error("an id remembered for cancel answered for submit_reply")
	}
	if _, found := remembered.recall("c1", CommandCancel, "t2"); found {
		t.Error("an id remembered for one turn answered for another")
	}
}

func TestRememberingIsBounded(t *testing.T) {
	remembered := newAcks()
	for i := 0; i < ackMemory*2; i++ {
		ack := ackFrame{Command: CommandCancel, Status: "accepted"}
		remembered.remember(idAt(i), CommandCancel, "t1", ack)
	}
	// The oldest are gone and the newest are kept: a connection that stays open
	// must not grow a map for every command it ever answered.
	if _, found := remembered.recall(idAt(0), CommandCancel, "t1"); found {
		t.Error("the very first command is still remembered after the memory filled")
	}
	if _, found := remembered.recall(idAt(ackMemory*2-1), CommandCancel, "t1"); !found {
		t.Error("the most recent command was forgotten")
	}
}

func seq(value int64) *int64 { return &value }

func idAt(index int) string {
	return "cmd-" + strconv.Itoa(index)
}
