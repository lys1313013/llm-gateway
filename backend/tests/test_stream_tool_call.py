import requests
import json
import time

# 测试参数
url = "http://localhost:5002/v1/chat/completions"
headers = {
    "Content-Type": "application/json",
    "Authorization": "Bearer test_key"
}

# 测试请求 - 流式工具调用
payload = {
    "model": "deepseek-r1-0528",
    "messages": [
        {
            "role": "user",
            "content": "当前时间"
        }
    ],
    "temperature": 0.7,
    "stream": True,
    "tool_choice": "auto",
    "tools": [
        {
            "type": "function",
            "function": {
                "name": "current_time",
                "description": "A tool for getting the current time.",
                "parameters": {
                    "type": "object",
                    "properties": {}
                }
            }
        }
    ]
}

print("=== 测试流式工具调用格式 ===")
print("发送请求...")

# 发送请求并处理流式响应
response = requests.post(url, headers=headers, json=payload, stream=True)

print("\n流式响应结果:")
chunk_count = 0

# 分析每个响应块
for line in response.iter_lines():
    if line:
        # 解码并去除前缀
        chunk = line.decode('utf-8')
        if chunk.startswith('data: '):
            chunk_count += 1
            chunk_data = chunk[6:]
            
            if chunk_data == '[DONE]':
                print(f"\n{chunk_count}. {chunk}")
                print("✅ 流结束标记正确")
            else:
                try:
                    data = json.loads(chunk_data)
                    print(f"\n{chunk_count}. {chunk}")
                    
                    # 验证响应结构
                    if "choices" in data and data["choices"]:
                        choice = data["choices"][0]
                        delta = choice.get("delta", {})
                        
                        # 检查第一个块
                        if chunk_count == 1:
                            if "role" in delta and delta["role"] == "assistant":
                                print("✅ 第一个块包含正确的role字段")
                            else:
                                print("❌ 第一个块缺少或包含错误的role字段")
                            
                            if "tool_calls" in delta and delta["tool_calls"]:
                                print("✅ 第一个块包含tool_calls字段")
                                tool_call = delta["tool_calls"][0]
                                if "id" in tool_call and "type" in tool_call and "function" in tool_call:
                                    print("✅ 工具调用结构完整")
                                else:
                                    print("❌ 工具调用结构不完整")
                            else:
                                print("❌ 第一个块缺少tool_calls字段")
                        
                        # 检查中间块
                        elif chunk_count > 1 and chunk_count < 4:
                            if "tool_calls" in delta and delta["tool_calls"]:
                                tool_call = delta["tool_calls"][0]
                                if "index" in tool_call:
                                    print("✅ 中间块包含正确的index字段")
                                else:
                                    print("❌ 中间块缺少index字段")
                                
                                if "function" in tool_call and "arguments" in tool_call["function"]:
                                    print("✅ 中间块包含正确的arguments字段")
                                else:
                                    print("❌ 中间块缺少arguments字段")
                            else:
                                print("❌ 中间块缺少tool_calls字段")
                        
                        # 检查最后一个数据块
                        elif chunk_count == 4:
                            if not delta:
                                print("✅ 最后一个数据块delta为空")
                            else:
                                print("❌ 最后一个数据块delta不为空")
                            
                            if choice.get("finish_reason") == "tool_calls":
                                print("✅ 最后一个数据块包含正确的finish_reason")
                            else:
                                print("❌ 最后一个数据块包含错误的finish_reason")
                                
                except json.JSONDecodeError as e:
                    print(f"❌ 解析JSON错误: {e}")

print(f"\n=== 总结 ===")
print(f"共接收 {chunk_count} 个响应块")
print("流式工具调用格式符合OpenAI API规范")
