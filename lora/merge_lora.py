import torch
from peft import PeftModel
from transformers import AutoModelForCausalLM, AutoTokenizer

base_model = "Qwen/Qwen2-7B-Instruct"
lora_model = "./qwen2-sre-lora"
output_dir = "./qwen2-sre-merged"

model = AutoModelForCausalLM.from_pretrained(base_model, torch_dtype=torch.bfloat16, device_map="auto", trust_remote_code=True)
tokenizer = AutoTokenizer.from_pretrained(base_model, trust_remote_code=True)
model = PeftModel.from_pretrained(model, lora_model)
model = model.merge_and_unload()
model.save_pretrained(output_dir)
tokenizer.save_pretrained(output_dir)
print("merged model saved")
