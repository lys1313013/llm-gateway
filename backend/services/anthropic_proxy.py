import json
import logging
import requests
import time
from flask import Response, jsonify
from db import insert_log
from token_utils import calculate_anthropic_usage, normalize_usage

logger = logging.getLogger(__name__)


def forward_anthropic_request(request_data, proxy_config):
    """Forward request to an Anthropic-compatible upstream API."""
    target_url = proxy_config.get('target_url')
    api_key = proxy_config.get('api_key')
    timeout = proxy_config.get('timeout', 60)
    log_requests = proxy_config.get('log_requests', True)
    anthropic_version = proxy_config.get('anthropic_version', '2023-06-01')
    model = proxy_config.get('model')
    if model:
        request_data['model'] = model

    if log_requests:
        logger.info(f"[ANTHROPIC-PROXY] Forwarding request to {target_url}")
        logger.info(f"[ANTHROPIC-PROXY] Request data: {json.dumps(request_data, ensure_ascii=False)}")

    headers = {
        'Content-Type': 'application/json',
        'x-api-key': api_key,
        'anthropic-version': anthropic_version,
    }

    is_stream = request_data.get('stream', False)

    try:
        response = requests.post(
            target_url,
            json=request_data,
            headers=headers,
            stream=is_stream,
            timeout=timeout,
        )

        if log_requests and response.status_code == 200 and not is_stream:
            logger.info(f"[ANTHROPIC-PROXY] Output response: {json.dumps(response.json(), ensure_ascii=False)}")

        return response
    except requests.exceptions.Timeout:
        logger.error(f"[ANTHROPIC-PROXY] Request timeout after {timeout} seconds")
        raise
    except requests.exceptions.RequestException as e:
        logger.error(f"[ANTHROPIC-PROXY] Request failed: {e}")
        raise


def _aggregate_anthropic_stream_events(events):
    """Aggregate Anthropic SSE events into a complete response dict for logging."""
    result = {
        'id': None,
        'type': 'message',
        'role': 'assistant',
        'model': None,
        'content': [],
        'stop_reason': None,
        'stop_sequence': None,
        'usage': {'input_tokens': 0, 'output_tokens': 0},
    }
    # content_blocks by index: {index: {type, text/id/name/input}}
    content_blocks = {}

    for event in events:
        if not isinstance(event, dict):
            continue
        event_type = event.get('type')

        if event_type == 'message_start':
            msg = event.get('message', {})
            result['id'] = msg.get('id', result['id'])
            result['model'] = msg.get('model', result['model'])
            result['role'] = msg.get('role', result['role'])
            if msg.get('usage'):
                result['usage']['input_tokens'] = msg['usage'].get('input_tokens', 0)
                result['usage']['output_tokens'] = msg['usage'].get('output_tokens', 0)
                result['usage']['cache_creation_input_tokens'] = msg['usage'].get('cache_creation_input_tokens', 0)
                result['usage']['cache_read_input_tokens'] = msg['usage'].get('cache_read_input_tokens', 0)

        elif event_type == 'content_block_start':
            idx = event.get('index', 0)
            cb = event.get('content_block', {})
            content_blocks[idx] = dict(cb)

        elif event_type == 'content_block_delta':
            idx = event.get('index', 0)
            delta = event.get('delta', {})
            if idx not in content_blocks:
                content_blocks[idx] = {}
            cb = content_blocks[idx]
            delta_type = delta.get('type')
            if delta_type == 'text_delta':
                cb['type'] = 'text'
                cb['text'] = (cb.get('text', '') or '') + delta.get('text', '')
            elif delta_type == 'input_json_delta':
                cb['type'] = 'tool_use'
                current = cb.get('input', '')
                if isinstance(current, dict):
                    current = ''
                cb['input'] = current + delta.get('partial_json', '')
            elif delta_type == 'thinking_delta':
                cb['type'] = 'thinking'
                cb['thinking'] = (cb.get('thinking', '') or '') + delta.get('thinking', '')

        elif event_type == 'content_block_stop':
            idx = event.get('index', 0)
            if idx in content_blocks and content_blocks[idx].get('type') == 'tool_use':
                cb = content_blocks[idx]
                raw_input = cb.get('input', '{}')
                try:
                    cb['input'] = json.loads(raw_input)
                except (json.JSONDecodeError, TypeError):
                    cb['input'] = raw_input

        elif event_type == 'message_delta':
            delta = event.get('delta', {})
            result['stop_reason'] = delta.get('stop_reason', result['stop_reason'])
            result['stop_sequence'] = delta.get('stop_sequence', result['stop_sequence'])
            if event.get('usage'):
                result['usage']['output_tokens'] = event['usage'].get('output_tokens', result['usage']['output_tokens'])

    # Assemble content array in index order
    result['content'] = [
        content_blocks[k] for k in sorted(content_blocks.keys())
    ]

    return result


def forward_anthropic_stream_response(response, request_data, proxy_config, start_time, log_responses=True):
    """Generator that passes through Anthropic SSE stream and logs aggregated result."""
    streamed_events = []
    for line in response.iter_lines():
        if line:
            decoded_line = line.decode('utf-8')
            if log_responses and decoded_line.startswith('data: '):
                try:
                    event_data = json.loads(decoded_line[6:])
                    streamed_events.append(event_data)
                except json.JSONDecodeError:
                    pass
            yield decoded_line + '\n'

    if streamed_events:
        aggregated = _aggregate_anthropic_stream_events(streamed_events)
        if log_responses:
            logger.info(f"[ANTHROPIC-PROXY] Stream finished. Aggregated: {json.dumps(aggregated, ensure_ascii=False)}")

        processing_time_ms = int((time.time() - start_time) * 1000)
        usage = aggregated.get('usage', {})
        norm = normalize_usage(usage)
        if not norm:
            norm = normalize_usage(calculate_anthropic_usage(request_data, aggregated))
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


def handle_anthropic_proxy_request(request_data, proxy_config):
    """Handle an Anthropic proxy-mode request."""
    start_time = time.time()
    try:
        is_stream = request_data.get('stream', False)
        log_responses = proxy_config.get('log_responses', True)

        response = forward_anthropic_request(request_data, proxy_config)

        if response.status_code != 200:
            logger.error(f"[ANTHROPIC-PROXY] Target API returned error: {response.status_code}")
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
                'response_data': error_resp if isinstance(error_resp, dict) else {'raw': error_resp},
                'error_message': f"Target API returned error: {response.status_code}",
                'protocol': proxy_config.get('protocol'),
            })

            # Return Anthropic-format error
            if isinstance(error_resp, dict) and 'error' in error_resp:
                return jsonify(error_resp), response.status_code
            anthropic_error = {
                'type': 'error',
                'error': {
                    'type': 'api_error',
                    'message': f"Upstream returned status {response.status_code}",
                },
            }
            return jsonify(anthropic_error), response.status_code

        if is_stream:
            return Response(
                forward_anthropic_stream_response(response, request_data, proxy_config, start_time, log_responses),
                mimetype='text/event-stream',
            )
        else:
            resp_json = response.json()
            processing_time_ms = int((time.time() - start_time) * 1000)
            usage = resp_json.get('usage', {})
            norm = normalize_usage(usage)
            if not norm:
                norm = normalize_usage(calculate_anthropic_usage(request_data, resp_json))
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
            'type': 'error',
            'error': {
                'type': 'timeout_error',
                'message': 'Request to target API timed out',
            },
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
            'type': 'error',
            'error': {
                'type': 'proxy_error',
                'message': f'Failed to forward request: {str(e)}',
            },
        }), 502