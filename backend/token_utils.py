import tiktoken
import json
import logging

logger = logging.getLogger(__name__)

def estimate_tokens(text, model="gpt-3.5-turbo"):
    """使用 tiktoken 估算文本的 token 数量"""
    if not text:
        return 0
    try:
        try:
            encoding = tiktoken.encoding_for_model(model)
        except KeyError:
            # 如果模型未找到，默认使用 cl100k_base
            encoding = tiktoken.get_encoding("cl100k_base")
        return len(encoding.encode(text))
    except Exception as e:
        logger.error(f"tiktoken encoding failed: {e}. Using fallback length-based estimation.")
        # 简单回退：英文字符约 4 个字母一个 token，中文字符约 1-2 个 token，这里粗略以长度/3计算
        return max(1, len(str(text)) // 3)

def estimate_anthropic_tokens(text):
    """Estimate token count for Anthropic models using cl100k_base as approximation."""
    if not text:
        return 0
    try:
        encoding = tiktoken.get_encoding("cl100k_base")
        return len(encoding.encode(text))
    except Exception as e:
        logger.warning(f"Anthropic token estimation failed: {e}. Using character fallback.")
        return max(1, len(str(text)) // 3)


def calculate_anthropic_usage(request_data, response_data):
    """
    Estimate Anthropic input_tokens and output_tokens from request/response data.
    Returns both Anthropic-format keys and DB-compatible keys.
    """
    try:
        input_text = ""
        # Top-level system prompt
        system = request_data.get('system')
        if system:
            if isinstance(system, str):
                input_text += system
            elif isinstance(system, list):
                for block in system:
                    if isinstance(block, dict) and 'text' in block:
                        input_text += block['text']
        # Messages
        for msg in request_data.get('messages', []):
            content = msg.get('content', '')
            if isinstance(content, str):
                input_text += content
            elif isinstance(content, list):
                for block in content:
                    if isinstance(block, dict) and block.get('type') == 'text':
                        input_text += block.get('text', '')
        # Tools
        tools = request_data.get('tools')
        if tools:
            input_text += json.dumps(tools, ensure_ascii=False)

        input_tokens = estimate_anthropic_tokens(input_text)

        # Estimate output tokens from response content blocks
        output_text = ""
        for block in response_data.get('content', []):
            if isinstance(block, dict):
                if block.get('type') == 'text':
                    output_text += block.get('text', '')
                elif block.get('type') == 'tool_use':
                    output_text += json.dumps(block.get('input', {}), ensure_ascii=False)

        output_tokens = estimate_anthropic_tokens(output_text)

        return {
            'input_tokens': input_tokens,
            'output_tokens': output_tokens,
            'total_tokens': input_tokens + output_tokens,
            'prompt_tokens': input_tokens,
            'completion_tokens': output_tokens,
        }
    except Exception as e:
        logger.error(f"Failed to calculate Anthropic token usage: {e}")
        return {
            'input_tokens': 0, 'output_tokens': 0, 'total_tokens': 0,
            'prompt_tokens': 0, 'completion_tokens': 0,
        }


def calculate_usage(request_data, response_data):
    """
    当返回结果没有提供 usage 时，自动估算 prompt 和 completion 的 token 数
    """
    try:
        model = request_data.get('model', 'gpt-3.5-turbo')
        
        # 1. 估算 Prompt Tokens
        prompt_text = ""
        for msg in request_data.get('messages', []):
            prompt_text += msg.get('content', '') or ''
        
        prompt_tokens = estimate_tokens(prompt_text, model)
        
        # 2. 估算 Completion Tokens
        completion_text = ""
        
        # 处理 _aggregate_stream_chunks 返回的结构
        if 'content' in response_data and isinstance(response_data.get('content'), str):
            completion_text += response_data['content']
            
        if 'tool_calls' in response_data and response_data['tool_calls']:
            completion_text += json.dumps(response_data['tool_calls'], ensure_ascii=False)
            
        if 'function_call' in response_data and response_data['function_call']:
            completion_text += json.dumps(response_data['function_call'], ensure_ascii=False)
            
        # 处理标准同步响应结构 (choices)
        if 'choices' in response_data and isinstance(response_data['choices'], list) and len(response_data['choices']) > 0:
            choice = response_data['choices'][0]
            message = choice.get('message', {})
            
            if 'content' in message and isinstance(message.get('content'), str):
                completion_text += message['content']
                
            if 'tool_calls' in message and message['tool_calls']:
                completion_text += json.dumps(message['tool_calls'], ensure_ascii=False)
                
            if 'function_call' in message and message['function_call']:
                completion_text += json.dumps(message['function_call'], ensure_ascii=False)
                
            # 部分 API 可能直接返回 text
            if not message and 'text' in choice and isinstance(choice.get('text'), str):
                completion_text += choice['text']
                
        completion_tokens = estimate_tokens(completion_text, model)
        
        return {
            'prompt_tokens': prompt_tokens,
            'completion_tokens': completion_tokens,
            'total_tokens': prompt_tokens + completion_tokens
        }
    except Exception as e:
        logger.error(f"Failed to calculate token usage: {e}")
        return {
            'prompt_tokens': 0,
            'completion_tokens': 0,
            'total_tokens': 0
        }
