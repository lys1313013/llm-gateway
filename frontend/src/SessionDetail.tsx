import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Alert, Button, Card, Checkbox, Col, Descriptions, Empty, Modal, Popconfirm, Row, Space, Statistic, Table, Tag, Typography, message,
} from 'antd'
import type { TableColumnsType } from 'antd'
import {
  ArrowLeftOutlined, ClearOutlined, CopyOutlined, DeleteOutlined, ReloadOutlined, SwapOutlined,
} from '@ant-design/icons'
import dayjs from 'dayjs'
import JsonViewer, { DiffJsonViewer } from './JsonViewer'
import ConversationPreview from './ConversationPreview'
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

// Full log record shape used inside the detail modal — fetched lazily via
// /api/logs/:id because the session list endpoint omits the heavy JSONB
// columns (mirrors LogViewer's behavior).
type LogDetail = SessionLog & {
  updated_at?: string
  provider_id?: number | null
  target_url?: string | null
  request_headers?: unknown
  response_headers?: unknown
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

type CompareToolbarProps = {
  selectedIds: number[]
  onClear: () => void
  onCompare: () => void
}

const CompareToolbar = ({ selectedIds, onClear, onCompare }: CompareToolbarProps) => {
  const ready = selectedIds.length === 2
  return (
    <div
      style={{
        marginBottom: 12,
        padding: '8px 12px',
        background: ready ? '#eff6ff' : '#fafafa',
        border: `1px solid ${ready ? '#bfdbfe' : '#e5e7eb'}`,
        borderRadius: 6,
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        flexWrap: 'wrap',
      }}
    >
      <Text type="secondary" style={{ fontSize: 12 }}>对比选择 {selectedIds.length}/2</Text>
      {selectedIds.length > 0 && (
        <Space size={4}>
          {selectedIds.map((id, i) => (
            <Tag key={id} color={i === 0 ? 'blue' : 'orange'}>
              {i === 0 ? 'A' : 'B'} #{id}
            </Tag>
          ))}
        </Space>
      )}
      <div style={{ flex: 1 }} />
      <Button
        size="small"
        type="primary"
        disabled={!ready}
        onClick={onCompare}
      >
        对比 Request Data
      </Button>
      <Button
        size="small"
        icon={<ClearOutlined />}
        disabled={selectedIds.length === 0}
        onClick={onClear}
      >
        清空
      </Button>
    </div>
  )
}

type CompareMetaItem = {
  key: string
  label: string
  a: React.ReactNode
  b: React.ReactNode
}

type CompareMetaCardProps = {
  left: LogDetail | null
  right: LogDetail | null
}

const buildCompareMetaItems = (left: LogDetail, right: LogDetail): CompareMetaItem[] => {
  const num = (v: number | null | undefined) => (v === null || v === undefined ? '-' : v.toLocaleString())
  const str = (v: string | null | undefined) => v ?? '-'

  return [
    {
      key: 'created_at',
      label: '请求时间',
      a: dayjs(left.created_at).format('YYYY-MM-DD HH:mm:ss'),
      b: dayjs(right.created_at).format('YYYY-MM-DD HH:mm:ss'),
    },
    {
      key: 'protocol',
      label: '协议',
      a: str(left.protocol),
      b: str(right.protocol),
    },
    {
      key: 'model',
      label: '模型',
      a: str(left.model),
      b: str(right.model),
    },
    {
      key: 'status_code',
      label: '状态码',
      a: left.status_code ?? '-',
      b: right.status_code ?? '-',
    },
    {
      key: 'provider',
      label: '供应商',
      a: str(left.provider_name),
      b: str(right.provider_name),
    },
    {
      key: 'is_stream',
      label: '流式',
      a: left.is_stream ? 'YES' : 'NO',
      b: right.is_stream ? 'YES' : 'NO',
    },
    {
      key: 'processing_time_ms',
      label: '耗时 (ms)',
      a: num(left.processing_time_ms),
      b: num(right.processing_time_ms),
    },
    {
      key: 'prompt_tokens',
      label: '输入 Token',
      a: num(left.prompt_tokens),
      b: num(right.prompt_tokens),
    },
    {
      key: 'cache_read',
      label: '输入（命中缓存）',
      a: num(left.cache_read_input_tokens),
      b: num(right.cache_read_input_tokens),
    },
    {
      key: 'cache_creation',
      label: '缓存创建',
      a: num(left.cache_creation_input_tokens),
      b: num(right.cache_creation_input_tokens),
    },
    {
      key: 'completion_tokens',
      label: '输出 Token',
      a: num(left.completion_tokens),
      b: num(right.completion_tokens),
    },
    {
      key: 'total_tokens',
      label: '总 Token',
      a: num(left.total_tokens),
      b: num(right.total_tokens),
    },
    {
      key: 'error',
      label: '错误信息',
      a: str(left.error_message),
      b: str(right.error_message),
    },
  ]
}

const CompareMetaCard = ({ left, right }: CompareMetaCardProps) => {
  if (!left || !right) return null
  const items = buildCompareMetaItems(left, right)
  return (
    <Row gutter={[12, 0]} wrap>
      {items.map(item => {
        const same = String(item.a) === String(item.b)
        const cellStyle: React.CSSProperties = {
          padding: '6px 10px',
          borderRadius: 4,
          background: same ? 'transparent' : '#fef3c7',
          color: same ? undefined : '#92400e',
          fontSize: 12,
          minWidth: 0,
          wordBreak: 'break-word',
        }
        return (
          <Col key={item.key} xs={24} sm={12} md={8} lg={6} xl={4} style={{ marginBottom: 8 }}>
            <div style={{ fontSize: 11, color: '#6b7280', marginBottom: 2 }}>{item.label}</div>
            <div style={{ display: 'flex', gap: 6, alignItems: 'stretch' }}>
              <div style={{ ...cellStyle, flex: 1 }}>
                <span style={{ fontSize: 10, color: '#3b82f6', marginRight: 4 }}>A</span>
                {item.a}
              </div>
              <div style={{ ...cellStyle, flex: 1 }}>
                <span style={{ fontSize: 10, color: '#f97316', marginRight: 4 }}>B</span>
                {item.b}
              </div>
            </div>
          </Col>
        )
      })}
    </Row>
  )
}

const SessionDetail = ({ sessionId }: Props) => {
  const [meta, setMeta] = useState<SessionMeta | null>(null)
  const [logs, setLogs] = useState<SessionLog[]>([])
  const [loading, setLoading] = useState(false)
  const [notFound, setNotFound] = useState(false)
  const [total, setTotal] = useState(0)
  const [currentPage, setCurrentPage] = useState(1)

  const [modalVisible, setModalVisible] = useState(false)
  const [headersModalVisible, setHeadersModalVisible] = useState(false)
  const [currentLog, setCurrentLog] = useState<LogDetail | null>(null)

  // Compare-two-requests state. We keep selected ids in insertion order so
  // the "A" / "B" slot shown in the toolbar and the diff is stable and
  // matches the user's click order.
  const [selectedIds, setSelectedIds] = useState<number[]>([])
  const [compareModalVisible, setCompareModalVisible] = useState(false)
  const [compareData, setCompareData] = useState<Record<number, LogDetail>>({})
  const [compareLoading, setCompareLoading] = useState(false)

  const compareLeft = selectedIds.length > 0 ? compareData[selectedIds[0]] : null
  const compareRight = selectedIds.length > 1 ? compareData[selectedIds[1]] : null

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
    void fetchDetail(1)
  }, [fetchDetail])

  const handleRefresh = () => {
    void fetchDetail(currentPage)
  }

  const handlePageChange = (page: number) => {
    setCurrentPage(page)
    void fetchDetail(page)
  }

  const goBack = () => {
    window.location.hash = '#/sessions'
  }

  const handleViewDetails = async (record: SessionLog) => {
    setCurrentLog(record as LogDetail)
    setModalVisible(true)
    try {
      const res = await apiFetch(`/api/logs/${record.id}`)
      const json = await res.json()
      if (json.success) {
        setCurrentLog(json.data as LogDetail)
      } else {
        message.error(json.message || '获取日志详情失败')
      }
    } catch (e) {
      console.error('获取日志详情失败:', e)
      message.error('获取日志详情失败')
    }
  }

  const handleDeleteLog = useCallback(async (record: LogDetail | SessionLog) => {
    try {
      const res = await apiFetch(`/api/logs/${record.id}`, { method: 'DELETE' })
      const json = await res.json()
      if (!res.ok || !json.success) {
        message.error(json.message || '删除失败')
        return
      }
      message.success('已删除日志')
      if (modalVisible && currentLog?.id === record.id) {
        setModalVisible(false)
        setCurrentLog(null)
      }
      const wasLast = logs.length === 1 && currentPage > 1
      const nextPage = wasLast ? currentPage - 1 : currentPage
      if (wasLast) setCurrentPage(nextPage)
      void fetchDetail(nextPage)
    } catch (e) {
      console.error('删除日志失败:', e)
      message.error('删除日志失败')
    }
  }, [modalVisible, currentLog, logs.length, currentPage, fetchDetail])

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

  const handleToggleSelect = useCallback((id: number) => {
    setSelectedIds(prev => {
      if (prev.includes(id)) return prev.filter(x => x !== id)
      if (prev.length >= 2) {
        message.warning('最多选择 2 条请求进行对比')
        return prev
      }
      return [...prev, id]
    })
  }, [])

  const handleClearSelection = () => setSelectedIds([])

  const handleSwapSelection = () => {
    setSelectedIds(prev => [prev[1], prev[0]])
  }

  const handleOpenCompare = useCallback(async () => {
    if (selectedIds.length !== 2) return
    setCompareLoading(true)
    setCompareModalVisible(true)
    try {
      const [aRes, bRes] = await Promise.all([
        apiFetch(`/api/logs/${selectedIds[0]}`),
        apiFetch(`/api/logs/${selectedIds[1]}`),
      ])
      const [aJson, bJson] = await Promise.all([aRes.json(), bRes.json()])
      const a = aJson.success ? (aJson.data as LogDetail) : null
      const b = bJson.success ? (bJson.data as LogDetail) : null
      if (!a || !b) {
        message.error('加载对比数据失败，所选日志可能已被删除')
        setCompareModalVisible(false)
        return
      }
      setCompareData({ [selectedIds[0]]: a, [selectedIds[1]]: b })
    } catch (e) {
      console.error('加载对比数据失败:', e)
      message.error('加载对比数据失败')
      setCompareModalVisible(false)
    } finally {
      setCompareLoading(false)
    }
  }, [selectedIds])

  const handleCloseCompare = () => {
    setCompareModalVisible(false)
    setCompareData({})
  }

  const columns: TableColumnsType<SessionLog> = useMemo(
    () => [
      {
        title: '对比',
        key: 'compare_select',
        width: 60,
        render: (_, record) => {
          const checked = selectedIds.includes(record.id)
          const isDisabled = !checked && selectedIds.length >= 2
          return (
            <Checkbox
              checked={checked}
              disabled={isDisabled}
              onChange={() => handleToggleSelect(record.id)}
              onClick={e => e.stopPropagation()}
            />
          )
        },
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
      {
        title: '操作',
        key: 'action',
        width: 110,
        render: (_, record) => (
          <Button type="link" size="small" onClick={() => handleViewDetails(record)}>
            查看详情
          </Button>
        ),
      },
    ],
    [selectedIds, handleToggleSelect],
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
        <CompareToolbar
          selectedIds={selectedIds}
          onClear={handleClearSelection}
          onCompare={handleOpenCompare}
        />
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
          locale={{
            emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该会话暂无请求" />,
          }}
        />
      </Card>

      <Modal
        title={
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', paddingRight: 32 }}>
            <span>{`日志详情 - ID: ${currentLog?.id ?? ''}`}</span>
            <Space>
              <Button size="small" onClick={() => setHeadersModalVisible(true)}>
                查看请求头
              </Button>
              {currentLog && (
                <Popconfirm
                  title={`确定删除日志 #${currentLog.id} 吗？此操作不可恢复。`}
                  okText="删除"
                  okButtonProps={{ danger: true }}
                  cancelText="取消"
                  onConfirm={() => handleDeleteLog(currentLog)}
                >
                  <Button size="small" danger>删除该日志</Button>
                </Popconfirm>
              )}
            </Space>
          </div>
        }
        open={modalVisible}
        onCancel={() => setModalVisible(false)}
        footer={null}
        width="min(96vw, 1720px)"
        style={{ top: 12 }}
        styles={{
          body: {
            maxHeight: 'calc(94vh - 88px)',
            overflowY: 'auto',
            paddingTop: 12,
          },
        }}
      >
        {currentLog && (
          <div>
            <Descriptions bordered size="small" column={4} style={{ marginBottom: 16 }}>
              <Descriptions.Item label="请求时间">
                {dayjs(currentLog.created_at).format('YYYY-MM-DD HH:mm:ss')}
              </Descriptions.Item>
              <Descriptions.Item label="是否流式">
                <Tag>{currentLog.is_stream ? 'YES' : 'NO'}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="协议">
                <Tag color={currentLog.protocol === 'anthropic' ? 'orange' : 'blue'}>
                  {currentLog.protocol || '-'}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="供应商">
                {currentLog.provider_name || '-'}
              </Descriptions.Item>
              <Descriptions.Item label="状态码">
                <Tag color={currentLog.status_code === 200 ? 'success' : 'error'}>
                  {currentLog.status_code ?? '-'}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="模型">{currentLog.model || '-'}</Descriptions.Item>
              <Descriptions.Item label="耗时 (ms)">
                {currentLog.processing_time_ms ?? '-'}
              </Descriptions.Item>
              <Descriptions.Item label="Tokens">
                <Space orientation="vertical" size={0}>
                  <Text><strong>输入（未命中缓存）:</strong> {currentLog.prompt_tokens ?? '-'}</Text>
                  <Text><strong>输入（命中缓存）:</strong> {currentLog.cache_read_input_tokens ?? '-'}</Text>
                  <Text><strong>输出:</strong> {currentLog.completion_tokens ?? '-'}</Text>
                  <Text><strong>总计:</strong> {currentLog.total_tokens ?? '-'}</Text>
                  <Text><strong>缓存创建:</strong> {currentLog.cache_creation_input_tokens ?? '-'}</Text>
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label="目标 URL">
                {currentLog.target_url || '-'}
              </Descriptions.Item>
            </Descriptions>

            {currentLog.error_message && (
              <div style={{ marginBottom: 16 }}>
                <Text type="danger">
                  <strong>错误信息:</strong> {currentLog.error_message}
                </Text>
              </div>
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
                title="Request Data"
                value={currentLog.request_data}
                height="70vh"
                style={{ flex: '1 1 360px', minWidth: 320 }}
              />
              <JsonViewer
                title="Response Data"
                value={currentLog.response_data}
                height="70vh"
                style={{ flex: '1 1 360px', minWidth: 320 }}
              />
              <ConversationPreview
                requestData={currentLog.request_data}
                responseData={currentLog.response_data}
                protocol={currentLog.protocol}
              />
            </div>
          </div>
        )}
      </Modal>

      <Modal
        title={`请求头 - 日志 ID: ${currentLog?.id ?? ''}`}
        open={headersModalVisible}
        onCancel={() => setHeadersModalVisible(false)}
        footer={null}
        width="min(72vw, 1100px)"
        style={{ top: 48 }}
        styles={{
          body: {
            maxHeight: 'calc(86vh - 88px)',
            overflowY: 'auto',
            paddingTop: 12,
          },
        }}
      >
        <div
          style={{
            display: 'flex',
            gap: 16,
            alignItems: 'stretch',
            flexWrap: 'wrap',
          }}
        >
          <JsonViewer title="Request Headers" value={currentLog?.request_headers} height="60vh" />
          <JsonViewer title="Response Headers" value={currentLog?.response_headers} height="60vh" />
        </div>
      </Modal>

      <Modal
        title={
          compareLeft && compareRight ? (
            <Space size={8} style={{ fontSize: 14 }}>
              <span>请求对比</span>
              <Tag color="blue">A: #{compareLeft.id}</Tag>
              <Text type="secondary">vs</Text>
              <Tag color="orange">B: #{compareRight.id}</Tag>
              <Button
                size="small"
                icon={<SwapOutlined />}
                onClick={handleSwapSelection}
              >
                互换
              </Button>
            </Space>
          ) : '请求对比'
        }
        open={compareModalVisible}
        onCancel={handleCloseCompare}
        footer={null}
        width="min(98vw, 1840px)"
        style={{ top: 8 }}
        styles={{
          body: {
            maxHeight: 'calc(96vh - 88px)',
            overflowY: 'auto',
            paddingTop: 12,
          },
        }}
        destroyOnHidden
      >
        {compareLoading || !compareLeft || !compareRight ? (
          <div style={{ padding: 80, textAlign: 'center' }}>
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={compareLoading ? '正在加载对比数据…' : '请选择 2 条请求进行对比'}
            />
          </div>
        ) : (
          <Space orientation="vertical" size={16} style={{ width: '100%' }}>
            <Card size="small" title="元数据对比">
              <CompareMetaCard left={compareLeft} right={compareRight} />
            </Card>
            <DiffJsonViewer
              title="Request Data Diff"
              leftLabel={`#${compareLeft.id} · ${dayjs(compareLeft.created_at).format('YYYY-MM-DD HH:mm:ss')}`}
              rightLabel={`#${compareRight.id} · ${dayjs(compareRight.created_at).format('YYYY-MM-DD HH:mm:ss')}`}
              left={compareLeft.request_data}
              right={compareRight.request_data}
              height="72vh"
            />
          </Space>
        )}
      </Modal>
    </div>
  )
}

export default SessionDetail