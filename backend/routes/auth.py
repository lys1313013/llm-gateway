import logging

from flask import Blueprint, jsonify, request

from auth import (
    hash_password, verify_password, generate_jwt, decode_jwt,
    generate_api_key, hash_api_key,
)
from db import (
    create_user, get_user_by_username, get_user_by_id, get_users,
    update_user_password, delete_user,
    create_api_key, get_api_keys_by_user, get_api_key_by_hash,
    delete_api_key, toggle_api_key, update_api_key_name,
)

logger = logging.getLogger(__name__)

auth_bp = Blueprint('auth', __name__, url_prefix='/api/auth')


def _get_current_user():
    """Extract and decode JWT from Authorization header."""
    auth_header = request.headers.get('Authorization', '')
    if not auth_header.startswith('Bearer '):
        return None
    token = auth_header[7:]
    payload = decode_jwt(token)
    if not payload:
        return None
    return payload


# ---------------------------------------------------------------------------
# Auth endpoints (no JWT required)
# ---------------------------------------------------------------------------
@auth_bp.route('/register', methods=['POST'])
def register():
    data = request.get_json(silent=True)
    if not data:
        return jsonify({'success': False, 'message': '请求体不能为空'}), 400

    username = (data.get('username') or '').strip()
    password = data.get('password') or ''

    if not username or not password:
        return jsonify({'success': False, 'message': '用户名和密码不能为空'}), 400
    if len(username) < 3 or len(username) > 100:
        return jsonify({'success': False, 'message': '用户名长度需在 3-100 之间'}), 400
    if len(password) < 6:
        return jsonify({'success': False, 'message': '密码长度至少 6 位'}), 400

    existing = get_user_by_username(username)
    if existing:
        return jsonify({'success': False, 'message': '用户名已存在'}), 409

    pw_hash = hash_password(password)
    user = create_user(username, pw_hash)
    if not user:
        return jsonify({'success': False, 'message': '注册失败，请重试'}), 500

    token = generate_jwt(user['id'], user['username'])
    return jsonify({
        'success': True,
        'data': {
            'user': {'id': user['id'], 'username': user['username']},
            'token': token,
        },
    }), 201


@auth_bp.route('/login', methods=['POST'])
def login():
    data = request.get_json(silent=True)
    if not data:
        return jsonify({'success': False, 'message': '请求体不能为空'}), 400

    username = (data.get('username') or '').strip()
    password = data.get('password') or ''

    if not username or not password:
        return jsonify({'success': False, 'message': '用户名和密码不能为空'}), 400

    user = get_user_by_username(username)
    if not user or not verify_password(password, user['password_hash']):
        return jsonify({'success': False, 'message': '用户名或密码错误'}), 401

    if not user.get('is_active', True):
        return jsonify({'success': False, 'message': '账号已被禁用'}), 403

    token = generate_jwt(user['id'], user['username'])
    return jsonify({
        'success': True,
        'data': {
            'user': {'id': user['id'], 'username': user['username']},
            'token': token,
        },
    })


# ---------------------------------------------------------------------------
# Protected endpoints (JWT required)
# ---------------------------------------------------------------------------
@auth_bp.route('/me', methods=['GET'])
def me():
    payload = _get_current_user()
    if not payload:
        return jsonify({'success': False, 'message': '未授权'}), 401
    user = get_user_by_id(payload['sub'])
    if not user:
        return jsonify({'success': False, 'message': '用户不存在'}), 404
    return jsonify({'success': True, 'data': user})


@auth_bp.route('/change_password', methods=['PUT'])
def change_password():
    payload = _get_current_user()
    if not payload:
        return jsonify({'success': False, 'message': '未授权'}), 401

    data = request.get_json(silent=True)
    if not data:
        return jsonify({'success': False, 'message': '请求体不能为空'}), 400

    old_password = data.get('old_password') or ''
    new_password = data.get('new_password') or ''

    if not old_password or not new_password:
        return jsonify({'success': False, 'message': '旧密码和新密码不能为空'}), 400
    if len(new_password) < 6:
        return jsonify({'success': False, 'message': '新密码长度至少 6 位'}), 400

    user = get_user_by_username(payload['username'])
    if not user:
        return jsonify({'success': False, 'message': '用户不存在'}), 404

    # Need full user record with password_hash for verification
    from db import get_user_by_username as _get_full
    full_user = _get_full(payload['username'])
    if not full_user or not verify_password(old_password, full_user['password_hash']):
        return jsonify({'success': False, 'message': '旧密码错误'}), 400

    new_hash = hash_password(new_password)
    if not update_user_password(payload['sub'], new_hash):
        return jsonify({'success': False, 'message': '修改密码失败'}), 500

    return jsonify({'success': True, 'message': '密码修改成功'})


@auth_bp.route('/users', methods=['GET'])
def list_users():
    payload = _get_current_user()
    if not payload:
        return jsonify({'success': False, 'message': '未授权'}), 401
    users = get_users()
    return jsonify({'success': True, 'data': users})


@auth_bp.route('/users/<int:user_id>', methods=['DELETE'])
def remove_user(user_id):
    payload = _get_current_user()
    if not payload:
        return jsonify({'success': False, 'message': '未授权'}), 401
    if payload['sub'] == user_id:
        return jsonify({'success': False, 'message': '不能删除自己'}), 400
    if not delete_user(user_id):
        return jsonify({'success': False, 'message': '删除用户失败'}), 500
    return jsonify({'success': True, 'message': '用户已删除'})


# ---------------------------------------------------------------------------
# API Key management (JWT required)
# ---------------------------------------------------------------------------
@auth_bp.route('/api_keys', methods=['GET'])
def list_api_keys():
    payload = _get_current_user()
    if not payload:
        return jsonify({'success': False, 'message': '未授权'}), 401
    keys = get_api_keys_by_user(payload['sub'])
    return jsonify({'success': True, 'data': keys})


@auth_bp.route('/api_keys', methods=['POST'])
def create_key():
    payload = _get_current_user()
    if not payload:
        return jsonify({'success': False, 'message': '未授权'}), 401

    data = request.get_json(silent=True)
    name = 'default'
    if data and data.get('name'):
        name = data['name'].strip() or 'default'

    full_key, key_hash, key_prefix = generate_api_key()
    key_record = create_api_key(payload['sub'], key_hash, key_prefix, name, key_value=full_key)
    if not key_record:
        return jsonify({'success': False, 'message': '创建 API Key 失败'}), 500

    return jsonify({
        'success': True,
        'data': {
            **key_record,
            'key': full_key,  # Full key only returned once
        },
    }), 201


@auth_bp.route('/api_keys/<int:key_id>', methods=['DELETE'])
def remove_api_key(key_id):
    payload = _get_current_user()
    if not payload:
        return jsonify({'success': False, 'message': '未授权'}), 401
    if not delete_api_key(key_id):
        return jsonify({'success': False, 'message': '删除 API Key 失败'}), 500
    return jsonify({'success': True, 'message': 'API Key 已删除'})


@auth_bp.route('/api_keys/<int:key_id>/toggle', methods=['PUT'])
def toggle_key(key_id):
    payload = _get_current_user()
    if not payload:
        return jsonify({'success': False, 'message': '未授权'}), 401

    data = request.get_json(silent=True)
    if not data or 'is_active' not in data:
        return jsonify({'success': False, 'message': '缺少 is_active 参数'}), 400

    result = toggle_api_key(key_id, data['is_active'])
    if not result:
        return jsonify({'success': False, 'message': '操作失败'}), 500
    return jsonify({'success': True, 'data': result})


@auth_bp.route('/api_keys/<int:key_id>', methods=['PUT'])
def update_key(key_id):
    payload = _get_current_user()
    if not payload:
        return jsonify({'success': False, 'message': '未授权'}), 401

    data = request.get_json(silent=True)
    if not data or not data.get('name'):
        return jsonify({'success': False, 'message': '名称不能为空'}), 400

    name = data['name'].strip()
    if not name:
        return jsonify({'success': False, 'message': '名称不能为空'}), 400
    if len(name) > 100:
        return jsonify({'success': False, 'message': '名称长度不能超过 100'}), 400

    result = update_api_key_name(key_id, name)
    if not result:
        return jsonify({'success': False, 'message': '更新失败'}), 500
    return jsonify({'success': True, 'data': result})
