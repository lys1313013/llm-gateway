import json
import logging
import fnmatch
from flask import Blueprint, request, jsonify
from db import get_active_routes, get_active_exposed_models
from services.proxy import handle_proxy_request

logger = logging.getLogger(__name__)
chat_bp = Blueprint('chat', __name__)

def match_route(model):
    active_routes = get_active_routes()
    for route in active_routes:
        if route.get('protocol') not in ('openai', None):
            continue
        if fnmatch.fnmatch(model, route['model_pattern']):
            return route
    return None

@chat_bp.route('/v1/models', methods=['GET'])
def list_models():
    models = get_active_exposed_models()
    data = []
    for m in models:
        ct = m.get('create_time')
        if isinstance(ct, str):
            from datetime import datetime
            created = int(datetime.fromisoformat(ct).timestamp())
        elif ct:
            created = int(ct.timestamp())
        else:
            created = 0
        data.append({
            'id': m['model_id'],
            'object': 'model',
            'created': created,
            'owned_by': m.get('owned_by', 'organization'),
        })
    return jsonify({'object': 'list', 'data': data})

@chat_bp.route('/v1/chat/completions', methods=['POST'])
def chat_completions():
    try:
        data = request.json
        if not data:
            return jsonify({'error': {'message': 'Request body must be JSON', 'type': 'invalid_request_error'}}), 400

        model = data.get('model', '')

        # 兼容同名 Header 的多值情况，将其转为逗号分隔的字符串，避免字典覆盖
        headers = {k: ", ".join(request.headers.getlist(k)) for k in request.headers.keys()}
        logger.info(f"[HEADERS] Received headers: {json.dumps(headers, ensure_ascii=False)}")
        logger.info(f"[INPUT] Received request: {json.dumps(data, ensure_ascii=False)}")

        
        route = match_route(model)

        if not route:
            logger.warning(f"[CHAT] No route matched for model '{model}'")
            return jsonify({'error': {'message': f"No route matched for model '{model}'", 'type': 'invalid_request_error'}}), 404

        logger.info(f"[PROXY] Using route '{route['model_pattern']}'")

        base_url = route.get('base_url', '').rstrip('/')
        if not base_url.endswith('/chat/completions'):
            target_url = f"{base_url}/chat/completions"
        else:
            target_url = base_url

        proxy_config = {
            'target_url': target_url,
            'api_key': route.get('api_key'),
            'timeout': route.get('timeout', 60),
            'log_requests': route.get('log_requests', True),
            'log_responses': route.get('log_responses', True),
            'model': route.get('target_model') or model,
            'protocol': route.get('protocol', 'openai'),
        }
        return handle_proxy_request(data, proxy_config)
            
    except Exception as e:
        logger.error(f"Error processing request: {e}")
        return jsonify({'error': {'message': str(e), 'type': 'internal_server_error'}}), 500
