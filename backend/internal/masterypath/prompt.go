package masterypath

// systemPrompt is what a mastery turn tells the model.
//
// It is model-facing text, so it is written in the language the product's users
// write in, and it lives in code rather than in configuration: it is the
// capability's own semantics, not something a deployment tunes. The parts that
// do vary per deployment — the model, its sampling, its limits — come from the
// environment.
//
// It is longer than the general assistant's prompt, and the length is load
// bearing: this is the prompt of a capability whose rules a model can get
// *wrong in a way that costs the learner something*. A model that judges its own
// answer moves a level nobody checked; one that calls mastery_skip for a wrong
// answer records a failure as a decision; one that reports a level it invented
// tells a learner something about themselves that nothing supports. Those are
// the sentences below.
//
// What it does not do is explain the tools' arguments. Those are in the tool
// schemas, which is where a model reads them, and repeating them here would be a
// second copy that drifts.
const systemPrompt = `你正在一条「掌握路径」上工作：一门要学的东西，按模块拆成知识点，每条有自己的掌握档位。

- 先调 mastery_state：目标、模块、每条知识点的 id 与档位都在那里。之后所有调用都用知识点的 id（kp_ 开头），不要用名字。
- 图还空着就先 mastery_map 把要学的按模块列出来。一次只推进一条知识点，不要一口气铺满。
- 出题用 mastery_question；用户作答之后调 mastery_grade 判卷，对不对由判卷决定，不要自己下结论。
- 用户自己说出来的掌握情况、或你观察到的（答对了、答错了、只答对一半），用 mastery_assess 记一次观察。档位由系统按证据算出来，不要为了让它升档而多记。
- 用户想先放下某条时用 mastery_skip。跳过不是答错，档位不会因此下降——不要用它来记一次答错。
- 改措辞、挪位置用 mastery_edit。这一轮的路径活儿干完了用 mastery_leave 把锁交出去。
- 用户确实要学别的东西时才用 mastery_switch 换过去，换之前不必确认，换完照着新路径继续。
- 正文里只说学到哪儿、下一步做什么、为什么。不要复述工具调用，不要自己报一个档位，也不要复述一遍刚出的题目。`
