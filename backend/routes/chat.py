import json
import logging
from flask import Blueprint, request, jsonify
from config import get_mode, get_proxy_config
from services.proxy import handle_proxy_request
from services.mock import handle_mock_request

logger = logging.getLogger(__name__)
chat_bp = Blueprint('chat', __name__)

@chat_bp.route('/v1/chat/completions', methods=['POST'])
def chat_completions():
    try:
        mode = get_mode()
        proxy_config = get_proxy_config()
        
        data = request.json
        # 兼容同名 Header 的多值情况，将其转为逗号分隔的字符串，避免字典覆盖
        headers = {k: ", ".join(request.headers.getlist(k)) for k in request.headers.keys()}
        logger.info(f"[HEADERS] Received headers: {json.dumps(headers, ensure_ascii=False)}")
        logger.info(f"[INPUT] Received request: {json.dumps(data, ensure_ascii=False)}")
        
        if mode == 'proxy' and proxy_config.get('enabled', False):
            logger.info(f"[MODE] Using proxy mode")
            return handle_proxy_request(data, proxy_config)
        else:
            logger.info(f"[MODE] Using mock mode")
            return handle_mock_request(data)
            
    except Exception as e:
        logger.error(f"Error processing request: {e}")
        return jsonify({'error': {'message': str(e), 'type': 'internal_server_error'}}), 500
