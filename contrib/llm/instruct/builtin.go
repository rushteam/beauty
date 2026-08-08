package instruct

// 内置常用模板。使用时取地址传给 openai.WithCompletionMode(&instruct.ChatML)。
// 自定义模板直接构造 Template{} 即可。

// ChatML 是 ChatML 格式(Qwen、Yi、多数通用 fine-tune 使用)。
//
//	<|im_start|>system
//	{system}<|im_end|>
//	<|im_start|>user
//	{user}<|im_end|>
//	<|im_start|>assistant
var ChatML = Template{
	Name:            "chatml",
	SystemPrefix:    "<|im_start|>system\n",
	SystemSuffix:    "<|im_end|>\n",
	UserPrefix:      "<|im_start|>user\n",
	UserSuffix:      "<|im_end|>\n",
	AssistantPrefix: "<|im_start|>assistant\n",
	AssistantSuffix: "<|im_end|>\n",
	StopStrings:     []string{"<|im_end|>"},
}

// Llama3 是 Meta Llama 3 / 3.1 / 3.2 的 chat 格式。
//
//	<|begin_of_text|><|start_header_id|>system<|end_header_id|>
//
//	{system}<|eot_id|><|start_header_id|>user<|end_header_id|>
//
//	{user}<|eot_id|><|start_header_id|>assistant<|end_header_id|>
var Llama3 = Template{
	Name:            "llama3",
	BOS:             "<|begin_of_text|>",
	SystemPrefix:    "<|start_header_id|>system<|end_header_id|>\n\n",
	SystemSuffix:    "<|eot_id|>",
	UserPrefix:      "<|start_header_id|>user<|end_header_id|>\n\n",
	UserSuffix:      "<|eot_id|>",
	AssistantPrefix: "<|start_header_id|>assistant<|end_header_id|>\n\n",
	AssistantSuffix: "<|eot_id|>",
	StopStrings:     []string{"<|eot_id|}"},
}

// Mistral 是 Mistral / Mixtral 的 [INST] 格式。
//
//	<s>[INST] {system}
//	{user} [/INST]{assistant}</s>[INST] {user2} [/INST]
var Mistral = Template{
	Name:            "mistral",
	BOS:             "<s>",
	SystemPrefix:    "[INST] ",
	SystemSuffix:    "\n",
	UserPrefix:      "[INST] ",
	UserSuffix:      " [/INST]",
	AssistantPrefix: "",
	AssistantSuffix: "</s>",
	StopStrings:     []string{"</s>"},
}

// Alpaca 是 Stanford Alpaca / Vicuna 风格的纯文本指令格式。
//
//	### System:
//	{system}
//
//	### Instruction:
//	{user}
//
//	### Response:
var Alpaca = Template{
	Name:            "alpaca",
	SystemPrefix:    "### System:\n",
	SystemSuffix:    "\n\n",
	UserPrefix:      "### Instruction:\n",
	UserSuffix:      "\n\n",
	AssistantPrefix: "### Response:\n",
	AssistantSuffix: "\n\n",
	StopStrings:     []string{"### Instruction:", "### System:"},
}

// Gemma 是 Google Gemma 的 chat 格式。Gemma 无原生 system 角色,
// system 内容以 [System: ...] 包裹放入 user turn。
//
//	<bos><start_of_turn>user
//	[System: {system}]
//	{user}<end_of_turn>
//	<start_of_turn>model
var Gemma = Template{
	Name:            "gemma",
	BOS:             "<bos>",
	SystemPrefix:    "<start_of_turn>user\n[System: ",
	SystemSuffix:    "]\n<end_of_turn>\n",
	UserPrefix:      "<start_of_turn>user\n",
	UserSuffix:      "<end_of_turn>\n",
	AssistantPrefix: "<start_of_turn>model\n",
	AssistantSuffix: "<end_of_turn>\n",
	StopStrings:     []string{"<end_of_turn>"},
}
