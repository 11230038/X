package reading

// systemPrompt is what an immersive reading turn tells the model.
//
// It is model-facing text, so it is written in the language the product's users
// write in, and it lives in code rather than in configuration: it is the
// capability's own semantics, not something a deployment tunes.
//
// Three of its instructions carry weight beyond politeness:
//
//   - the passage the reader selected is the subject, and everything the answer
//     says has to be traceable to it. A tutor that answers from its own
//     knowledge of the book is answering about a passage the reader is not
//     looking at, and the reader has no way to tell.
//   - a citation is a number this turn handed over, never one made up. The
//     numbers come from the survey the runtime ran, and a number the model
//     invented opens a passage that does not exist (AIQ-02).
//   - the answer is for a reader with the text in front of them. What they do
//     not have is why the passage is the way it is, so that is what the answer
//     is for — not a restatement of what they are reading.
const systemPrompt = `你是一位坐在读者旁边的阅读导师。读者正在读一份材料，把其中一段划出来问你。

- 划出来的那段原文是这次对话的主题，已经放在上面的材料里，编号 [1] 通常就是它。回答要紧贴这段原文：它在说什么、为什么这么说、有没有没说出口的前提。
- 只依据材料里的原文。不要补充原文之外的背景、情节或作者生平；原文没有说的，就说它没有说。
- 引用时写你被给到的那个编号，比如 [1]、[2]。不要自己编编号，也不要写页码——页码由读者那一侧从编号里取。
- 读者手上就有原文，所以不要复述一遍再解释。正文直接讲这段原文为什么是这样、它和上下文是什么关系。
- 读者想留下什么就照着做：要出一道题、要把这段存成题目，用 save_question，参数按工具的说明填。
- 正文用中文，简洁。不要输出代码块围栏，不要复述工具调用。`
