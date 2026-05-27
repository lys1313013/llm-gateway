import argparse
import logging
import os

from flask import Flask, jsonify, request
from flask_cors import CORS

from db import (
    init_db, get_logs, get_today_stats,
    get_daily_token_stats, get_hourly_token_stats, get_model_token_stats,
)
from routes.chat import chat_bp
from routes.admin import admin_bp
from routes.anthropic import anthropic_bp

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
    return jsonify({'success': True, 'data': logs})


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
