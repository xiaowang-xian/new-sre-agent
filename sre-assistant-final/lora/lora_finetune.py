import torch
from datasets import load_dataset
from transformers import AutoModelForCausalLM, AutoTokenizer, BitsAndBytesConfig, TrainingArguments
from peft import LoraConfig, get_peft_model
from trl import SFTTrainer

model_name = "Qwen/Qwen2-7B-Instruct"
dataset_path = "./train_data.json"
lora_output_dir = "./qwen2-sre-lora"

bnb_config = BitsAndBytesConfig(
    load_in_4bit=True,
    bnb_4bit_use_double_quant=True,
    bnb_4bit_quant_type="nf4",
    bnb_4bit_compute_dtype=torch.bfloat16,
)

lora_config = LoraConfig(
    r=8,
    lora_alpha=32,
    target_modules=["q_proj", "v_proj"],
    lora_dropout=0.05,
    bias="none",
    task_type="CAUSAL_LM",
)

model = AutoModelForCausalLM.from_pretrained(
    model_name,
    quantization_config=bnb_config,
    device_map="auto",
    trust_remote_code=True,
)
tokenizer = AutoTokenizer.from_pretrained(model_name, trust_remote_code=True)
tokenizer.pad_token = tokenizer.eos_token
model = get_peft_model(model, lora_config)
model.print_trainable_parameters()

dataset = load_dataset("json", data_files=dataset_path, split="train")

def format_prompt(sample):
    return f"""<|im_start|>system
你是一个资深 Kubernetes SRE 工程师，专门处理集群故障。<|im_end|>
<|im_start|>user
{sample['instruction']}
{sample['input']}<|im_end|>
<|im_start|>assistant
{sample['output']}<|im_end|>"""

args = TrainingArguments(
    output_dir=lora_output_dir,
    per_device_train_batch_size=1,
    gradient_accumulation_steps=4,
    learning_rate=2e-4,
    num_train_epochs=3,
    logging_steps=1,
    save_strategy="epoch",
    fp16=True,
    optim="paged_adamw_8bit",
)

trainer = SFTTrainer(
    model=model,
    train_dataset=dataset,
    args=args,
    tokenizer=tokenizer,
    peft_config=lora_config,
    formatting_func=format_prompt,
    max_seq_length=512,
)
trainer.train()
trainer.model.save_pretrained(lora_output_dir)
tokenizer.save_pretrained(lora_output_dir)
print(f"LoRA saved to {lora_output_dir}")
