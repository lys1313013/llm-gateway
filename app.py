import json
import time
import uuid
import argparse
from flask import Flask, request, Response, jsonify
from flask_cors import CORS
import logging
import requests

# 配置日志
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(name)s - %(levelname)s - %(message)s')
logger = logging.getLogger(__name__)

app = Flask(__name__)
CORS(app)  # 添加CORS支持


def read_config():
    """读取并解析config.json文件"""
    with open('config.json', 'r') as f:
        return json.load(f)


def get_proxy_config():
    """获取代理配置"""
    config = read_config()
    return config.get('proxy_config', {})


def get_mode():
    """获取当前模式"""
    config = read_config()
    return config.get('mode', 'mock')


def forward_request(request_data, proxy_config):
    """转发请求到第三方 API"""
    target_url = proxy_config.get('target_url')
    api_key = proxy_config.get('api_key')
    timeout = proxy_config.get('timeout', 60)
    log_requests = proxy_config.get('log_requests', True)
    model = proxy_config.get('model', None)
    if model:
        request_data['model'] = model

    if log_requests:
        logger.info(f"[PROXY] Forwarding request to {target_url}")
        logger.info(f"[PROXY] Request data: {json.dumps(request_data, ensure_ascii=False)}")
    
    headers = {
        'Content-Type': 'application/json',
        'Authorization': f"Bearer {api_key}"
    }
    
    is_stream = request_data.get('stream', False)
    
    try:
        if is_stream:
            response = requests.post(
                target_url,
                json=request_data,
                headers=headers,
                stream=True,
                timeout=timeout
            )
            return response
        else:
            response = requests.post(
                target_url,
                json=request_data,
                headers=headers,
                timeout=timeout
            )
            
            if log_requests and response.status_code == 200:
                logger.info(f"[PROXY] Response data: {json.dumps(response.json(), ensure_ascii=False)}")
            
            return response
    except requests.exceptions.Timeout:
        logger.error(f"[PROXY] Request timeout after {timeout} seconds")
        raise
    except requests.exceptions.RequestException as e:
        logger.error(f"[PROXY] Request failed: {e}")
        raise


def forward_stream_response(response, log_responses=True):
    """转发流式响应"""
    for line in response.iter_lines():
        if line:
            decoded_line = line.decode('utf-8')
            if log_responses and decoded_line.startswith('data: '):
                logger.info(f"[PROXY] Stream chunk: {decoded_line}")
            yield decoded_line + '\n\n'



@app.route('/v1/chat/completions', methods=['POST'])
def chat_completions():
    try:
        mode = get_mode()
        proxy_config = get_proxy_config()
        
        data = request.json
        logger.info(f"Received request: {json.dumps(data)}")
        
        if mode == 'proxy' and proxy_config.get('enabled', False):
            logger.info(f"[MODE] Using proxy mode")
            return handle_proxy_request(data, proxy_config)
        else:
            logger.info(f"[MODE] Using mock mode")
            return handle_mock_request(data)
            
    except Exception as e:
        logger.error(f"Error processing request: {e}")
        return jsonify({'error': {'message': str(e), 'type': 'internal_server_error'}}), 500


def handle_proxy_request(request_data, proxy_config):
    """处理代理模式请求"""
    try:
        is_stream = request_data.get('stream', False)
        log_responses = proxy_config.get('log_responses', True)
        
        response = forward_request(request_data, proxy_config)
        
        if response.status_code != 200:
            logger.error(f"[PROXY] Target API returned error: {response.status_code}")
            return jsonify(response.json()), response.status_code
        
        if is_stream:
            return Response(
                forward_stream_response(response, log_responses),
                mimetype='text/event-stream'
            )
        else:
            return jsonify(response.json()), 200
            
    except requests.exceptions.Timeout:
        return jsonify({
            'error': {
                'message': 'Request to target API timed out',
                'type': 'timeout_error'
            }
        }), 504
    except requests.exceptions.RequestException as e:
        return jsonify({
            'error': {
                'message': f'Failed to forward request: {str(e)}',
                'type': 'proxy_error'
            }
        }), 502


def handle_mock_request(request_data):
    """处理 mock 模式请求"""
    if not request_data.get('model'):
        return jsonify({'error': {'message': 'model parameter is required', 'type': 'invalid_request_error'}}), 400
    
    if not request_data.get('messages'):
        return jsonify({'error': {'message': 'messages parameter is required', 'type': 'invalid_request_error'}}), 400
    
    preset = get_preset_response(request_data)
    
    if preset:
        if request_data.get('stream', False) and preset.get('stream_response_chunks'):
            logger.info(f"Using preset stream response chunks")
            return Response(stream_preset_chunks(preset.get('stream_response_chunks')), mimetype='text/event-stream')
        elif preset.get('response'):
            logger.info(f"Using preset non-stream response")
            return preset.get('response'), 200
    
    response_data = generate_default_response(request_data)
    
    if request_data.get('stream', False):
        return Response(stream_response(response_data), mimetype='text/event-stream')
    else:
        return response_data, 200

def get_preset_response(request_data):
    """检查是否有匹配的预设响应"""
    # 每次调用都重新读取配置文件
    config = read_config()
    preset_responses = config.get('preset_responses', [])
    
    for preset in preset_responses:
        match = True
        
        # 检查所有匹配条件
        for key, value in preset.get('match_conditions', {}).items():
            if key == 'model' and request_data.get(key) != value:
                match = False
                break
            elif key == 'messages' and not match_messages(request_data.get(key, []), value):
                match = False
                break
            elif key == 'user' and request_data.get(key) != value:
                match = False
                break
            elif key == 'stream' and request_data.get(key) != value:
                match = False
                break
        
        if match:
            logger.info(f"Found preset response for request")
            return preset
    
    return None

def match_messages(request_messages, preset_messages):
    """匹配消息内容"""
    # 简单实现：检查是否所有预设消息都在请求消息中
    for preset_msg in preset_messages:
        found = False
        for req_msg in request_messages:
            if (req_msg.get('role') == preset_msg.get('role') and 
                req_msg.get('content') == preset_msg.get('content')):
                found = True
                break
        if not found:
            return False
    return True

def generate_default_response(request_data):
    """生成默认响应"""
    # 检查是否需要工具调用（新版格式）
    tool_choice = request_data.get('tool_choice')
    tools = request_data.get('tools', [])
    
    # 检查是否需要函数调用（旧版格式）
    function_call = request_data.get('function_call')
    functions = request_data.get('functions', [])
    
    if tool_choice and tools:
        # 生成工具调用响应（新版格式）
        tool_name = 'default_tool'
        tool_args = {}
        
        # 如果有工具定义，使用第一个工具
        if tools:
            first_tool = tools[0]
            if first_tool.get('type') == 'function':
                tool_name = first_tool['function'].get('name', 'default_tool')
        
        return {
            'id': f'chatcmpl-{str(uuid.uuid4())[:28]}',
            'object': 'chat.completion',
            'created': int(time.time()),
            'model': request_data.get('model', 'gpt-3.5-turbo'),
            'choices': [
                {
                    'index': 0,
                    'message': {
                        'role': 'assistant',
                        'content': None,
                        'tool_calls': [
                            {
                                'id': f'toolcall-{str(uuid.uuid4())[:16]}',
                                'type': 'function',
                                'function': {
                                    'name': tool_name,
                                    'arguments': json.dumps(tool_args)
                                }
                            }
                        ]
                    },
                    'finish_reason': 'tool_calls'
                }
            ],
            'usage': {
                'prompt_tokens': 100,
                'completion_tokens': 50,
                'total_tokens': 150
            }
        }
    elif function_call:
        # 生成函数调用响应（旧版格式）
        function_name = 'default_function'
        function_args = {}
        
        # 处理function_call参数可能是字符串或对象的情况
        if isinstance(function_call, dict):
            function_name = function_call.get('name', 'default_function')
            function_args = function_call.get('arguments', {})
        
        return {
            'id': f'chatcmpl-{str(uuid.uuid4())[:28]}',
            'object': 'chat.completion',
            'created': int(time.time()),
            'model': request_data.get('model', 'gpt-3.5-turbo'),
            'choices': [
                {
                    'index': 0,
                    'message': {
                        'role': 'assistant',
                        'content': None,
                        'function_call': {
                            'name': function_name,
                            'arguments': json.dumps(function_args)
                        }
                    },
                    'finish_reason': 'function_call'
                }
            ],
            'usage': {
                'prompt_tokens': 100,
                'completion_tokens': 50,
                'total_tokens': 150
            }
        }
    else:
        # 生成普通响应
        return {
            'id': f'chatcmpl-{str(uuid.uuid4())[:28]}',
            'object': 'chat.completion',
            'created': int(time.time()),
            'model': request_data.get('model', 'gpt-3.5-turbo'),
            'choices': [
                {
                    'index': 0,
                    'message': {
                        'role': 'assistant',
                        'content': 'This is a simulated response from the mock OpenAI API.'
                    },
                    'finish_reason': 'stop'
                }
            ],
            'usage': {
                'prompt_tokens': 100,
                'completion_tokens': 20,
                'total_tokens': 120
            }
        }

def stream_preset_chunks(chunks):
    """生成预设的流式响应分块"""
    for chunk in chunks:
        # 确保每个分块都是有效的JSON
        try:
            # 如果分块已经是字符串，直接返回
            if isinstance(chunk, str):
                yield f'{chunk}\n\n'
            # 如果分块是字典，转换为JSON字符串
            else:
                yield f'data: {json.dumps(chunk)}\n\n'
            # 模拟延迟，使流更真实
            time.sleep(0.0005)
        except Exception as e:
            logger.error(f"Error processing chunk: {chunk}, error: {e}")
            continue
    
    # 结束流
    yield 'data: [DONE]\n\n'


def stream_response(response_data):
    """生成流式响应"""
    # 模拟流式响应的分块输出
    messages = response_data['choices'][0]['message']
    
    if messages.get('tool_calls'):
        # 流式输出工具调用
        tool_call = messages['tool_calls'][0]
        tool_id = tool_call['id']
        tool_name = tool_call['function']['name']
        
        # 输出初始工具调用信息
        yield f'data: {json.dumps({
            "id": response_data["id"],
            "object": "chat.completion.chunk",
            "created": response_data["created"],
            "model": response_data["model"],
            "choices": [{
                "index": 0,
                "delta": {
                    "role": "assistant",
                    "tool_calls": [{
                        "id": tool_id,
                        "type": "function",
                        "function": {
                            "name": tool_name,
                            "arguments": ""
                        }
                    }]
                },
                "finish_reason": None
            }]})}\n\n'
        
        # 模拟延迟
        time.sleep(0.0005)
        
        # 输出arguments的每个字符
        arguments = tool_call['function']['arguments']
        for char in arguments:
            yield f'data: {json.dumps({
                "id": response_data["id"],
                "object": "chat.completion.chunk",
                "created": response_data["created"],
                "model": response_data["model"],
                "choices": [{
                    "index": 0,
                    "delta": {
                        "tool_calls": [{
                            "index": 0,
                            "function": {
                                "arguments": char
                            }
                        }]
                    },
                    "finish_reason": None
                }]})}\n\n'
            time.sleep(0.0005)
        
        # 输出完成
        yield f'data: {json.dumps({
            "id": response_data["id"],
            "object": "chat.completion.chunk",
            "created": response_data["created"],
            "model": response_data["model"],
            "choices": [{
                "index": 0,
                "delta": {},
                "finish_reason": "tool_calls"
            }]})}\n\n'
    elif messages.get('function_call'):
        # 流式输出函数调用
        yield f'data: {json.dumps({
            "id": response_data["id"],
            "object": "chat.completion.chunk",
            "created": response_data["created"],
            "model": response_data["model"],
            "choices": [{
                "index": 0,
                "delta": {
                    "role": "assistant",
                    "function_call": {
                        "name": messages["function_call"]["name"],
                        "arguments": ""
                    }
                },
                "finish_reason": None
            }]})}\n\n'
        
        # 模拟延迟
        time.sleep(0.5)
        
        # 输出arguments的每个字符
        arguments = messages["function_call"]["arguments"]
        for char in arguments:
            yield f'data: {json.dumps({
                "id": response_data["id"],
                "object": "chat.completion.chunk",
                "created": response_data["created"],
                "model": response_data["model"],
                "choices": [{
                    "index": 0,
                    "delta": {
                        "function_call": {
                            "arguments": char
                        }
                    },
                    "finish_reason": None
                }]})}\n\n'
            time.sleep(0.0005)
        
        # 输出完成
        yield f'data: {json.dumps({
            "id": response_data["id"],
            "object": "chat.completion.chunk",
            "created": response_data["created"],
            "model": response_data["model"],
            "choices": [{
                "index": 0,
                "delta": {},
                "finish_reason": "function_call"
            }]})}\n\n'
    else:
        # 流式输出普通响应
        yield f'data: {json.dumps({
            "id": response_data["id"],
            "object": "chat.completion.chunk",
            "created": response_data["created"],
            "model": response_data["model"],
            "choices": [{
                "index": 0,
                "delta": {
                    "role": "assistant"
                },
                "finish_reason": None
            }]})}\n\n'
        
        # 模拟延迟
        time.sleep(0.0005)
        
        # 输出content的每个字符
        content = messages["content"]
        for char in content:
            yield f'data: {json.dumps({
                "id": response_data["id"],
                "object": "chat.completion.chunk",
                "created": response_data["created"],
                "model": response_data["model"],
                "choices": [{
                    "index": 0,
                    "delta": {
                        "content": char
                    },
                    "finish_reason": None
                }]})}\n\n'
            time.sleep(0.0005)
        
        # 输出完成
        yield f'data: {json.dumps({
            "id": response_data["id"],
            "object": "chat.completion.chunk",
            "created": response_data["created"],
            "model": response_data["model"],
            "choices": [{
                "index": 0,
                "delta": {},
                "finish_reason": "stop"
            }]})}\n\n'
    
    # 结束流
    yield 'data: [DONE]\n\n'

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='Mock OpenAI API Server')
    parser.add_argument('--port', type=int, default=5001, help='Port to run the server on (default: 5001)')
    args = parser.parse_args()
    
    app.run(host='0.0.0.0', port=args.port, debug=True)
