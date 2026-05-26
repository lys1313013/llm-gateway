import psycopg
from psycopg.rows import dict_row
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
DB_TIMEZONE = os.environ.get("DB_TIMEZONE", "Asia/Shanghai")

def get_db_connection():
    try:
        conn = psycopg.connect(
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
    """Initialize database and create necessary tables if not exists"""
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
                error_message TEXT,
                protocol VARCHAR(50)
            );

            -- 迁移：删除已废弃的 mode 字段
            ALTER TABLE api_logs DROP COLUMN IF EXISTS mode;

            -- 迁移：添加 protocol 字段
            ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS protocol VARCHAR(50);
            
            CREATE TABLE IF NOT EXISTS provider (
                id SERIAL PRIMARY KEY,
                name VARCHAR(100) UNIQUE NOT NULL,
                base_url VARCHAR(255),
                api_key VARCHAR(255),
                protocol VARCHAR(50) DEFAULT 'openai',
                remark TEXT,
                create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
            );
            
            CREATE TABLE IF NOT EXISTS model_route (
                id SERIAL PRIMARY KEY,
                model_pattern VARCHAR(255) NOT NULL,
                route_type VARCHAR(20) NOT NULL,
                provider_id INTEGER REFERENCES provider(id) ON DELETE SET NULL,
                target_model VARCHAR(100),
                timeout INTEGER DEFAULT 60,
                log_requests BOOLEAN DEFAULT TRUE,
                log_responses BOOLEAN DEFAULT TRUE,
                priority INTEGER DEFAULT 0,
                is_active BOOLEAN DEFAULT TRUE,
                create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
            );

            CREATE TABLE IF NOT EXISTS exposed_model (
                id SERIAL PRIMARY KEY,
                model_id VARCHAR(255) UNIQUE NOT NULL,
                owned_by VARCHAR(100) DEFAULT 'organization',
                is_active BOOLEAN DEFAULT TRUE,
                last_openai_test_time TIMESTAMP WITH TIME ZONE,
                last_anthropic_test_time TIMESTAMP WITH TIME ZONE,
                create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
            );

            -- Migrate: add test time columns if not exists
            ALTER TABLE exposed_model ADD COLUMN IF NOT EXISTS last_openai_test_time TIMESTAMP WITH TIME ZONE;
            ALTER TABLE exposed_model ADD COLUMN IF NOT EXISTS last_anthropic_test_time TIMESTAMP WITH TIME ZONE;
            """)
        conn.commit()
        logger.info("Database initialized successfully.")
        
        # Run migration if needed
        migrate_config_to_db(conn)
        
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to initialize database table: {e}")
    finally:
        conn.close()

def migrate_config_to_db(conn):
    """Migrate config.json to database if tables are empty"""
    try:
        with conn.cursor() as cur:
            cur.execute("SELECT COUNT(*) FROM provider")
            if cur.fetchone()[0] > 0:
                return

            if not os.path.exists('config.json'):
                return

            with open('config.json', 'r') as f:
                config = json.load(f)

            # Migrate proxy config
            proxy_config = config.get('proxy_config', {})
            provider_id = None
            if proxy_config:
                cur.execute("""
                    INSERT INTO provider (name, base_url, api_key, protocol)
                    VALUES (%s, %s, %s, %s) RETURNING id
                """, ('Default Provider', proxy_config.get('target_url', ''), proxy_config.get('api_key', ''), 'openai'))
                provider_id = cur.fetchone()[0]

            # Insert a default proxy route
            cur.execute("""
                INSERT INTO model_route (model_pattern, route_type, provider_id, timeout, log_requests, log_responses, is_active)
                VALUES (%s, %s, %s, %s, %s, %s, %s)
            """, ('*', 'proxy', provider_id, proxy_config.get('timeout', 60),
                  proxy_config.get('log_requests', True), proxy_config.get('log_responses', True), True))

            conn.commit()
            logger.info("Migrated config.json to database successfully.")
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to migrate config.json: {e}")

def insert_log(log_data: dict):
    """Insert a new log entry into api_logs table"""
    conn = get_db_connection()
    if not conn:
        return
        
    try:
        with conn.cursor() as cur:
            query = """
            INSERT INTO api_logs (
                model, is_stream, status_code, processing_time_ms,
                prompt_tokens, completion_tokens, total_tokens, target_url,
                request_data, response_data, error_message, protocol
            ) VALUES (
                %(model)s, %(is_stream)s, %(status_code)s, %(processing_time_ms)s,
                %(prompt_tokens)s, %(completion_tokens)s, %(total_tokens)s, %(target_url)s,
                %(request_data)s, %(response_data)s, %(error_message)s, %(protocol)s
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
                'error_message': log_data.get('error_message'),
                'protocol': log_data.get('protocol')
            }
            
            cur.execute(query, data_to_insert)
        conn.commit()
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to insert log: {e}")
    finally:
        conn.close()

def get_logs(limit=50, offset=0, model=None, protocol=None):
    """Retrieve logs from api_logs table with optional filters"""
    conn = get_db_connection()
    if not conn:
        return []

    try:
        conditions = []
        params = []

        if model:
            conditions.append("model ILIKE %s")
            params.append(f"%{model}%")
        if protocol:
            conditions.append("protocol = %s")
            params.append(protocol)

        where_clause = ""
        if conditions:
            where_clause = "WHERE " + " AND ".join(conditions)

        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute(f"""
                SELECT id, created_at, updated_at, model, is_stream,
                       status_code, processing_time_ms, prompt_tokens,
                       completion_tokens, total_tokens, target_url,
                       request_data, response_data, error_message, protocol
                FROM api_logs
                {where_clause}
                ORDER BY created_at DESC
                LIMIT %s OFFSET %s
            """, (*params, limit, offset))
            
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


def get_today_stats():
    """Retrieve today's token usage statistics"""
    conn = get_db_connection()
    if not conn:
        return None

    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                SELECT
                    COUNT(id) as request_count,
                    COALESCE(SUM(prompt_tokens), 0) as prompt_tokens,
                    COALESCE(SUM(completion_tokens), 0) as completion_tokens,
                    COALESCE(SUM(total_tokens), 0) as total_tokens
                FROM api_logs
                WHERE DATE(created_at) = CURRENT_DATE
            """)
            return cur.fetchone()
    except Exception as e:
        logger.error(f"Failed to get today stats: {e}")
        return None
    finally:
        conn.close()


def get_daily_token_stats(start_date=None, end_date=None):
    """Retrieve daily token usage statistics"""
    conn = get_db_connection()
    if not conn:
        return []
        
    try:
        with conn.cursor(row_factory=dict_row) as cur:
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


def get_hourly_token_stats(date):
    """Retrieve hourly token usage statistics for a specific date.

    Returns 24 rows (one per hour, 0-23) with zero-filled values
    for hours that have no requests.
    """
    conn = get_db_connection()
    if not conn:
        return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute(f"""
                SELECT
                    g.hour AS hour,
                    COALESCE(s.request_count, 0)        AS request_count,
                    COALESCE(s.prompt_tokens, 0)        AS prompt_tokens,
                    COALESCE(s.completion_tokens, 0)    AS completion_tokens,
                    COALESCE(s.total_tokens, 0)         AS total_tokens
                FROM generate_series(0, 23) AS g(hour)
                LEFT JOIN (
                    SELECT
                        EXTRACT(HOUR FROM created_at AT TIME ZONE '{DB_TIMEZONE}')::int AS hour,
                        COUNT(id)                       AS request_count,
                        SUM(prompt_tokens)              AS prompt_tokens,
                        SUM(completion_tokens)          AS completion_tokens,
                        SUM(total_tokens)               AS total_tokens
                    FROM api_logs
                    WHERE DATE(created_at AT TIME ZONE '{DB_TIMEZONE}') = %s
                    GROUP BY EXTRACT(HOUR FROM created_at AT TIME ZONE '{DB_TIMEZONE}')
                ) s ON s.hour = g.hour
                ORDER BY g.hour ASC
            """, (date,))
            return cur.fetchall()
    except Exception as e:
        logger.error(f"Failed to get hourly token stats: {e}")
        return []
    finally:
        conn.close()


def get_model_token_stats(start_date=None, end_date=None):
    """Retrieve token usage statistics grouped by model"""
    conn = get_db_connection()
    if not conn:
        return []
        
    try:
        with conn.cursor(row_factory=dict_row) as cur:
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

# --- CRUD for Providers ---
def get_providers():
    conn = get_db_connection()
    if not conn: return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM provider ORDER BY id ASC")
            rows = cur.fetchall()
            for r in rows:
                if r.get('create_time'): r['create_time'] = r['create_time'].isoformat()
                if r.get('update_time'): r['update_time'] = r['update_time'].isoformat()
            return rows
    except Exception as e:
        logger.error(f"Failed to get providers: {e}")
        return []
    finally:
        conn.close()

def get_provider(provider_id):
    conn = get_db_connection()
    if not conn: return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM provider WHERE id = %s", (provider_id,))
            row = cur.fetchone()
            if row:
                if row.get('create_time'): row['create_time'] = row['create_time'].isoformat()
                if row.get('update_time'): row['update_time'] = row['update_time'].isoformat()
            return row
    except Exception as e:
        logger.error(f"Failed to get provider: {e}")
        return None
    finally:
        conn.close()

def create_provider(data):
    conn = get_db_connection()
    if not conn: return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                INSERT INTO provider (name, base_url, api_key, protocol, remark)
                VALUES (%s, %s, %s, %s, %s) RETURNING *
            """, (data.get('name'), data.get('base_url'), data.get('api_key'), data.get('protocol', 'openai'), data.get('remark')))
            row = cur.fetchone()
            conn.commit()
            if row and row.get('create_time'): row['create_time'] = row['create_time'].isoformat()
            if row and row.get('update_time'): row['update_time'] = row['update_time'].isoformat()
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to create provider: {e}")
        return None
    finally:
        conn.close()

def update_provider(provider_id, data):
    conn = get_db_connection()
    if not conn: return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                UPDATE provider SET 
                name = COALESCE(%s, name), 
                base_url = COALESCE(%s, base_url), 
                api_key = COALESCE(%s, api_key), 
                protocol = COALESCE(%s, protocol),
                remark = COALESCE(%s, remark),
                update_time = CURRENT_TIMESTAMP
                WHERE id = %s RETURNING *
            """, (data.get('name'), data.get('base_url'), data.get('api_key'), data.get('protocol'), data.get('remark'), provider_id))
            row = cur.fetchone()
            conn.commit()
            if row and row.get('create_time'): row['create_time'] = row['create_time'].isoformat()
            if row and row.get('update_time'): row['update_time'] = row['update_time'].isoformat()
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update provider: {e}")
        return None
    finally:
        conn.close()

def delete_provider(provider_id):
    conn = get_db_connection()
    if not conn: return False
    try:
        with conn.cursor() as cur:
            cur.execute("DELETE FROM provider WHERE id = %s", (provider_id,))
            conn.commit()
            return True
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to delete provider: {e}")
        return False
    finally:
        conn.close()

# --- CRUD for Model Routes ---
def get_routes():
    conn = get_db_connection()
    if not conn: return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM model_route ORDER BY priority DESC, id ASC")
            rows = cur.fetchall()
            for r in rows:
                if r.get('create_time'): r['create_time'] = r['create_time'].isoformat()
                if r.get('update_time'): r['update_time'] = r['update_time'].isoformat()
            return rows
    except Exception as e:
        logger.error(f"Failed to get routes: {e}")
        return []
    finally:
        conn.close()

def get_route(route_id):
    conn = get_db_connection()
    if not conn: return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM model_route WHERE id = %s", (route_id,))
            row = cur.fetchone()
            if row:
                if row.get('create_time'): row['create_time'] = row['create_time'].isoformat()
                if row.get('update_time'): row['update_time'] = row['update_time'].isoformat()
            return row
    except Exception as e:
        logger.error(f"Failed to get route: {e}")
        return None
    finally:
        conn.close()

def create_route(data):
    conn = get_db_connection()
    if not conn: return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                INSERT INTO model_route (model_pattern, route_type, provider_id, target_model, timeout, log_requests, log_responses, priority, is_active)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s) RETURNING *
            """, (data.get('model_pattern'), data.get('route_type', 'proxy'), data.get('provider_id'), data.get('target_model'),
                  data.get('timeout', 60), data.get('log_requests', True), data.get('log_responses', True),
                  data.get('priority', 0), data.get('is_active', True)))
            row = cur.fetchone()
            conn.commit()
            if row and row.get('create_time'): row['create_time'] = row['create_time'].isoformat()
            if row and row.get('update_time'): row['update_time'] = row['update_time'].isoformat()
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to create route: {e}")
        return None
    finally:
        conn.close()

def update_route(route_id, data):
    conn = get_db_connection()
    if not conn: return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                UPDATE model_route SET 
                model_pattern = COALESCE(%s, model_pattern),
                route_type = COALESCE(%s, route_type),
                provider_id = COALESCE(%s, provider_id),
                target_model = COALESCE(%s, target_model),
                timeout = COALESCE(%s, timeout),
                log_requests = COALESCE(%s, log_requests),
                log_responses = COALESCE(%s, log_responses),
                priority = COALESCE(%s, priority),
                is_active = COALESCE(%s, is_active),
                update_time = CURRENT_TIMESTAMP
                WHERE id = %s RETURNING *
            """, (data.get('model_pattern'), data.get('route_type'), data.get('provider_id'), data.get('target_model'),
                  data.get('timeout'), data.get('log_requests'), data.get('log_responses'),
                  data.get('priority'), data.get('is_active'), route_id))
            row = cur.fetchone()
            conn.commit()
            if row and row.get('create_time'): row['create_time'] = row['create_time'].isoformat()
            if row and row.get('update_time'): row['update_time'] = row['update_time'].isoformat()
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update route: {e}")
        return None
    finally:
        conn.close()

def delete_route(route_id):
    conn = get_db_connection()
    if not conn: return False
    try:
        with conn.cursor() as cur:
            cur.execute("DELETE FROM model_route WHERE id = %s", (route_id,))
            conn.commit()
            return True
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to delete route: {e}")
        return False
    finally:
        conn.close()

def get_active_routes():
    """Get active routes joined with provider info"""
    conn = get_db_connection()
    if not conn: return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                SELECT r.*, p.base_url, p.api_key, p.protocol
                FROM model_route r
                LEFT JOIN provider p ON r.provider_id = p.id
                WHERE r.is_active = TRUE
                ORDER BY r.priority DESC, r.id ASC
            """)
            return cur.fetchall()
    except Exception as e:
        logger.error(f"Failed to get active routes: {e}")
        return []
    finally:
        conn.close()

# --- CRUD for Exposed Models ---
def get_exposed_models():
    conn = get_db_connection()
    if not conn: return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM exposed_model ORDER BY id ASC")
            rows = cur.fetchall()
            for r in rows:
                for f in ('create_time', 'update_time', 'last_openai_test_time', 'last_anthropic_test_time'):
                    if r.get(f): r[f] = r[f].isoformat()
            return rows
    except Exception as e:
        logger.error(f"Failed to get exposed models: {e}")
        return []
    finally:
        conn.close()

def get_exposed_model(model_id):
    conn = get_db_connection()
    if not conn: return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM exposed_model WHERE id = %s", (model_id,))
            row = cur.fetchone()
            if row:
                for f in ('create_time', 'update_time', 'last_openai_test_time', 'last_anthropic_test_time'):
                    if row.get(f): row[f] = row[f].isoformat()
            return row
    except Exception as e:
        logger.error(f"Failed to get exposed model: {e}")
        return None
    finally:
        conn.close()

def create_exposed_model(data):
    conn = get_db_connection()
    if not conn: return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                INSERT INTO exposed_model (model_id, owned_by, is_active)
                VALUES (%s, %s, %s) RETURNING *
            """, (data.get('model_id'), data.get('owned_by', 'organization'), data.get('is_active', True)))
            row = cur.fetchone()
            conn.commit()
            if row:
                for f in ('create_time', 'update_time', 'last_openai_test_time', 'last_anthropic_test_time'):
                    if row.get(f): row[f] = row[f].isoformat()
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to create exposed model: {e}")
        return None
    finally:
        conn.close()

def update_exposed_model(model_id, data):
    conn = get_db_connection()
    if not conn: return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                UPDATE exposed_model SET
                model_id = COALESCE(%s, model_id),
                owned_by = COALESCE(%s, owned_by),
                is_active = COALESCE(%s, is_active),
                update_time = CURRENT_TIMESTAMP
                WHERE id = %s RETURNING *
            """, (data.get('model_id'), data.get('owned_by'), data.get('is_active'), model_id))
            row = cur.fetchone()
            conn.commit()
            if row:
                for f in ('create_time', 'update_time', 'last_openai_test_time', 'last_anthropic_test_time'):
                    if row.get(f): row[f] = row[f].isoformat()
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update exposed model: {e}")
        return None
    finally:
        conn.close()

def update_exposed_model_test_time(model_id, protocol):
    """Update the last test success time for a specific protocol"""
    conn = get_db_connection()
    if not conn: return None
    col = 'last_openai_test_time' if protocol == 'openai' else 'last_anthropic_test_time'
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute(f"""
                UPDATE exposed_model SET {col} = CURRENT_TIMESTAMP, update_time = CURRENT_TIMESTAMP
                WHERE id = %s RETURNING *
            """, (model_id,))
            row = cur.fetchone()
            conn.commit()
            if row:
                for f in ('create_time', 'update_time', 'last_openai_test_time', 'last_anthropic_test_time'):
                    if row.get(f): row[f] = row[f].isoformat()
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update exposed model test time: {e}")
        return None
    finally:
        conn.close()

def delete_exposed_model(model_id):
    conn = get_db_connection()
    if not conn: return False
    try:
        with conn.cursor() as cur:
            cur.execute("DELETE FROM exposed_model WHERE id = %s", (model_id,))
            conn.commit()
            return True
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to delete exposed model: {e}")
        return False
    finally:
        conn.close()

def get_active_exposed_models():
    conn = get_db_connection()
    if not conn: return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM exposed_model WHERE is_active = TRUE ORDER BY id ASC")
            rows = cur.fetchall()
            for r in rows:
                for f in ('create_time', 'update_time', 'last_openai_test_time', 'last_anthropic_test_time'):
                    if r.get(f): r[f] = r[f].isoformat()
            return rows
    except Exception as e:
        logger.error(f"Failed to get active exposed models: {e}")
        return []
    finally:
        conn.close()
