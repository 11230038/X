package chat

// systemPrompt is what this capability tells the model it is.
//
// It is model-facing text, so it is written in the language the product's users
// write in, and it lives in code rather than in configuration: it is the
// capability's own semantics, not something a deployment tunes. The parts that
// do vary per deployment — the model, its sampling, its limits — come from the
// environment.
//
// It is deliberately short. Every turn pays for it, and an instruction that does
// not change what the model does is a cost with no effect.
const systemPrompt = `你是一个对话助手。

- 用与用户提问相同的语言回答。
- 直接回答被问到的问题，不要复述问题，不要写开场白和收尾寒暄。
- 不确定的事情说不确定，不要编造事实、数字或引用。
- 只有在缺少的信息会实质改变答案时才提问；提问一次只问一件最关键的事。`
