import { useCallback, useEffect, useMemo, useState } from 'react'
import { Button, Card, Col, Descriptions, Input, Modal, Popconfirm, Row, Select, Space, Statistic, Table, Tag, Tooltip, Typography, message } from 'antd'
import type { TableColumnsType } from 'antd'
import { ClusterOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import JsonViewer from './JsonViewer'
import ConversationPreview from './ConversationPreview'
import { apiFetch } from './api'

const { Title, Text } = Typography

type LogRecord = {
  id: number
  created_at: string
  updated_at?: string
  model?: string | null
  provider_id?: number | null
  provider_name?: string | null
  is_stream: boolean
  protocol?: string | null
  status_code?: number | null
  processing_time_ms?: number | null
  prompt_tokens?: number | null
  completion_tokens?: number | null
  total_tokens?: number | null
  cache_creation_input_tokens?: number | null
  cache_read_input_tokens?: number | null
  target_url?: string | null
  request_data?: unknown
  response_data?: unknown
  request_headers?: unknown
  response_headers?: unknown
  error_message?: string | null
  session_id?: string | null
}

const truncateId = (id: string, head = 8) =>
  id.length <= head + 3 ? id : `${id.slice(0, head)}…`

const LogViewer = () => {
  const [currentPage, setCurrentPage] = useState(1)
  const [logs, setLogs] = useState<LogRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [modalVisible, setModalVisible] = useState(false)
  const [currentLog, setCurrentLog] = useState<LogRecord | null>(null)
  const [headersModalVisible, setHeadersModalVisible] = useState(false)
  const [filterModel, setFilterModel] = useState('')
  const [filterProtocol, setFilterProtocol] = useState<string | undefined>(undefined)
  const [filterStatusCode, setFilterStatusCode] = useState<number | undefined>(undefined)
  const [statusCodeOptions, setStatusCodeOptions] = useState<number[]>([])
  const [total, setTotal] = useState(0)
  const [todayStats, setTodayStats] = useState({ requestCount: 0, promptTokens: 0, completionTokens: 0, totalTokens: 0 })

  const fetchTodayStats = async () => {
    try {
      const res = await apiFetch('/api/logs/today_stats')
      const result = await res.json()
      if (result.success) {
        setTodayStats({
          requestCount: result.data.total_requests,
          promptTokens: result.data.prompt_tokens,
          completionTokens: result.data.completion_tokens,
          totalTokens: result.data.total_tokens,
        })
      }
    } catch (e) {
      console.error('获取今日统计失败:', e)
    }
  }

  const fetchStatusCodes = async () => {
    try {
      const res = await apiFetch('/api/logs/status_codes')
      const result = await res.json()
      if (result.success && Array.isArray(result.data)) {
        setStatusCodeOptions(result.data as number[])
      }
    } catch (e) {
      console.error('获取状态码列表失败:', e)
    }
  }

  const PAGE_SIZE = 20

  const fetchLogs = async (page = 1, model?: string, protocol?: string, statusCode?: number) => {
    setLoading(true)
    try {
      const params = new URLSearchParams({
        limit: String(PAGE_SIZE),
        offset: String((page - 1) * PAGE_SIZE),
      })
      if (model) params.append('model', model)
      if (protocol) params.append('protocol', protocol)
      if (statusCode) params.append('status_code', String(statusCode))
      const response = await apiFetch(`/api/logs?${params}`)
      const result = await response.json()
      if (result.success) {
        setLogs((result.data as LogRecord[]) ?? [])
        setTotal(result.total ?? 0)
      }
    } catch (error) {
      console.error('获取日志失败:', error)
      message.error('获取日志失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void fetchLogs()
    void fetchTodayStats()
    void fetchStatusCodes()
  }, [])

  const handlePageChange = (page: number) => {
    setCurrentPage(page)
    void fetchLogs(page, filterModel || undefined, filterProtocol, filterStatusCode)
  }

  const handleRefresh = () => {
    setCurrentPage(1)
    void fetchLogs(1, filterModel || undefined, filterProtocol, filterStatusCode)
    void fetchTodayStats()
  }

  const handleSearch = () => {
    setCurrentPage(1)
    void fetchLogs(1, filterModel || undefined, filterProtocol, filterStatusCode)
  }

  const handleResetFilters = () => {
    setCurrentPage(1)
    setFilterModel('')
    setFilterProtocol(undefined)
    setFilterStatusCode(undefined)
    void fetchLogs(1)
  }

  const handleViewDetails = async (record: LogRecord) => {
    setCurrentLog(record)
    setModalVisible(true)
    try {
      const res = await apiFetch(`/api/logs/${record.id}`)
      const result = await res.json()
      if (result.success) {
        setCurrentLog((result.data as LogRecord) ?? record)
      }
    } catch (e) {
      console.error('获取日志详情失败:', e)
      message.error('获取日志详情失败')
    }
  }

  const handleDelete = useCallback(async (record: LogRecord) => {
    try {
      const res = await apiFetch(`/api/logs/${record.id}`, { method: 'DELETE' })
      const json = await res.json()
      if (!res.ok || !json.success) {
        message.error(json.message || '删除失败')
        return
      }
      message.success('已删除日志')
      // Close the detail modal if it was showing the row we just deleted.
      if (modalVisible && currentLog?.id === record.id) {
        setModalVisible(false)
        setCurrentLog(null)
      }
      // Empty page fallback — re-fetch the previous page if this was the
      // last row on the current page.
      const wasLast = logs.length === 1 && currentPage > 1
      const nextPage = wasLast ? currentPage - 1 : currentPage
      if (wasLast) setCurrentPage(nextPage)
      void fetchLogs(nextPage, filterModel || undefined, filterProtocol, filterStatusCode)
      void fetchTodayStats()
    } catch (e) {
      console.error('删除日志失败:', e)
      message.error('删除日志失败')
    }
  }, [modalVisible, currentLog, logs.length, currentPage, filterModel, filterProtocol, filterStatusCode])

  const columns: TableColumnsType<LogRecord> = useMemo(
    () => [
      {
        title: '请求时间',
        dataIndex: 'created_at',
        key: 'created_at',
        width: 180,
        render: (text: string) => dayjs(text).format('YYYY-MM-DD HH:mm:ss'),
      },
      {
        title: '流式',
        dataIndex: 'is_stream',
        key: 'is_stream',
        width: 80,
        render: (isStream: boolean) => (
          <Tag color={isStream ? 'purple' : 'default'}>{isStream ? 'YES' : 'NO'}</Tag>
        ),
      },
      {
        title: '协议',
        dataIndex: 'protocol',
        key: 'protocol',
        width: 100,
        render: (protocol?: string | null) => {
          if (!protocol) return '-'
          return <Tag color={protocol === 'anthropic' ? 'orange' : 'blue'}>{protocol}</Tag>
        },
      },
      {
        title: '供应商',
        dataIndex: 'provider_name',
        key: 'provider_name',
        width: 130,
        render: (name?: string | null) => name || '-',
      },
      {
        title: '模型',
        dataIndex: 'model',
        key: 'model',
        width: 150,
      },
      {
        title: '状态码',
        dataIndex: 'status_code',
        key: 'status_code',
        width: 90,
        render: (code?: number | null) => {
          if (!code) {
            return '-'
          }
          return <Tag color={code === 200 ? 'success' : 'error'}>{code}</Tag>
        },
      },
      {
        title: '耗时',
        dataIndex: 'processing_time_ms',
        key: 'processing_time_ms',
        width: 110,
        render: (value?: number | null) => value ?? '-',
      },
      {
        title: '输入 Token',        dataIndex: 'prompt_tokens',
        key: 'prompt_tokens',
        width: 90,
        render: (v?: number | null) => v ?? '-',
      },
      {
        title: '输入（命中缓存）',
        dataIndex: 'cache_read_input_tokens',
        key: 'cache_read_input_tokens',
        width: 90,
        render: (v?: number | null) => v ?? '-',
      },
      {
        title: '输出 Token',
        dataIndex: 'completion_tokens',
        key: 'completion_tokens',
        width: 90,
        render: (v?: number | null) => v ?? '-',
      },
      {
        title: '总 Token',
        dataIndex: 'total_tokens',
        key: 'total_tokens',
        width: 90,
        render: (v?: number | null) => v ?? '-',
      },
      {
        title: 'Token/s',
        key: 'tokens_per_sec',
        width: 90,
        render: (_, record) => {
          const output = record.completion_tokens
          const ms = record.processing_time_ms
          if (!output || !ms || ms === 0) return '-'
          return (output / (ms / 1000)).toFixed(1)
        },
      },
      {
        title: '会话 ID',
        dataIndex: 'session_id',
        width: 170,
        render: (sid?: string | null) => {
          if (!sid) return <Text type="secondary">-</Text>
          return (
            <Tooltip title={sid} mouseEnterDelay={0.4}>
              <Button
                type="link"
                size="small"
                style={{ padding: 0, fontFamily: 'monospace', fontSize: 12 }}
                onClick={() => { window.location.hash = `#/sessions/${sid}` }}
              >
                {truncateId(sid)}
              </Button>
            </Tooltip>
          )
        },
      },
      {
        title: '操作',
        key: 'action',
        width: 160,
        render: (_, record) => (
          <Space size={4}>
            <Button type="link" size="small" onClick={() => handleViewDetails(record)}>
              查看详情
            </Button>
            <Popconfirm
              title={`确定删除日志 #${record.id} 吗？此操作不可恢复。`}
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
    ],
    [handleDelete],
  )

  return (
    <div>
      <div
        style={{
          marginBottom: '24px',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
        }}
      >
        <Title level={2} style={{ margin: 0 }}>
          请求日志
        </Title>
        <Space>
          <Button type="primary" onClick={handleRefresh} loading={loading}>
            刷新
          </Button>
        </Space>
      </div>

      <Row gutter={[16, 16]} style={{ marginBottom: 24 }}>
        <Col span={6}>
          <Card><Statistic title="今日请求次数" value={todayStats.requestCount} /></Card>
        </Col>
        <Col span={6}>
          <Card><Statistic title="今日总计 Token" value={todayStats.totalTokens} /></Card>
        </Col>
        <Col span={6}>
          <Card><Statistic title="今日输入 Token" value={todayStats.promptTokens} /></Card>
        </Col>
        <Col span={6}>
          <Card><Statistic title="今日输出 Token" value={todayStats.completionTokens} /></Card>
        </Col>
      </Row>

      <Card style={{ marginBottom: 16 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <Space wrap>
            <Input
              placeholder="搜索模型"
              value={filterModel}
              onChange={e => setFilterModel(e.target.value)}
              onPressEnter={handleSearch}
              allowClear
              style={{ width: 200 }}
            />
            <Select
              placeholder="协议"
              value={filterProtocol}
              onChange={v => setFilterProtocol(v)}
              allowClear
              style={{ width: 140 }}
              options={[
                { value: 'openai', label: 'OpenAI' },
                { value: 'anthropic', label: 'Anthropic' },
              ]}
            />
            <Select
              placeholder="状态码"
              value={filterStatusCode}
              onChange={v => setFilterStatusCode(v)}
              allowClear
              style={{ width: 120 }}
              options={statusCodeOptions.map(code => ({ value: code, label: String(code) }))}
            />
            <Button type="primary" onClick={handleSearch} loading={loading}>
              搜索
            </Button>
          </Space>
          <Button onClick={handleResetFilters}>重置</Button>
        </div>
      </Card>

      <Card>
        <Table
          columns={columns}
          dataSource={logs}
          rowKey="id"
          loading={loading}
          pagination={{
            pageSize: PAGE_SIZE,
            current: currentPage,
            total,
            showTotal: (t) => `共 ${t} 条`,
            showSizeChanger: false,
            onChange: handlePageChange,
          }}
        />
      </Card>

      <Modal
        title={
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', paddingRight: 32 }}>
            <span>{`日志详情 - ID: ${currentLog?.id ?? ''}`}</span>
            <Space>
              {currentLog?.session_id && (
                <Button
                  size="small"
                  icon={<ClusterOutlined />}
                  onClick={() => {
                    const sid = currentLog.session_id
                    if (sid) window.location.hash = `#/sessions/${sid}`
                  }}
                >
                  查看该会话
                </Button>
              )}
              <Button size="small" onClick={() => setHeadersModalVisible(true)}>
                查看请求头
              </Button>
              {currentLog && (
                <Popconfirm
                  title={`确定删除日志 #${currentLog.id} 吗？此操作不可恢复。`}
                  okText="删除"
                  okButtonProps={{ danger: true }}
                  cancelText="取消"
                  onConfirm={() => handleDelete(currentLog)}
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
    </div>
  )
}

export default LogViewer
