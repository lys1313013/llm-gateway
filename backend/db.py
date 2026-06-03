import atexit
import json
import logging
import os
import re

import psycopg
from psycopg.rows import dict_row
from psycopg_pool import ConnectionPool

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Database connection parameters
# ---------------------------------------------------------------------------
DB_HOST = os.environ.get("DB_HOST", "localhost")
DB_PORT = os.environ.get("DB_PORT", "5432")
DB_NAME = os.environ.get("DB_NAME", "llm_gateway")
DB_USER = os.environ.get("DB_USER", "postgres")
DB_PASSWORD = os.environ.get("DB_PASSWORD", "password")

_RAW_TZ = os.environ.get("DB_TIMEZONE", "Asia/Shanghai")
DB_TIMEZONE = _RAW_TZ if re.fullmatch(r"[A-Za-z0-9/_+\-]+", _RAW_TZ) else "UTC"

# ---------------------------------------------------------------------------
# Connection pool
# ---------------------------------------------------------------------------
_pool: ConnectionPool | None = None


def _init_pool():
    """Initialise the global connection pool (idempotent)."""
    global _pool
    if _pool is not None:
        return

    conninfo = (
        f"host={DB_HOST} port={DB_PORT} dbname={DB_NAME} "
        f"user={DB_USER} password={DB_PASSWORD}"
    )
    try:
        _pool = ConnectionPool(
            conninfo=conninfo,
            min_size=2,
            max_size=10,
            timeout=30,
            kwargs={"row_factory": dict_row, "autocommit": False},
        )
        logger.info("Database connection pool initialised (min=2, max=10)")
    except Exception as e:
        logger.error(f"Failed to initialise connection pool: {e}")


def _close_pool():
    global _pool
    if _pool:
        _pool.close()
        _pool = None


atexit.register(_close_pool)

# Eagerly create the pool so the first request is fast.
_init_pool()


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
def get_db_connection():
    """Get a connection from the pool (for legacy / one-off callers)."""
    _init_pool()
    if _pool is None:
        return None
    try:
        return _pool.getconn()
    except Exception as e:
        logger.error(f"Failed to get connection from pool: {e}")
        return None


def _release(conn):
    """Return a connection to the pool, clearing any pending transaction state."""
    if _pool and conn:
        try:
            if conn.info.transaction_status != psycopg.pq.TransactionStatus.IDLE:
                conn.rollback()
            _pool.putconn(conn)
        except Exception:
            try:
                conn.close()
            except Exception:
                pass


# ---------------------------------------------------------------------------
# Schema initialisation
# ---------------------------------------------------------------------------
def init_db():
    """Create tables if they do not exist."""
    conn = get_db_connection()
    if not conn:
        logger.error("Failed to initialise database: no connection")
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
                protocol VARCHAR(50),
                usage_data JSONB,
                cache_creation_input_tokens INTEGER,
                cache_read_input_tokens INTEGER
            );

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

            CREATE TABLE IF NOT EXISTS users (
                id SERIAL PRIMARY KEY,
                username VARCHAR(100) UNIQUE NOT NULL,
                password_hash VARCHAR(255) NOT NULL,
                is_active BOOLEAN DEFAULT TRUE,
                created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
            );

            CREATE TABLE IF NOT EXISTS api_keys (
                id SERIAL PRIMARY KEY,
                user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                key_hash VARCHAR(255) NOT NULL,
                key_prefix VARCHAR(16) NOT NULL,
                key_value VARCHAR(255),
                name VARCHAR(100) NOT NULL DEFAULT 'default',
                is_active BOOLEAN DEFAULT TRUE,
                created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                last_used_at TIMESTAMP WITH TIME ZONE
            );
            CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);
            """)
            # Migrate: add columns that may be missing from older schemas
            for alter_sql in [
                "ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS usage_data JSONB",
                "ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cache_creation_input_tokens INTEGER",
                "ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cache_read_input_tokens INTEGER",
                "ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_value VARCHAR(255)",
            ]:
                cur.execute(alter_sql)
        conn.commit()
        logger.info("Database initialised successfully.")
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to initialise database: {e}")
    finally:
        _release(conn)


# ---------------------------------------------------------------------------
# Log helpers
# ---------------------------------------------------------------------------
def _json_dumps_if_dict(val):
    if val is not None and not isinstance(val, str):
        return json.dumps(val)
    return val


def insert_log(log_data: dict):
    """Insert a new log entry into api_logs."""
    conn = get_db_connection()
    if not conn:
        return

    try:
        with conn.cursor() as cur:
            cur.execute("""
            INSERT INTO api_logs (
                model, is_stream, status_code, processing_time_ms,
                prompt_tokens, completion_tokens, total_tokens, target_url,
                request_data, response_data, error_message, protocol, usage_data,
                cache_creation_input_tokens, cache_read_input_tokens
            ) VALUES (
                %(model)s, %(is_stream)s, %(status_code)s, %(processing_time_ms)s,
                %(prompt_tokens)s, %(completion_tokens)s, %(total_tokens)s, %(target_url)s,
                %(request_data)s, %(response_data)s, %(error_message)s, %(protocol)s,
                %(usage_data)s,
                %(cache_creation_input_tokens)s, %(cache_read_input_tokens)s
            )
            """, {
                'model': log_data.get('model'),
                'is_stream': log_data.get('is_stream', False),
                'status_code': log_data.get('status_code'),
                'processing_time_ms': log_data.get('processing_time_ms'),
                'prompt_tokens': log_data.get('prompt_tokens'),
                'completion_tokens': log_data.get('completion_tokens'),
                'total_tokens': log_data.get('total_tokens'),
                'target_url': log_data.get('target_url'),
                'request_data': _json_dumps_if_dict(log_data.get('request_data')),
                'response_data': _json_dumps_if_dict(log_data.get('response_data')),
                'error_message': log_data.get('error_message'),
                'protocol': log_data.get('protocol'),
                'usage_data': _json_dumps_if_dict(log_data.get('usage_data')),
                'cache_creation_input_tokens': log_data.get('cache_creation_input_tokens'),
                'cache_read_input_tokens': log_data.get('cache_read_input_tokens'),
            })
        conn.commit()
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to insert log: {e}")
    finally:
        _release(conn)


# ---------------------------------------------------------------------------
# Query: logs
# ---------------------------------------------------------------------------
def get_logs(limit=50, offset=0, model=None, protocol=None):
    """Retrieve logs with optional filters."""
    conn = get_db_connection()
    if not conn:
        return []

    try:
        conditions, params = [], []
        if model:
            conditions.append("model ILIKE %s")
            params.append(f"%{model}%")
        if protocol:
            conditions.append("protocol = %s")
            params.append(protocol)

        where = ("WHERE " + " AND ".join(conditions)) if conditions else ""

        with conn.cursor(row_factory=dict_row) as cur:
            # request_data / response_data 是体积巨大的 JSONB 字段，列表接口不返回，按需通过详情接口取
            cur.execute(f"""
                SELECT id, created_at, updated_at, model, is_stream,
                       status_code, processing_time_ms, prompt_tokens,
                       completion_tokens, total_tokens, target_url,
                       error_message, protocol, usage_data,
                       cache_creation_input_tokens, cache_read_input_tokens
                FROM api_logs
                {where}
                ORDER BY created_at DESC
                LIMIT %s OFFSET %s
            """, (*params, limit, offset))

            rows = cur.fetchall()
            for row in rows:
                for f in ('created_at', 'updated_at'):
                    if row.get(f):
                        row[f] = row[f].isoformat()
            return rows
    except Exception as e:
        logger.error(f"Failed to get logs: {e}")
        return []
    finally:
        _release(conn)


def get_log_by_id(log_id):
    """Retrieve a single log entry with full request/response data."""
    conn = get_db_connection()
    if not conn:
        return None

    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                SELECT id, created_at, updated_at, model, is_stream,
                       status_code, processing_time_ms, prompt_tokens,
                       completion_tokens, total_tokens, target_url,
                       request_data, response_data, error_message, protocol,
                       usage_data, cache_creation_input_tokens, cache_read_input_tokens
                FROM api_logs
                WHERE id = %s
            """, (log_id,))
            row = cur.fetchone()
            if row:
                for f in ('created_at', 'updated_at'):
                    if row.get(f):
                        row[f] = row[f].isoformat()
            return row
    except Exception as e:
        logger.error(f"Failed to get log by id: {e}")
        return None
    finally:
        _release(conn)


def get_log_count(model=None, protocol=None):
    """Count logs with optional filters."""
    conn = get_db_connection()
    if not conn:
        return 0

    try:
        conditions, params = [], []
        if model:
            conditions.append("model ILIKE %s")
            params.append(f"%{model}%")
        if protocol:
            conditions.append("protocol = %s")
            params.append(protocol)

        where = ("WHERE " + " AND ".join(conditions)) if conditions else ""

        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute(f"SELECT COUNT(*) AS cnt FROM api_logs {where}", params)
            return cur.fetchone()['cnt']
    except Exception as e:
        logger.error(f"Failed to count logs: {e}")
        return 0
    finally:
        _release(conn)


# ---------------------------------------------------------------------------
# Query: statistics
# ---------------------------------------------------------------------------
def get_today_stats():
    """Today's aggregate token usage."""
    conn = get_db_connection()
    if not conn:
        return None

    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                SELECT
                    COUNT(id)                          AS request_count,
                    COALESCE(SUM(prompt_tokens), 0)    AS prompt_tokens,
                    COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
                    COALESCE(SUM(total_tokens), 0)     AS total_tokens
                FROM api_logs
                WHERE DATE(created_at) = CURRENT_DATE
            """)
            return cur.fetchone()
    except Exception as e:
        logger.error(f"Failed to get today stats: {e}")
        return None
    finally:
        _release(conn)


def get_daily_token_stats(start_date=None, end_date=None):
    """Daily token usage over a date range."""
    conn = get_db_connection()
    if not conn:
        return []

    try:
        with conn.cursor(row_factory=dict_row) as cur:
            query = """
                SELECT
                    DATE(created_at) AS date,
                    COUNT(id) AS request_count,
                    COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
                    COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
                    COALESCE(SUM(total_tokens), 0) AS total_tokens
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
            for row in rows:
                if row.get('date'):
                    row['date'] = row['date'].isoformat()
            return rows
    except Exception as e:
        logger.error(f"Failed to get daily token stats: {e}")
        return []
    finally:
        _release(conn)


def get_hourly_token_stats(date):
    """Hourly token usage for a single date (24 rows, 0-23)."""
    conn = get_db_connection()
    if not conn:
        return []

    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute(f"""
                SELECT
                    g.hour AS hour,
                    COALESCE(s.request_count, 0)     AS request_count,
                    COALESCE(s.prompt_tokens, 0)     AS prompt_tokens,
                    COALESCE(s.completion_tokens, 0) AS completion_tokens,
                    COALESCE(s.total_tokens, 0)      AS total_tokens
                FROM generate_series(0, 23) AS g(hour)
                LEFT JOIN (
                    SELECT
                        EXTRACT(HOUR FROM created_at AT TIME ZONE '{DB_TIMEZONE}')::int AS hour,
                        COUNT(id)              AS request_count,
                        SUM(prompt_tokens)     AS prompt_tokens,
                        SUM(completion_tokens) AS completion_tokens,
                        SUM(total_tokens)      AS total_tokens
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
        _release(conn)


def get_model_token_stats(start_date=None, end_date=None):
    """Token usage grouped by model."""
    conn = get_db_connection()
    if not conn:
        return []

    try:
        with conn.cursor(row_factory=dict_row) as cur:
            query = """
                SELECT
                    COALESCE(model, 'unknown') AS model,
                    COUNT(id) AS request_count,
                    COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
                    COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
                    COALESCE(SUM(total_tokens), 0) AS total_tokens
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
        _release(conn)


# ---------------------------------------------------------------------------
# CRUD: provider
# ---------------------------------------------------------------------------
_TIME_FIELDS = ('create_time', 'update_time')
_MODEL_TIME_FIELDS = ('create_time', 'update_time', 'last_openai_test_time', 'last_anthropic_test_time')


def _isoformat_fields(row, fields):
    if not row:
        return
    for f in fields:
        if row.get(f):
            row[f] = row[f].isoformat()


def get_providers():
    conn = get_db_connection()
    if not conn:
        return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM provider ORDER BY id ASC")
            rows = cur.fetchall()
            for r in rows:
                _isoformat_fields(r, _TIME_FIELDS)
            return rows
    except Exception as e:
        logger.error(f"Failed to get providers: {e}")
        return []
    finally:
        _release(conn)


def get_provider(provider_id):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM provider WHERE id = %s", (provider_id,))
            row = cur.fetchone()
            _isoformat_fields(row, _TIME_FIELDS)
            return row
    except Exception as e:
        logger.error(f"Failed to get provider: {e}")
        return None
    finally:
        _release(conn)


def create_provider(data):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                INSERT INTO provider (name, base_url, api_key, protocol, remark)
                VALUES (%s, %s, %s, %s, %s) RETURNING *
            """, (
                data.get('name'), data.get('base_url'), data.get('api_key'),
                data.get('protocol', 'openai'), data.get('remark'),
            ))
            row = cur.fetchone()
            conn.commit()
            _isoformat_fields(row, _TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to create provider: {e}")
        return None
    finally:
        _release(conn)


def update_provider(provider_id, data):
    conn = get_db_connection()
    if not conn:
        return None
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
            """, (
                data.get('name'), data.get('base_url'), data.get('api_key'),
                data.get('protocol'), data.get('remark'), provider_id,
            ))
            row = cur.fetchone()
            conn.commit()
            _isoformat_fields(row, _TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update provider: {e}")
        return None
    finally:
        _release(conn)


def delete_provider(provider_id):
    conn = get_db_connection()
    if not conn:
        return False
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
        _release(conn)


# ---------------------------------------------------------------------------
# CRUD: model_route
# ---------------------------------------------------------------------------
def get_routes():
    conn = get_db_connection()
    if not conn:
        return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM model_route ORDER BY priority DESC, id ASC")
            rows = cur.fetchall()
            for r in rows:
                _isoformat_fields(r, _TIME_FIELDS)
            return rows
    except Exception as e:
        logger.error(f"Failed to get routes: {e}")
        return []
    finally:
        _release(conn)


def get_route(route_id):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM model_route WHERE id = %s", (route_id,))
            row = cur.fetchone()
            _isoformat_fields(row, _TIME_FIELDS)
            return row
    except Exception as e:
        logger.error(f"Failed to get route: {e}")
        return None
    finally:
        _release(conn)


def create_route(data):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                INSERT INTO model_route
                    (model_pattern, route_type, provider_id, target_model,
                     timeout, log_requests, log_responses, priority, is_active)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s) RETURNING *
            """, (
                data.get('model_pattern'), data.get('route_type', 'proxy'),
                data.get('provider_id'), data.get('target_model'),
                data.get('timeout', 60), data.get('log_requests', True),
                data.get('log_responses', True), data.get('priority', 0),
                data.get('is_active', True),
            ))
            row = cur.fetchone()
            conn.commit()
            _isoformat_fields(row, _TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to create route: {e}")
        return None
    finally:
        _release(conn)


def update_route(route_id, data):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                UPDATE model_route SET
                    model_pattern = COALESCE(%s, model_pattern),
                    route_type    = COALESCE(%s, route_type),
                    provider_id   = COALESCE(%s, provider_id),
                    target_model  = COALESCE(%s, target_model),
                    timeout       = COALESCE(%s, timeout),
                    log_requests  = COALESCE(%s, log_requests),
                    log_responses = COALESCE(%s, log_responses),
                    priority      = COALESCE(%s, priority),
                    is_active     = COALESCE(%s, is_active),
                    update_time   = CURRENT_TIMESTAMP
                WHERE id = %s RETURNING *
            """, (
                data.get('model_pattern'), data.get('route_type'),
                data.get('provider_id'), data.get('target_model'),
                data.get('timeout'), data.get('log_requests'),
                data.get('log_responses'), data.get('priority'),
                data.get('is_active'), route_id,
            ))
            row = cur.fetchone()
            conn.commit()
            _isoformat_fields(row, _TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update route: {e}")
        return None
    finally:
        _release(conn)


def delete_route(route_id):
    conn = get_db_connection()
    if not conn:
        return False
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
        _release(conn)


def get_active_routes():
    """Active routes joined with provider info."""
    conn = get_db_connection()
    if not conn:
        return []
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
        _release(conn)


# ---------------------------------------------------------------------------
# CRUD: exposed_model
# ---------------------------------------------------------------------------
def get_exposed_models():
    conn = get_db_connection()
    if not conn:
        return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM exposed_model ORDER BY id ASC")
            rows = cur.fetchall()
            for r in rows:
                _isoformat_fields(r, _MODEL_TIME_FIELDS)
            return rows
    except Exception as e:
        logger.error(f"Failed to get exposed models: {e}")
        return []
    finally:
        _release(conn)


def get_exposed_model(model_id):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM exposed_model WHERE id = %s", (model_id,))
            row = cur.fetchone()
            _isoformat_fields(row, _MODEL_TIME_FIELDS)
            return row
    except Exception as e:
        logger.error(f"Failed to get exposed model: {e}")
        return None
    finally:
        _release(conn)


def create_exposed_model(data):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                INSERT INTO exposed_model (model_id, owned_by, is_active)
                VALUES (%s, %s, %s) RETURNING *
            """, (
                data.get('model_id'),
                data.get('owned_by', 'organization'),
                data.get('is_active', True),
            ))
            row = cur.fetchone()
            conn.commit()
            _isoformat_fields(row, _MODEL_TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to create exposed model: {e}")
        return None
    finally:
        _release(conn)


def update_exposed_model(model_id, data):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                UPDATE exposed_model SET
                    model_id  = COALESCE(%s, model_id),
                    owned_by  = COALESCE(%s, owned_by),
                    is_active = COALESCE(%s, is_active),
                    update_time = CURRENT_TIMESTAMP
                WHERE id = %s RETURNING *
            """, (
                data.get('model_id'), data.get('owned_by'),
                data.get('is_active'), model_id,
            ))
            row = cur.fetchone()
            conn.commit()
            _isoformat_fields(row, _MODEL_TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update exposed model: {e}")
        return None
    finally:
        _release(conn)


def update_exposed_model_test_time(model_id, protocol):
    """Update the last successful test time for a protocol."""
    conn = get_db_connection()
    if not conn:
        return None

    # Whitelist column name to prevent SQL injection
    col_map = {
        'openai': 'last_openai_test_time',
        'anthropic': 'last_anthropic_test_time',
    }
    col = col_map.get(protocol)
    if not col:
        logger.error(f"Invalid protocol for test_time update: {protocol}")
        return None

    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute(f"""
                UPDATE exposed_model
                SET {col} = CURRENT_TIMESTAMP, update_time = CURRENT_TIMESTAMP
                WHERE id = %s RETURNING *
            """, (model_id,))
            row = cur.fetchone()
            conn.commit()
            _isoformat_fields(row, _MODEL_TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update exposed model test time: {e}")
        return None
    finally:
        _release(conn)


def delete_exposed_model(model_id):
    conn = get_db_connection()
    if not conn:
        return False
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
        _release(conn)


def get_active_exposed_models():
    conn = get_db_connection()
    if not conn:
        return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("SELECT * FROM exposed_model WHERE is_active = TRUE ORDER BY id ASC")
            rows = cur.fetchall()
            for r in rows:
                _isoformat_fields(r, _MODEL_TIME_FIELDS)
            return rows
    except Exception as e:
        logger.error(f"Failed to get active exposed models: {e}")
        return []
    finally:
        _release(conn)


# ---------------------------------------------------------------------------
# CRUD: users
# ---------------------------------------------------------------------------
_USER_TIME_FIELDS = ('created_at', 'updated_at')


def create_user(username, password_hash):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                INSERT INTO users (username, password_hash)
                VALUES (%s, %s) RETURNING id, username, is_active, created_at, updated_at
            """, (username, password_hash))
            row = cur.fetchone()
            conn.commit()
            _isoformat_fields(row, _USER_TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to create user: {e}")
        return None
    finally:
        _release(conn)


def get_user_by_username(username):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                SELECT id, username, password_hash, is_active, created_at, updated_at
                FROM users WHERE username = %s
            """, (username,))
            row = cur.fetchone()
            _isoformat_fields(row, _USER_TIME_FIELDS)
            return row
    except Exception as e:
        logger.error(f"Failed to get user by username: {e}")
        return None
    finally:
        _release(conn)


def get_user_by_id(user_id):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                SELECT id, username, is_active, created_at, updated_at
                FROM users WHERE id = %s
            """, (user_id,))
            row = cur.fetchone()
            _isoformat_fields(row, _USER_TIME_FIELDS)
            return row
    except Exception as e:
        logger.error(f"Failed to get user by id: {e}")
        return None
    finally:
        _release(conn)


def get_users():
    conn = get_db_connection()
    if not conn:
        return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                SELECT id, username, is_active, created_at, updated_at
                FROM users ORDER BY id ASC
            """)
            rows = cur.fetchall()
            for r in rows:
                _isoformat_fields(r, _USER_TIME_FIELDS)
            return rows
    except Exception as e:
        logger.error(f"Failed to get users: {e}")
        return []
    finally:
        _release(conn)


def update_user_password(user_id, password_hash):
    conn = get_db_connection()
    if not conn:
        return False
    try:
        with conn.cursor() as cur:
            cur.execute("""
                UPDATE users SET password_hash = %s, updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
            """, (password_hash, user_id))
            conn.commit()
            return cur.rowcount > 0
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update user password: {e}")
        return False
    finally:
        _release(conn)


def delete_user(user_id):
    conn = get_db_connection()
    if not conn:
        return False
    try:
        with conn.cursor() as cur:
            cur.execute("DELETE FROM users WHERE id = %s", (user_id,))
            conn.commit()
            return cur.rowcount > 0
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to delete user: {e}")
        return False
    finally:
        _release(conn)


def get_user_count():
    conn = get_db_connection()
    if not conn:
        return 0
    try:
        with conn.cursor() as cur:
            cur.execute("SELECT COUNT(*) FROM users")
            return cur.fetchone()[0]
    except Exception as e:
        logger.error(f"Failed to get user count: {e}")
        return 0
    finally:
        _release(conn)


# ---------------------------------------------------------------------------
# CRUD: api_keys
# ---------------------------------------------------------------------------
_API_KEY_TIME_FIELDS = ('created_at', 'last_used_at')


def create_api_key(user_id, key_hash, key_prefix, name='default', key_value=None):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                INSERT INTO api_keys (user_id, key_hash, key_prefix, key_value, name)
                VALUES (%s, %s, %s, %s, %s)
                RETURNING id, user_id, key_prefix, key_value, name, is_active, created_at, last_used_at
            """, (user_id, key_hash, key_prefix, key_value, name))
            row = cur.fetchone()
            conn.commit()
            _isoformat_fields(row, _API_KEY_TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to create api key: {e}")
        return None
    finally:
        _release(conn)


def get_api_keys_by_user(user_id):
    conn = get_db_connection()
    if not conn:
        return []
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                SELECT id, user_id, key_prefix, key_value, name, is_active, created_at, last_used_at
                FROM api_keys WHERE user_id = %s ORDER BY id DESC
            """, (user_id,))
            rows = cur.fetchall()
            for r in rows:
                _isoformat_fields(r, _API_KEY_TIME_FIELDS)
            return rows
    except Exception as e:
        logger.error(f"Failed to get api keys: {e}")
        return []
    finally:
        _release(conn)


def get_api_key_by_hash(key_hash):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                SELECT k.id, k.user_id, k.key_prefix, k.name, k.is_active,
                       k.created_at, k.last_used_at, u.username, u.is_active AS user_active
                FROM api_keys k
                JOIN users u ON k.user_id = u.id
                WHERE k.key_hash = %s
            """, (key_hash,))
            row = cur.fetchone()
            _isoformat_fields(row, _API_KEY_TIME_FIELDS)
            return row
    except Exception as e:
        logger.error(f"Failed to get api key by hash: {e}")
        return None
    finally:
        _release(conn)


def update_api_key_last_used(key_id):
    conn = get_db_connection()
    if not conn:
        return
    try:
        with conn.cursor() as cur:
            cur.execute("""
                UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = %s
            """, (key_id,))
            conn.commit()
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update api key last_used: {e}")
    finally:
        _release(conn)


def delete_api_key(key_id):
    conn = get_db_connection()
    if not conn:
        return False
    try:
        with conn.cursor() as cur:
            cur.execute("DELETE FROM api_keys WHERE id = %s", (key_id,))
            conn.commit()
            return cur.rowcount > 0
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to delete api key: {e}")
        return False
    finally:
        _release(conn)


def update_api_key_name(key_id, name):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                UPDATE api_keys SET name = %s WHERE id = %s
                RETURNING id, user_id, key_prefix, key_value, name, is_active, created_at, last_used_at
            """, (name, key_id))
            row = cur.fetchone()
            conn.commit()
            if row:
                _isoformat_fields(row, _API_KEY_TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to update api key name: {e}")
        return None
    finally:
        _release(conn)


def toggle_api_key(key_id, is_active):
    conn = get_db_connection()
    if not conn:
        return None
    try:
        with conn.cursor(row_factory=dict_row) as cur:
            cur.execute("""
                UPDATE api_keys SET is_active = %s WHERE id = %s
                RETURNING id, user_id, key_prefix, key_value, name, is_active, created_at, last_used_at
            """, (is_active, key_id))
            row = cur.fetchone()
            conn.commit()
            _isoformat_fields(row, _API_KEY_TIME_FIELDS)
            return row
    except Exception as e:
        conn.rollback()
        logger.error(f"Failed to toggle api key: {e}")
        return None
    finally:
        _release(conn)
