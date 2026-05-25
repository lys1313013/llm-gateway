import openai
import os
import json
import argparse

# 解析命令行参数
parser = argparse.ArgumentParser(description='Test Mock OpenAI API Server')
parser.add_argument('--port', type=int, default=5001, help='Port where the mock server is running (default: 5001)')
args = parser.parse_args()

# 配置OpenAI客户端连接到我们的模拟服务
openai.api_key = "test_key"  # 这里的API密钥可以是任意值，因为模拟服务不验证它
openai.api_base = f"http://localhost:{args.port}/v1"  # 指向我们的模拟服务

def test_non_streaming_response():
    """测试非流式响应"""
    print("\n=== 测试非流式响应 ===")
    
    try:
        response = openai.ChatCompletion.create(
            model="gpt-3.5-turbo",
            messages=[
                {"role": "system", "content": "You are a helpful assistant."},
                {"role": "user", "content": "Hello"}
            ],
            stream=False
        )
        
        print(f"响应ID: {response.id}")
        print(f"模型: {response.model}")
        print(f"生成时间: {response.created}")
        print(f"响应内容: {response.choices[0].message.content}")
        print(f"完成原因: {response.choices[0].finish_reason}")
        print(f"使用的令牌数: {response.usage.total_tokens}")
        
    except Exception as e:
        print(f"错误: {e}")

def test_streaming_response():
    """测试流式响应"""
    print("\n=== 测试流式响应 ===")
    
    try:
        response = openai.ChatCompletion.create(
            model="gpt-3.5-turbo",
            messages=[
                {"role": "system", "content": "You are a helpful assistant."},
                {"role": "user", "content": "Tell me a short story"}
            ],
            stream=True
        )
        
        print("流式响应内容:")
        for chunk in response:
            if chunk.choices[0].delta.get("content"):
                print(chunk.choices[0].delta.content, end="", flush=True)
        
    except Exception as e:
        print(f"错误: {e}")

def test_preset_response():
    """测试预设响应"""
    print("\n=== 测试预设响应 ===")
    
    try:
        # 测试匹配预设响应的请求
        response = openai.ChatCompletion.create(
            model="gpt-3.5-turbo",
            messages=[{"role": "user", "content": "Hello"}],
            stream=False
        )
        
        print(f"响应内容: {response.choices[0].message.content}")
        
        # 测试匹配user特定的预设响应
        response = openai.ChatCompletion.create(
            model="gpt-4",
            messages=[{"role": "user", "content": "Test"}],
            user="test_user",
            stream=False
        )
        
        print(f"针对test_user的响应: {response.choices[0].message.content}")
        
    except Exception as e:
        print(f"错误: {e}")

def test_function_call():
    """测试函数调用"""
    print("\n=== 测试函数调用 ===")
    
    try:
        # 测试函数调用的非流式响应
        response = openai.ChatCompletion.create(
            model="gpt-3.5-turbo",
            messages=[{"role": "user", "content": "Call function"}],
            stream=False
        )
        
        print(f"响应类型: {response.choices[0].finish_reason}")
        if hasattr(response.choices[0].message, 'function_call'):
            print(f"函数名称: {response.choices[0].message.function_call.name}")
            print(f"函数参数: {response.choices[0].message.function_call.arguments}")
        
        # 测试函数调用的流式响应
        print("\n流式函数调用响应:")
        response = openai.ChatCompletion.create(
            model="gpt-3.5-turbo",
            messages=[{"role": "user", "content": "Call function"}],
            stream=True
        )
        
        for chunk in response:
            if chunk.choices[0].delta.get("function_call"):
                if chunk.choices[0].delta.function_call.get("name"):
                    print(f"函数名称: {chunk.choices[0].delta.function_call.name}")
                if chunk.choices[0].delta.function_call.get("arguments"):
                    print(chunk.choices[0].delta.function_call.arguments, end="", flush=True)
        
    except Exception as e:
        print(f"错误: {e}")

def test_error_handling():
    """测试错误处理"""
    print("\n=== 测试错误处理 ===")
    
    try:
        # 测试缺少model参数
        response = openai.ChatCompletion.create(
            messages=[{"role": "user", "content": "Hello"}],
            stream=False
        )
        print(response)
    except Exception as e:
        print(f"缺少model参数的错误: {e}")
    
    try:
        # 测试缺少messages参数
        response = openai.ChatCompletion.create(
            model="gpt-3.5-turbo",
            stream=False
        )
        print(response)
    except Exception as e:
        print(f"缺少messages参数的错误: {e}")

def test_custom_function_call():
    """测试自定义函数调用"""
    print("\n=== 测试自定义函数调用 ===")
    
    try:
        response = openai.ChatCompletion.create(
            model="gpt-3.5-turbo",
            messages=[
                {"role": "system", "content": "You are a helpful assistant that can call functions."},
                {"role": "user", "content": "What's the weather like in Beijing?"}
            ],
            functions=[
                {
                    "name": "get_weather",
                    "description": "Get the current weather in a specified location",
                    "parameters": {
                        "type": "object",
                        "properties": {
                            "city": {
                                "type": "string",
                                "description": "The city name"
                            },
                            "units": {
                                "type": "string",
                                "enum": ["celsius", "fahrenheit"],
                                "description": "The temperature unit"
                            }
                        },
                        "required": ["city"]
                    }
                }
            ],
            function_call="auto",
            stream=False
        )
        
        print(f"响应: {response}")
        
    except Exception as e:
        print(f"错误: {e}")

if __name__ == "__main__":
    print("开始测试模拟OpenAI API服务...")
    
    # 先启动模拟服务（在另一个终端中）
    print(f"\n请确保模拟服务已经在端口{args.port}上运行。")
    print("你可以通过运行以下命令启动服务:")
    print(f"python app.py --port {args.port}")
    print("或者指定不同的端口:")
    print("python app.py --port 5000")
    
    input("\n按Enter键继续测试...")
    
    # 运行所有测试
    test_non_streaming_response()
    test_streaming_response()
    test_preset_response()
    test_function_call()
    test_error_handling()
    test_custom_function_call()
    
    print("\n所有测试完成！")
