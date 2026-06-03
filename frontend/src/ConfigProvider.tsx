import { useEffect, useState } from 'react'
import { Button, Card, Divider, Form, Input, Modal, Popconfirm, Space, Table, Tag, Typography, message } from 'antd'
import type { TableColumnsType } from 'antd'
import dayjs from 'dayjs'
import { apiFetch } from './api'

export type ProviderRecord = {
  id: number
  name: string
  openai_base_url: string | null
  anthropic_base_url: string | null
  api_key: string | null
  remark: string | null
  create_time: string
  update_time: string
}

const { Title } = Typography

const supportedProtocols = (p: ProviderRecord) => {
  const tags: { color: string; label: string }[] = []
  if (p.openai_base_url) tags.push({ color: 'green', label: 'OpenAI' })
  if (p.anthropic_base_url) tags.push({ color: 'orange', label: 'Anthropic' })
  return tags
}

const ConfigProvider = () => {
  const [data, setData] = useState<ProviderRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [modalVisible, setModalVisible] = useState(false)
  const [editingRecord, setEditingRecord] = useState<ProviderRecord | null>(null)
  const [form] = Form.useForm()

  const fetchData = async () => {
    setLoading(true)
    try {
      const res = await apiFetch('/api/provider')
      const json = await res.json()
      if (json.success) setData(json.data)
    } catch (e) {
      message.error('获取产商列表失败')
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
    setModalVisible(true)
  }

  const handleEdit = (record: ProviderRecord) => {
    setEditingRecord(record)
    form.setFieldsValue({
      name: record.name,
      openai_base_url: record.openai_base_url,
      anthropic_base_url: record.anthropic_base_url,
      api_key: record.api_key,
      remark: record.remark,
    })
    setModalVisible(true)
  }

  const handleDelete = async (id: number) => {
    try {
      const res = await apiFetch(`/api/provider/${id}`, { method: 'DELETE' })
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
      if (!values.openai_base_url && !values.anthropic_base_url) {
        message.error('请至少填写一个协议的 Base URL')
        return
      }
      if (!values.api_key) {
        message.error('请填写 API Key')
        return
      }

      const isEdit = !!editingRecord
      const url = isEdit ? `/api/provider/${editingRecord.id}` : '/api/provider'
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

  const columns: TableColumnsType<ProviderRecord> = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '产商名称', dataIndex: 'name' },
    {
      title: '支持协议',
      dataIndex: 'id',
      width: 200,
      render: (_, record) => {
        const tags = supportedProtocols(record)
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
    { title: '备注', dataIndex: 'remark', ellipsis: true },
    { title: '更新时间', dataIndex: 'update_time', width: 170, render: (t) => dayjs(t).format('YYYY-MM-DD HH:mm:ss') },
    {
      title: '操作',
      width: 150,
      render: (_, record) => (
        <Space>
          <Button type="link" size="small" onClick={() => handleEdit(record)}>编辑</Button>
          <Popconfirm title="确定删除吗？" onConfirm={() => handleDelete(record.id)}>
            <Button type="link" danger size="small">删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <Card
      title="大模型产商配置"
      extra={<Button type="primary" onClick={handleAdd}>新增产商</Button>}
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
        title={editingRecord ? '编辑产商' : '新增产商'}
        open={modalVisible}
        onOk={handleSave}
        onCancel={() => setModalVisible(false)}
        destroyOnHidden
        width={640}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="产商名称" rules={[{ required: true }]}>
            <Input placeholder="如: DashScope" />
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input.TextArea placeholder="输入备注信息..." rows={2} />
          </Form.Item>

          <Divider style={{ margin: '12px 0 16px' }} />
          <Title level={5} style={{ marginTop: 0 }}>接口地址</Title>
          <Form.Item
            name="openai_base_url"
            label="OpenAI Base URL"
            tooltip="留空表示该厂商不提供 OpenAI 协议"
          >
            <Input placeholder="如: https://dashscope.aliyuncs.com/compatible-mode/v1" />
          </Form.Item>
          <Form.Item
            name="anthropic_base_url"
            label="Anthropic Base URL"
            tooltip="留空表示该厂商不提供 Anthropic 协议"
          >
            <Input placeholder="如: https://dashscope.aliyuncs.com/apps/anthropic" />
          </Form.Item>

          <Divider style={{ margin: '12px 0 16px' }} />
          <Title level={5} style={{ marginTop: 0 }}>认证</Title>
          <Form.Item
            name="api_key"
            label="API Key"
            tooltip="同一厂商对两种协议共用一个 Key"
            rules={[{ required: true }]}
          >
            <Input.Password placeholder="输入 API Key" />
          </Form.Item>

          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            提示：至少填写一个协议的 Base URL；API Key 在两种协议间共用。
          </Typography.Text>
        </Form>
      </Modal>
    </Card>
  )
}

export default ConfigProvider
