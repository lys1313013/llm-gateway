import json
import logging
import fnmatch
from flask import Blueprint, request, jsonify
from db import get_active_routes
from services.anthropic_proxy import handle_anthropic_proxy_request

logger = logging.getLogger(__name__)
anthropic_bp = Blueprint('anthropic', __name__)


def match_route_for_anthropic(model):
    """Match a route for Anthropic protocol."""
    active_routes = get_active_routes()
    for route in active_routes:
        if route.get('protocol') != 'anthropic':
            continue
        if fnmatch.fnmatch(model, route['model_pattern']):
            return route
    return None


@anthropic_bp.route('/v1/messages', methods=['POST'])
def anthropic_messages():
    try:
        data = request.json
        if not data:
            return jsonify({
                'type': 'error',
                'error': {
                    'type': 'invalid_request_error',
                    'message': 'Request body must be JSON',
                },
            }), 400

        model = data.get('model', '')

        headers = {k: ", ".join(request.headers.getlist(k)) for k in request.headers.keys()}
        logger.info(f"[ANTHROPIC] Received headers: {json.dumps(headers, ensure_ascii=False)}")
        logger.info(f"[ANTHROPIC] Received request: {json.dumps(data, ensure_ascii=False)}")

        route = match_route_for_anthropic(model)

        if not route:
            logger.warning(f"[ANTHROPIC] No route matched for model '{model}'")
            return jsonify({
                'type': 'error',
                'error': {
                    'type': 'not_found_error',
                    'message': f"No route matched for model '{model}'",
                },
            }), 404

        logger.info(f"[ANTHROPIC] Using route '{route['model_pattern']}'")

        base_url = route.get('base_url', '').rstrip('/')
        target_url = f"{base_url}/v1/messages"

        anthropic_version = request.headers.get('anthropic-version', '2023-06-01')

        proxy_config = {
            'target_url': target_url,
            'api_key': route.get('api_key'),
            'timeout': route.get('timeout', 60),
            'log_requests': route.get('log_requests', True),
            'log_responses': route.get('log_responses', True),
            'model': route.get('target_model') or model,
            'anthropic_version': anthropic_version,
            'protocol': route.get('protocol', 'anthropic'),
        }
        return handle_anthropic_proxy_request(data, proxy_config)

    except Exception as e:
        logger.error(f"[ANTHROPIC] Error processing request: {e}")
        return jsonify({
            'type': 'error',
            'error': {
                'type': 'internal_server_error',
                'message': str(e),
            },
        }), 500