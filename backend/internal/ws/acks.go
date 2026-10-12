package ws

import "sync"

// ackMemory is how many of a connection's answers are kept. A client repeats a
// command within seconds of not hearing back, so the window only has to be long
// enough to cover a retry, not the life of the connection.
const ackMemory = 256

// acks remembers what a connection answered, so that a command it repeats is
// answered again rather than performed again (WS-09).
//
// It lives on the connection rather than in the runtime, for two reasons: a
// repeat arrives on the connection that sent the first one, so a process-wide
// table would be answering clients it will never hear from; and the memory is
// then freed when the client goes away instead of having to be aged out.
//
// The key is the whole identity of the operation — its id, its command and the
// turn it names — because a client that reused an id for a different turn would
// otherwise be told about the turn it did not mean.
type acks struct {
	mu     sync.Mutex
	memory map[string]ackFrame
	order  []string
}

func newAcks() *acks {
	return &acks{memory: make(map[string]ackFrame, ackMemory)}
}

// recall returns the answer already given to this command, if there is one.
func (a *acks) recall(id, command, turnID string) (ackFrame, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ack, found := a.memory[key(id, command, turnID)]
	return ack, found
}

// remember keeps an answer so that the same command is not performed twice.
func (a *acks) remember(id, command, turnID string, ack ackFrame) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := key(id, command, turnID)
	if _, exists := a.memory[entry]; exists {
		return
	}
	if len(a.order) >= ackMemory {
		oldest := a.order[0]
		a.order = a.order[1:]
		delete(a.memory, oldest)
	}
	a.memory[entry] = ack
	a.order = append(a.order, entry)
}

func key(id, command, turnID string) string {
	return id + "\x00" + command + "\x00" + turnID
}
