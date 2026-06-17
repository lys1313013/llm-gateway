import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Alert, Button, Card, Col, Descriptions, Empty, Popconfirm, Row, Space, Statistic, Table, Tag, Typography, message,
} from 'antd'
import type { TableColumnsType } from 'antd'
import {
  ArrowLeftOutlined, CopyOutlined, DeleteOutlined, ReloadOutlined,
} from '@ant-design/icons'
import dayjs from 'dayjs'
import JsonViewer from './JsonViewer'
import { apiFetch } from './api'

const { Title, Text } = Typography

type SessionLog = {
  id: number
  created_at: string
  model?: string | null
  provider_name?: string | null
  is_stream: boolean
  protocol?: string | null
  status_code?: number | null
  processing_time_ms?: number | null
  prompt_tokens?: number | null
  completion_tokens?: number | null
  total_tokens?: number | null
  cache_read_input_tokens?: number | null
  cache_creation_input_tokens?: number | null
  request_data?: unknown
  response_data?: unknown
  error_message?: string | null
}

type SessionMeta = {
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

type Props = {
  sessionId: string
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

const SessionDetail = ({ sessionId }: Props) => {
  const [meta, setMeta] = useState<SessionMeta | null>(null)
  const [logs, setLogs] = useState<SessionLog[]>([])
  const [loading, setLoading] = useState(false)
  const [notFound, setNotFound] = useState(false)
  const [total, setTotal] = useState(0)
  const [currentPage, setCurrentPage] = useState(1)
  const [expandedRowKeys, setExpandedRowKeys] = useState<React.Key[]>([])

  const fetchDetail = useCallback(async (page = 1) => {
    setLoading(true)
    setNotFound(false)
    try {
      const params = new URLSearchParams({
        limit: '100',
        offset: String((page - 1) * 100),
      })
      const res = await apiFetch(`/api/sessions/${encodeURIComponent(sessionId)}?${params}`)
      if (res.status === 404) {
        setNotFound(true)
        setMeta(null)
        setLogs([])
        setTotal(0)
        return
      }
      const json = await res.json()
      if (json.success) {
        setMeta(json.meta as SessionMeta)
        setLogs(json.data as SessionLog[])
        setTotal(json.total ?? 0)
      } else {
        message.error(json.message || '获取会话详情失败')
      }
    } catch (e) {
      console.error('获取会话详情失败:', e)
      message.error('获取会话详情失败')
    } finally {
      setLoading(false)
    }
  }, [sessionId])

  useEffect(() => {
    setCurrentPage(1)
    setExpandedRowKeys([])
    void fetchDetail(1)
  }, [fetchDetail])

  const handleRefresh = () => {
    void fetchDetail(currentPage)
  }

  const handlePageChange = (page: number) => {
    setCurrentPage(page)
    setExpandedRowKeys([])
    void fetchDetail(page)
  }

  const goBack = () => {
    window.location.hash = '#/sessions'
  }

  const handleDeleteSession = async () => {
    try {
      const res = await apiFetch(
        `/api/sessions/${encodeURIComponent(sessionId)}`,
        { method: 'DELETE' },
      )
      const json = await res.json()
      if (!res.ok || !json.success) {
        message.error(json.message || '删除失败')
        return
      }
      const n = (json.data && json.data.deleted_logs) || 0
      message.success(`已删除会话及其 ${n} 条日志`)
      window.location.hash = '#/sessions'
    } catch (e) {
      console.error('删除会话失败:', e)
      message.error('删除会话失败')
    }
  }

  const columns: TableColumnsType<SessionLog> = useMemo(
    () => [
      {
        title: '#',
        dataIndex: 'id',
        width: 60,
      },
      {
        title: '时间',
        dataIndex: 'created_at',
        width: 170,
        render: (t: string) => dayjs(t).format('YYYY-MM-DD HH:mm:ss'),
      },
      {
        title: '协议',
        dataIndex: 'protocol',
        width: 100,
        render: (p?: string | null) =>
          p ? <Tag color={p === 'anthropic' ? 'orange' : 'blue'}>{p}</Tag> : '-',
      },
      {
        title: '模型',
        dataIndex: 'model',
        width: 160,
        render: (m?: string | null) => m || '-',
      },
      {
        title: '状态',
        dataIndex: 'status_code',
        width: 80,
        render: (c?: number | null) =>
          c ? <Tag color={c === 200 ? 'success' : 'error'}>{c}</Tag> : '-',
      },
      {
        title: '耗时(ms)',
        dataIndex: 'processing_time_ms',
        width: 90,
        render: (v?: number | null) => v ?? '-',
      },
      {
        title: '输入',
        dataIndex: 'prompt_tokens',
        width: 80,
        render: (v?: number | null) => v ?? '-',
      },
      {
        title: '输出',
        dataIndex: 'completion_tokens',
        width: 80,
        render: (v?: number | null) => v ?? '-',
      },
      {
        title: '总',
        dataIndex: 'total_tokens',
        width: 80,
        render: (v?: number | null) => v ?? '-',
      },
      {
        title: '流式',
        dataIndex: 'is_stream',
        width: 70,
        render: (s: boolean) => (s ? <Tag color="purple">YES</Tag> : '-'),
      },
    ],
    [],
  )

  if (notFound) {
    return (
      <div>
        <Space style={{ marginBottom: 16 }}>
          <Button icon={<ArrowLeftOutlined />} onClick={goBack}>返回</Button>
        </Space>
        <Alert
          type="error"
          showIcon
          message="会话不存在"
          description={`未找到 session_id 为 ${sessionId} 的会话记录`}
        />
      </div>
    )
  }

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={goBack}>返回会话列表</Button>
        <Button icon={<ReloadOutlined />} onClick={handleRefresh} loading={loading}>
          刷新
        </Button>
        {meta && (
          <Popconfirm
            title={
              <span>
                确定删除整个会话吗？
                <br />
                将同时删除该会话下的 <strong>{meta.request_count}</strong> 条日志，且不可恢复。
              </span>
            }
            okText="删除"
            okButtonProps={{ danger: true }}
            cancelText="取消"
            onConfirm={handleDeleteSession}
          >
            <Button danger icon={<DeleteOutlined />}>删除会话</Button>
          </Popconfirm>
        )}
      </Space>

      <Card style={{ marginBottom: 16 }}>
        <Space align="center" style={{ marginBottom: 12, width: '100%', justifyContent: 'space-between' }}>
          <Space>
            <Title level={4} style={{ margin: 0 }}>会话详情</Title>
            <Text code style={{ fontSize: 13 }}>{sessionId}</Text>
            <Button
              type="text"
              size="small"
              icon={<CopyOutlined />}
              onClick={() => copyText(sessionId)}
            >
              复制
            </Button>
          </Space>
        </Space>
        {meta && (
          <>
            <Row gutter={[16, 16]}>
              <Col span={6}>
                <Card size="small"><Statistic title="请求数" value={meta.request_count} /></Card>
              </Col>
              <Col span={6}>
                <Card size="small"><Statistic title="总 Token" value={meta.total_tokens} /></Card>
              </Col>
              <Col span={6}>
                <Card size="small"><Statistic title="输入 Token" value={meta.prompt_tokens} /></Card>
              </Col>
              <Col span={6}>
                <Card size="small"><Statistic title="输出 Token" value={meta.completion_tokens} /></Card>
              </Col>
            </Row>
            <Descriptions
              bordered
              size="small"
              column={2}
              style={{ marginTop: 16 }}
            >
              <Descriptions.Item label="时间范围">
                {dayjs(meta.first_at).format('YYYY-MM-DD HH:mm:ss')}
                {' '}~{' '}
                {dayjs(meta.last_at).format('YYYY-MM-DD HH:mm:ss')}
              </Descriptions.Item>
              <Descriptions.Item label="涉及模型">
                {meta.models && meta.models.length > 0
                  ? meta.models.map(m => <Tag key={m} color="blue">{m}</Tag>)
                  : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="状态码分布">
                {meta.status_summary
                  ? Object.entries(meta.status_summary).map(([code, n]) => (
                      <Tag
                        key={code}
                        color={code === '200' ? 'success' : 'error'}
                      >{`${code}×${n}`}</Tag>
                    ))
                  : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="协议分布">
                {meta.protocol_summary
                  ? Object.entries(meta.protocol_summary).map(([p, n]) => (
                      <Tag
                        key={p}
                        color={p === 'anthropic' ? 'orange' : 'blue'}
                      >{`${p}×${n}`}</Tag>
                    ))
                  : '-'}
              </Descriptions.Item>
            </Descriptions>
          </>
        )}
      </Card>

      <Card title={`请求列表 (共 ${total} 条)`}>
        <Table
          columns={columns}
          dataSource={logs}
          rowKey="id"
          loading={loading}
          pagination={{
            pageSize: 100,
            current: currentPage,
            total,
            showSizeChanger: false,
            onChange: handlePageChange,
          }}
          expandable={{
            expandedRowKeys,
            onExpand: (expanded, record) => {
              setExpandedRowKeys(prev =>
                expanded
                  ? [...prev, record.id]
                  : prev.filter(k => k !== record.id),
              )
            },
            expandedRowRender: (record) => (
              <div>
                {record.error_message && (
                  <Alert
                    type="error"
                    showIcon
                    style={{ marginBottom: 12 }}
                    message={record.error_message}
                  />
                )}
                <div
                  style={{
                    display: 'flex',
                    gap: 16,
                    alignItems: 'stretch',
                    flexWrap: 'wrap',
                  }}
                >
                  <JsonViewer
                    title="Request"
                    value={record.request_data}
                    height="50vh"
                  />
                  <JsonViewer
                    title="Response"
                    value={record.response_data}
                    height="50vh"
                  />
                </div>
              </div>
            ),
          }}
          locale={{
            emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该会话暂无请求" />,
          }}
        />
      </Card>
    </div>
  )
}

export default SessionDetail
