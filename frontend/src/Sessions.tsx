import { useCallback, useEffect, useState } from 'react'
import {
  Button, Card, Empty, Input, Popconfirm, Space, Table, Tag, Tooltip, Typography, message,
} from 'antd'
import type { TableColumnsType } from 'antd'
import { CopyOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import { apiFetch, getCurrentUser } from './api'

const { Title, Text } = Typography

type SessionSummary = {
  session_id: string
  request_count: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  first_at: string
  last_at: string
  models?: string[]
  status_summary?: Record<string, number>
  protocol_summary?: Record<string, number>
}

const PAGE_SIZE = 20

const truncate = (id: string, head = 8, tail = 4) => {
  if (id.length <= head + tail + 3) return id
  return `${id.slice(0, head)}…${id.slice(-tail)}`
}

const copyText = (text: string) => {
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(
      () => message.success('已复制到剪贴板'),
      () => message.error('复制失败，请手动复制'),
    )
  } else {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    try {
      document.execCommand('copy')
      message.success('已复制到剪贴板')
    } catch {
      message.error('复制失败，请手动复制')
    } finally {
      document.body.removeChild(ta)
    }
  }
}

const Sessions = () => {
  const isAdmin = (getCurrentUser()?.role ?? 99) <= 2
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [total, setTotal] = useState(0)
  const [currentPage, setCurrentPage] = useState(1)
  const [query, setQuery] = useState('')
  const [appliedQuery, setAppliedQuery] = useState('')

  const fetchSessions = useCallback(async (page = 1, q = '') => {
    setLoading(true)
    try {
      const params = new URLSearchParams({
        limit: String(PAGE_SIZE),
        offset: String((page - 1) * PAGE_SIZE),
      })
      if (q) params.append('q', q)
      const res = await apiFetch(`/api/sessions?${params}`)
      const json = await res.json()
      if (json.success) {
        setSessions(json.data as SessionSummary[])
        setTotal(json.total ?? 0)
      } else {
        message.error(json.message || '获取会话列表失败')
      }
    } catch (e) {
      console.error('获取会话列表失败:', e)
      message.error('获取会话列表失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchSessions(1, appliedQuery)
  }, [fetchSessions, appliedQuery])

  const handleRefresh = () => {
    void fetchSessions(currentPage, appliedQuery)
  }

  const handleSearch = () => {
    setCurrentPage(1)
    setAppliedQuery(query.trim())
  }

  const handleReset = () => {
    setCurrentPage(1)
    setQuery('')
    setAppliedQuery('')
  }

  const handlePageChange = (page: number) => {
    setCurrentPage(page)
    void fetchSessions(page, appliedQuery)
  }

  const handleDelete = async (record: SessionSummary) => {
    try {
      const res = await apiFetch(
        `/api/sessions/${encodeURIComponent(record.session_id)}`,
        { method: 'DELETE' },
      )
      const json = await res.json()
      if (!res.ok || !json.success) {
        message.error(json.message || '删除失败')
        return
      }
      const n = (json.data && json.data.deleted_logs) || 0
      message.success(`已删除会话及其 ${n} 条日志`)
      // After deleting a row the page may now be empty — fall back to the
      // previous page when this was the last row on the current page.
      const wasLast = sessions.length === 1 && currentPage > 1
      const nextPage = wasLast ? currentPage - 1 : currentPage
      if (wasLast) setCurrentPage(nextPage)
      void fetchSessions(nextPage, appliedQuery)
    } catch (e) {
      console.error('删除会话失败:', e)
      message.error('删除会话失败')
    }
  }

  const columns: TableColumnsType<SessionSummary> = [
    {
      title: '会话 ID',
      dataIndex: 'session_id',
      width: 220,
      render: (id: string) => (
        <Space size={4}>
          <Tooltip title={id} mouseEnterDelay={0.4}>
            <Text code style={{ fontSize: 12 }}>{truncate(id)}</Text>
          </Tooltip>
          <Button
            type="text"
            size="small"
            icon={<CopyOutlined />}
            onClick={() => copyText(id)}
          />
        </Space>
      ),
    },
    {
      title: '请求数',
      dataIndex: 'request_count',
      width: 90,
      sorter: (a, b) => a.request_count - b.request_count,
    },
    {
      title: '总 Token',
      dataIndex: 'total_tokens',
      width: 110,
      render: (v: number) => v.toLocaleString(),
    },
    {
      title: '输入',
      dataIndex: 'prompt_tokens',
      width: 90,
      render: (v: number) => v.toLocaleString(),
    },
    {
      title: '输出',
      dataIndex: 'completion_tokens',
      width: 90,
      render: (v: number) => v.toLocaleString(),
    },
    {
      title: '起始时间',
      dataIndex: 'first_at',
      width: 170,
      render: (t: string) => dayjs(t).format('YYYY-MM-DD HH:mm:ss'),
    },
    {
      title: '最后时间',
      dataIndex: 'last_at',
      width: 170,
      render: (t: string) => dayjs(t).format('YYYY-MM-DD HH:mm:ss'),
    },
    {
      title: '涉及模型',
      dataIndex: 'models',
      width: 200,
      render: (models?: string[]) => {
        if (!models || models.length === 0) return '-'
        const shown = models.slice(0, 3)
        const rest = models.length - shown.length
        return (
          <Space size={4} wrap>
            {shown.map(m => <Tag key={m} color="blue">{m}</Tag>)}
            {rest > 0 && <Tag>+{rest}</Tag>}
          </Space>
        )
      },
    },
    {
      title: '状态码',
      dataIndex: 'status_summary',
      width: 140,
      render: (s?: Record<string, number>) => {
        if (!s) return '-'
        const ok = s['200'] ?? 0
        const err = Object.entries(s)
          .filter(([k]) => k !== '200')
          .reduce((acc, [, n]) => acc + n, 0)
        return (
          <Space size={4}>
            {ok > 0 && <Tag color="success">{`200×${ok}`}</Tag>}
            {err > 0 && <Tag color="error">{`非 200×${err}`}</Tag>}
          </Space>
        )
      },
    },
    {
      title: '协议',
      dataIndex: 'protocol_summary',
      width: 120,
      render: (p?: Record<string, number>) => {
        if (!p) return '-'
        return (
          <Space size={4}>
            {p.openai && <Tag color="blue">{`openai×${p.openai}`}</Tag>}
            {p.anthropic && <Tag color="orange">{`anthropic×${p.anthropic}`}</Tag>}
          </Space>
        )
      },
    },
    {
      title: '操作',
      key: 'action',
      width: 170,
      render: (_, record) => (
        <Space size={4}>
          <Button
            type="link"
            size="small"
            onClick={() => { window.location.hash = `#/sessions/${record.session_id}` }}
          >
            查看详情
          </Button>
          <Popconfirm
            title={
              <span>
                确定删除会话 <Text code style={{ fontSize: 12 }}>{truncate(record.session_id, 6, 3)}</Text> 吗？
                <br />
                将同时删除该会话下的 <strong>{record.request_count}</strong> 条日志，且不可恢复。
              </span>
            }
            okText="删除"
            okButtonProps={{ danger: true }}
            cancelText="取消"
            onConfirm={() => handleDelete(record)}
          >
            <Button type="link" size="small" danger>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <div>
      <div
        style={{
          marginBottom: 24,
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
        }}
      >
        <Title level={2} style={{ margin: 0 }}>会话视图</Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={handleRefresh} loading={loading}>
            刷新
          </Button>
        </Space>
      </div>

      <Card style={{ marginBottom: 16 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <Space wrap>
            <Input
              placeholder="搜索 session_id"
              value={query}
              onChange={e => setQuery(e.target.value)}
              onPressEnter={handleSearch}
              allowClear
              style={{ width: 280 }}
            />
            <Button type="primary" icon={<SearchOutlined />} onClick={handleSearch} loading={loading}>
              搜索
            </Button>
          </Space>
          <Button onClick={handleReset}>重置</Button>
        </div>
      </Card>

      <Card>
        <Table
          columns={columns}
          dataSource={sessions}
          rowKey="session_id"
          loading={loading}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="暂无会话记录，请确认请求是否带了 session id 请求头（如 X-Claude-Code-Session-Id、X-Agent-Session-Id，具体由后端 SESSION_ID_HEADERS 配置）"
              />
            ),
          }}
          pagination={{
            pageSize: PAGE_SIZE,
            current: currentPage,
            total,
            showTotal: t => `共 ${t} 个会话`,
            showSizeChanger: false,
            onChange: handlePageChange,
          }}
        />
      </Card>
    </div>
  )
}

export default Sessions
