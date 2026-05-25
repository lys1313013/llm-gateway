import requests
import json

# 测试参数
url = "http://localhost:5002/v1/chat/completions"
headers = {
    "Content-Type": "application/json",
    "Authorization": "Bearer test_key"
}

# 用户提供的测试请求
payload = {
    "model": "deepseek-r1-0528",
    "messages": [
        {
            "role": "user",
            "content": "当前时间"
        }
    ],
    "temperature": 0.7,
    "stream": False,
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

print("=== 测试工具调用功能 ===")
print("请求参数:")
print(json.dumps(payload, indent=2, ensure_ascii=False))

# 发送请求
response = requests.post(url, headers=headers, json=payload)

print("\n响应结果:")
print(f"状态码: {response.status_code}")
print(json.dumps(response.json(), indent=2, ensure_ascii=False))

# 检查是否返回了工具调用
response_data = response.json()
if "choices" in response_data:
    choice = response_data["choices"][0]
    message = choice["message"]
    if "tool_calls" in message:
        print("\n✅ 工具调用功能已支持！")
        print(f"工具调用ID: {message['tool_calls'][0]['id']}")
        print(f"工具类型: {message['tool_calls'][0]['type']}")
        print(f"函数名称: {message['tool_calls'][0]['function']['name']}")
        print(f"函数参数: {message['tool_calls'][0]['function']['arguments']}")
        print(f"完成原因: {choice['finish_reason']}")
    else:
        print("\n❌ 未返回工具调用")
        print(f"返回内容: {message.get('content')}")
        print(f"完成原因: {choice['finish_reason']}")
