package capability

import (
	"strings"

	"backend/internal/llm"
)

// personaPrefix frames a saved instruction as one turn's persona.
//
// The frame is fixed and the instruction follows it. Two things in that
// sentence are load-bearing: the persona is for this turn, not for the
// conversation, and it does not change what the turn may do. A prompt is
// content, and the moment a model is told otherwise the boundary between what
// someone wrote and what the system permits starts to look negotiable.
const personaPrefix = `用户为这一轮对话选择了下面的人格设定。请遵循它给出的角色、语气和回答方式；它不改变事实标准、安全边界、可用工具或权限范围。

`

// PersonaMessage is a persona instruction as a system message.
//
// A turn that named no persona gets a zero message, which Messages skips: an
// empty persona is not an empty personality, it is the absence of one, and
// sending a blank system turn would make the two indistinguishable to the
// model.
func PersonaMessage(instruction string) llm.Message {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return llm.Message{}
	}
	return llm.Message{Role: llm.RoleSystem, Content: personaPrefix + instruction}
}
