import fnmatch
import logging

from flask import Blueprint, request, jsonify
from db import (
    get_providers, get_provider, create_provider, update_provider, delete_provider,
    get_routes, get_route, create_route, update_route, delete_route,
    get_exposed_models, get_exposed_model, get_exposed_model_by_name, create_exposed_model, update_exposed_model, delete_exposed_model,
    update_exposed_model_test_time, get_active_routes,
)
from services.proxy import handle_proxy_request
from services.anthropic_proxy import handle_anthropic_proxy_request

logger = logging.getLogger(__name__)

admin_bp = Blueprint('admin', __name__, url_prefix='/api')


def _get_json():
    """Return request JSON or None if body is empty/invalid."""
    data = request.json
    if not data:
        return None
    return data


def _bad_json():
    return jsonify({'success': False, 'message': 'Request body must be JSON'}), 400


# ---------------------------------------------------------------------------
# Provider
# ---------------------------------------------------------------------------
@admin_bp.route('/provider', methods=['GET'])
def list_providers():
    return jsonify({'success': True, 'data': get_providers()})


@admin_bp.route('/provider/<int:id>', methods=['GET'])
def retrieve_provider(id):
    data = get_provider(id)
    if not data:
        return jsonify({'success': False, 'message': 'Not found'}), 404
    return jsonify({'success': True, 'data': data})


@admin_bp.route('/provider', methods=['POST'])
def add_provider():
    data = _get_json()
    if data is None:
        return _bad_json()
    result = create_provider(data)
    if not result:
        return jsonify({'success': False, 'message': 'Failed to create'}), 500
    return jsonify({'success': True, 'data': result})


@admin_bp.route('/provider/<int:id>', methods=['PUT'])
def modify_provider(id):
    data = _get_json()
    if data is None:
        return _bad_json()
    result = update_provider(id, data)
    if not result:
        return jsonify({'success': False, 'message': 'Failed to update'}), 500
    return jsonify({'success': True, 'data': result})


@admin_bp.route('/provider/<int:id>', methods=['DELETE'])
def remove_provider(id):
    if delete_provider(id):
        return jsonify({'success': True})
    return jsonify({'success': False, 'message': 'Failed to delete'}), 500


# ---------------------------------------------------------------------------
# Model Route
# ---------------------------------------------------------------------------
@admin_bp.route('/route', methods=['GET'])
def list_routes():
    return jsonify({'success': True, 'data': get_routes()})


@admin_bp.route('/route/<int:id>', methods=['GET'])
def retrieve_route(id):
    data = get_route(id)
    if not data:
        return jsonify({'success': False, 'message': 'Not found'}), 404
    return jsonify({'success': True, 'data': data})


@admin_bp.route('/route', methods=['POST'])
def add_route():
    data = _get_json()
    if data is None:
        return _bad_json()
    result = create_route(data)
    if not result:
        return jsonify({'success': False, 'message': 'Failed to create'}), 500
    return jsonify({'success': True, 'data': result})


@admin_bp.route('/route/<int:id>', methods=['PUT'])
def modify_route(id):
    data = _get_json()
    if data is None:
        return _bad_json()
    result = update_route(id, data)
    if not result:
        return jsonify({'success': False, 'message': 'Failed to update'}), 500
    return jsonify({'success': True, 'data': result})


@admin_bp.route('/route/<int:id>', methods=['DELETE'])
def remove_route(id):
    if delete_route(id):
        return jsonify({'success': True})
    return jsonify({'success': False, 'message': 'Failed to delete'}), 500


# ---------------------------------------------------------------------------
# Exposed Model
# ---------------------------------------------------------------------------
@admin_bp.route('/exposed_model', methods=['GET'])
def list_exposed_models():
    return jsonify({'success': True, 'data': get_exposed_models()})


@admin_bp.route('/exposed_model/<int:id>', methods=['GET'])
def retrieve_exposed_model(id):
    data = get_exposed_model(id)
    if not data:
        return jsonify({'success': False, 'message': 'Not found'}), 404
    return jsonify({'success': True, 'data': data})


@admin_bp.route('/exposed_model', methods=['POST'])
def add_exposed_model():
    data = _get_json()
    if data is None:
        return _bad_json()
    if get_exposed_model_by_name(data.get('model_id')):
        return jsonify({'success': False, 'message': f"model_id '{data.get('model_id')}' 已存在，不能重复添加"}), 409
    result = create_exposed_model(data)
    if not result:
        return jsonify({'success': False, 'message': 'Failed to create'}), 500
    return jsonify({'success': True, 'data': result})


@admin_bp.route('/exposed_model/<int:id>', methods=['PUT'])
def modify_exposed_model(id):
    data = _get_json()
    if data is None:
        return _bad_json()
    result = update_exposed_model(id, data)
    if not result:
        return jsonify({'success': False, 'message': 'Failed to update'}), 500
    return jsonify({'success': True, 'data': result})


@admin_bp.route('/exposed_model/<int:id>', methods=['DELETE'])
def remove_exposed_model(id):
    if delete_exposed_model(id):
        return jsonify({'success': True})
    return jsonify({'success': False, 'message': 'Failed to delete'}), 500


@admin_bp.route('/exposed_model/<int:id>/test_time', methods=['PUT'])
def update_test_time(id):
    data = _get_json()
    if data is None:
        return _bad_json()
    protocol = data.get('protocol')
    if protocol not in ('openai', 'anthropic'):
        return jsonify({'success': False, 'message': 'protocol must be openai or anthropic'}), 400
    result = update_exposed_model_test_time(id, protocol)
    if not result:
        return jsonify({'success': False, 'message': 'Failed to update'}), 500
    return jsonify({'success': True, 'data': result})


# ---------------------------------------------------------------------------
# Test Proxy — admin-only endpoints that proxy requests using JWT auth
# (no API Key required; used by the frontend model test feature)
# ---------------------------------------------------------------------------
def _match_route_for_test(model, protocol):
    """Match an active route for the given model and protocol."""
    active_routes = get_active_routes()
    url_field = 'openai_base_url' if protocol == 'openai' else 'anthropic_base_url'
    for route in active_routes:
        if not route.get(url_field):
            continue
        if fnmatch.fnmatch(model, route['model_pattern']):
            return route
    return None


@admin_bp.route('/test/chat', methods=['POST'])
def test_chat():
    """Admin-only test endpoint for OpenAI protocol."""
    try:
        data = request.json
        if not data:
            return jsonify({'error': {'message': 'Request body must be JSON', 'type': 'invalid_request_error'}}), 400

        model = data.get('model', '')
        logger.info(f"[TEST-CHAT] Testing model: {model}")

        route = _match_route_for_test(model, 'openai')
        if not route:
            return jsonify({'error': {'message': f"No route matched for model '{model}'", 'type': 'invalid_request_error'}}), 404

        base_url = route.get('openai_base_url', '').rstrip('/')
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
            'protocol': 'openai',
        }
        return handle_proxy_request(data, proxy_config)

    except Exception as e:
        logger.error(f"[TEST-CHAT] Error: {e}")
        return jsonify({'error': {'message': str(e), 'type': 'internal_server_error'}}), 500


@admin_bp.route('/test/messages', methods=['POST'])
def test_messages():
    """Admin-only test endpoint for Anthropic protocol."""
    try:
        data = request.json
        if not data:
            return jsonify({
                'type': 'error',
                'error': {'type': 'invalid_request_error', 'message': 'Request body must be JSON'},
            }), 400

        model = data.get('model', '')
        logger.info(f"[TEST-ANTHROPIC] Testing model: {model}")

        route = _match_route_for_test(model, 'anthropic')
        if not route:
            return jsonify({
                'type': 'error',
                'error': {'type': 'not_found_error', 'message': f"No route matched for model '{model}'"},
            }), 404

        base_url = route.get('anthropic_base_url', '').rstrip('/')
        target_url = f"{base_url}/v1/messages"

        proxy_config = {
            'target_url': target_url,
            'api_key': route.get('api_key'),
            'timeout': route.get('timeout', 60),
            'log_requests': route.get('log_requests', True),
            'log_responses': route.get('log_responses', True),
            'model': route.get('target_model') or model,
            'anthropic_version': '2023-06-01',
            'protocol': 'anthropic',
        }
        return handle_anthropic_proxy_request(data, proxy_config)

    except Exception as e:
        logger.error(f"[TEST-ANTHROPIC] Error: {e}")
        return jsonify({
            'type': 'error',
            'error': {'type': 'internal_server_error', 'message': str(e)},
        }), 500
