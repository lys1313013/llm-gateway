import argparse
import logging
from flask import Flask, jsonify, request
from flask_cors import CORS
from db import init_db, get_logs, get_daily_token_stats, get_model_token_stats
from routes.chat import chat_bp

# 配置日志
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(name)s - %(levelname)s - %(message)s')
logger = logging.getLogger(__name__)

app = Flask(__name__)
CORS(app)  # 添加CORS支持

# 注册 Blueprint
app.register_blueprint(chat_bp)

@app.route('/api/logs', methods=['GET'])
def api_logs():
    try:
        limit = int(request.args.get('limit', 50))
        offset = int(request.args.get('offset', 0))
    except ValueError:
        limit = 50
        offset = 0
        
    logs = get_logs(limit=limit, offset=offset)
    return jsonify({'success': True, 'data': logs})

@app.route('/api/stats/daily_tokens', methods=['GET'])
def api_daily_token_stats():
    start_date = request.args.get('start_date')
    end_date = request.args.get('end_date')
    
    daily_stats = get_daily_token_stats(start_date=start_date, end_date=end_date)
    model_stats = get_model_token_stats(start_date=start_date, end_date=end_date)
    
    return jsonify({
        'success': True, 
        'data': {
            'daily': daily_stats,
            'models': model_stats
        }
    })

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='Mock OpenAI API Server')
    parser.add_argument('--port', type=int, default=5001, help='Port to run the server on (default: 5001)')
    args = parser.parse_args()
    
    # 初始化数据库
    init_db()
    
    app.run(host='0.0.0.0', port=args.port, debug=True)
