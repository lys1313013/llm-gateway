import { useEffect, useMemo, useState } from 'react'
import { Button, Card, Descriptions, Modal, Space, Table, Tabs, Tag, Typography, message } from 'antd'
import type { TableColumnsType } from 'antd'
import dayjs from 'dayjs'
import JsonViewer from './JsonViewer'
import TokenStats from './TokenStats'

const { Title, Text } = Typography

type LogRecord = {
  id: number
  created_at: string
  updated_at?: string
  mode: string
  model?: string | null
  is_stream: boolean
  status_code?: number | null
  processing_time_ms?: number | null
  prompt_tokens?: number | null
  completion_tokens?: number | null
  total_tokens?: number | null
  target_url?: string | null
  request_data?: unknown
  response_data?: unknown
  error_message?: string | null
}

const App = () => {
  const [logs, setLogs] = useState<LogRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [modalVisible, setModalVisible] = useState(false)
  const [currentLog, setCurrentLog] = useState<LogRecord | null>(null)

  const fetchLogs = async () => {
    setLoading(true)
    try {
      const response = await fetch('/api/logs?limit=100')
      const result = await response.json()
      if (result.success) {
        setLogs(result.data as LogRecord[])
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
  }, [])

  const handleViewDetails = (record: LogRecord) => {
    setCurrentLog(record)
    setModalVisible(true)
  }

  const columns: TableColumnsType<LogRecord> = useMemo(
    () => [
      {
        title: 'ID',
        dataIndex: 'id',
        key: 'id',
        width: 60,
      },
      {
        title: '请求时间',
        dataIndex: 'created_at',
        key: 'created_at',
        width: 180,
        render: (text: string) => dayjs(text).format('YYYY-MM-DD HH:mm:ss'),
      },
      {
        title: '模式',
        dataIndex: 'mode',
        key: 'mode',
        width: 80,
        render: (mode: string) => (
          <Tag color={mode === 'mock' ? 'blue' : 'green'}>{mode.toUpperCase()}</Tag>
        ),
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
        title: '耗时 (ms)',
        dataIndex: 'processing_time_ms',
        key: 'processing_time_ms',
        width: 110,
        render: (value?: number | null) => value ?? '-',
      },
      {
        title: 'Tokens',
        key: 'tokens',
        width: 140,
        render: (_, record) => {
          if (!record.total_tokens && !record.prompt_tokens && !record.completion_tokens) {
            return '-'
          }
          return (
            <Space direction="vertical" size={0}>
              <Text style={{ fontSize: '12px', color: '#8c8c8c' }}>
                输入: {record.prompt_tokens ?? '-'} | 输出: {record.completion_tokens ?? '-'}
              </Text>
              <Text>总计: {record.total_tokens ?? '-'}</Text>
            </Space>
          )
        },
      },
      {
        title: '操作',
        key: 'action',
        width: 100,
        render: (_, record) => (
          <Button type="link" onClick={() => handleViewDetails(record)}>
            查看详情
          </Button>
        ),
      },
    ],
    [],
  )

  const logContent = (
    <>
      <div
        style={{
          marginBottom: '24px',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
        }}
      >
        <Title level={2} style={{ margin: 0 }}>
          Mock OpenAI 请求日志
        </Title>
        <Space>
          <Button type="primary" onClick={() => void fetchLogs()} loading={loading}>
            刷新
          </Button>
        </Space>
      </div>

      <Card>
        <Table
          columns={columns}
          dataSource={logs}
          rowKey="id"
          loading={loading}
          pagination={{ pageSize: 20 }}
          scroll={{ x: 980 }}
        />
      </Card>
    </>
  )

  const items = [
    {
      key: '1',
      label: '请求日志',
      children: logContent,
    },
    {
      key: '2',
      label: 'Token 统计',
      children: <TokenStats />,
    },
  ]

  return (
    <div
      style={{
        padding: '24px',
        maxWidth: '1400px',
        margin: '0 auto',
        background: '#f0f2f5',
        minHeight: '100vh',
      }}
    >
      <Tabs defaultActiveKey="1" items={items} />

      <Modal
        title={`日志详情 - ID: ${currentLog?.id ?? ''}`}
        open={modalVisible}
        onCancel={() => setModalVisible(false)}
        footer={null}
        width="min(82vw, 1380px)"
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
              <Descriptions.Item label="模式">
                <Tag color={currentLog.mode === 'mock' ? 'blue' : 'green'}>
                  {currentLog.mode.toUpperCase()}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="是否流式">
                <Tag>{currentLog.is_stream ? 'YES' : 'NO'}</Tag>
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
                <Space direction="vertical" size={0}>
                  <Text><strong>输入:</strong> {currentLog.prompt_tokens ?? '-'}</Text>
                  <Text><strong>输出:</strong> {currentLog.completion_tokens ?? '-'}</Text>
                  <Text><strong>总计:</strong> {currentLog.total_tokens ?? '-'}</Text>
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
              <JsonViewer title="Request Data" value={currentLog.request_data} height="70vh" />
              <JsonViewer title="Response Data" value={currentLog.response_data} height="70vh" />
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}

export default App
