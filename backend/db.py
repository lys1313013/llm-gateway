import psycopg2
from psycopg2.extras import RealDictCursor
import json
import logging
import os

logger = logging.getLogger(__name__)

# Default database connection parameters
DB_HOST = os.environ.get("DB_HOST", "localhost")
DB_PORT = os.environ.get("DB_PORT", "5432")
DB_NAME = os.environ.get("DB_NAME", "mock_openai")
DB_USER = os.environ.get("DB_USER", "postgres")
DB_PASSWORD = os.environ.get("DB_PASSWORD", "password")

def get_db_connection():
    try:
        conn = psycopg2.connect(
            host=DB_HOST,
            port=DB_PORT,
            dbname=DB_NAME,
            user=DB_USER,
            password=DB_PASSWORD
        )
        return conn
    except Exception as e:
        logger.error(f"Database connection failed: {e}")
        return None

def init_db():
    """Initialize database and create api_logs table if not exists"""
    conn = get_db_connection()
    if not conn:
        logger.error("Failed to initialize database: No connection")
        return
        
    try:
        with conn.cursor() as cur:
            cur.execute("""
            CREATE TABLE IF NOT EXISTS api_logs (
                id SERIAL PRIMARY KEY,
                created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                mode VARCHAR(20) NOT NULL,
                model VARCHAR(100),
                is_stream BOOLEAN DEFAULT FALSE,
                status_code INTEGER,
                processing_time_ms INTEGER,
                prompt_tokens INTEGER,
                completion_tokens INTEGER,
                total_tokens INTEGER,
                target_url VARCHAR(255),
                request_data JSONB,
                response_data JSONB,
                error_message TEXT
            );
            """)
        conn.commit()
        logger.info("Database initialized successfully.")
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to initialize database table: {e}")
    finally:
        conn.close()

def insert_log(log_data: dict):
    """Insert a new log entry into api_logs table"""
    conn = get_db_connection()
    if not conn:
        return
        
    try:
        with conn.cursor() as cur:
            query = """
            INSERT INTO api_logs (
                mode, model, is_stream, status_code, processing_time_ms,
                prompt_tokens, completion_tokens, total_tokens, target_url,
                request_data, response_data, error_message
            ) VALUES (
                %(mode)s, %(model)s, %(is_stream)s, %(status_code)s, %(processing_time_ms)s,
                %(prompt_tokens)s, %(completion_tokens)s, %(total_tokens)s, %(target_url)s,
                %(request_data)s, %(response_data)s, %(error_message)s
            )
            """
            
            # Serialize JSON fields if they are dicts
            req_data = log_data.get('request_data')
            if req_data is not None and not isinstance(req_data, str):
                req_data = json.dumps(req_data)
                
            res_data = log_data.get('response_data')
            if res_data is not None and not isinstance(res_data, str):
                res_data = json.dumps(res_data)
                
            data_to_insert = {
                'mode': log_data.get('mode', 'unknown'),
                'model': log_data.get('model'),
                'is_stream': log_data.get('is_stream', False),
                'status_code': log_data.get('status_code'),
                'processing_time_ms': log_data.get('processing_time_ms'),
                'prompt_tokens': log_data.get('prompt_tokens'),
                'completion_tokens': log_data.get('completion_tokens'),
                'total_tokens': log_data.get('total_tokens'),
                'target_url': log_data.get('target_url'),
                'request_data': req_data,
                'response_data': res_data,
                'error_message': log_data.get('error_message')
            }
            
            cur.execute(query, data_to_insert)
        conn.commit()
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to insert log: {e}")
    finally:
        conn.close()

def get_logs(limit=50, offset=0):
    """Retrieve logs from api_logs table"""
    conn = get_db_connection()
    if not conn:
        return []
        
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            cur.execute("""
                SELECT id, created_at, updated_at, mode, model, is_stream, 
                       status_code, processing_time_ms, prompt_tokens, 
                       completion_tokens, total_tokens, target_url, 
                       request_data, response_data, error_message
                FROM api_logs 
                ORDER BY created_at DESC 
                LIMIT %s OFFSET %s
            """, (limit, offset))
            
            rows = cur.fetchall()
            # Convert datetime to string for JSON serialization
            for row in rows:
                if row.get('created_at'):
                    row['created_at'] = row['created_at'].isoformat()
                if row.get('updated_at'):
                    row['updated_at'] = row['updated_at'].isoformat()
            return rows
    except Exception as e:
        logger.error(f"Failed to get logs: {e}")
        return []
    finally:
        conn.close()

def get_daily_token_stats(start_date=None, end_date=None):
    """Retrieve daily token usage statistics"""
    conn = get_db_connection()
    if not conn:
        return []
        
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            query = """
                SELECT 
                    DATE(created_at) as date,
                    COUNT(id) as request_count,
                    COALESCE(SUM(prompt_tokens), 0) as prompt_tokens,
                    COALESCE(SUM(completion_tokens), 0) as completion_tokens,
                    COALESCE(SUM(total_tokens), 0) as total_tokens
                FROM api_logs
                WHERE 1=1
            """
            params = []
            
            if start_date:
                query += " AND DATE(created_at) >= %s"
                params.append(start_date)
                
            if end_date:
                query += " AND DATE(created_at) <= %s"
                params.append(end_date)
                
            query += " GROUP BY DATE(created_at) ORDER BY date ASC"
            
            cur.execute(query, params)
            rows = cur.fetchall()
            
            # Convert date to string for JSON serialization
            for row in rows:
                if row.get('date'):
                    row['date'] = row['date'].isoformat()
            return rows
    except Exception as e:
        logger.error(f"Failed to get daily token stats: {e}")
        return []
    finally:
        conn.close()

def get_model_token_stats(start_date=None, end_date=None):
    """Retrieve token usage statistics grouped by model"""
    conn = get_db_connection()
    if not conn:
        return []
        
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            query = """
                SELECT 
                    COALESCE(model, 'unknown') as model,
                    COUNT(id) as request_count,
                    COALESCE(SUM(prompt_tokens), 0) as prompt_tokens,
                    COALESCE(SUM(completion_tokens), 0) as completion_tokens,
                    COALESCE(SUM(total_tokens), 0) as total_tokens
                FROM api_logs
                WHERE 1=1
            """
            params = []
            
            if start_date:
                query += " AND DATE(created_at) >= %s"
                params.append(start_date)
                
            if end_date:
                query += " AND DATE(created_at) <= %s"
                params.append(end_date)
                
            query += " GROUP BY COALESCE(model, 'unknown') ORDER BY total_tokens DESC"
            
            cur.execute(query, params)
            return cur.fetchall()
    except Exception as e:
        logger.error(f"Failed to get model token stats: {e}")
        return []
    finally:
        conn.close()
