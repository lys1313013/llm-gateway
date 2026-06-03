import argparse
import logging
import os

from dotenv import load_dotenv
load_dotenv()

from flask import Flask, jsonify, request
from flask_cors import CORS

from db import (
    init_db, get_logs, get_log_count, get_today_stats,
    get_daily_token_stats, get_hourly_token_stats, get_model_token_stats,
    get_user_count, create_user, update_api_key_last_used,
    get_api_key_by_hash,
)
from auth import decode_jwt, hash_password, hash_api_key
from routes.chat import chat_bp
from routes.admin import admin_bp
from routes.anthropic import anthropic_bp
from routes.auth import auth_bp

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
)
logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Flask app
# ---------------------------------------------------------------------------
app = Flask(__name__)
CORS(app)

app.register_blueprint(chat_bp)
app.register_blueprint(admin_bp)
app.register_blueprint(anthropic_bp)
app.register_blueprint(auth_bp)

# ---------------------------------------------------------------------------
# Auth middleware
# ---------------------------------------------------------------------------
# Paths that do not require any authentication
_AUTH_WHITELIST = ('/api/auth/login', '/api/auth/register')


@app.before_request
def require_auth():
    path = request.path

    # Allow OPTIONS requests (CORS preflight)
    if request.method == 'OPTIONS':
        return None

    # Allow auth endpoints without authentication
    if any(path.startswith(wl) for wl in _AUTH_WHITELIST):
        return None

    # Admin API routes require JWT
    if path.startswith('/api/'):
        auth_header = request.headers.get('Authorization', '')
        if not auth_header.startswith('Bearer '):
            return jsonify({'success': False, 'message': '未授权：缺少 Token'}), 401
        token = auth_header[7:]
        payload = decode_jwt(token)
        if not payload:
            return jsonify({'success': False, 'message': '未授权：Token 无效或已过期'}), 401
        # Store user info in flask.g for downstream use
        from flask import g
        g.current_user_id = payload['sub']
        g.current_username = payload.get('username', '')
        return None

    # Proxy API routes require API Key
    if path.startswith('/v1/'):
        api_key = request.headers.get('x-api-key') or ''
        # Also accept Authorization: Bearer sk-...
        if not api_key:
            auth_header = request.headers.get('Authorization', '')
            if auth_header.startswith('Bearer '):
                bearer_val = auth_header[7:]
                if bearer_val.startswith('sk-'):
                    api_key = bearer_val

        if not api_key:
            return jsonify({'error': {'message': '未授权：缺少 API Key', 'type': 'authentication_error'}}), 401

        key_hash = hash_api_key(api_key)
        key_record = get_api_key_by_hash(key_hash)
        if not key_record:
            return jsonify({'error': {'message': '未授权：API Key 无效', 'type': 'authentication_error'}}), 401
        if not key_record.get('is_active'):
            return jsonify({'error': {'message': '未授权：API Key 已被禁用', 'type': 'authentication_error'}}), 403
        if not key_record.get('user_active'):
            return jsonify({'error': {'message': '未授权：用户已被禁用', 'type': 'authentication_error'}}), 403

        # Update last used time (fire-and-forget, don't block request)
        update_api_key_last_used(key_record['id'])

        from flask import g
        g.current_user_id = key_record['user_id']
        g.current_username = key_record.get('username', '')
        return None

    # All other paths (static files, etc.) pass through
    return None

# ---------------------------------------------------------------------------
# Admin / stats API
# ---------------------------------------------------------------------------
@app.route('/api/logs', methods=['GET'])
def api_logs():
    try:
        limit = int(request.args.get('limit', 50))
        offset = int(request.args.get('offset', 0))
    except ValueError:
        limit, offset = 50, 0

    model = request.args.get('model') or None
    protocol = request.args.get('protocol') or None

    logs = get_logs(limit=limit, offset=offset, model=model, protocol=protocol)
    total = get_log_count(model=model, protocol=protocol)
    return jsonify({'success': True, 'data': logs, 'total': total})


@app.route('/api/logs/today_stats', methods=['GET'])
def api_today_stats():
    stats = get_today_stats()
    if stats is None:
        return jsonify({'success': False, 'message': 'Failed to get stats'})
    return jsonify({'success': True, 'data': stats})


@app.route('/api/stats/daily_tokens', methods=['GET'])
def api_daily_token_stats():
    start_date = request.args.get('start_date')
    end_date = request.args.get('end_date')

    is_single_day = (start_date and end_date and start_date == end_date)

    if is_single_day:
        hourly_stats = get_hourly_token_stats(date=start_date)
        model_stats = get_model_token_stats(start_date=start_date, end_date=end_date)
        return jsonify({
            'success': True,
            'data': {
                'hourly': hourly_stats,
                'daily': [],
                'models': model_stats,
                'is_single_day': True,
            },
        })
    else:
        daily_stats = get_daily_token_stats(start_date=start_date, end_date=end_date)
        model_stats = get_model_token_stats(start_date=start_date, end_date=end_date)
        return jsonify({
            'success': True,
            'data': {
                'hourly': [],
                'daily': daily_stats,
                'models': model_stats,
                'is_single_day': False,
            },
        })


# ---------------------------------------------------------------------------
# Initialise schema (runs on import — works for both `python app.py` and gunicorn)
# ---------------------------------------------------------------------------
init_db()

# Create default admin user if no users exist
if get_user_count() == 0:
    default_hash = hash_password('llm_gateway')
    admin = create_user('admin', default_hash)
    if admin:
        logger.info('Default admin user created (username: admin, password: admin123)')
    else:
        logger.warning('Failed to create default admin user')

# ---------------------------------------------------------------------------
# Entry-point (direct execution only)
# ---------------------------------------------------------------------------
if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='LLM Gateway API Server')
    parser.add_argument('--port', type=int, default=5001,
                        help='Port to listen on (default: 5001)')
    args = parser.parse_args()

    debug = os.environ.get('FLASK_DEBUG', 'false').lower() in ('1', 'true', 'yes')
    app.run(host='0.0.0.0', port=args.port, debug=debug)
