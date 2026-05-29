import json
import logging
import requests
import time
from flask import Response, jsonify
from db import insert_log
from token_utils import calculate_usage, normalize_usage

logger = logging.getLogger(__name__)

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
                logger.info(f"[PROXY] Output response: {json.dumps(response.json(), ensure_ascii=False)}")
            
            return response
    except requests.exceptions.Timeout:
        logger.error(f"[PROXY] Request timeout after {timeout} seconds")
        raise
    except requests.exceptions.RequestException as e:
        logger.error(f"[PROXY] Request failed: {e}")
        raise

def _aggregate_stream_chunks(chunks):
    """汇总流式分块"""
    aggregated = {'role': 'assistant', 'content': ''}
    tool_calls = {}
    function_call = {'name': '', 'arguments': ''}
    has_function_call = False
    usage = None
    
    for chunk in chunks:
        if not isinstance(chunk, dict):
            continue
        
        # Check if usage is provided in chunk
        if 'usage' in chunk and chunk['usage']:
            usage = chunk['usage']
            
        choices = chunk.get('choices', [])
        if not choices:
            continue
        delta = choices[0].get('delta', {})
        
        if 'content' in delta and delta.get('content'):
            aggregated['content'] += delta['content']
            
        if 'reasoning_content' in delta and delta.get('reasoning_content'):
            if 'reasoning_content' not in aggregated:
                aggregated['reasoning_content'] = ''
            aggregated['reasoning_content'] += delta['reasoning_content']
            
        if delta.get('function_call'):
            has_function_call = True
            if 'name' in delta['function_call'] and delta['function_call']['name']:
                function_call['name'] += delta['function_call']['name']
            if 'arguments' in delta['function_call'] and delta['function_call']['arguments']:
                function_call['arguments'] += delta['function_call']['arguments'] or ''
                
        if delta.get('tool_calls'):
            for tc in delta['tool_calls']:
                idx = tc.get('index', 0)
                if idx not in tool_calls:
                    tool_calls[idx] = {'id': tc.get('id', ''), 'type': tc.get('type', 'function'), 'function': {'name': '', 'arguments': ''}}
                
                if 'id' in tc and tc['id']:
                    tool_calls[idx]['id'] = tc['id']
                if 'type' in tc and tc['type']:
                    tool_calls[idx]['type'] = tc['type']
                    
                if 'function' in tc:
                    if 'name' in tc['function'] and tc['function']['name']:
                        tool_calls[idx]['function']['name'] += tc['function']['name']
                    if 'arguments' in tc['function'] and tc['function']['arguments']:
                        tool_calls[idx]['function']['arguments'] += tc['function']['arguments'] or ''
                        
    if has_function_call:
        aggregated['function_call'] = function_call
    if tool_calls:
        aggregated['tool_calls'] = [tool_calls[k] for k in sorted(tool_calls.keys())]
    if usage:
        aggregated['usage'] = usage
        
    return aggregated

def forward_stream_response(response, request_data, proxy_config, start_time, log_responses=True):
    """转发流式响应"""
    streamed_chunks = []
    for line in response.iter_lines():
        if line:
            decoded_line = line.decode('utf-8')
            if log_responses and decoded_line.startswith('data: '):
                if decoded_line != 'data: [DONE]':
                    try:
                        chunk_data = json.loads(decoded_line[6:])
                        streamed_chunks.append(chunk_data)
                    except json.JSONDecodeError:
                        pass
            yield decoded_line + '\n\n'
            
    if streamed_chunks:
        aggregated = _aggregate_stream_chunks(streamed_chunks)
        if log_responses:
            logger.info(f"[PROXY] Stream output finished. Aggregated result: {json.dumps(aggregated, ensure_ascii=False)}")
        
        # Log to DB
        processing_time_ms = int((time.time() - start_time) * 1000)
        usage = aggregated.get('usage', {})
        norm = normalize_usage(usage)
        if not norm:
            norm = normalize_usage(calculate_usage(request_data, aggregated))
        try:
            insert_log({
                'model': request_data.get('model'),
                'is_stream': True,
                'status_code': response.status_code,
                'processing_time_ms': processing_time_ms,
                'prompt_tokens': norm['prompt_tokens'],
                'completion_tokens': norm['completion_tokens'],
                'total_tokens': norm['total_tokens'],
                'cache_creation_input_tokens': norm.get('cache_creation_input_tokens'),
                'cache_read_input_tokens': norm.get('cache_read_input_tokens'),
                'target_url': proxy_config.get('target_url'),
                'request_data': request_data,
                'response_data': aggregated,
                'protocol': proxy_config.get('protocol'),
                'usage_data': norm['raw'],
            })
        except Exception as e:
            logger.error(f"[PROXY] Failed to log stream response: {e}")


def handle_proxy_request(request_data, proxy_config):
    """处理代理模式请求"""
    start_time = time.time()
    try:
        is_stream = request_data.get('stream', False)
        log_responses = proxy_config.get('log_responses', True)
        
        response = forward_request(request_data, proxy_config)
        
        if response.status_code != 200:
            logger.error(f"[PROXY] Target API returned error: {response.status_code}")
            processing_time_ms = int((time.time() - start_time) * 1000)
            
            try:
                error_resp = response.json()
            except Exception:
                error_resp = response.text
                
            insert_log({
                'model': request_data.get('model'),
                'is_stream': is_stream,
                'status_code': response.status_code,
                'processing_time_ms': processing_time_ms,
                'target_url': proxy_config.get('target_url'),
                'request_data': request_data,
                'response_data': error_resp,
                'error_message': f"Target API returned error: {response.status_code}",
                'protocol': proxy_config.get('protocol'),
            })
            return jsonify(error_resp), response.status_code
        
        if is_stream:
            return Response(
                forward_stream_response(response, request_data, proxy_config, start_time, log_responses),
                mimetype='text/event-stream'
            )
        else:
            resp_json = response.json()
            processing_time_ms = int((time.time() - start_time) * 1000)
            usage = resp_json.get('usage', {})
            norm = normalize_usage(usage)
            if not norm:
                norm = normalize_usage(calculate_usage(request_data, resp_json))

            insert_log({
                'model': request_data.get('model'),
                'is_stream': False,
                'status_code': 200,
                'processing_time_ms': processing_time_ms,
                'prompt_tokens': norm['prompt_tokens'],
                'completion_tokens': norm['completion_tokens'],
                'total_tokens': norm['total_tokens'],
                'cache_creation_input_tokens': norm.get('cache_creation_input_tokens'),
                'cache_read_input_tokens': norm.get('cache_read_input_tokens'),
                'target_url': proxy_config.get('target_url'),
                'request_data': request_data,
                'response_data': resp_json,
                'protocol': proxy_config.get('protocol'),
                'usage_data': norm['raw'],
            })
            return jsonify(resp_json), 200
            
    except requests.exceptions.Timeout:
        processing_time_ms = int((time.time() - start_time) * 1000)
        insert_log({
            'model': request_data.get('model'),
            'is_stream': request_data.get('stream', False),
            'status_code': 504,
            'processing_time_ms': processing_time_ms,
            'target_url': proxy_config.get('target_url'),
            'request_data': request_data,
            'error_message': 'Request to target API timed out',
            'protocol': proxy_config.get('protocol'),
        })
        return jsonify({
            'error': {
                'message': 'Request to target API timed out',
                'type': 'timeout_error'
            }
        }), 504
    except requests.exceptions.RequestException as e:
        processing_time_ms = int((time.time() - start_time) * 1000)
        insert_log({
            'model': request_data.get('model'),
            'is_stream': request_data.get('stream', False),
            'status_code': 502,
            'processing_time_ms': processing_time_ms,
            'target_url': proxy_config.get('target_url'),
            'request_data': request_data,
            'error_message': f'Failed to forward request: {str(e)}',
            'protocol': proxy_config.get('protocol'),
        })
        return jsonify({
            'error': {
                'message': f'Failed to forward request: {str(e)}',
                'type': 'proxy_error'
            }
        }), 502
