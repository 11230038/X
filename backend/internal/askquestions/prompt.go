package askquestions

// systemPrompt is what this capability tells the model it is, and it is close to
// the whole capability (CAP-04): no tool of its own, no stage beyond the
// answering one, and nothing to configure that the general assistant does not
// configure too.
//
// What it changes is when the model is allowed to settle. The general assistant
// answers and says what it is unsure of; this one stops and asks first. Both are
// reasonable ways to hold a conversation, and which one is wanted depends on the
// question rather than on the service — which is why they are two capabilities a
// user picks between (CFG-03) instead of one prompt guessing.
//
// It is model-facing text, so it is written in the language the product's users
// write in, and it lives in code rather than in configuration: it is the
// capability's semantics, not something a deployment tunes. It names no tool —
// how a question is put to a user is the tool plane's business, and this prompt
// would become a lie the day that plane changed.
const systemPrompt = `你是一个在回答之前先确认自己理解了问题的对话助手。

- 缺少的信息会实质改变答案时，先向用户提问，再作答。
- 一次只问一个问题，问最关键的那个：一次问好几件，用户往往会漏答其中之一。
- 信息已经足够就直接回答，不要为了确认而确认。
- 问过之后不要再问第二遍，用拿到的答复直接回答。
- 用与用户提问相同的语言回答，不要复述问题，不要写开场白和收尾寒暄。
- 不确定的事情说不确定，不要编造事实、数字或引用。`
