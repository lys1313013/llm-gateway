import json
import logging
import time
import uuid
from flask import Response, jsonify
from config import read_config
from db import insert_log
from services.proxy import _aggregate_stream_chunks

logger = logging.getLogger(__name__)

def handle_mock_request(request_data):
    """处理 mock 模式请求"""
    start_time = time.time()
    if not request_data.get('model'):
        return jsonify({'error': {'message': 'model parameter is required', 'type': 'invalid_request_error'}}), 400
    
    if not request_data.get('messages'):
        return jsonify({'error': {'message': 'messages parameter is required', 'type': 'invalid_request_error'}}), 400
    
    preset = get_preset_response(request_data)
    
    if preset:
        if request_data.get('stream', False) and preset.get('stream_response_chunks'):
            logger.info(f"Using preset stream response chunks")
            return Response(stream_preset_chunks(preset.get('stream_response_chunks'), request_data, start_time), mimetype='text/event-stream')
        elif preset.get('response'):
            logger.info(f"Using preset non-stream response")
            response_data = preset.get('response')
            logger.info(f"[MOCK] Output response: {json.dumps(response_data, ensure_ascii=False)}")
            
            processing_time_ms = int((time.time() - start_time) * 1000)
            usage = response_data.get('usage', {})
            insert_log({
                'mode': 'mock',
                'model': request_data.get('model'),
                'is_stream': False,
                'status_code': 200,
                'processing_time_ms': processing_time_ms,
                'prompt_tokens': usage.get('prompt_tokens'),
                'completion_tokens': usage.get('completion_tokens'),
                'total_tokens': usage.get('total_tokens'),
                'request_data': request_data,
                'response_data': response_data
            })
            return jsonify(response_data), 200
    
    response_data = generate_default_response(request_data)
    
    if request_data.get('stream', False):
        return Response(stream_response(response_data, request_data, start_time), mimetype='text/event-stream')
    else:
        logger.info(f"[MOCK] Output response: {json.dumps(response_data, ensure_ascii=False)}")
        processing_time_ms = int((time.time() - start_time) * 1000)
        usage = response_data.get('usage', {})
        insert_log({
            'mode': 'mock',
            'model': request_data.get('model'),
            'is_stream': False,
            'status_code': 200,
            'processing_time_ms': processing_time_ms,
            'prompt_tokens': usage.get('prompt_tokens'),
            'completion_tokens': usage.get('completion_tokens'),
            'total_tokens': usage.get('total_tokens'),
            'request_data': request_data,
            'response_data': response_data
        })
        return jsonify(response_data), 200

def get_preset_response(request_data):
    """检查是否有匹配的预设响应"""
    config = read_config()
    preset_responses = config.get('preset_responses', [])
    
    for preset in preset_responses:
        match = True
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
    tool_choice = request_data.get('tool_choice')
    tools = request_data.get('tools', [])
    function_call = request_data.get('function_call')
    functions = request_data.get('functions', [])
    
    if tool_choice and tools:
        tool_name = 'default_tool'
        tool_args = {}
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
        function_name = 'default_function'
        function_args = {}
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

def stream_preset_chunks(chunks, request_data, start_time):
    """生成预设的流式响应分块"""
    streamed_data = []
    for chunk in chunks:
        try:
            if isinstance(chunk, str):
                if chunk.startswith('data: ') and chunk != 'data: [DONE]':
                    try:
                        streamed_data.append(json.loads(chunk[6:]))
                    except:
                        pass
                yield f'{chunk}\n\n'
            else:
                streamed_data.append(chunk)
                yield f'data: {json.dumps(chunk)}\n\n'
            time.sleep(0.0005)
        except Exception as e:
            logger.error(f"Error processing chunk: {chunk}, error: {e}")
            continue
    
    yield 'data: [DONE]\n\n'
    
    if streamed_data:
        aggregated = _aggregate_stream_chunks(streamed_data)
        logger.info(f"[MOCK] Stream output finished. Aggregated result: {json.dumps(aggregated, ensure_ascii=False)}")
        
        processing_time_ms = int((time.time() - start_time) * 1000)
        usage = aggregated.get('usage', {})
        insert_log({
            'mode': 'mock',
            'model': request_data.get('model'),
            'is_stream': True,
            'status_code': 200,
            'processing_time_ms': processing_time_ms,
            'prompt_tokens': usage.get('prompt_tokens'),
            'completion_tokens': usage.get('completion_tokens'),
            'total_tokens': usage.get('total_tokens'),
            'request_data': request_data,
            'response_data': aggregated
        })

def stream_response(response_data, request_data, start_time):
    """生成流式响应"""
    messages = response_data['choices'][0]['message']
    
    if messages.get('tool_calls'):
        tool_call = messages['tool_calls'][0]
        tool_id = tool_call['id']
        tool_name = tool_call['function']['name']
        
        yield f'data: {json.dumps({"id": response_data["id"], "object": "chat.completion.chunk", "created": response_data["created"], "model": response_data["model"], "choices": [{"index": 0, "delta": {"role": "assistant", "tool_calls": [{"id": tool_id, "type": "function", "function": {"name": tool_name, "arguments": ""}}]}, "finish_reason": None}]})}\n\n'
        time.sleep(0.0005)
        
        arguments = tool_call['function']['arguments']
        for char in arguments:
            yield f'data: {json.dumps({"id": response_data["id"], "object": "chat.completion.chunk", "created": response_data["created"], "model": response_data["model"], "choices": [{"index": 0, "delta": {"tool_calls": [{"index": 0, "function": {"arguments": char}}]}, "finish_reason": None}]})}\n\n'
            time.sleep(0.0005)
        
        yield f'data: {json.dumps({"id": response_data["id"], "object": "chat.completion.chunk", "created": response_data["created"], "model": response_data["model"], "choices": [{"index": 0, "delta": {}, "finish_reason": "tool_calls"}]})}\n\n'
    elif messages.get('function_call'):
        yield f'data: {json.dumps({"id": response_data["id"], "object": "chat.completion.chunk", "created": response_data["created"], "model": response_data["model"], "choices": [{"index": 0, "delta": {"role": "assistant", "function_call": {"name": messages["function_call"]["name"], "arguments": ""}}, "finish_reason": None}]})}\n\n'
        time.sleep(0.5)
        
        arguments = messages["function_call"]["arguments"]
        for char in arguments:
            yield f'data: {json.dumps({"id": response_data["id"], "object": "chat.completion.chunk", "created": response_data["created"], "model": response_data["model"], "choices": [{"index": 0, "delta": {"function_call": {"arguments": char}}, "finish_reason": None}]})}\n\n'
            time.sleep(0.0005)
        
        yield f'data: {json.dumps({"id": response_data["id"], "object": "chat.completion.chunk", "created": response_data["created"], "model": response_data["model"], "choices": [{"index": 0, "delta": {}, "finish_reason": "function_call"}]})}\n\n'
    else:
        yield f'data: {json.dumps({"id": response_data["id"], "object": "chat.completion.chunk", "created": response_data["created"], "model": response_data["model"], "choices": [{"index": 0, "delta": {"role": "assistant"}, "finish_reason": None}]})}\n\n'
        time.sleep(0.0005)
        
        content = messages["content"]
        for char in content:
            yield f'data: {json.dumps({"id": response_data["id"], "object": "chat.completion.chunk", "created": response_data["created"], "model": response_data["model"], "choices": [{"index": 0, "delta": {"content": char}, "finish_reason": None}]})}\n\n'
            time.sleep(0.0005)
        
        yield f'data: {json.dumps({"id": response_data["id"], "object": "chat.completion.chunk", "created": response_data["created"], "model": response_data["model"], "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]})}\n\n'
    
    # Optional: Send a chunk with usage if requested or just let aggregation use the original response_data usage
    usage = response_data.get('usage')
    if usage:
        yield f'data: {json.dumps({"id": response_data["id"], "object": "chat.completion.chunk", "created": response_data["created"], "model": response_data["model"], "choices": [], "usage": usage})}\n\n'
        
    yield 'data: [DONE]\n\n'
    
    logger.info(f"[MOCK] Stream output finished. Aggregated result: {json.dumps(messages, ensure_ascii=False)}")
    
    processing_time_ms = int((time.time() - start_time) * 1000)
    insert_log({
        'mode': 'mock',
        'model': request_data.get('model'),
        'is_stream': True,
        'status_code': 200,
        'processing_time_ms': processing_time_ms,
        'prompt_tokens': usage.get('prompt_tokens') if usage else None,
        'completion_tokens': usage.get('completion_tokens') if usage else None,
        'total_tokens': usage.get('total_tokens') if usage else None,
        'request_data': request_data,
        'response_data': messages
    })
