from flask import Blueprint, request, jsonify
from db import (
    get_providers, get_provider, create_provider, update_provider, delete_provider,
    get_routes, get_route, create_route, update_route, delete_route,
    get_exposed_models, get_exposed_model, create_exposed_model, update_exposed_model, delete_exposed_model,
    update_exposed_model_test_time
)

admin_bp = Blueprint('admin', __name__, url_prefix='/api')

# --- Provider APIs ---
@admin_bp.route('/provider', methods=['GET'])
def list_providers():
    return jsonify({'success': True, 'data': get_providers()})

@admin_bp.route('/provider/<int:id>', methods=['GET'])
def retrieve_provider(id):
    data = get_provider(id)
    if not data: return jsonify({'success': False, 'message': 'Not found'}), 404
    return jsonify({'success': True, 'data': data})

@admin_bp.route('/provider', methods=['POST'])
def add_provider():
    data = request.json
    result = create_provider(data)
    if not result: return jsonify({'success': False, 'message': 'Failed to create'}), 500
    return jsonify({'success': True, 'data': result})

@admin_bp.route('/provider/<int:id>', methods=['PUT'])
def modify_provider(id):
    data = request.json
    result = update_provider(id, data)
    if not result: return jsonify({'success': False, 'message': 'Failed to update'}), 500
    return jsonify({'success': True, 'data': result})

@admin_bp.route('/provider/<int:id>', methods=['DELETE'])
def remove_provider(id):
    if delete_provider(id): return jsonify({'success': True})
    return jsonify({'success': False, 'message': 'Failed to delete'}), 500

# --- Model Route APIs ---
@admin_bp.route('/route', methods=['GET'])
def list_routes():
    return jsonify({'success': True, 'data': get_routes()})

@admin_bp.route('/route/<int:id>', methods=['GET'])
def retrieve_route(id):
    data = get_route(id)
    if not data: return jsonify({'success': False, 'message': 'Not found'}), 404
    return jsonify({'success': True, 'data': data})

@admin_bp.route('/route', methods=['POST'])
def add_route():
    data = request.json
    result = create_route(data)
    if not result: return jsonify({'success': False, 'message': 'Failed to create'}), 500
    return jsonify({'success': True, 'data': result})

@admin_bp.route('/route/<int:id>', methods=['PUT'])
def modify_route(id):
    data = request.json
    result = update_route(id, data)
    if not result: return jsonify({'success': False, 'message': 'Failed to update'}), 500
    return jsonify({'success': True, 'data': result})

@admin_bp.route('/route/<int:id>', methods=['DELETE'])
def remove_route(id):
    if delete_route(id): return jsonify({'success': True})
    return jsonify({'success': False, 'message': 'Failed to delete'}), 500

# --- Exposed Model APIs ---
@admin_bp.route('/exposed_model', methods=['GET'])
def list_exposed_models():
    return jsonify({'success': True, 'data': get_exposed_models()})

@admin_bp.route('/exposed_model/<int:id>', methods=['GET'])
def retrieve_exposed_model(id):
    data = get_exposed_model(id)
    if not data: return jsonify({'success': False, 'message': 'Not found'}), 404
    return jsonify({'success': True, 'data': data})

@admin_bp.route('/exposed_model', methods=['POST'])
def add_exposed_model():
    data = request.json
    result = create_exposed_model(data)
    if not result: return jsonify({'success': False, 'message': 'Failed to create'}), 500
    return jsonify({'success': True, 'data': result})

@admin_bp.route('/exposed_model/<int:id>', methods=['PUT'])
def modify_exposed_model(id):
    data = request.json
    result = update_exposed_model(id, data)
    if not result: return jsonify({'success': False, 'message': 'Failed to update'}), 500
    return jsonify({'success': True, 'data': result})

@admin_bp.route('/exposed_model/<int:id>', methods=['DELETE'])
def remove_exposed_model(id):
    if delete_exposed_model(id): return jsonify({'success': True})
    return jsonify({'success': False, 'message': 'Failed to delete'}), 500

@admin_bp.route('/exposed_model/<int:id>/test_time', methods=['PUT'])
def update_test_time(id):
    data = request.json
    protocol = data.get('protocol')
    if protocol not in ('openai', 'anthropic'):
        return jsonify({'success': False, 'message': 'protocol must be openai or anthropic'}), 400
    result = update_exposed_model_test_time(id, protocol)
    if not result: return jsonify({'success': False, 'message': 'Failed to update'}), 500
    return jsonify({'success': True, 'data': result})
