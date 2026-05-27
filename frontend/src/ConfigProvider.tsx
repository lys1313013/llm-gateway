import { useEffect, useState } from 'react'
import { Button, Card, Form, Input, Modal, Popconfirm, Radio, Space, Table, message } from 'antd'
import type { TableColumnsType } from 'antd'
import dayjs from 'dayjs'
import { apiFetch } from './api'

export type ProviderRecord = {
  id: number
  name: string
  base_url: string
  api_key: string
  protocol: string
  remark: string
  create_time: string
  update_time: string
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
    form.setFieldsValue({ protocol: 'openai' })
    setModalVisible(true)
  }

  const handleEdit = (record: ProviderRecord) => {
    setEditingRecord(record)
    form.setFieldsValue(record)
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
    { title: 'Base URL', dataIndex: 'base_url' },
    { title: 'API Key', dataIndex: 'api_key', render: () => '***' },
    { title: '协议', dataIndex: 'protocol', width: 100 },
    { title: '备注', dataIndex: 'remark' },
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
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="产商名称" rules={[{ required: true }]}>
            <Input placeholder="如: DashScope" />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL" rules={[{ required: true }]}>
            <Input placeholder="如: https://dashscope.aliyuncs.com/compatible-mode/v1" />
          </Form.Item>
          <Form.Item name="api_key" label="API Key" rules={[{ required: true }]}>
            <Input.Password placeholder="输入 API Key" />
          </Form.Item>
          <Form.Item name="protocol" label="协议">
            <Radio.Group>
              <Radio value="openai">OpenAI</Radio>
              <Radio value="anthropic">Anthropic</Radio>
            </Radio.Group>
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input.TextArea placeholder="输入备注信息..." rows={3} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}

export default ConfigProvider
