import { useEffect, useState } from 'react'
import { Button, Card, Col, Form, Input, InputNumber, Modal, Popconfirm, Row, Select, Space, Switch, Table, Tag, message } from 'antd'
import type { TableColumnsType } from 'antd'
import dayjs from 'dayjs'
import type { ProviderRecord } from './ConfigProvider'
import { apiFetch, getCurrentUser } from './api'

export type RouteRecord = {
  id: number
  model_pattern: string
  route_type: string
  provider_id: number | null
  target_model: string | null
  timeout: number
  log_requests: boolean
  log_responses: boolean
  priority: number
  is_active: boolean
  create_time: string
  update_time: string
}

const ConfigRoute = () => {
  const currentUser = getCurrentUser()
  const isRoot = (currentUser?.role ?? 99) === 1
  const [data, setData] = useState<RouteRecord[]>([])
  const [providers, setProviders] = useState<ProviderRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [modalVisible, setModalVisible] = useState(false)
  const [editingRecord, setEditingRecord] = useState<RouteRecord | null>(null)
  const [form] = Form.useForm()

  const fetchData = async () => {
    setLoading(true)
    try {
      const [resRoutes, resProviders] = await Promise.all([
        apiFetch('/api/route'),
        apiFetch('/api/provider')
      ])
      const [jsonRoutes, jsonProviders] = await Promise.all([
        resRoutes.json(),
        resProviders.json()
      ])
      if (jsonRoutes.success) setData(jsonRoutes.data)
      if (jsonProviders.success) setProviders(jsonProviders.data)
    } catch (e) {
      message.error('获取路由或产商列表失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void fetchData()
  }, [])

  const handleAdd = () => {
    setEditingRecord(null)
    form.resetFields()

    const lastProviderId = localStorage.getItem('last_selected_provider_id')
    let defaultProviderId = undefined
    if (lastProviderId && providers.some(p => p.id.toString() === lastProviderId)) {
      defaultProviderId = Number(lastProviderId)
    } else if (providers.length > 0) {
      defaultProviderId = providers[0].id
    }

    form.setFieldsValue({
      provider_id: defaultProviderId,
      timeout: 600,
      log_requests: true,
      log_responses: true,
      priority: 0,
      is_active: true
    })
    setModalVisible(true)
  }

  const handleEdit = (record: RouteRecord) => {
    setEditingRecord(record)
    form.setFieldsValue(record)
    setModalVisible(true)
  }

  const handleDelete = async (id: number) => {
    try {
      const res = await apiFetch(`/api/route/${id}`, { method: 'DELETE' })
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

  const handleSave = async () => {
    try {
      const values = await form.validateFields()

      if (values.provider_id) {
        localStorage.setItem('last_selected_provider_id', values.provider_id.toString())
      }

      const isEdit = !!editingRecord
      const url = isEdit ? `/api/route/${editingRecord.id}` : '/api/route'
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

  const columns: TableColumnsType<RouteRecord> = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '模型匹配规则', dataIndex: 'model_pattern' },
    {
      title: '目标产商',
      dataIndex: 'provider_id',
      width: 140,
      render: (pid) => providers.find(p => p.id === pid)?.name || '-'
    },
    {
      title: '支持协议',
      dataIndex: 'provider_id',
      width: 200,
      render: (pid) => {
        const p = providers.find(x => x.id === pid)
        if (!p) return '-'
        const tags: { color: string; label: string }[] = []
        if (p.openai_base_url) tags.push({ color: 'green', label: 'OpenAI' })
        if (p.anthropic_base_url) tags.push({ color: 'orange', label: 'Anthropic' })
        if (tags.length === 0) return <Tag color="default">未配置</Tag>
        return (
          <Space size={4}>
            {tags.map((t) => (
              <Tag key={t.label} color={t.color}>{t.label}</Tag>
            ))}
          </Space>
        )
      },
    },
    { title: '目标模型', dataIndex: 'target_model', render: (val) => val || '-' },
    { title: '优先级', dataIndex: 'priority', width: 80 },
    {
      title: '状态',
      dataIndex: 'is_active',
      width: 80,
      render: (isActive) => <Switch checked={isActive} disabled size="small" />
    },
    { title: '更新时间', dataIndex: 'update_time', width: 170, render: (t) => dayjs(t).format('YYYY-MM-DD HH:mm:ss') },
    {
      title: '操作',
      width: 150,
      render: (_, record) => (
        <Space>
          {isRoot && <Button type="link" size="small" onClick={() => handleEdit(record)}>编辑</Button>}
          {isRoot && <Popconfirm title="确定删除吗？" onConfirm={() => handleDelete(record.id)}>
            <Button type="link" danger size="small">删除</Button>
          </Popconfirm>}
        </Space>
      ),
    },
  ]

  return (
    <Card
      title="模型路由配置"
      extra={isRoot ? <Button type="primary" onClick={handleAdd}>新增路由</Button> : null}
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
        title={editingRecord ? '编辑路由' : '新增路由'}
        open={modalVisible}
        onOk={handleSave}
        onCancel={() => setModalVisible(false)}
        destroyOnHidden
        width={600}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="model_pattern" label="模型匹配规则" rules={[{ required: true }]} tooltip="支持通配符，如 gpt-4*">
            <Input placeholder="如: gpt-4*" />
          </Form.Item>

          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="provider_id" label="目标产商" rules={[{ required: true }]}>
                <Select
                  style={{ width: '100%' }}
                  placeholder="选择产商"
                  showSearch
                  optionFilterProp="children"
                  filterOption={(input, option) =>
                    (option?.children as unknown as string).toLowerCase().includes(input.toLowerCase())
                  }
                >
                  {providers.map(p => (
                    <Select.Option key={p.id} value={p.id}>{p.name}</Select.Option>
                  ))}
                </Select>
              </Form.Item>
            </Col>
          </Row>

          <Form.Item name="target_model" label="目标模型（可选）" tooltip="转发时将请求的 model 替换为该值">
            <Input placeholder="如: gpt-4o" />
          </Form.Item>

          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="timeout" label="超时时间 (秒)" tooltip="-1 表示不超时（永久等待），其他为秒数">
                <InputNumber min={-1} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="priority" label="优先级" tooltip="数值越大优先级越高">
                <InputNumber style={{ width: '100%' }} />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={16}>
            <Col span={8}>
              <Form.Item name="log_requests" label="记录请求" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="log_responses" label="记录响应" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="is_active" label="启用" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>
    </Card>
  )
}

export default ConfigRoute