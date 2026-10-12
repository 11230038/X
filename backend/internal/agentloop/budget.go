package agentloop

import "backend/internal/llm"

// bytesPerToken approximates how many bytes of message text one token costs.
//
// It is deliberately pessimistic. Nothing here tokenizes: the real counts differ
// per model, and a request that overflows the window fails outright, so
// over-estimating trades a little context for a turn that still runs.
const bytesPerToken = 3

// Trim returns the messages that fit the budget, dropping the oldest first.
//
// Three things are never dropped: the system prompt, the most recent user
// message and everything after it — that is the question being answered and the
// work it has produced so far. If they alone exceed the budget the result
// overruns it, because there is nothing left that may be removed.
//
// Trimming happens in whole groups rather than message by message. An assistant
// message that asked for tools and the tool messages answering it must survive
// or be dropped together: providers reject a tool result whose call is missing,
// so splitting a group would turn a long conversation into a failed request.
func Trim(messages []llm.Message, budgetTokens int) []llm.Message {
	if budgetTokens <= 0 {
		return messages
	}

	groups := groupMessages(messages)
	total := 0
	for _, group := range groups {
		total += group.tokens
	}
	if total <= budgetTokens {
		return messages
	}

	keep := make([]bool, len(groups))
	for i := range keep {
		keep[i] = true
	}
	for i, group := range groups {
		if total <= budgetTokens {
			break
		}
		if group.protected {
			continue
		}
		keep[i] = false
		total -= group.tokens
	}

	trimmed := make([]llm.Message, 0, len(messages))
	for i, group := range groups {
		if keep[i] {
			trimmed = append(trimmed, messages[group.start:group.end]...)
		}
	}
	return trimmed
}

// messageGroup is a run of messages that must be kept or dropped as one.
type messageGroup struct {
	start, end int
	tokens     int
	protected  bool
}

// groupMessages splits a conversation into keep-or-drop units and marks the
// ones that may not be dropped.
func groupMessages(messages []llm.Message) []messageGroup {
	lastUser := -1
	for i, message := range messages {
		if message.Role == llm.RoleUser {
			lastUser = i
		}
	}

	groups := make([]messageGroup, 0, len(messages))
	for i := 0; i < len(messages); {
		end := i + 1
		// A call and its answers are one unit: dropping the call alone would
		// orphan the tool results that follow it.
		if messages[i].Role == llm.RoleAssistant && len(messages[i].ToolCalls) > 0 {
			for end < len(messages) && messages[end].Role == llm.RoleTool {
				end++
			}
		}

		group := messageGroup{
			start: i,
			end:   end,
			// A system message is the capability's prompt, and everything from
			// the last user message on is the turn in progress.
			protected: messages[i].Role == llm.RoleSystem || (lastUser >= 0 && i >= lastUser),
		}
		for _, message := range messages[i:end] {
			group.tokens += messageTokens(message)
		}
		groups = append(groups, group)
		i = end
	}
	return groups
}

// messageTokens estimates what one message costs.
func messageTokens(message llm.Message) int {
	size := len(message.Role) + len(message.Content) + len(message.ToolCallID)
	for _, call := range message.ToolCalls {
		size += len(call.Name) + len(call.Arguments)
	}
	if size == 0 {
		return 0
	}
	return (size + bytesPerToken - 1) / bytesPerToken
}
