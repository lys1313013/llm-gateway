import { useEffect, useRef, useState } from 'react'
import { Button, Card, Dropdown, Form, Input, Modal, Popconfirm, Progress, Space, Switch, Table, Tag, Typography, message } from 'antd'
import { DownOutlined, LoadingOutlined, ThunderboltOutlined } from '@ant-design/icons'
import type { TableColumnsType } from 'antd'
import dayjs from 'dayjs'
import { apiFetch } from './api'

export type ExposedModelRecord = {
  id: number
  model_id: string
  owned_by: string
  is_active: boolean
  last_openai_test_time: string | null
  last_anthropic_test_time: string | null
  create_time: string
  update_time: string
}

type TestResult = {
  modelId: string
  modelDbId: number
  protocol: 'openai' | 'anthropic'
  endpoint: string
  status: number
  latency: number
  content: string
  tokens: number
  error?: string
  pending?: boolean
}

const { Text, Paragraph } = Typography

/** Run a single model test and return the result (no side-effects on React state). */
async function runSingleTest(
  record: ExposedModelRecord,
  protocol: 'openai' | 'anthropic',
): Promise<TestResult> {
  const modelId = record.model_id
  const isAnthropic = protocol === 'anthropic'
  // Use admin-only test endpoints (JWT auth) instead of /v1/ (API key auth)
  const endpoint = isAnthropic ? '/api/test/messages' : '/api/test/chat'
  const body = isAnthropic
    ? JSON.stringify({ model: modelId, messages: [{ role: 'user', content: 'Hi' }], max_tokens: 20 })
    : JSON.stringify({ model: modelId, messages: [{ role: 'user', content: 'Hi' }] })

  const start = performance.now()
  try {
    const res = await apiFetch(endpoint, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body,
    })
    const latency = Math.round(performance.now() - start)
    const respJson = await res.json()

    let content = ''
    let tokens = 0

    if (isAnthropic) {
      content = respJson.content?.[0]?.text || JSON.stringify(respJson.content?.[0]) || ''
      tokens = respJson.usage?.input_tokens || 0
    } else {
      content = respJson.choices?.[0]?.message?.content || ''
      tokens = respJson.usage?.total_tokens || 0
    }

    return {
      modelId,
      modelDbId: record.id,
      protocol,
      endpoint: isAnthropic ? '/v1/messages' : '/v1/chat/completions',
      status: res.status,
      latency,
      content,
      tokens,
      error: res.ok ? undefined : (respJson.error?.message || JSON.stringify(respJson)),
    }
  } catch (e: unknown) {
    return {
      modelId,
      modelDbId: record.id,
      protocol,
      endpoint: isAnthropic ? '/v1/messages' : '/v1/chat/completions',
      status: 0,
      latency: Math.round(performance.now() - start),
      content: '',
      tokens: 0,
      error: e instanceof Error ? e.message : '请求失败',
    }
  }
}

/** Run tasks with a concurrency limit. Each task is a thunk that returns a promise. */
async function runWithConcurrency<T>(
  tasks: (() => Promise<T>)[],
  limit: number,
  onTaskDone?: (result: T, index: number) => void,
): Promise<T[]> {
  const results: T[] = new Array(tasks.length)
  let idx = 0
  const run = async () => {
    while (idx < tasks.length) {
      const i = idx++
      results[i] = await tasks[i]()
      onTaskDone?.(results[i], i)
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, tasks.length) }, () => run()))
  return results
}

const ConfigExposedModel = () => {
  const [data, setData] = useState<ExposedModelRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [modalVisible, setModalVisible] = useState(false)
  const [editingRecord, setEditingRecord] = useState<ExposedModelRecord | null>(null)
  const [form] = Form.useForm()

  // Single test state
  const [testModalOpen, setTestModalOpen] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testProtocol, setTestProtocol] = useState<'openai' | 'anthropic'>('openai')
  const [testResult, setTestResult] = useState<TestResult | null>(null)

  // Batch test state
  const [batchModalOpen, setBatchModalOpen] = useState(false)
  const [batchRunning, setBatchRunning] = useState(false)
  const [batchResults, setBatchResults] = useState<TestResult[]>([])
  const [batchProgress, setBatchProgress] = useState({ done: 0, total: 0 })
  const batchRunningRef = useRef(false)

  const fetchData = async (signal?: AbortSignal) => {
    setLoading(true)
    try {
      const res = await apiFetch('/api/exposed_model', signal ? { signal } : undefined)
      const json = await res.json()
      if (json.success) setData(json.data || [])
    } catch (e) {
      if (e instanceof Error && e.name !== 'AbortError') {
        message.error('获取模型列表失败')
      }
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    const controller = new AbortController()
    void fetchData(controller.signal)
    return () => controller.abort()
  }, [])

  const handleAdd = () => {
    setEditingRecord(null)
    form.resetFields()
    form.setFieldsValue({ owned_by: 'organization', is_active: true })
    setModalVisible(true)
  }

  const handleEdit = (record: ExposedModelRecord) => {
    setEditingRecord(record)
    form.setFieldsValue(record)
    setModalVisible(true)
  }

  const handleDelete = async (id: number) => {
    try {
      const res = await apiFetch(`/api/exposed_model/${id}`, { method: 'DELETE' })
      const json = await res.json()
      if (json.success) {
        message.success('删除成功')
        void fetchData()
      } else {
        message.error('删除失败: ' + json.message)
      }
    } catch (e) {
      message.error('删除失败')
    }
  }

  const handleToggleActive = async (record: ExposedModelRecord) => {
    try {
      const res = await apiFetch(`/api/exposed_model/${record.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ is_active: !record.is_active }),
      })
      const json = await res.json()
      if (json.success) {
        void fetchData()
      } else {
        message.error('操作失败: ' + json.message)
      }
    } catch (e) {
      message.error('操作失败')
    }
  }

  const handleSave = async () => {
    try {
      const values = await form.validateFields()
      const isEdit = !!editingRecord
      const url = isEdit ? `/api/exposed_model/${editingRecord.id}` : '/api/exposed_model'
      const method = isEdit ? 'PUT' : 'POST'

      const res = await apiFetch(url, {
        method,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(values),
      })
      const json = await res.json()
      if (json.success) {
        message.success('保存成功')
        setModalVisible(false)
        void fetchData()
      } else {
        message.error('保存失败: ' + json.message)
      }
    } catch (e) {
      console.error(e)
    }
  }

  /** Single model test — uses runSingleTest and shows result in single-test modal. */
  const handleTest = async (record: ExposedModelRecord, protocol: 'openai' | 'anthropic') => {
    setTesting(true)
    setTestResult(null)
    setTestProtocol(protocol)
    setTestModalOpen(true)

    try {
      const result = await runSingleTest(record, protocol)
      setTestResult(result)

      if (!result.error && result.status === 200) {
        try {
          await apiFetch(`/api/exposed_model/${record.id}/test_time`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ protocol }),
          })
          void fetchData()
        } catch (e) {
          console.error('[test_time update failed]', e)
        }
      }
    } finally {
      setTesting(false)
    }
  }

  /** Batch test — tests all active models × both protocols concurrently. */
  const handleTestAll = () => {
    const activeModels = data.filter((m) => m.is_active)
    if (activeModels.length === 0) {
      message.warning('没有已启用的模型可供测试')
      return
    }

    const protocols: ('openai' | 'anthropic')[] = ['openai', 'anthropic']
    const total = activeModels.length * protocols.length

    // Pre-populate all rows with pending state
    const pendingRows: TestResult[] = activeModels.flatMap((record) =>
      protocols.map((protocol) => ({
        modelId: record.model_id,
        modelDbId: record.id,
        protocol,
        endpoint: protocol === 'anthropic' ? '/v1/messages' : '/v1/chat/completions',
        status: 0,
        latency: 0,
        content: '',
        tokens: 0,
        pending: true,
      })),
    )

    setBatchResults(pendingRows)
    setBatchProgress({ done: 0, total })
    setBatchModalOpen(true)
    setBatchRunning(true)
    batchRunningRef.current = true

    const tasks = activeModels.flatMap((record) =>
      protocols.map(
        (protocol) =>
          () =>
            runSingleTest(record, protocol),
      ),
    )

    runWithConcurrency(
      tasks,
      5,
      (result, index) => {
        if (!batchRunningRef.current) return
        // Update the row at the matching index (tasks and pendingRows share the same order)
        setBatchResults((prev) => {
          const next = [...prev]
          next[index] = result
          return next
        })
        setBatchProgress((prev) => ({ ...prev, done: prev.done + 1 }))
      },
    ).then(async (results) => {
      if (!batchRunningRef.current) return

      // Batch update test_time for successful results
      const successResults = results.filter((r) => r.status === 200 && !r.error)
      const updatePromises = successResults.map((r) =>
        apiFetch(`/api/exposed_model/${r.modelDbId}/test_time`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ protocol: r.protocol }),
        }).catch((e) => console.error('[test_time update failed]', e)),
      )
      await Promise.all(updatePromises)
      void fetchData()

      setBatchRunning(false)
      batchRunningRef.current = false
    })
  }

  const formatTestTime = (t: string | null) => {
    if (!t) return <Tag color="default">未测试</Tag>
    return <Tag color="success">{dayjs(t).format('YYYY-MM-DD HH:mm:ss')}</Tag>
  }

  const columns: TableColumnsType<ExposedModelRecord> = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '模型 ID', dataIndex: 'model_id' },
    { title: 'Owned By', dataIndex: 'owned_by', width: 130 },
    {
      title: '状态',
      dataIndex: 'is_active',
      width: 80,
      render: (v: boolean, record) => (
        <Switch checked={v} onChange={() => handleToggleActive(record)} />
      ),
    },
    {
      title: 'OpenAI 测试',
      dataIndex: 'last_openai_test_time',
      width: 120,
      align: 'center',
      render: (t: string | null) => formatTestTime(t),
    },
    {
      title: 'Anthropic 测试',
      dataIndex: 'last_anthropic_test_time',
      width: 120,
      align: 'center',
      render: (t: string | null) => formatTestTime(t),
    },
    { title: '更新时间', dataIndex: 'update_time', width: 170, render: (t) => dayjs(t).format('YYYY-MM-DD HH:mm:ss') },
    {
      title: '操作',
      width: 200,
      render: (_, record) => (
        <Space>
          <Button type="link" size="small" onClick={() => handleEdit(record)}>编辑</Button>
          <Dropdown
            menu={{
              items: [
                { key: 'openai', label: 'OpenAI 协议测试' },
                { key: 'anthropic', label: 'Anthropic 协议测试' },
              ],
              onClick: ({ key }) => handleTest(record, key as 'openai' | 'anthropic'),
            }}
          >
            <Button type="link" size="small">
              测试 <DownOutlined />
            </Button>
          </Dropdown>
          <Popconfirm title="确定删除吗？" onConfirm={() => handleDelete(record.id)}>
            <Button type="link" danger size="small">删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  // Batch results table columns
  const batchColumns: TableColumnsType<TestResult> = [
    {
      title: '模型',
      dataIndex: 'modelId',
      width: 180,
    },
    {
      title: '协议',
      dataIndex: 'protocol',
      width: 110,
      render: (p: string) => (
        <Tag color={p === 'openai' ? 'green' : 'orange'}>
          {p === 'openai' ? 'OpenAI' : 'Anthropic'}
        </Tag>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (status: number, record: TestResult) => {
        if (record.pending) return <Tag icon={<LoadingOutlined spin />} color="processing">测试中</Tag>
        if (status === 200 && !record.error) return <Tag color="success">200 OK</Tag>
        if (status === 0) return <Tag color="error">网络错误</Tag>
        return <Tag color="error">{status}</Tag>
      },
    },
    {
      title: '延迟',
      dataIndex: 'latency',
      width: 90,
      render: (v: number, record: TestResult) => record.pending ? <Text type="secondary">—</Text> : `${v}ms`,
      sorter: (a, b) => a.latency - b.latency,
    },
    {
      title: 'Tokens',
      dataIndex: 'tokens',
      width: 80,
      render: (v: number, record: TestResult) => record.pending ? <Text type="secondary">—</Text> : v,
    },
    {
      title: '错误信息',
      dataIndex: 'error',
      ellipsis: true,
      render: (err: string | undefined, record: TestResult) =>
        record.pending ? <Text type="secondary">—</Text> : err ? <Text type="danger">{err}</Text> : <Text type="secondary">—</Text>,
    },
  ]

  // Sort batch results: keep original order while running; after done, failures first
  const sortedBatchResults = batchRunning
    ? batchResults
    : [...batchResults].sort((a, b) => {
        const aOk = a.status === 200 && !a.error
        const bOk = b.status === 200 && !b.error
        if (aOk === bOk) return 0
        return aOk ? 1 : -1
      })

  const batchPassed = batchResults.filter((r) => !r.pending && r.status === 200 && !r.error).length
  const batchFailed = batchResults.filter((r) => !r.pending && (r.status !== 200 || r.error)).length

  return (
    <Card
      title="模型列表配置"
      extra={
        <Space>
          <Button
            icon={<ThunderboltOutlined />}
            onClick={handleTestAll}
            loading={batchRunning}
            disabled={data.filter((m) => m.is_active).length === 0}
          >
            一键测试全部
          </Button>
          <Button type="primary" onClick={handleAdd}>新增模型</Button>
        </Space>
      }
      variant="borderless"
    >
      <Table
        columns={columns}
        dataSource={data}
        rowKey="id"
        loading={loading}
        pagination={{ pageSize: 10 }}
        size="middle"
      />

      <Modal
        title={editingRecord ? '编辑模型' : '新增模型'}
        open={modalVisible}
        onOk={handleSave}
        onCancel={() => setModalVisible(false)}
        destroyOnHidden
      >
        <Form form={form} layout="vertical">
          <Form.Item name="model_id" label="模型 ID" rules={[{ required: true }]}>
            <Input placeholder="如: gpt-4o, deepseek-chat" />
          </Form.Item>
          <Form.Item name="owned_by" label="Owned By" rules={[{ required: true }]}>
            <Input placeholder="如: organization, openai" />
          </Form.Item>
          <Form.Item name="is_active" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>

      {/* Single test result modal */}
      <Modal
        title={`模型联调测试 — ${testProtocol === 'openai' ? 'OpenAI' : 'Anthropic'} 协议`}
        open={testModalOpen}
        onCancel={() => setTestModalOpen(false)}
        footer={<Button onClick={() => setTestModalOpen(false)}>关闭</Button>}
        width={560}
      >
        {testing ? (
          <Paragraph>正在发送测试请求...</Paragraph>
        ) : testResult ? (
          <Space direction="vertical" size="middle" style={{ width: '100%' }}>
            <div>
              <Text strong>模型：</Text>
              <Text code>{testResult.modelId}</Text>
              <Text style={{ marginLeft: 16 }} strong>协议：</Text>
              <Tag color={testResult.protocol === 'openai' ? 'green' : 'orange'}>
                {testResult.protocol === 'openai' ? 'OpenAI' : 'Anthropic'}
              </Tag>
              <Text style={{ marginLeft: 8 }} strong>端点：</Text>
              <Text code>{testResult.endpoint}</Text>
            </div>
            <div>
              <Text strong>HTTP 状态：</Text>
              {testResult.status === 200 ? (
                <Tag color="success">200 OK</Tag>
              ) : testResult.status === 0 ? (
                <Tag color="error">网络错误</Tag>
              ) : (
                <Tag color="error">{testResult.status}</Tag>
              )}
              <Text style={{ marginLeft: 24 }} strong>延迟：</Text>
              <Text>{testResult.latency}ms</Text>
            </div>
            <div>
              <Text strong>Token 消耗：</Text>
              <Text>{testResult.tokens}</Text>
            </div>
            <div>
              <Text strong>响应内容：</Text>
              <Paragraph
                style={{
                  background: '#f5f5f5',
                  padding: 12,
                  borderRadius: 6,
                  marginTop: 4,
                  whiteSpace: 'pre-wrap',
                }}
              >
                {testResult.content || '(空)'}
              </Paragraph>
            </div>
            {testResult.error && (
              <div>
                <Text strong style={{ color: '#ff4d4f' }}>错误信息：</Text>
                <Paragraph
                  style={{
                    background: '#fff2f0',
                    padding: 12,
                    borderRadius: 6,
                    marginTop: 4,
                    color: '#ff4d4f',
                    whiteSpace: 'pre-wrap',
                  }}
                >
                  {testResult.error}
                </Paragraph>
              </div>
            )}
          </Space>
        ) : null}
      </Modal>

      {/* Batch test results modal */}
      <Modal
        title="批量模型测试"
        open={batchModalOpen}
        onCancel={() => {
          batchRunningRef.current = false
          setBatchModalOpen(false)
          if (!batchRunning) {
            setBatchResults([])
          }
        }}
        footer={<Button onClick={() => setBatchModalOpen(false)}>关闭</Button>}
        width={820}
      >
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          {batchProgress.total > 0 && (
            <Progress
              percent={Math.round((batchProgress.done / batchProgress.total) * 100)}
              format={() => `${batchProgress.done} / ${batchProgress.total}`}
              status={batchRunning ? 'active' : 'success'}
            />
          )}
          <Space size="large">
            <Text>
              <Tag color="success">{batchPassed} 通过</Tag>
              <Tag color="error">{batchFailed} 失败</Tag>
              <Tag color="processing">{batchProgress.total - batchProgress.done} 剩余</Tag>
            </Text>
          </Space>
          <Table
            columns={batchColumns}
            dataSource={sortedBatchResults}
            rowKey={(r) => `${r.modelDbId}-${r.protocol}`}
            size="small"
            pagination={false}
            scroll={{ y: 400 }}
          />
        </Space>
      </Modal>
    </Card>
  )
}

export default ConfigExposedModel
